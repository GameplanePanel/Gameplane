// Command sentinel is the wake-on-connect daemon that runs as a pod in
// place of a sleeping game server. It listens on the server's advertised
// ports, classifies inbound connections using gameproto, and — for a real
// join attempt — patches the GameServer's wake-request annotation and holds
// the connection open until the operator brings the real game pod up, then
// hands the connection through.
//
// Configuration and contract (see operator/internal/controller/gameserver_sentinel.go
// on the operator side, which is authoritative):
//
//   - Env: GAMESERVER_NAME, GAMESERVER_NAMESPACE, PORTS_CONFIG.
//   - PORTS_CONFIG is a comma-separated "port:protocol:wakeProtocol" list,
//     e.g. "25565:TCP:minecraft,19133:UDP:generic". Stray/leading/trailing
//     commas are tolerated.
//   - Waking a server means patching the GameServer annotation
//     "gameplane.local/idle-wake-requested" with a fresh token via a JSON
//     merge patch, so any other annotations already on the object survive.
//     The RBAC the operator grants this pod's ServiceAccount is exactly
//     "get" + "patch" on that one named GameServer — nothing else.
//   - The real game pod, once up, is reachable at
//     "<GAMESERVER_NAME>-game-direct.<GAMESERVER_NAMESPACE>.svc.cluster.local".
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/GameplanePanel/gameplane/gameproto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Version is overridden at build time via -ldflags (see Dockerfile).
var Version = "dev"

const (
	// idleWakeRequestedAnnotation must match
	// operator/internal/controller/gameserver_idle.go's
	// IdleWakeRequestedAnnotation exactly: the operator watches for this
	// annotation to change to know a wake was requested.
	idleWakeRequestedAnnotation = "gameplane.local/idle-wake-requested"

	// wakingUpMessage is the protocol-native disconnect/bounce reason sent
	// to a client that gave up (deadline expired) waiting for the real
	// game pod to come up.
	wakingUpMessage = "Server is waking up, try again in a moment."

	// minecraftAsleepStatusJSON is the server-list ping reply sent for a
	// Status handshake without ever waking the server.
	minecraftAsleepStatusJSON = `{"version":{"name":"Asleep","protocol":0},"players":{"max":0,"online":0,"sample":[]},"description":{"text":"Asleep — joining wakes it"}}`

	// classifyReadTimeout bounds how long the sentinel waits for a client
	// to finish sending its handshake before giving up and closing.
	classifyReadTimeout = 5 * time.Second

	// defaultPollInterval is how often waitForUpstream retries dialing the
	// game-direct Service while holding a connection open.
	defaultPollInterval = 250 * time.Millisecond

	// defaultShutdownDrainTimeout bounds how long the sentinel keeps
	// in-flight sessions alive after it is told to shut down (see
	// Config.ShutdownDrainTimeout). It must match the operator's
	// sentinelShutdownDrainTimeout in gameserver_sentinel.go, which also
	// sizes the pod's terminationGracePeriodSeconds to cover it.
	defaultShutdownDrainTimeout = 4 * time.Hour
)

// gameServerGVR identifies GameServer custom resources. A dynamic client
// against this GVR is all the sentinel needs — the RBAC Role the operator
// grants only allows get/patch on the resource, not its status subresource,
// and only for the one named GameServer this pod belongs to.
var gameServerGVR = schema.GroupVersionResource{
	Group:    "gameplane.local",
	Version:  "v1alpha1",
	Resource: "gameservers",
}

func main() {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	dynClient, err := newInClusterDynamicClient()
	if err != nil {
		log.Fatalf("kubernetes client: %v", err)
	}
	w := newWaker(dynClient.Resource(gameServerGVR).Namespace(cfg.GameServerNamespace), cfg.GameServerName, cfg.WakePatchInterval)

	log.Printf("sentinel %s starting for %s/%s", Version, cfg.GameServerNamespace, cfg.GameServerName)

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Printf("received signal %v, shutting down", sig)
		cancel()
	}()

	err = run(ctx, cfg, w)
	cancel()
	signal.Stop(sigCh)
	if err != nil {
		// Exit non-zero so the container restarts (and the restart count
		// makes the failure visible) instead of the pod staying Ready
		// with a dead port.
		log.Fatalf("sentinel exiting: %v", err)
	}
}

// Config holds the sentinel's parsed configuration.
type Config struct {
	GameServerName      string
	GameServerNamespace string
	Ports               []PortConfig

	// WakeDeadline bounds how long a held connection waits for the real
	// game pod to come up before it is bounced/closed. Must stay below a
	// game client's own connection timeout (Minecraft/Terraria are both
	// around 30s) or the client gives up first and never sees the bounce.
	WakeDeadline time.Duration

	// UDPThreshold/UDPWindow configure the "N packets from one source in a
	// window" heuristic used to decide when a UDP port has a real player
	// (UDP has no handshake to classify or connection to hold).
	UDPThreshold int
	UDPWindow    time.Duration

	// UDPMaxSources bounds the UDP per-source tracking map so an
	// unauthenticated port scan can't grow it without bound.
	UDPMaxSources int

	// MaxConnections bounds concurrent in-flight TCP connection handler
	// goroutines across all TCP listeners, so a port scan can't spawn an
	// unbounded number of goroutines.
	MaxConnections int

	// WakePatchInterval is the minimum spacing between wake-annotation
	// patches, coalescing a burst of connecting clients into one apiserver
	// call.
	WakePatchInterval time.Duration

	// ShutdownDrainTimeout bounds how long in-flight sessions (a held or
	// handed-through player connection) are kept alive once the sentinel
	// is told to shut down. Listeners stop accepting immediately; sessions
	// already established get up to this long to finish on their own
	// before they are force-closed. Zero means close them immediately.
	ShutdownDrainTimeout time.Duration

	// upstreamAddrOverride, when non-empty, replaces the game-direct
	// Service address for every port. It is never set from the
	// environment: it exists only so tests can point run() at a loopback
	// listener standing in for the game pod.
	upstreamAddrOverride string
}

// loadConfig reads and validates the sentinel's configuration via getenv
// (os.Getenv in production; a stub map in tests).
func loadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		WakeDeadline:      25 * time.Second,
		UDPThreshold:      3,
		UDPWindow:         10 * time.Second,
		UDPMaxSources:     4096,
		MaxConnections:    256,
		WakePatchInterval: 2 * time.Second,

		ShutdownDrainTimeout: defaultShutdownDrainTimeout,
	}

	cfg.GameServerName = getenv("GAMESERVER_NAME")
	cfg.GameServerNamespace = getenv("GAMESERVER_NAMESPACE")
	if cfg.GameServerName == "" || cfg.GameServerNamespace == "" {
		return Config{}, errors.New("GAMESERVER_NAME and GAMESERVER_NAMESPACE are required")
	}

	ports, err := parsePortsConfig(getenv("PORTS_CONFIG"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid PORTS_CONFIG: %w", err)
	}
	cfg.Ports = ports

	if v := getenv("WAKE_DEADLINE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid WAKE_DEADLINE: %w", err)
		}
		cfg.WakeDeadline = d
	}
	if v := getenv("UDP_PACKET_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("invalid UDP_PACKET_THRESHOLD: %q", v)
		}
		cfg.UDPThreshold = n
	}
	if v := getenv("UDP_PACKET_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid UDP_PACKET_WINDOW: %w", err)
		}
		cfg.UDPWindow = d
	}
	if v := getenv("UDP_MAX_SOURCES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("invalid UDP_MAX_SOURCES: %q", v)
		}
		cfg.UDPMaxSources = n
	}
	if v := getenv("MAX_CONNECTIONS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("invalid MAX_CONNECTIONS: %q", v)
		}
		cfg.MaxConnections = n
	}
	if v := getenv("WAKE_PATCH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid WAKE_PATCH_INTERVAL: %w", err)
		}
		cfg.WakePatchInterval = d
	}
	if v := getenv("SHUTDOWN_DRAIN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return Config{}, fmt.Errorf("invalid SHUTDOWN_DRAIN_TIMEOUT: %q", v)
		}
		cfg.ShutdownDrainTimeout = d
	}

	return cfg, nil
}

// PortConfig holds one advertised port's parsed configuration.
type PortConfig struct {
	ContainerPort int
	Protocol      string // "TCP" or "UDP"
	WakeProtocol  string // "minecraft", "terraria", "generic", or "none"
}

// parsePortsConfig parses the PORTS_CONFIG env var.
// Format: "port:protocol:wakeProtocol,..." e.g. "25565:TCP:minecraft,19133:UDP:generic".
// Leading, trailing, and doubled commas are tolerated and ignored.
// Validates wakeProtocol names against the registry at startup.
func parsePortsConfig(config string) ([]PortConfig, error) {
	config = strings.Trim(config, ",")
	if config == "" {
		return nil, nil
	}

	var ports []PortConfig
	for _, part := range strings.Split(config, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		fields := strings.Split(part, ":")
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid port config: %q", part)
		}

		port, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			return nil, fmt.Errorf("invalid port number in %q: %w", part, err)
		}

		protocol := strings.TrimSpace(strings.ToUpper(fields[1]))
		if protocol != "TCP" && protocol != "UDP" {
			return nil, fmt.Errorf("invalid protocol in %q: %q", part, fields[1])
		}

		wakeProto := strings.TrimSpace(strings.ToLower(fields[2]))
		// Validate wakeProtocol: "generic" and "none" are special cases (handled outside registry).
		// All others must be registered in the gameproto registry.
		if wakeProto != "generic" && wakeProto != "none" {
			if _, ok := gameproto.Lookup(wakeProto); !ok {
				available := gameproto.ListRegistered()
				return nil, fmt.Errorf("unknown wakeProtocol %q in %q (registered protocols: %v)", fields[2], part, available)
			}
		}

		ports = append(ports, PortConfig{
			ContainerPort: port,
			Protocol:      protocol,
			WakeProtocol:  wakeProto,
		})
	}

	return ports, nil
}

// wantsListener reports whether the sentinel should bind a listener for
// this port at all. wakeProtocol "none" means the operator still advertises
// the port (so it shows up in the Service/container spec) but nothing on it
// should ever wake the server, so the sentinel doesn't listen on it.
func wantsListener(p PortConfig) bool {
	return p.WakeProtocol != "none"
}

// gameDirectAddr is the dial target for the real game pod once it's up.
func gameDirectAddr(cfg Config, port PortConfig) string {
	if cfg.upstreamAddrOverride != "" {
		return cfg.upstreamAddrOverride
	}
	return fmt.Sprintf("%s-game-direct.%s.svc.cluster.local:%d", cfg.GameServerName, cfg.GameServerNamespace, port.ContainerPort)
}

// newInClusterDynamicClient builds a dynamic client from in-cluster
// config. A dynamic client (rather than the typed operator/api/v1alpha1
// client) keeps this module's dependency graph light — it doesn't need
// controller-runtime — and is all that's needed for get/patch against one
// resource kind.
func newInClusterDynamicClient() (dynamic.Interface, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	client, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("new dynamic client: %w", err)
	}
	return client, nil
}

// wakeRequester is the interface the connection handlers need from the
// wake path. *waker implements it against a real GameServer; tests
// substitute a fake dynamic client so the wake path is genuinely exercised
// rather than skipped or nil-panicking.
type wakeRequester interface {
	RequestWake(ctx context.Context) error
}

// waker patches the GameServer's idle-wake-requested annotation to signal
// the operator that this server should wake.
//
// It coalesces bursts: while a patch is in flight, or within minInterval of
// the last one landing, RequestWake is a cheap no-op instead of issuing
// another apiserver call. This is what keeps a port scan, or a pile of
// players reconnecting at once, from hammering the apiserver with patches —
// the operator only needs to see the annotation change once to act on it.
type waker struct {
	res         dynamic.ResourceInterface
	name        string
	minInterval time.Duration

	mu        sync.Mutex
	inFlight  bool
	lastPatch time.Time
}

func newWaker(res dynamic.ResourceInterface, name string, minInterval time.Duration) *waker {
	return &waker{res: res, name: name, minInterval: minInterval}
}

// RequestWake stamps a fresh token onto the GameServer's
// idle-wake-requested annotation via a JSON merge patch (rather than a full
// object update), so any other annotations already on the object survive.
func (w *waker) RequestWake(ctx context.Context) error {
	w.mu.Lock()
	now := time.Now()
	if w.inFlight || (!w.lastPatch.IsZero() && now.Sub(w.lastPatch) < w.minInterval) {
		w.mu.Unlock()
		return nil
	}
	w.inFlight = true
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.inFlight = false
		w.lastPatch = time.Now()
		w.mu.Unlock()
	}()

	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				idleWakeRequestedAnnotation: wakeToken(),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("encode wake patch: %w", err)
	}

	if _, err := w.res.Patch(ctx, w.name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("patch %s annotation: %w", idleWakeRequestedAnnotation, err)
	}
	return nil
}

// wakeToken returns a value that differs from the previous call, so the
// operator (which compares this annotation against the "completed" one it
// stamps back) can tell a fresh wake was requested.
func wakeToken() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

// sessionGroup tracks the per-connection handler goroutines ("sessions")
// separately from the listeners that spawn them, so shutdown can stop
// accepting new connections without cutting the ones already in flight.
//
// ctx is the sessions' own context: it is NOT the listener context, and is
// only cancelled once the shutdown drain is over (or on a fatal listener
// error). A handed-through connection's proxyBidirectional therefore keeps
// copying after SIGTERM until both directions finish on their own.
type sessionGroup struct {
	ctx    context.Context
	wg     sync.WaitGroup
	active atomic.Int64
}

// start registers one session and runs fn in its own goroutine.
func (g *sessionGroup) start(fn func(ctx context.Context)) {
	g.wg.Add(1)
	g.active.Add(1)
	go func() {
		defer g.wg.Done()
		defer g.active.Add(-1)
		fn(g.ctx)
	}()
}

// drain waits up to timeout for every session to finish on its own, then
// calls cancel (force-closing whatever is left) and waits for the
// stragglers to exit. timeout <= 0 cancels immediately.
func (g *sessionGroup) drain(timeout time.Duration, cancel context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	if n := g.active.Load(); n > 0 && timeout > 0 {
		log.Printf("listeners closed; waiting up to %v for %d in-flight session(s) to finish", timeout, n)
		timer := time.NewTimer(timeout)
		select {
		case <-done:
			timer.Stop()
		case <-timer.C:
			log.Printf("shutdown drain of %v elapsed with %d session(s) still open; closing them", timeout, g.active.Load())
		}
	}
	cancel()
	<-done
}

// run starts a listener (TCP) or reader (UDP) for every configured,
// non-"none" port and blocks until ctx is cancelled or a listener reports a
// fatal error.
//
// On ctx cancellation (SIGTERM) every listener stops immediately, so no new
// connection is accepted, but sessions already in flight — notably a
// connection already handed through to the game pod — are left to finish
// on their own for up to cfg.ShutdownDrainTimeout (see sessionGroup). run
// returns nil once they have.
//
// A fatal listener error (a UDP bind failure, an Accept or ReadFrom error)
// is logged and returned as soon as it happens: the other listeners are
// stopped and every session is closed without a drain, because a sentinel
// with a dead port must restart rather than keep looking healthy.
func run(ctx context.Context, cfg Config, w wakeRequester) error {
	listenCtx, stopListening := context.WithCancel(ctx)
	defer stopListening()
	sessCtx, cancelSessions := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelSessions()
	sessions := &sessionGroup{ctx: sessCtx}

	sem := make(chan struct{}, cfg.MaxConnections)
	errCh := make(chan error, len(cfg.Ports)+1)
	var listeners sync.WaitGroup

	var fatal error
	for _, port := range cfg.Ports {
		if !wantsListener(port) {
			continue
		}
		port := port

		if port.Protocol == "UDP" {
			listeners.Add(1)
			go func() {
				defer listeners.Done()
				serveUDP(listenCtx, port, w, cfg, errCh)
			}()
			continue
		}

		lc := &net.ListenConfig{}
		l, err := lc.Listen(listenCtx, "tcp", fmt.Sprintf(":%d", port.ContainerPort))
		if err != nil {
			fatal = fmt.Errorf("listen tcp :%d: %w", port.ContainerPort, err)
			break
		}
		listeners.Add(1)
		go func() {
			defer listeners.Done()
			serveTCP(listenCtx, l, port, w, cfg, sem, sessions, errCh)
		}()
	}

	if fatal == nil {
		select {
		case fatal = <-errCh:
		case <-ctx.Done():
		}
	}

	stopListening()
	listeners.Wait()

	if fatal != nil {
		log.Printf("listener failed, shutting down: %v", fatal)
		sessions.drain(0, cancelSessions)
		return fatal
	}
	sessions.drain(cfg.ShutdownDrainTimeout, cancelSessions)
	return nil
}

// serveTCP accepts connections on l until ctx is cancelled or Accept fails.
// ctx is the listener's context only: each accepted connection is handled
// as a session in sessions, under the sessions' own context, so closing
// the listener on shutdown does not cut connections already accepted.
//
// Each accepted connection is handled in its own goroutine, but only after
// acquiring a slot in sem — bounding the number of concurrent handler
// goroutines so an unauthenticated port scan can't spawn an unbounded
// number of them. When sem is full, Accept simply isn't called again until
// a slot frees, so excess connections queue in the OS accept backlog
// instead of piling up as goroutines.
func serveTCP(ctx context.Context, l net.Listener, port PortConfig, w wakeRequester, cfg Config, sem chan struct{}, sessions *sessionGroup, errCh chan<- error) {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case errCh <- fmt.Errorf("accept on port %d: %w", port.ContainerPort, err):
			default:
			}
			return
		}
		if ctx.Err() != nil {
			// Accepted concurrently with shutdown: don't start a new
			// session for it.
			_ = conn.Close()
			return
		}

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			_ = conn.Close()
			return
		}

		sessions.start(func(sessCtx context.Context) {
			defer func() { <-sem }()
			handleTCPConnection(sessCtx, conn, port, w, cfg)
		})
	}
}

// handleTCPConnection dispatches an accepted connection to its protocol
// handler. Every path through the handlers eventually returns (deadline,
// EOF, or classification failure), at which point this defer closes it.
//
// The game-direct address is resolved once, here, from cfg/port — the
// protocol handlers below take it as a plain string precisely so they don't
// depend on Config or DNS resolution themselves, which is what lets tests
// drive them directly against a loopback listener standing in for the game
// pod.
func handleTCPConnection(ctx context.Context, conn net.Conn, port PortConfig, w wakeRequester, cfg Config) {
	defer func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("close connection: %v", err)
		}
	}()

	addr := gameDirectAddr(cfg, port)
	if port.WakeProtocol == "generic" {
		handleGeneric(ctx, conn, w, addr, cfg.WakeDeadline)
	} else {
		handleRegistryProtocol(ctx, conn, w, addr, port.WakeProtocol, cfg.WakeDeadline)
	}
}

// handleRegistryProtocol classifies an incoming connection using a registered
// protocol Classifier. It replaces the hardcoded handleMinecraft/handleTerraria
// dispatch with a registry-based lookup, making protocol addition a matter of
// registry entry + Classifier implementation with zero edits to sentinel.
//
// The connection is wrapped in a *bufio.Reader exactly once, and every
// subsequent read — classification, and later the client-to-game copy in
// proxyBidirectional — goes through that same reader. Reading the raw
// net.Conn again after this point would drop whatever gameproto's internal
// bufio.Reader already buffered past the handshake (the exact bug class
// gameproto #196 fixed).
func handleRegistryProtocol(ctx context.Context, conn net.Conn, w wakeRequester, upstreamAddr string, protocol string, deadline time.Duration) {
	classifier, ok := gameproto.Lookup(protocol)
	if !ok {
		// Should not happen if parsePortsConfig validated properly, but log it defensively.
		log.Printf("protocol %q not found in registry (internal error, this should have been caught at startup)", protocol)
		return
	}

	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(classifyReadTimeout))
	result, err := classifier.Classify(br)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}

	switch result.Kind {
	case gameproto.Status:
		// Only send status response if the protocol supports out-of-band status pings.
		if classifier.SupportsStatusPing() {
			reply, buildErr := classifier.BuildStatusResponse(minecraftAsleepStatusJSON)
			if buildErr != nil {
				log.Printf("build %s status response: %v", protocol, buildErr)
				return
			}
			_, _ = conn.Write(reply)
		}

	case gameproto.Join:
		// Build a bounce function that sends a protocol-native disconnect message
		// when the wake deadline expires.
		bounce := func(c net.Conn) {
			disconnect, err := classifier.BuildDisconnect(wakingUpMessage)
			if err != nil {
				log.Printf("build %s disconnect: %v", protocol, err)
				return
			}
			_, _ = c.Write(disconnect)
		}

		handleJoin(ctx, conn, br, w, joinParams{
			upstreamAddr: upstreamAddr,
			deadline:     deadline,
			pollInterval: defaultPollInterval,
			consumed:     result.Consumed,
			bounce:       bounce,
		})

	case gameproto.Unknown:
		// Unrecognized bytes: close without waking (deferred by the caller).
	}
}

// handleGeneric treats a completed TCP connection as a join outright — a
// generic wakeProtocol has no handshake to parse, so there's nothing to
// classify. There is also no protocol-native way to bounce a generic
// client, so a deadline expiry just closes the connection.
func handleGeneric(ctx context.Context, conn net.Conn, w wakeRequester, upstreamAddr string, deadline time.Duration) {
	br := bufio.NewReader(conn)
	handleJoin(ctx, conn, br, w, joinParams{
		upstreamAddr: upstreamAddr,
		deadline:     deadline,
		pollInterval: defaultPollInterval,
		consumed:     nil,
		bounce:       nil,
	})
}

// joinParams bundles what handleJoin needs to hold a connection open across
// a wake.
type joinParams struct {
	upstreamAddr string
	deadline     time.Duration
	pollInterval time.Duration
	// consumed is any handshake bytes already read off the client
	// connection that must be replayed to the game pod once it's up, per
	// gameproto's replay contract.
	consumed []byte
	// bounce, if non-nil, sends a protocol-native "try again" message to
	// the client when the deadline expires before the game pod comes up.
	bounce func(net.Conn)
}

// handleJoin is the shared hold/poll/proxy path for every wakeProtocol:
// request a wake, then poll the game-direct Service until it accepts a
// connection or the deadline expires. On success, the consumed handshake
// bytes are replayed to the game pod before the connection is proxied
// bidirectionally; on timeout, the connection is bounced (if a bounce
// function is set) and closed.
func handleJoin(ctx context.Context, conn net.Conn, br *bufio.Reader, w wakeRequester, p joinParams) {
	if err := w.RequestWake(ctx); err != nil {
		log.Printf("request wake: %v", err)
	}

	upstream, err := waitForUpstream(ctx, p.upstreamAddr, p.deadline, p.pollInterval)
	if err != nil {
		if p.bounce != nil {
			p.bounce(conn)
		}
		return
	}
	defer func() {
		if err := upstream.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("close upstream connection: %v", err)
		}
	}()

	if len(p.consumed) > 0 {
		if _, err := upstream.Write(p.consumed); err != nil {
			log.Printf("replay consumed handshake bytes: %v", err)
			return
		}
	}

	proxyBidirectional(ctx, upstream, conn, br)
}

// waitForUpstream dials addr repeatedly until it accepts a connection or
// deadline elapses (bounded further by ctx). This is the hold: while the
// operator brings the real game pod up, the client's TCP connection to the
// sentinel stays open and this loop is what's waiting on the other side.
func waitForUpstream(ctx context.Context, addr string, deadline, pollInterval time.Duration) (net.Conn, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	dialer := &net.Dialer{}
	for {
		conn, err := dialer.DialContext(deadlineCtx, "tcp", addr)
		if err == nil {
			return conn, nil
		}

		select {
		case <-deadlineCtx.Done():
			return nil, deadlineCtx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// closeWriter is implemented by *net.TCPConn (and similar), letting
// proxyBidirectional half-close a direction instead of only ever fully
// closing both connections at once.
type closeWriter interface {
	CloseWrite() error
}

// proxyBidirectional copies bytes both ways between upstream (the game
// pod) and downstream (the client), reading the downstream side through
// downstreamReader rather than downstream directly — this must be the same
// *bufio.Reader classification already read from, so any bytes it buffered
// past the handshake aren't dropped.
//
// It waits for BOTH directions to finish before returning: when one
// direction hits EOF, it half-closes the write side of the other
// connection (if supported) so that side can still drain any remaining
// data already in flight, rather than being killed mid-copy. Only once
// both io.Copy calls have returned are both connections fully closed.
//
// ctx is the session context from run's sessionGroup, not the listener
// context: SIGTERM alone does not cancel it. It is cancelled only when the
// shutdown drain times out or a listener fails fatally, and only then are
// both copies cut short.
func proxyBidirectional(ctx context.Context, upstream, downstream net.Conn, downstreamReader io.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, downstreamReader)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(downstream, upstream)
		closeWrite(downstream)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		// The shutdown drain ran out (or a listener failed fatally): force
		// both copies to unblock immediately rather than waiting for them
		// to notice on their own.
	}
	_ = upstream.Close()
	_ = downstream.Close()
}

func closeWrite(conn net.Conn) {
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

// serveUDP applies the packet-counting wake heuristic to one UDP port.
// Unlike TCP, there is no connection to accept and hold — a UDP "packet"
// is fire-and-forget, so there's nothing to keep open while the server
// wakes. Instead, N packets from the same source within a window are taken
// as a real player (rather than a single stray probe), which then triggers
// a wake; the packet itself is simply dropped either way, since forwarding
// it anywhere useful would require the game pod to already be up.
func serveUDP(ctx context.Context, port PortConfig, w wakeRequester, cfg Config, errCh chan<- error) {
	lc := &net.ListenConfig{}
	pc, err := lc.ListenPacket(ctx, "udp", fmt.Sprintf(":%d", port.ContainerPort))
	if err != nil {
		select {
		case errCh <- fmt.Errorf("listen udp :%d: %w", port.ContainerPort, err):
		default:
		}
		return
	}

	// Belt-and-suspenders shutdown: Close() unblocks a ReadFrom that's
	// already parked waiting for a packet, and the read deadline below
	// bounds the (much smaller) window between checking ctx and the next
	// ReadFrom call. Either alone would do; together they guarantee this
	// goroutine actually exits on SIGTERM instead of blocking forever on a
	// UDP port nobody is sending to.
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	heuristic := newUDPHeuristic(cfg.UDPThreshold, cfg.UDPWindow, cfg.UDPMaxSources)
	sweepCtx, cancelSweep := context.WithCancel(ctx)
	defer cancelSweep()
	go heuristic.sweepLoop(sweepCtx, cfg.UDPWindow)

	buf := make([]byte, 2048)
	for {
		_ = pc.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			select {
			case errCh <- fmt.Errorf("read udp :%d: %w", port.ContainerPort, err):
			default:
			}
			return
		}
		if n == 0 {
			continue
		}

		if heuristic.shouldWake(addr.String(), time.Now()) {
			if err := w.RequestWake(ctx); err != nil {
				log.Printf("request wake: %v", err)
			}
		}
	}
}

// udpSourceState tracks one source address's recent packet timestamps and
// whether it has already triggered a wake.
type udpSourceState struct {
	packets  []time.Time
	lastSeen time.Time
	wokenAt  time.Time
}

// udpHeuristic implements the "N packets from one source within a window"
// join heuristic for UDP ports, which have no handshake to classify and no
// connection to hold open.
type udpHeuristic struct {
	threshold  int
	window     time.Duration
	maxSources int

	mu      sync.Mutex
	sources map[string]*udpSourceState
}

func newUDPHeuristic(threshold int, window time.Duration, maxSources int) *udpHeuristic {
	return &udpHeuristic{
		threshold:  threshold,
		window:     window,
		maxSources: maxSources,
		sources:    make(map[string]*udpSourceState),
	}
}

// shouldWake records a packet from key (a UDP remote address) and reports
// whether it just pushed that source over the wake threshold within the
// configured window.
//
// An internet-facing UDP port sees arbitrary, spoofable source addresses,
// and an attacker can churn through many of them, so the per-source map is
// bounded: once maxSources distinct sources are tracked, the
// least-recently-active one is evicted to make room for a new one rather
// than letting the map grow without bound. sweepLoop separately evicts
// sources that have simply gone quiet, so a burst of scanners doesn't leave
// the map pinned at maxSources long after they move on.
func (h *udpHeuristic) shouldWake(key string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	st, ok := h.sources[key]
	if !ok {
		if len(h.sources) >= h.maxSources {
			h.evictOldestLocked()
		}
		st = &udpSourceState{}
		h.sources[key] = st
	}
	st.lastSeen = now

	if !st.wokenAt.IsZero() && now.Sub(st.wokenAt) < h.window {
		// Already requested a wake for this source recently; suppress
		// further counting until the window passes so every subsequent
		// packet during the wake doesn't re-trigger the (already
		// coalescing) RequestWake call.
		return false
	}

	filtered := st.packets[:0]
	for _, t := range st.packets {
		if now.Sub(t) < h.window {
			filtered = append(filtered, t)
		}
	}
	filtered = append(filtered, now)
	st.packets = filtered

	if len(filtered) < h.threshold {
		return false
	}

	st.wokenAt = now
	st.packets = nil
	return true
}

// evictOldestLocked removes the least-recently-active source. Callers must
// hold h.mu.
func (h *udpHeuristic) evictOldestLocked() {
	var oldestKey string
	var oldestSeen time.Time
	for k, v := range h.sources {
		if oldestKey == "" || v.lastSeen.Before(oldestSeen) {
			oldestKey = k
			oldestSeen = v.lastSeen
		}
	}
	if oldestKey != "" {
		delete(h.sources, oldestKey)
	}
}

// sweepLoop periodically evicts sources that have gone quiet. It runs until
// ctx is cancelled.
func (h *udpHeuristic) sweepLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.sweepOnce(now)
		}
	}
}

// sweepOnce removes sources whose last packet is older than twice the
// window — well past the point where they could still contribute to a
// threshold decision.
func (h *udpHeuristic) sweepOnce(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, v := range h.sources {
		if now.Sub(v.lastSeen) > h.window*2 {
			delete(h.sources, k)
		}
	}
}
