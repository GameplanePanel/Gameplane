package ws

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const gatewayCredentialsLabel = "gameplane.local/agent-gateway-credentials"

var errGatewayUnavailable = errors.New("agent gateway not configured")

// AgentGatewayOptions enables registered-cluster agent routes. Secrets are read
// only from the namespace where the central API runs.
type AgentGatewayOptions struct{ Namespace string }

type agentGatewayResolver struct {
	registry  *kube.Registry
	namespace string
}

// resolve binds a remote transport to a live GameServer UID. Both the Cluster
// and its credential Secret are read for every new operation: deletion or
// rotation must never leave a cached credential/route silently usable.
func (r *agentGatewayResolver) resolve(ctx context.Context, cluster string, target agentTarget) (*kube.Client, *gatewayAgentTransport, error) {
	k, ok := r.registry.Get(cluster)
	if !ok || k == nil {
		return nil, nil, scope.ErrForbiddenCluster
	}
	home := r.registry.Management()
	if home == nil || home.Clusters() == nil {
		return nil, nil, errGatewayUnavailable
	}
	registration, err := home.Clusters().Get(ctx, cluster, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("get gateway registration: %w", err)
	}
	endpoint, _, err := unstructured.NestedString(registration.Object, "spec", "agentGateway", "url")
	if err != nil {
		return nil, nil, fmt.Errorf("read gateway endpoint: %w", err)
	}
	secretName, _, err := unstructured.NestedString(registration.Object, "spec", "agentGateway", "tlsSecretRef", "name")
	if err != nil {
		return nil, nil, fmt.Errorf("read gateway credential reference: %w", err)
	}
	if endpoint == "" || secretName == "" || r.namespace == "" {
		return nil, nil, errGatewayUnavailable
	}
	if len(validation.IsDNS1123Subdomain(secretName)) != 0 {
		return nil, nil, errors.New("invalid gateway credential reference")
	}
	base, err := gatewayURL(endpoint)
	if err != nil {
		return nil, nil, err
	}
	dial := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	if home.IsStandalone() {
		if err := home.RemoteAccess.ValidateURL(endpoint); err != nil {
			return nil, nil, err
		}
		dial = home.RemoteAccess.DialContext
	}
	secret, err := home.Secrets(r.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("read gateway credentials: %w", err)
	}
	if secret.Labels[gatewayCredentialsLabel] != "true" {
		return nil, nil, errors.New("gateway credential Secret is not labeled for gateway access")
	}
	cert, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid gateway client certificate or key: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(secret.Data["ca.crt"]) {
		return nil, nil, errors.New("invalid gateway CA bundle")
	}
	server, err := k.GetServer(ctx, target.namespace, target.name)
	if err != nil {
		return nil, nil, fmt.Errorf("read gateway target: %w", err)
	}
	if err := rbac.ValidateServerIdentity(ctx, cluster, target.namespace, target.name, string(server.GetUID())); err != nil {
		return nil, nil, apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), target.name)
	}
	if server.GetUID() == "" {
		return nil, nil, errors.New("gateway target has no UID")
	}
	transport := &gatewayAgentTransport{
		base:   base,
		target: gatewayprotocol.Target{Cluster: cluster, Namespace: target.namespace, Name: target.name, UID: string(server.GetUID())},
		http: &http.Client{
			Transport: &http.Transport{
				DialContext:         dial,
				TLSHandshakeTimeout: 10 * time.Second,
				TLSClientConfig:     &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: roots, MinVersion: tls.VersionTLS12},
				// One resolved transport belongs to one operation. Do not retain
				// idle TLS sessions using credentials after a Secret rotation.
				DisableKeepAlives:     true,
				ResponseHeaderTimeout: 30 * time.Second,
			},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	return k, transport, nil
}

func gatewayURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return nil, errors.New("gateway URL must be an HTTPS origin without userinfo, query, fragment, or path")
	}
	u.Path = ""
	return u, nil
}

type gatewayAgentTransport struct {
	http   *http.Client
	base   *url.URL
	target gatewayprotocol.Target
}

func (t *gatewayAgentTransport) endpoint(target agentTarget, method, path, rawQuery string, websocketRoute bool) (string, error) {
	if err := target.validate(); err != nil {
		return "", err
	}
	if target.name != t.target.Name || target.namespace != t.target.Namespace {
		return "", errors.New("gateway target mismatch")
	}
	if _, ok := gatewayprotocol.Allowed(method, path); !ok {
		return "", errors.New("unsupported gateway operation")
	}
	gatewayPath, err := gatewayprotocol.Path(t.target, path)
	if err != nil {
		return "", err
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", errors.New("invalid agent query")
	}
	// These selectors have already been resolved and are not agent parameters.
	query.Del("cluster")
	query.Del("namespace")
	u := *t.base
	u.Path, u.RawQuery = gatewayPath, query.Encode()
	if websocketRoute {
		u.Scheme = "wss"
	}
	return u.String(), nil
}

func (t *gatewayAgentTransport) Do(ctx context.Context, operation agentRequest) (*http.Response, error) {
	endpoint, err := t.endpoint(operation.target, operation.method, operation.path, operation.rawQuery, false)
	if err != nil {
		return nil, fmt.Errorf("build gateway agent endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, operation.method, endpoint, operation.body)
	if err != nil {
		return nil, fmt.Errorf("build gateway agent request: %w", err)
	}
	copyProxyHeaders(req.Header, operation.header)
	client := t.http
	if gatewayprotocol.LongRunningOperation(operation.method, operation.path) {
		if transport, ok := client.Transport.(*http.Transport); ok {
			// A slow install may not send headers until extraction completes.
			// Clone before changing policy so concurrent ordinary requests keep
			// their header limit and all requests keep their TLS/redirect policy.
			transport = transport.Clone()
			transport.ResponseHeaderTimeout = 0
			defer transport.CloseIdleConnections()
			copyClient := *client
			copyClient.Transport = transport
			client = &copyClient
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send gateway agent request: %w", err)
	}
	return resp, nil
}

func (t *gatewayAgentTransport) Dial(ctx context.Context, target agentTarget, path string, rawQuery string) (*websocket.Conn, *http.Response, error) {
	// Filter query parameters: only allow "from" and "tail" keys for agent operations.
	filteredQuery := filterAllowedQueryParams(rawQuery, []string{"from", "tail"})
	endpoint, err := t.endpoint(target, http.MethodGet, path, filteredQuery, true)
	if err != nil {
		return nil, nil, err
	}
	return websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: t.http})
}

// agentRoute resolves one immutable proxy per request, keeping concurrent local
// and remote requests independent. The local adapter remains available when the
// optional gateway registration is absent.
func (p *proxy) agentRoute(handler func(*proxy) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		cluster := strings.TrimSpace(req.URL.Query().Get("cluster"))
		if cluster == "" || cluster == scope.DefaultCluster {
			if p.k == nil || p.k.Dynamic == nil {
				http.Error(w, "cluster unavailable", http.StatusServiceUnavailable)
				return
			}
			ns, err := scope.Resolve(req)
			if err != nil {
				httperr.Write(w, req, err)
				return
			}
			target, err := localAgentTarget(req.Context(), p.k, agentTarget{name: chi.URLParam(req, "name"), namespace: ns})
			if err != nil {
				httperr.Write(w, req, err)
				return
			}
			selected := *p
			selected.remoteUID = target.uid
			handler(&selected)(w, req)
			return
		}
		if p.gateway == nil || p.gateway.registry == nil {
			httperr.WriteRemoteClusterNotImplemented(w)
			return
		}
		if _, ok := p.gateway.registry.Get(cluster); !ok {
			http.NotFound(w, req)
			return
		}
		ns, err := scope.Resolve(req)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		target := agentTarget{name: chi.URLParam(req, "name"), namespace: ns}
		if err := target.validate(); err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, err)
			return
		}
		k, transport, err := p.gateway.resolve(req.Context(), cluster, target)
		if err != nil {
			if errors.Is(err, errGatewayUnavailable) {
				http.Error(w, "agent gateway not configured", http.StatusServiceUnavailable)
				return
			}
			httperr.Write(w, req, err)
			return
		}
		selected := *p
		selected.k, selected.stdin, selected.transport = k, k, transport
		selected.remoteUID = transport.target.UID
		handler(&selected)(w, req)
	}
}

func (p *proxy) agentHTTP(path string) http.HandlerFunc {
	return p.agentRoute(func(selected *proxy) http.HandlerFunc { return selected.httpProxy(path) })
}
func (p *proxy) agentHTTPLimit(path string, limit int64) http.HandlerFunc {
	return p.agentRoute(func(selected *proxy) http.HandlerFunc { return selected.httpProxyLimit(path, limit) })
}
func (p *proxy) agentWS(path string) http.HandlerFunc {
	return p.agentRoute(func(selected *proxy) http.HandlerFunc { return selected.wsProxy(path) })
}
func (p *proxy) agentAction() http.HandlerFunc {
	return p.agentRoute(func(selected *proxy) http.HandlerFunc { return selected.runAction })
}
