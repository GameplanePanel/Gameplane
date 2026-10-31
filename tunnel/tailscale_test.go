package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/views"
)

func TestTailscaleServerPreservesIdentityAndTags(t *testing.T) {
	cfg := Config{TailscaleHostname: "my-game", TailscaleTags: "game,tag:game,tag:friends"}
	s, tags, err := tailscaleServer(cfg, "test-auth-key")
	if err != nil {
		t.Fatal(err)
	}
	if s.Hostname != "my-game" || s.AuthKey != "test-auth-key" || s.Dir != tailscaleStateDir || s.Ephemeral || len(tags) != 2 || !equalArgs(tags, s.AdvertiseTags) {
		t.Fatalf("server lost identity or tags: %+v", tags)
	}
	cfg.TailscaleTags = "tag:bad tag"
	if _, _, err := tailscaleServer(cfg, "key"); err == nil {
		t.Fatal("invalid tags silently dropped")
	}
	cfg.TailscaleTags = ""
	cfg.TailscaleHostname = ""
	if _, _, err := tailscaleServer(cfg, "key"); err == nil {
		t.Fatal("empty hostname accepted")
	}
	cfg.TailscaleHostname = "my-game"
	if _, _, err := tailscaleServer(cfg, ""); err == nil {
		t.Fatal("empty auth key accepted")
	}
}

func onlineTailnet() *ipnstate.Status {
	tags := views.SliceOf([]string{"tag:game"})
	return &ipnstate.Status{BackendState: "Running", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1"), netip.MustParseAddr("fd7a:115c:a1e0::1")}, Self: &ipnstate.PeerStatus{Online: true, Tags: &tags}}
}

func TestTailscaleHostnameMustMatchAdvertisedEndpoint(t *testing.T) {
	status := onlineTailnet()
	status.Self.DNSName = "game.tailnet.ts.net."
	if _, err := checkTailnetHostname(status, "game"); err != nil {
		t.Fatal(err)
	}
	status.Self.DNSName = "game-1.tailnet.ts.net."
	if _, err := checkTailnetHostname(status, "game"); err == nil {
		t.Fatal("colliding hostname advertised as ready")
	}
}

type fakeTailnetNode struct {
	mu        sync.Mutex
	status    *ipnstate.Status
	upErr     error
	upCalls   int
	listenErr bool
	listened  []string
	listeners []net.Listener
	packets   []net.PacketConn
}

func (f *fakeTailnetNode) Up(ctx context.Context) (*ipnstate.Status, error) {
	f.upCalls++
	if f.upErr != nil {
		return nil, f.upErr
	}
	return f.Status(ctx)
}
func (f *fakeTailnetNode) Status(_ context.Context) (*ipnstate.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}
func (f *fakeTailnetNode) Listen(network, addr string) (net.Listener, error) {
	f.listened = append(f.listened, network+addr)
	l, err := (&net.ListenConfig{}).Listen(context.Background(), network, "127.0.0.1:0")
	if err == nil {
		f.listeners = append(f.listeners, l)
	}
	return l, err
}
func (f *fakeTailnetNode) ListenPacket(network, addr string) (net.PacketConn, error) {
	f.listened = append(f.listened, network+addr)
	if f.listenErr {
		return nil, errors.New("bind failed")
	}
	l, err := (&net.ListenConfig{}).ListenPacket(context.Background(), network, "127.0.0.1:0")
	if err == nil {
		f.packets = append(f.packets, l)
	}
	return l, err
}

func awaitTailnetReady(t *testing.T, ready *atomic.Bool, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for ready.Load() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if ready.Load() != want {
		t.Fatalf("readiness=%v want %v", ready.Load(), want)
	}
}

func TestTailnetReadinessTracksAuthenticationListenersAndDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node := &fakeTailnetNode{status: onlineTailnet()}
	var ready atomic.Bool
	ports := []forwardPort{{network: "tcp", port: "25565", target: "127.0.0.1:1"}, {network: "udp", port: "19132", target: "127.0.0.1:2"}}
	done := make(chan error, 1)
	go func() { done <- serveTailnet(ctx, node, []string{"tag:game"}, ports, &ready, time.Millisecond) }()
	awaitTailnetReady(t, &ready, true)
	if len(node.listened) != 3 || node.listened[0] != "tcp:25565" || node.listened[1] != "udp100.64.0.1:19132" || node.listened[2] != "udp[fd7a:115c:a1e0::1]:19132" {
		t.Fatalf("unexpected port/family exposure: %v", node.listened)
	}
	node.mu.Lock()
	offline := onlineTailnet()
	offline.Self.Online = false
	node.status = offline
	node.mu.Unlock()
	awaitTailnetReady(t, &ready, false)
	node.mu.Lock()
	node.status = onlineTailnet()
	node.mu.Unlock()
	awaitTailnetReady(t, &ready, true)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stalled")
	}
	if ready.Load() {
		t.Fatal("readiness survives shutdown")
	}
	for _, listener := range node.listeners {
		if _, err := listener.Accept(); err == nil {
			t.Fatal("listener survives shutdown")
		}
	}
}

func TestTailnetFailuresNeverBecomeReady(t *testing.T) {
	for _, scenario := range []string{"authentication", "denied tag", "listener"} {
		t.Run(scenario, func(t *testing.T) {
			node := &fakeTailnetNode{status: onlineTailnet()}
			tags := []string{"tag:game"}
			switch scenario {
			case "authentication":
				node.upErr = errors.New("unauthorized")
			case "denied tag":
				tags = []string{"tag:ungranted"}
			case "listener":
				node.listenErr = true
			}
			var ready atomic.Bool
			err := serveTailnet(context.Background(), node, tags, []forwardPort{{network: "tcp", port: "1", target: "127.0.0.1:1"}, {network: "udp", port: "2", target: "127.0.0.1:2"}}, &ready, time.Millisecond)
			if err == nil || ready.Load() {
				t.Fatalf("failure became ready: %v", err)
			}
			if node.upCalls != 1 {
				t.Fatalf("failed registration retried without requested tags: %d attempts", node.upCalls)
			}
			for _, listener := range node.listeners {
				if _, err := listener.Accept(); err == nil {
					t.Fatal("partially opened listener leaked")
				}
			}
		})
	}
}

func TestTailscaleRetryBackoffCancelsImmediately(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	healthDone := make(chan error)
	done := make(chan error, 1)
	go func() { done <- waitTailnetRetry(ctx, healthDone, time.Hour) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled retry: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry backoff ignored cancellation")
	}
}

func TestTailnetListenerFailureClearsReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node := &fakeTailnetNode{status: onlineTailnet()}
	var ready atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- serveTailnet(ctx, node, nil, []forwardPort{{network: "tcp", port: "25565", target: "127.0.0.1:1"}}, &ready, time.Millisecond)
	}()
	awaitTailnetReady(t, &ready, true)
	_ = node.listeners[0].Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("failed listener was not reported")
		}
	case <-time.After(time.Second):
		t.Fatal("failed listener did not stop forwarding")
	}
	if ready.Load() {
		t.Fatal("failed forwarding listener still ready")
	}
}

func TestTailnetHealthNeedsForwardingReadiness(t *testing.T) {
	var ready atomic.Bool
	handler := tailscaleHealth(&ready)
	for _, check := range []struct {
		path  string
		ready bool
		want  int
	}{{"/readyz", false, 503}, {"/healthz", false, 200}, {"/readyz", true, 200}, {"/readyz", false, 503}} {
		ready.Store(check.ready)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, check.path, nil))
		if rec.Code != check.want {
			t.Fatalf("%s=%d want%d", check.path, rec.Code, check.want)
		}
	}
}

func TestTailnetConfirmedObservationsResetRetryEscalation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	node := &fakeTailnetNode{status: onlineTailnet()}
	backoff := &exponentialBackoff{}
	for i := 0; i < 12; i++ {
		backoff.next()
	}
	history := &relayHealth{}
	var ready atomic.Bool
	confirmed := 0
	start := time.Unix(100, 0)
	err := serveTailnetObserved(ctx, node, nil, []forwardPort{{network: "tcp", port: "25565", target: "127.0.0.1:1"}}, &ready, time.Millisecond, func(healthy bool) {
		if !healthy {
			history.observe(start.Add(time.Duration(confirmed)*healthPollInterval), relayObservation{Connection: "unknown"}, backoff)
			return
		}
		history.observe(start.Add(time.Duration(confirmed)*healthPollInterval), relayObservation{Connection: "connected", Forwards: true, Assigned: true}, backoff)
		confirmed++
		if time.Duration(confirmed-1)*healthPollInterval >= healthyResetAfter {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed < 31 || ready.Load() {
		t.Fatalf("confirmed=%d ready=%v", confirmed, ready.Load())
	}
	if got := backoff.next(); got != time.Second {
		t.Fatalf("confirmed tailnet health did not reset backoff: %v", got)
	}
}
