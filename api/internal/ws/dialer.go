// Package ws proxies WebSocket connections from the dashboard to the
// agent running in a game pod. Auth to the agent is via mTLS (the API
// loads its client cert from a Secret mounted by the Helm chart).
package ws

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// Mount attaches the WS/file proxy routes under /ws and /servers/:name/files.
func Mount(r chi.Router, reg *kube.Registry, caBundle, clientCert, clientKey string, gatewayOptions ...AgentGatewayOptions) {
	MountWithPlayerNames(r, reg, caBundle, clientCert, clientKey, nil, gatewayOptions...)
}

// MountWithPlayerNames is Mount plus an optional Steam display-name resolver
// for the player list. A nil names resolver leaves the list as raw Steam IDs.
func MountWithPlayerNames(r chi.Router, reg *kube.Registry, caBundle, clientCert, clientKey string, names PlayerNameResolver, gatewayOptions ...AgentGatewayOptions) {
	var k *kube.Client
	if reg != nil {
		k = reg.Default()
	}
	tlsCfg, err := agentTLSConfig(caBundle, clientCert, clientKey)
	if err != nil {
		// Allow startup without mTLS in dev — every request 503s until
		// the chart's mTLS hook populates the Secrets.
		tlsCfg = nil
	}
	var transport agentTransport
	if tlsCfg != nil {
		transport = newDirectAgentTransport(tlsCfg, 0)
	}

	p := &proxy{k: k, transport: transport, stdin: k, playerNames: names}
	if len(gatewayOptions) > 0 && gatewayOptions[0].Namespace != "" && reg != nil {
		p.gateway = &agentGatewayResolver{registry: reg, namespace: gatewayOptions[0].Namespace}
	}
	r.Get("/ws/servers/{name}/console", p.agentWS("/console"))
	r.Get("/ws/servers/{name}/logs", p.agentWS("/logs/tail"))
	r.Get("/servers/{name}/logs/download", p.agentHTTP("/logs/download"))
	// PTY console attaches via the Kubernetes API (not the agent), so it
	// doesn't need agent mTLS material — it uses the selected cluster's
	// kubeconfig. Mounted unconditionally.
	mountAttach(r, reg)
	// Startup logs stream the game container's stdout via the pod-log API
	// (also no agent mTLS needed), so download/config output is visible
	// before the game's own log file exists. Mounted unconditionally.
	mountPodLogs(r, reg)
	r.Route("/servers/{name}/files", func(r chi.Router) {
		r.Get("/list", p.agentHTTP("/files/list"))
		r.Get("/read", p.agentHTTP("/files/read"))
		r.Get("/download", p.agentHTTP("/files/download"))
		r.Post("/write", p.agentHTTP("/files/write"))
		r.Post("/upload", p.agentHTTP("/files/upload"))
		r.Post("/mkdir", p.agentHTTP("/files/mkdir"))
		r.Delete("/delete", p.agentHTTP("/files/delete"))
	})
	r.Route("/servers/{name}/players", func(r chi.Router) {
		// Hydrated with Steam display names when a resolver is configured;
		// every other case is the agent's bytes verbatim (players_hydrate.go).
		r.Get("/", p.agentRoute(func(selected *proxy) http.HandlerFunc { return selected.hydratedPlayers("/players") }))
		r.Get("/banned", p.agentHTTP("/players/banned"))
		r.Post("/kick", p.agentHTTP("/players/kick"))
		r.Post("/ban", p.agentHTTP("/players/ban"))
		r.Post("/unban", p.agentHTTP("/players/unban"))
		r.Get("/whitelist", p.agentHTTP("/players/whitelist"))
		r.Post("/whitelist/add", p.agentHTTP("/players/whitelist/add"))
		r.Post("/whitelist/remove", p.agentHTTP("/players/whitelist/remove"))
	})
	// Module-declared operator actions and live status metrics. RBAC
	// (api/internal/rbac) gates these by the same method+segment rules as
	// the rest of /servers: the action run is a POST → operator+, while
	// the status read is a GET → viewer+. Unlike every other route here,
	// actions/run is not a pure proxy: it fetches the template, resolves
	// the action's transport, and either proxies to the agent (rcon) or
	// executes here via pods/attach (stdin) — see actions.go.
	r.Post("/servers/{name}/actions/run", p.agentAction())
	r.Get("/servers/{name}/status", p.agentHTTP("/status"))
	// Mod/plugin management. Listing is a GET → viewer+; install (POST)
	// and remove (DELETE) are mutations → operator+, by the same rbac
	// method+segment rules as the rest of /servers. Upload gets its own
	// body cap matching the largest module install policy (the agent still
	// enforces the module's real per-file limit).
	r.Get("/servers/{name}/mods", p.agentHTTP("/mods"))
	r.Post("/servers/{name}/mods/install", p.agentHTTP("/mods/install"))
	r.Post("/servers/{name}/mods/upload", p.agentHTTPLimit("/mods/upload", 512<<20))
	r.Delete("/servers/{name}/mods", p.agentHTTP("/mods"))
}

// isDNS1123Label reports whether name is a valid DNS-1123 label (valid
// Kubernetes resource name component). Namespace and pod names flow into a
// constructed proxy URL, so both are validated against this rule before use
// to prevent SSRF injection. A label must consist of lower-case alphanumerics
// and hyphens, start and end with an alphanumeric, and be at most 63
// characters long.
func isDNS1123Label(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
		if i == 0 || i == len(name)-1 {
			if r == '-' {
				return false
			}
		}
	}
	return true
}

// rejectRemoteCluster remains a fail-closed guard for consumers without an
// explicitly configured remote agent resolver. The central API's agentRoute
// replaces it only after binding a request to a registered remote gateway.
func rejectRemoteCluster(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if c := strings.TrimSpace(req.URL.Query().Get("cluster")); c != "" && c != scope.DefaultCluster {
			httperr.WriteRemoteClusterNotImplemented(w)
			return
		}
		next(w, req)
	}
}

type proxy struct {
	k         *kube.Client
	transport agentTransport
	gateway   *agentGatewayResolver
	// remoteUID pins both local and remote operations to the resolved server.
	remoteUID string
	// stdin executes the stdin-transport branch of runAction. Defaults to
	// k (see Mount) but is a separate interface field so tests can inject
	// a fake that records writes instead of attaching to a real pod.
	stdin stdinWriter
	// playerNames, when non-nil, hydrates structured player lists with
	// display names. It never reaches a moderation route (kick/ban/unban).
	playerNames PlayerNameResolver
}

func (p *proxy) wsProxy(agentPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if p.transport == nil {
			http.Error(w, "agent mTLS not configured", http.StatusServiceUnavailable)
			return
		}
		name := chi.URLParam(req, "name")
		ns, err := scope.Resolve(req)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		target := agentTarget{name: name, namespace: ns, uid: p.remoteUID}
		// Validate before opening an upstream or upgrading the browser connection.
		if err := target.validate(); err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, err)
			return
		}
		downConn, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = downConn.Close(websocket.StatusNormalClosure, "") }()

		// Filter query parameters: only allow "from" and "tail" keys for agent operations.
		// This prevents callers from injecting unauthorized parameters (e.g., cluster, ns).
		filteredQuery := filterAllowedQueryParams(req.URL.RawQuery, []string{"from", "tail"})
		upConn, upResp, err := p.transport.Dial(req.Context(), target, agentPath, filteredQuery)
		if upResp != nil && upResp.Body != nil {
			_ = upResp.Body.Close()
		}
		if err != nil {
			// The upgrade to the browser already succeeded (Console.tsx
			// printed "— connected —"), so the close reason below isn't
			// visible to it. Send a structured error frame first, matching
			// the agent's {kind,body} envelope, so the console shows a real
			// message instead of a bare disconnect.
			_ = downConn.Write(req.Context(), websocket.MessageText,
				[]byte(`{"kind":"err","body":"agent unreachable"}`))
			_ = downConn.Close(websocket.StatusBadGateway, "agent dial failed")
			return
		}
		defer func() { _ = upConn.Close(websocket.StatusNormalClosure, "") }()

		// Bidirectional copy.
		errCh := make(chan error, 2)
		go func() { errCh <- copyWS(req, downConn, upConn) }()
		go func() { errCh <- copyWS(req, upConn, downConn) }()
		<-errCh
	}
}

func copyWS(req *http.Request, src, dst *websocket.Conn) error {
	for {
		typ, data, err := src.Read(req.Context())
		if err != nil {
			return err
		}
		if err := dst.Write(req.Context(), typ, data); err != nil {
			return err
		}
	}
}

// httpProxy forwards a request to the agent with the default 64 MiB body
// cap — enough for modest uploads through the file-browser proxy while
// blocking unbounded spam.
func (p *proxy) httpProxy(agentPath string) http.HandlerFunc {
	return p.httpProxyLimit(agentPath, 64<<20)
}

// httpProxyLimit is httpProxy with an explicit request-body cap, for the
// few routes that legitimately carry more (mod uploads).
func (p *proxy) httpProxyLimit(agentPath string, maxBody int64) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if p.transport == nil {
			http.Error(w, "agent mTLS not configured", http.StatusServiceUnavailable)
			return
		}
		name := chi.URLParam(req, "name")
		ns, err := scope.Resolve(req)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		target := agentTarget{name: name, namespace: ns, uid: p.remoteUID}
		// Validate before opening an upstream or upgrading the browser connection.
		if err := target.validate(); err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, err)
			return
		}
		// Forward only headers the agent actually consumes. Never proxy
		// Cookie, Authorization, X-Gameplane-CSRF — those are the user's
		// session material and the agent doesn't need them (mTLS is what
		// it authenticates on). Leaking them to agent logs or a
		// compromised sidecar would hand over live session tokens.
		resp, err := p.transport.Do(req.Context(), agentRequest{
			target: target, method: req.Method, path: agentPath,
			rawQuery: req.URL.RawQuery, header: req.Header,
			body: http.MaxBytesReader(w, req.Body, maxBody),
		})
		if err != nil {
			writeUpstreamErr(w, req, err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

// writeUpstreamErr maps transport-level failures on the API→agent leg
// to gateway statuses so the dashboard can tell "agent down" (502/504)
// apart from API bugs (500). The dashboard's error handling and
// TestAPI_AgentUnreachable both rely on this distinction.
func writeUpstreamErr(w http.ResponseWriter, req *http.Request, err error) {
	status := http.StatusBadGateway
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		status = http.StatusGatewayTimeout
	}
	slog.Error("agent proxy upstream error",
		"method", req.Method, "path", req.URL.Path, "status", status, "err", err)
	http.Error(w, "agent unreachable", status)
}

// proxyHeaderAllowlist is the set of request headers forwarded from the
// dashboard-facing API to the in-cluster agent. Anything not in this
// set is dropped — most importantly Cookie, Authorization, and the CSRF
// header, which are user-session material the agent doesn't need.
var proxyHeaderAllowlist = map[string]bool{
	"Accept":              true,
	"Accept-Encoding":     true,
	"Content-Type":        true,
	"Content-Length":      true,
	"Content-Encoding":    true,
	"Content-Disposition": true,
	"Range":               true,
	"If-None-Match":       true,
	"If-Modified-Since":   true,
}

// responseHeaderDenylist strips hop-by-hop and auth-adjacent response
// headers from the agent before passing the body through to the browser.
var responseHeaderDenylist = map[string]bool{
	"Set-Cookie":         true,
	"Www-Authenticate":   true,
	"Proxy-Authenticate": true,
	"Connection":         true,
	"Keep-Alive":         true,
	"Transfer-Encoding":  true,
	"Upgrade":            true,
}

func copyProxyHeaders(dst, src http.Header) {
	for k, v := range src {
		if !proxyHeaderAllowlist[http.CanonicalHeaderKey(k)] {
			continue
		}
		dst[http.CanonicalHeaderKey(k)] = v
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for k, v := range src {
		if responseHeaderDenylist[http.CanonicalHeaderKey(k)] {
			continue
		}
		dst[http.CanonicalHeaderKey(k)] = v
	}
}

func agentTLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	if caFile == "" || certFile == "" || keyFile == "" {
		return nil, errors.New("mTLS material missing")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	// caFile is operator configuration (a CLI flag / mounted secret path),
	// not user input; filepath.Clean here normalises the path but does not
	// constrain it to a directory.
	ca, err := os.ReadFile(filepath.Clean(caFile))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("empty CA bundle")
	}
	// ServerName is left unset on purpose: the dialer derives it from
	// the wss:// URL's host, which is the pod FQDN and the authority
	// that the agent's server cert is issued for.
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
