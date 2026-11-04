package main

// Playit address discovery (F-174, OD-026 option a).
//
// playit.gg assigns a tunnel's public address server-side, so -- unlike frp
// and Tailscale, whose addresses the operator computes from the spec -- the
// supervisor has to learn it at runtime and report it back. This file polls
// playitd's local IPC control socket for the assigned addresses and patches
// them into the GameServer's status.tunnelEndpoints; the operator validates
// those entries (validatePlayitEndpoints in
// operator/internal/controller/gameserver_status.go) and merges them into
// status.endpoints, exactly like the endpoints it computes for frp/tailscale.
//
// IPC protocol, established from playit-cloud/playit-agent at the pinned
// v1.0.10 (packages/playit-ipc/src/ipc.rs + model.rs, and
// packages/playitd/src/ipc_server.rs):
//
//   - Transport: a Unix-domain stream socket at the path given by playitd's
//     --socket-path flag (default /run/playit/playitd.sock on Linux, which a
//     non-root distroless container cannot create -- hence the explicit
//     /tmp path below). playitd chmods it 0660 owned by its own uid, so this
//     supervisor (same uid 65532) can connect.
//   - Framing: newline-delimited JSON (tokio LinesCodec), one object per line.
//   - On connect the server immediately writes a hello frame:
//     {"message_kind":"hello","data":{"protocol":{"ipc_version":2,"capabilities":[...]}}}
//   - Client request: {"ipc_version":2,"request_id":N,"request":{"type":"get_state"}}
//   - Server response:
//     {"message_kind":"response","data":{"ipc_version":2,"request_id":N,
//     "response":{"type":"state","data":<AgentLifecycle>}}}
//     where AgentLifecycle is {"state":"starting"} (and other unit states) or
//     {"state":"running","data":{"tunnels":[{"display_address":"host:port",
//     "destination":"ip:port","is_disabled":false,"disabled_reason":null}],...}}.
//     An error is {"type":"error","data":{"code":...,"message":...}}.
//   - Event frames ("message_kind":"event") are only sent after a subscribe
//     request, which this client never makes; they are skipped defensively.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// playitSocketPath is the IPC socket playitd is told to listen on via
	// --socket-path. A compile-time constant for the same G204 reason as
	// the other fixed paths in main.go.
	playitSocketPath = "/tmp/gameplane-tunnel-playitd.sock"

	// playitIPCVersion is playit-ipc's IPC_VERSION at v1.0.10. A server that
	// reports another version is treated as an error rather than guessed at.
	playitIPCVersion = 2

	// playitIOTimeout bounds one whole query (dial, hello, request, reply).
	playitIOTimeout = 5 * time.Second

	// playitMaxFrameBytes caps one JSON line; a state frame is a few KiB.
	playitMaxFrameBytes = 1 << 20

	// maxTunnelEndpoints mirrors the CRD's MaxItems=32 on tunnelEndpoints.
	maxTunnelEndpoints = 32

	// serviceAccountMountDir is the standard in-cluster ServiceAccount
	// mount (projected token, CA bundle).
	serviceAccountMountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

	// kubeAPIServerURL is the in-cluster API server address. The
	// kubernetes.default.svc name is in the API server certificate's SANs
	// on every conformant cluster.
	kubeAPIServerURL = "https://kubernetes.default.svc"
)

// playitTunnel is one entry of AgentState.tunnels.
type playitTunnel struct {
	DisplayAddress string `json:"display_address"`
	Destination    string `json:"destination"`
	IsDisabled     bool   `json:"is_disabled"`
}

// playitState is the decoded result of one get_state query.
type playitState struct {
	// Phase is the AgentLifecycle state tag ("running", "starting", ...).
	Phase   string
	Tunnels []playitTunnel
}

// tunnelEndpoint is the JSON shape of a GameServerEndpoint entry in
// status.tunnelEndpoints (operator/api/v1alpha1/gameserver_types.go).
type tunnelEndpoint struct {
	Name           string `json:"name"`
	Host           string `json:"host"`
	Port           int32  `json:"port"`
	TunnelProvider string `json:"tunnelProvider,omitempty"`
}

// queryPlayitState connects to playitd's IPC socket, performs one get_state
// request, and returns the decoded lifecycle.
func queryPlayitState(ctx context.Context, socketPath string) (playitState, error) {
	ctx, cancel := context.WithTimeout(ctx, playitIOTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return playitState{}, fmt.Errorf("dial playitd socket %s: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return playitState{}, fmt.Errorf("set playitd socket deadline: %w", err)
		}
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), playitMaxFrameBytes)

	var hello struct {
		MessageKind string `json:"message_kind"`
		Data        struct {
			Protocol struct {
				IPCVersion int `json:"ipc_version"`
			} `json:"protocol"`
		} `json:"data"`
	}
	if err := readFrame(sc, &hello); err != nil {
		return playitState{}, fmt.Errorf("read playitd hello: %w", err)
	}
	if hello.MessageKind != "hello" {
		return playitState{}, fmt.Errorf("playitd sent %q frame, want hello", hello.MessageKind)
	}
	if hello.Data.Protocol.IPCVersion != playitIPCVersion {
		return playitState{}, fmt.Errorf("playitd IPC version %d, want %d", hello.Data.Protocol.IPCVersion, playitIPCVersion)
	}

	const requestID = 1
	req, err := json.Marshal(map[string]any{
		"ipc_version": playitIPCVersion,
		"request_id":  requestID,
		"request":     map[string]string{"type": "get_state"},
	})
	if err != nil {
		return playitState{}, fmt.Errorf("encode get_state request: %w", err)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return playitState{}, fmt.Errorf("write get_state request: %w", err)
	}

	for {
		var frame struct {
			MessageKind string `json:"message_kind"`
			Data        struct {
				IPCVersion int `json:"ipc_version"`
				RequestID  int `json:"request_id"`
				Response   struct {
					Type string          `json:"type"`
					Data json.RawMessage `json:"data"`
				} `json:"response"`
			} `json:"data"`
		}
		if err := readFrame(sc, &frame); err != nil {
			return playitState{}, fmt.Errorf("read get_state response: %w", err)
		}
		if frame.MessageKind == "event" {
			continue
		}
		if frame.MessageKind != "response" {
			return playitState{}, fmt.Errorf("playitd sent unexpected %q frame", frame.MessageKind)
		}
		if frame.Data.RequestID != requestID {
			return playitState{}, fmt.Errorf("playitd response id %d, want %d", frame.Data.RequestID, requestID)
		}
		return decodePlayitResponse(frame.Data.Response.Type, frame.Data.Response.Data)
	}
}

// readFrame reads one newline-delimited JSON frame into v.
func readFrame(sc *bufio.Scanner, v any) error {
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return fmt.Errorf("scan frame: %w", err)
		}
		return io.ErrUnexpectedEOF
	}
	if err := json.Unmarshal(sc.Bytes(), v); err != nil {
		return fmt.Errorf("decode frame: %w", err)
	}
	return nil
}

// decodePlayitResponse turns a ServiceResponse body into a playitState.
func decodePlayitResponse(respType string, data json.RawMessage) (playitState, error) {
	switch respType {
	case "state":
		var lifecycle struct {
			State string `json:"state"`
			Data  struct {
				Tunnels []playitTunnel `json:"tunnels"`
			} `json:"data"`
		}
		// Unit lifecycle states carry no "data"; error states carry a
		// ServiceError object there, which the anonymous struct above simply
		// decodes to zero tunnels.
		if err := json.Unmarshal(data, &lifecycle); err != nil {
			return playitState{}, fmt.Errorf("decode playitd lifecycle: %w", err)
		}
		if lifecycle.State == "" {
			return playitState{}, errors.New("playitd lifecycle has no state")
		}
		return playitState{Phase: lifecycle.State, Tunnels: lifecycle.Data.Tunnels}, nil
	case "error":
		var svcErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &svcErr); err != nil {
			return playitState{}, fmt.Errorf("decode playitd error: %w", err)
		}
		return playitState{}, fmt.Errorf("playitd error %s: %s", svcErr.Code, svcErr.Message)
	default:
		return playitState{}, fmt.Errorf("playitd sent unexpected %q response to get_state", respType)
	}
}

// backingPort is one "name:port" entry of BACKING_SERVICE_PORTS.
type backingPort struct {
	name string
	port int32
}

// parsePort parses a decimal TCP/UDP port number (1-65535).
func parsePort(s string) (int32, bool) {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil || v < 1 || v > 65535 {
		return 0, false
	}
	return int32(v), true
}

// parseBackingPorts parses BACKING_SERVICE_PORTS ("name:port,...").
func parseBackingPorts(spec string) ([]backingPort, error) {
	var out []backingPort
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, portStr, ok := strings.Cut(entry, ":")
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid backing port entry %q", entry)
		}
		port, ok := parsePort(portStr)
		if !ok {
			return nil, fmt.Errorf("invalid port in backing port entry %q", entry)
		}
		out = append(out, backingPort{name: name, port: port})
	}
	return out, nil
}

// destinationPort extracts the local port from a TunnelState.destination.
// playitd formats port tunnels as "<ip-or-host>:<port>" and HTTPS tunnels as
// "<ip> (http: N, https: M)"; the latter yields ok=false.
func destinationPort(dest string) (int32, bool) {
	i := strings.LastIndex(dest, ":")
	if i < 0 {
		return 0, false
	}
	return parsePort(dest[i+1:])
}

// playitEndpoints maps playitd's tunnels onto the template's advertised port
// names. playitd's TunnelState carries no tunnel name, so each tunnel is
// matched to a backing port by its destination (local) port. When exactly
// one port is advertised and exactly one enabled tunnel exists, that pair is
// matched even if the ports differ (the playit dashboard may point it at the
// Service on a different port). A display address without a port (a playit
// hostname served via an SRV record) is reported with the backing port.
// The result is sorted by name for stable comparison.
func playitEndpoints(tunnels []playitTunnel, ports []backingPort) []tunnelEndpoint {
	var enabled []playitTunnel
	for _, t := range tunnels {
		if !t.IsDisabled && t.DisplayAddress != "" {
			enabled = append(enabled, t)
		}
	}

	seen := make(map[string]bool)
	var out []tunnelEndpoint
	for _, t := range enabled {
		var match *backingPort
		if p, ok := destinationPort(t.Destination); ok {
			for i := range ports {
				if ports[i].port == p {
					match = &ports[i]
					break
				}
			}
		}
		if match == nil && len(ports) == 1 && len(enabled) == 1 {
			match = &ports[0]
		}
		if match == nil || seen[match.name] {
			continue
		}

		host, port := t.DisplayAddress, match.port
		if h, p, err := net.SplitHostPort(t.DisplayAddress); err == nil {
			n, ok := parsePort(p)
			if !ok {
				continue
			}
			host, port = h, n
		}
		seen[match.name] = true
		out = append(out, tunnelEndpoint{
			Name:           match.name,
			Host:           host,
			Port:           port,
			TunnelProvider: "playit",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > maxTunnelEndpoints {
		out = out[:maxTunnelEndpoints]
	}
	return out
}

// statusPatcher writes the tunnel endpoints into the GameServer's status.
type statusPatcher interface {
	PatchTunnelEndpoints(ctx context.Context, eps []tunnelEndpoint) error
}

// kubeStatusPatcher patches gameservers/status with the pod's projected
// ServiceAccount token over plain HTTPS, keeping the module stdlib-only.
// The operator's per-GameServer Role (tunnel_rbac.go) allows exactly this
// verb on exactly this object.
type kubeStatusPatcher struct {
	baseURL   string // kubeAPIServerURL in production; an httptest URL in tests
	namespace string
	name      string
	caPath    string
	tokenPath string
	client    *http.Client
}

// newInClusterStatusPatcher builds a patcher from the ServiceAccount mount
// under saDir (serviceAccountMountDir in production).
func newInClusterStatusPatcher(saDir, namespace, name string) (*kubeStatusPatcher, error) {
	p := &kubeStatusPatcher{
		baseURL:   kubeAPIServerURL,
		namespace: namespace,
		name:      name,
		caPath:    filepath.Join(saDir, "ca.crt"),
		tokenPath: filepath.Join(saDir, "token"),
	}
	pool, err := p.loadCAPool()
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}
	p.client = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	return p, nil
}

// loadCAPool reads the ServiceAccount CA bundle from the path recorded on p
// and parses it into a certificate pool.
func (p *kubeStatusPatcher) loadCAPool() (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(p.caPath)
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("service account CA contains no certificates")
	}
	return pool, nil
}

// PatchTunnelEndpoints merge-patches status.tunnelEndpoints. An empty set is
// sent as null, which removes the field so stale addresses from a previous
// pod don't linger.
func (p *kubeStatusPatcher) PatchTunnelEndpoints(ctx context.Context, eps []tunnelEndpoint) error {
	var list any
	if len(eps) > 0 {
		list = eps
	}
	body, err := json.Marshal(map[string]any{"status": map[string]any{"tunnelEndpoints": list}})
	if err != nil {
		return fmt.Errorf("encode status patch: %w", err)
	}
	// Re-read the token each time: projected tokens rotate.
	token, err := os.ReadFile(p.tokenPath)
	if err != nil {
		return fmt.Errorf("read service account token: %w", err)
	}
	target := fmt.Sprintf("%s/apis/gameplane.local/v1alpha1/namespaces/%s/gameservers/%s/status",
		p.baseURL, url.PathEscape(p.namespace), url.PathEscape(p.name))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build status patch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("patch gameserver status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("patch gameserver status: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// reporterTiming holds the poll cadence; a variable so tests can shrink it.
type reporterTiming struct {
	minBackoff time.Duration // first retry delay after a failure / not-running
	maxBackoff time.Duration // retry delay cap
	steady     time.Duration // poll interval once the address is reported
}

var defaultReporterTiming = reporterTiming{
	minBackoff: 1 * time.Second,
	maxBackoff: 30 * time.Second,
	steady:     30 * time.Second,
}

// runPlayitReporter polls playitd until ctx ends, patching status whenever
// the discovered endpoint set changes. Failures (socket not up yet, playitd
// restarting, API errors) back off exponentially from minBackoff to
// maxBackoff; once a set is reported, polling continues every steady
// interval so a changed or removed address is picked up.
func runPlayitReporter(ctx context.Context, socketPath string, ports []backingPort, patcher statusPatcher, timing reporterTiming) {
	var last []tunnelEndpoint
	reported := false
	delay := timing.minBackoff

	for {
		wait := delay
		state, err := queryPlayitState(ctx, socketPath)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			log.Print("playit reporter: local provider state unavailable")
			delay = min(delay*2, timing.maxBackoff)
		case state.Phase != "running":
			// Not yet assigned (starting, waiting_for_secret, ...): keep
			// backing off without touching status.
			delay = min(delay*2, timing.maxBackoff)
		default:
			eps := playitEndpoints(state.Tunnels, ports)
			if !reported || !reflect.DeepEqual(eps, last) {
				if err := patcher.PatchTunnelEndpoints(ctx, eps); err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Print("playit reporter: endpoint status update failed")
					delay = min(delay*2, timing.maxBackoff)
					break
				}
				log.Printf("playit reporter: reported %d endpoint(s)", len(eps))
				last, reported = eps, true
			}
			delay = timing.minBackoff
			wait = timing.steady
		}

		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// startPlayitReporter launches the reporter goroutine for a playit tunnel
// and returns a function that waits for it to exit. If the in-cluster
// client can't be built the reporter is skipped (logged): the relay itself
// still works, only the address report is missing.
func startPlayitReporter(ctx context.Context, cfg Config) (wait func()) {
	noop := func() {}
	ports, err := parseBackingPorts(cfg.BackingServicePorts)
	if err != nil {
		log.Printf("playit reporter disabled: %v", err)
		return noop
	}
	patcher, err := newInClusterStatusPatcher(serviceAccountMountDir, cfg.GameServerNamespace, cfg.GameServerName)
	if err != nil {
		log.Printf("playit reporter disabled: %v", err)
		return noop
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPlayitReporter(ctx, playitSocketPath, ports, patcher, defaultReporterTiming)
	}()
	return func() { <-done }
}
