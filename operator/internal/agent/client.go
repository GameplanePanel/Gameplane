// Package agent is a small typed HTTP client the operator uses to talk
// to the in-pod agent during backup orchestration. It mirrors the
// dialing strategy used by the API gateway in api/internal/ws/dialer.go
// — same FQDN format, same mTLS material, same port — so we have one
// reference impl for "operator → agent" connectivity to maintain.
package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrUnsupported means the agent declined the pause because it cannot quiesce
// this game. A first verified refusal allows the backup to proceed without a
// pause. It cannot clear the resume obligation from an earlier ambiguous call.
var ErrUnsupported = errors.New("agent: game does not support quiesce")

// Client speaks to the agent's HTTPS endpoints. Zero value is unusable;
// build via New.
type Client struct {
	http *http.Client
	// Disabled is true when no mTLS material was configured. Legacy requests
	// may return success without dialing. Pod-bound operations return an error.
	Disabled bool
}

// Config carries the cert/CA paths typically populated from operator
// flags. Either all three fields are non-empty (mTLS active) or all
// three are empty (Client.Disabled = true).
type Config struct {
	CABundle   string
	ClientCert string
	ClientKey  string
	Timeout    time.Duration
}

// Enabled reports whether the client has mTLS configured. It does not probe an agent.
func (c *Client) Enabled() bool {
	return !c.Disabled
}

// New builds an mTLS-configured Client. Empty cert paths produce a
// disabled client. Partial configuration returns an error.
func New(cfg Config) (*Client, error) {
	if cfg.CABundle == "" && cfg.ClientCert == "" && cfg.ClientKey == "" {
		return &Client{Disabled: true}, nil
	}
	if cfg.CABundle == "" || cfg.ClientCert == "" || cfg.ClientKey == "" {
		return nil, errors.New("agent: partial mTLS config; need ca-bundle, client-cert, and client-key together")
	}
	cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
	if err != nil {
		return nil, fmt.Errorf("agent: load client cert: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.CABundle)
	if err != nil {
		return nil, fmt.Errorf("agent: read CA bundle: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("agent: empty CA bundle")
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		http: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}, nil
}

// agentURL builds the in-cluster URL for a given GameServer's agent,
// via the dedicated `<gs>-agent` ClusterIP Service the GameServer
// reconciler maintains. Layout matches api/internal/ws/dialer.go's
// agentHost exactly.
func agentURL(namespace, server, path string) string {
	return fmt.Sprintf("https://%s-agent.%s.svc.cluster.local:8090%s", server, namespace, path)
}

type quiesceResponse struct {
	Quiesced *bool  `json:"quiesced"`
	Outcome  string `json:"outcome"`
	Reason   string `json:"reason,omitempty"`
}

// Quiesce calls /quiesce on the agent for the given GameServer. Returns
// nil on success, ErrUnsupported when the game has no quiesce action,
// and a non-nil error for transport failures or 5xx responses.
func (c *Client) Quiesce(ctx context.Context, namespace, server string) error {
	return c.callQuiesce(ctx, namespace, server, "/quiesce", true)
}

// Unquiesce reverses Quiesce. Best-effort: callers should record but
// not surface its errors as backup failures.
func (c *Client) Unquiesce(ctx context.Context, namespace, server string) error {
	return c.callQuiesce(ctx, namespace, server, "/unquiesce", false)
}

// QuiesceEnabled reports whether this client can perform quiesce operations.
func (c *Client) QuiesceEnabled() bool { return c != nil && !c.Disabled }

// CheckPod verifies that the responding agent belongs to the selected Pod.
// Older agents return 404; callers must not fall back to unbound mutations.
func (c *Client) CheckPod(ctx context.Context, namespace, server, podUID string) error {
	_, err := c.PodBackupMode(ctx, namespace, server, podUID)
	return err
}

// PodBackupMode also accepts older v2 agents whose identity response has no
// mode header. Those agents only implement the original pause/resume protocol.
func (c *Client) PodBackupMode(ctx context.Context, namespace, server, podUID string) (string, error) {
	if c.Disabled || podUID == "" {
		return "", fmt.Errorf("agent: pod identity check requires an enabled client and pod UID")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL(namespace, server, "/v2/pods/"+url.PathEscape(podUID)+"/identity"), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("agent: pod identity check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("agent: pod identity check: status %d", resp.StatusCode)
	}
	return resp.Header.Get("Gameplane-Backup-Mode"), nil
}

// FlushPod checkpoints the game without changing its write policy.
func (c *Client) FlushPod(ctx context.Context, namespace, server, podUID string) error {
	return c.callPodQuiesce(ctx, namespace, server, podUID, "/flush", false)
}

// QuiescePod disables saving only on the selected Pod's agent.
// A lost acknowledgment can leave saving disabled. Record the resume obligation
// before sending the request.
func (c *Client) QuiescePod(ctx context.Context, namespace, server, podUID string) error {
	return c.callPodQuiesce(ctx, namespace, server, podUID, "/quiesce", true)
}

// UnquiescePod restores saving only on the selected Pod's agent.
// A Pod UID survives a container restart. The caller must check game container
// identity before sending save-on to the process that owes a resume.
func (c *Client) UnquiescePod(ctx context.Context, namespace, server, podUID string) error {
	return c.callPodQuiesce(ctx, namespace, server, podUID, "/unquiesce", false)
}

func (c *Client) callPodQuiesce(ctx context.Context, namespace, server, podUID, path string, expectQuiesced bool) error {
	if c.Disabled || podUID == "" {
		return fmt.Errorf("agent: pod-bound quiesce requires an enabled client and pod UID")
	}
	return c.callQuiesce(ctx, namespace, server, "/v2/pods/"+url.PathEscape(podUID)+path, expectQuiesced)
}

// expectQuiesced is what the response should report on a successful
// completion: true after /quiesce, false after /unquiesce. A mismatch
// is a protocol bug worth surfacing.
func (c *Client) callQuiesce(ctx context.Context, namespace, server, path string, expectQuiesced bool) error {
	if c.Disabled {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL(namespace, server, path), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agent: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	const maxReply = 16 << 10
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReply+1))
	if err != nil {
		return fmt.Errorf("agent: %s: read response: %w", path, err)
	}
	if len(body) > maxReply {
		return fmt.Errorf("agent: %s: oversized response", path)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent: %s: status %d", path, resp.StatusCode)
	}

	var parsed quiesceResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("agent: %s: decode response: %w", path, err)
	}
	if parsed.Quiesced == nil {
		return fmt.Errorf("agent: %s: missing quiesced field", path)
	}
	verified := strings.HasPrefix(path, "/v2/pods/")
	if expectQuiesced && !*parsed.Quiesced && parsed.Reason == "game does not support quiesce" &&
		(!verified || parsed.Outcome == "unsupported") {
		return ErrUnsupported
	}
	if *parsed.Quiesced != expectQuiesced || parsed.Reason != "" {
		return fmt.Errorf("agent: %s: refused or contradictory acknowledgment", path)
	}
	want := "resumed"
	if strings.HasSuffix(path, "/flush") {
		want = "flushed"
	}
	if expectQuiesced {
		want = "paused"
	}
	if verified && parsed.Outcome != want {
		return fmt.Errorf("agent: %s: missing or contradictory outcome", path)
	}
	return nil
}

// Stop calls /lifecycle/stop on the agent, asking it to run the module's
// declared in-game stop sequence over RCON. Best-effort: the operator records
// but does not block on the result — server readiness and the grace deadline
// drive the actual scale-down. A disabled client (no mTLS) is a no-op.
func (c *Client) Stop(ctx context.Context, namespace, server string) error {
	if c.Disabled {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL(namespace, server, "/lifecycle/stop"), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agent: /lifecycle/stop: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent: /lifecycle/stop: status %d", resp.StatusCode)
	}
	return nil
}
