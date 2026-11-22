package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// AgentClient makes one-shot JSON GETs against agent sidecars over the same
// mTLS material the proxy uses. Handlers use it when they need agent data
// server-side (e.g. the mod update check reads the installed-mod manifest)
// instead of proxying a browser request through.
type AgentClient struct {
	transport agentTransport
	gateway   *agentGatewayResolver
}

// NewAgentClient builds a client from the agent mTLS flags. It fails when
// the material is missing/invalid — callers treat that like the proxy does
// (degrade to 503, don't crash startup).
func NewAgentClient(caBundle, clientCert, clientKey string) (*AgentClient, error) {
	tlsCfg, err := agentTLSConfig(caBundle, clientCert, clientKey)
	if err != nil {
		return nil, err
	}
	return &AgentClient{
		transport: newDirectAgentTransport(tlsCfg, 15*time.Second),
	}, nil
}

// NewClusterAgentClient supports local agents and configured remote gateways.
// Missing local mTLS does not disable remote clusters with their own credentials.
func NewClusterAgentClient(reg *kube.Registry, namespace, caBundle, clientCert, clientKey string) *AgentClient {
	client := &AgentClient{gateway: &agentGatewayResolver{registry: reg, namespace: namespace}}
	if tlsCfg, err := agentTLSConfig(caBundle, clientCert, clientKey); err == nil {
		client.transport = newDirectAgentTransport(tlsCfg, 15*time.Second)
	}
	return client
}

// ConfiguredForCluster preserves the local missing-mTLS response while allowing
// remote-only central deployments to use gateway credentials.
func (c *AgentClient) ConfiguredForCluster(cluster string) bool {
	if cluster == "" || cluster == scope.DefaultCluster {
		return c.transport != nil
	}
	return c.gateway != nil && c.gateway.registry != nil
}

// agentRespCap bounds one-shot agent responses. These are small JSON
// payloads (mod listings), nothing like the file proxy's traffic.
const agentRespCap = 8 << 20 // 8 MiB

// GetJSON fetches an agent endpoint and decodes its JSON body into out.
func (c *AgentClient) GetJSON(ctx context.Context, name, namespace, path string, out any) error {
	return c.GetJSONForCluster(ctx, scope.DefaultCluster, name, namespace, path, out)
}

// GetJSONForCluster resolves the same registered cluster as its caller's resource
// lookup. The remote transport binds the operation to the live server UID.
func (c *AgentClient) GetJSONForCluster(ctx context.Context, cluster, name, namespace, path string, out any) error {
	resp, cancel, err := c.get(ctx, cluster, name, namespace, path, "", "application/json")
	if err != nil {
		return err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent GET %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, agentRespCap)).Decode(out); err != nil {
		return fmt.Errorf("agent GET %s: decode: %w", path, err)
	}
	return nil
}

// ErrAgentFileNotFound is returned by ReadFileForCluster when the agent
// answers 404 for the requested path.
var ErrAgentFileNotFound = errors.New("agent file not found")

// ReadFileForCluster reads one small file below the server's data mount
// through the agent's /files/read. At most limit bytes are returned (a longer
// file is cut, not an error). A missing file is ErrAgentFileNotFound; any other
// failure (no pod, agent unreachable, 4xx/5xx) is a wrapped error.
func (c *AgentClient) ReadFileForCluster(ctx context.Context, cluster, name, namespace, path string, limit int64) ([]byte, error) {
	query := url.Values{"path": {path}}.Encode()
	resp, cancel, err := c.get(ctx, cluster, name, namespace, "/files/read", query, "*/*")
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrAgentFileNotFound
	default:
		return nil, fmt.Errorf("agent GET /files/read: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("agent GET /files/read: read: %w", err)
	}
	return data, nil
}

// get resolves the target and transport (the former body of GetJSONForCluster,
// unchanged) and performs one GET. The caller must call the returned cancel
// after it has finished with the response body.
func (c *AgentClient) get(ctx context.Context, cluster, name, namespace, path, rawQuery, accept string) (*http.Response, context.CancelFunc, error) {
	target := agentTarget{name: name, namespace: namespace}
	if err := target.validate(); err != nil {
		return nil, nil, err
	}
	transport := c.transport
	cluster = strings.TrimSpace(cluster)
	if cluster != "" && cluster != scope.DefaultCluster {
		if c.gateway == nil || c.gateway.registry == nil {
			return nil, nil, errGatewayUnavailable
		}
		var err error
		_, transport, err = c.gateway.resolve(ctx, cluster, target)
		if err != nil {
			return nil, nil, err
		}
	} else if c.gateway != nil && c.gateway.registry != nil {
		var err error
		target, err = localAgentTarget(ctx, c.gateway.registry.Default(), target)
		if err != nil {
			return nil, nil, err
		}
	} else if bound, ok := rbac.BoundServerIdentity(ctx); ok {
		// Legacy clients lack a registry, but can still enforce the exact
		// ownership grant at the receiving agent's UID route.
		if err := rbac.ValidateServerIdentity(ctx, scope.DefaultCluster, namespace, name, bound.UID); err != nil {
			return nil, nil, err
		}
		target.uid = bound.UID
	}
	if transport == nil {
		return nil, nil, errors.New("agent mTLS not configured")
	}
	// Match the historical bound on internal one-shot reads for gateways too.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	resp, err := transport.Do(ctx, agentRequest{
		target: target,
		method: http.MethodGet, path: path, rawQuery: rawQuery,
		header: http.Header{"Accept": {accept}},
	})
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("agent GET %s: %w", path, err)
	}
	return resp, cancel, nil
}

// agentHostFor is the in-cluster DNS + port of a server's agent sidecar —
// the operator maintains the <gs>-agent ClusterIP Service; the agent
// listens on :8090.
func agentHostFor(name, namespace string) string {
	return fmt.Sprintf("%s-agent.%s.svc.cluster.local:8090", name, namespace)
}
