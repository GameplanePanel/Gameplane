package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// CaptureGatewayCapabilities describes this site's supported capture transport.
type CaptureGatewayCapabilities struct {
	Protocol                string `json:"protocol"`
	TargetUID               bool   `json:"targetUID"`
	CaptureFiles            bool   `json:"captureFiles"`
	CaptureEnabled          bool   `json:"captureEnabled"`
	DefaultRetentionSeconds int64  `json:"defaultRetentionSeconds"`
	MaxRetentionSeconds     int64  `json:"maxRetentionSeconds"`
	DefaultMaxDurationSecs  int64  `json:"defaultMaxDurationSeconds"`
	DefaultMaxSizeBytes     int64  `json:"defaultMaxSizeBytes"`
}

// CaptureGatewayClient uses fresh registered gateway credentials per operation.
type CaptureGatewayClient struct{ resolver *agentGatewayResolver }

// NewCaptureGatewayClient creates a capture client without retaining credentials.
func NewCaptureGatewayClient(reg *kube.Registry, namespace string) *CaptureGatewayClient {
	return &CaptureGatewayClient{resolver: &agentGatewayResolver{registry: reg, namespace: namespace}}
}

// Capabilities authenticates to the selected gateway with a bounded fresh lookup.
// Older or unknown protocols do not claim support for immutable capture files.
func (c *CaptureGatewayClient) Capabilities(ctx context.Context, cluster, namespace, serverName string) (CaptureGatewayCapabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, transport, err := c.resolver.resolve(ctx, cluster, agentTarget{name: serverName, namespace: namespace})
	if err != nil {
		return CaptureGatewayCapabilities{}, fmt.Errorf("resolve gateway capability target: %w", err)
	}
	u := *transport.base
	u.Path = "/v1/capabilities"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return CaptureGatewayCapabilities{}, fmt.Errorf("build gateway capability request: %w", err)
	}
	resp, err := transport.http.Do(req)
	if err != nil {
		return CaptureGatewayCapabilities{}, fmt.Errorf("read gateway capabilities: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return CaptureGatewayCapabilities{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return CaptureGatewayCapabilities{}, fmt.Errorf("gateway capabilities returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return CaptureGatewayCapabilities{}, fmt.Errorf("read gateway capability body: %w", err)
	}
	var caps CaptureGatewayCapabilities
	if len(body) > 4096 {
		return caps, errors.New("gateway capability response too large")
	}
	if err := json.Unmarshal(body, &caps); err != nil {
		return CaptureGatewayCapabilities{}, fmt.Errorf("decode gateway capabilities: %w", err)
	}
	if caps.Protocol != "v1" || !caps.TargetUID {
		return CaptureGatewayCapabilities{}, nil
	}
	return caps, nil
}

// SupportsCaptureFiles reports support without assuming an older gateway has it.
func (c *CaptureGatewayClient) SupportsCaptureFiles(ctx context.Context, cluster, namespace, serverName string) (bool, error) {
	caps, err := c.Capabilities(ctx, cluster, namespace, serverName)
	return caps.CaptureFiles, err
}

// Download streams a file identified by its server and capture UIDs.
func (c *CaptureGatewayClient) Download(ctx context.Context, target gatewayprotocol.CaptureTarget, headers http.Header) (*http.Response, error) {
	return c.request(ctx, target, http.MethodGet, headers)
}

// Delete removes only the file with the bound identities; it never retries.
func (c *CaptureGatewayClient) Delete(ctx context.Context, target gatewayprotocol.CaptureTarget) (*http.Response, error) {
	return c.request(ctx, target, http.MethodDelete, nil)
}

func (c *CaptureGatewayClient) request(ctx context.Context, target gatewayprotocol.CaptureTarget, method string, headers http.Header) (*http.Response, error) {
	path, err := gatewayprotocol.CapturePath(target)
	if err != nil {
		return nil, fmt.Errorf("build gateway capture path: %w", err)
	}
	_, transport, err := c.resolver.resolve(ctx, target.Cluster, agentTarget{name: target.Name, namespace: target.Namespace})
	if err != nil {
		return nil, fmt.Errorf("resolve gateway capture target: %w", err)
	}
	if transport.target.UID != target.UID {
		return nil, errors.New("capture server identity changed")
	}
	u := *transport.base
	u.Path = path
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build gateway capture request: %w", err)
	}
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := headers.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}
	resp, err := transport.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send gateway capture request: %w", err)
	}
	return resp, nil
}
