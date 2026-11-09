package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// These timeouts only prevent a stuck fixture from hanging CI. The logged
// timings describe this loopback run, not a supported production capacity.
const loadSafetyTimeout = 10 * time.Second

func receiveLoadEvent[T any](t *testing.T, events <-chan T, name string) T {
	t.Helper()
	select {
	case value := <-events:
		return value
	case <-time.After(loadSafetyTimeout):
		t.Fatalf("timed out waiting for %s", name)
		var zero T
		return zero
	}
}

func waitLoadEvent(t *testing.T, events <-chan struct{}, name string) {
	t.Helper()
	receiveLoadEvent(t, events, name)
}

func updateLoadMaximum(maximum *atomic.Int32, value int32) {
	for old := maximum.Load(); value > old; old = maximum.Load() {
		if maximum.CompareAndSwap(old, value) {
			return
		}
	}
}

type loadWaitContext struct {
	context.Context
	joined chan<- struct{}
	once   sync.Once
}

func (c *loadWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { c.joined <- struct{}{} })
	return c.Context.Done()
}

// observeLoadWait derives the observation wrapper from the caller's context;
// cancellation and deadlines still come directly from that parent.
func observeLoadWait(ctx context.Context, joined chan struct{}) context.Context {
	if joined == nil {
		return ctx
	}
	return &loadWaitContext{Context: ctx, joined: joined}
}

type loadWakeObserver struct {
	wake      wakeRequester
	joined    chan struct{}
	deadlines chan time.Time
	finished  chan error
	calls     atomic.Int32
	active    atomic.Int32
	maximum   atomic.Int32
}

func (w *loadWakeObserver) RequestWake(ctx context.Context) error {
	w.calls.Add(1)
	updateLoadMaximum(&w.maximum, w.active.Add(1))
	defer w.active.Add(-1)
	if w.deadlines != nil {
		deadline, _ := ctx.Deadline()
		w.deadlines <- deadline
	}
	err := w.wake.RequestWake(observeLoadWait(ctx, w.joined))
	w.finished <- err
	return err
}

func TestWakeMixedLoadOutageRecovery(t *testing.T) {
	const tcpPlayers, udpSources = 16, 24
	const joinBudget = 4 * time.Second
	started := time.Now()
	listenerCtx, stopListeners := context.WithCancel(t.Context())
	defer stopListeners()
	sessionCtx, stopSessions := context.WithCancel(context.WithoutCancel(t.Context()))
	defer stopSessions()
	sessions := &sessionGroup{ctx: sessionCtx}
	defer sessions.drain(0, stopSessions)

	// The first API request is deliberately stalled until the entire mixed
	// burst has joined/arrived. It then fails; only the retry can recover.
	releaseOutage := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOutage) }) }
	defer release()
	apiCalls := make(chan int32, 16)
	var patches, apiActive, apiMaximum atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer apiActive.Add(-1)
		updateLoadMaximum(&apiMaximum, apiActive.Add(1))
		_, _ = io.Copy(io.Discard, req.Body)
		if req.Method != http.MethodPatch {
			t.Errorf("API method = %s, want PATCH", req.Method)
		}
		attempt := patches.Add(1)
		apiCalls <- attempt
		w.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			select {
			case <-releaseOutage:
			case <-req.Context().Done():
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"ServiceUnavailable","message":"fixture outage","code":503}`)
			return
		}
		_, _ = io.WriteString(w, `{"apiVersion":"gameplane.local/v1alpha1","kind":"GameServer","metadata":{"name":"alpha"}}`)
	}))
	defer api.Close()
	// Unblock the handler before Close even if an assertion fails mid-outage.
	defer release()
	client, err := dynamic.NewForConfig(&rest.Config{Host: api.URL})
	if err != nil {
		t.Fatal(err)
	}
	shared := newWaker(client.Resource(gameServerGVR).Namespace("games"), "alpha", time.Minute)
	udpWake := &loadWakeObserver{wake: shared, finished: make(chan error, 4)}
	tcpWake := &loadWakeObserver{wake: shared, joined: make(chan struct{}, tcpPlayers), deadlines: make(chan time.Time, tcpPlayers), finished: make(chan error, tcpPlayers)}

	lc := &net.ListenConfig{}
	tcpListener, err := lc.Listen(listenerCtx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpListener.Close()
	udpListener, err := lc.ListenPacket(listenerCtx, "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udpListener.Close()
	// Reserve then release the upstream port: there is no game listener
	// during outage/recovery. Binding is an explicit cold-start event below.
	probe, err := lc.Listen(listenerCtx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstreamAddr := probe.Addr().String()
	_ = probe.Close()
	cfg := Config{WakeDeadline: joinBudget, MaxConnections: tcpPlayers, UDPThreshold: 1, UDPWindow: time.Minute, UDPMaxSources: udpSources + 1, upstreamAddrOverride: upstreamAddr}
	reads := make(chan struct{}, udpSources+2)
	errCh := make(chan error, 2)
	tcpDone, udpDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(tcpDone)
		serveTCP(listenerCtx, tcpListener, PortConfig{Protocol: "TCP", WakeProtocol: "generic"}, tcpWake, cfg, make(chan struct{}, tcpPlayers), sessions, errCh)
	}()
	go func() {
		defer close(udpDone)
		serveUDPPackets(listenerCtx, observedPacketConn{udpListener, reads}, PortConfig{Protocol: "UDP"}, udpWake, cfg, errCh)
	}()

	udpClients := make([]net.Conn, udpSources+1)
	for i := range udpClients {
		conn, dialErr := (&net.Dialer{}).DialContext(listenerCtx, "udp", udpListener.LocalAddr().String())
		if dialErr != nil {
			t.Fatal(dialErr)
		}
		udpClients[i] = conn
		defer conn.Close()
	}
	if _, err := udpClients[0].Write([]byte("start wake")); err != nil {
		t.Fatal(err)
	}
	if attempt := receiveLoadEvent(t, apiCalls, "stalled API request"); attempt != 1 {
		t.Fatalf("first API attempt = %d", attempt)
	}
	waitLoadEvent(t, reads, "initial UDP packet")

	type playerResult struct {
		conn    net.Conn
		latency time.Duration
		err     error
	}
	players := make(chan playerResult, tcpPlayers)
	udpSent := make(chan error, udpSources)
	burst := make(chan struct{})
	for i := range tcpPlayers {
		go func() {
			<-burst
			joinStarted := time.Now()
			conn, dialErr := (&net.Dialer{}).DialContext(listenerCtx, "tcp", tcpListener.Addr().String())
			if dialErr != nil {
				players <- playerResult{err: dialErr}
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			_ = conn.SetDeadline(time.Now().Add(loadSafetyTimeout))
			payload := fmt.Sprintf("player-%02d", i)
			err := exchangeLoadPayload(conn, payload)
			players <- playerResult{conn: conn, latency: time.Since(joinStarted), err: err}
		}()
	}
	for _, conn := range udpClients[1:] {
		go func() {
			<-burst
			_, err := conn.Write([]byte("burst wake"))
			udpSent <- err
		}()
	}
	close(burst)
	for range udpSources {
		if err := receiveLoadEvent(t, udpSent, "UDP burst send"); err != nil {
			t.Fatal(err)
		}
		waitLoadEvent(t, reads, "UDP receive while API stalled")
	}
	// An additional read proves the previous datagram finished heuristic
	// processing (ReadFrom is called again only after worker.request returns).
	if _, err := udpClients[0].Write([]byte("receive barrier")); err != nil {
		t.Fatal(err)
	}
	waitLoadEvent(t, reads, "UDP heuristic barrier")
	var latestDeadline time.Time
	for range tcpPlayers {
		waitLoadEvent(t, tcpWake.joined, "TCP caller sharing stalled wake")
		deadline := receiveLoadEvent(t, tcpWake.deadlines, "TCP join deadline")
		if deadline.After(latestDeadline) {
			latestDeadline = deadline
		}
	}
	if patches.Load() != 1 || apiActive.Load() != 1 || udpWake.calls.Load() != 1 || udpWake.maximum.Load() != 1 {
		t.Fatalf("stalled burst: patches=%d API active=%d UDP calls=%d UDP max=%d", patches.Load(), apiActive.Load(), udpWake.calls.Load(), udpWake.maximum.Load())
	}
	udpElapsed := time.Since(started)
	release()
	if attempt := receiveLoadEvent(t, apiCalls, "recovery PATCH"); attempt != 2 {
		t.Fatalf("recovery attempt = %d, want 2", attempt)
	}
	for range tcpPlayers {
		if err := receiveLoadEvent(t, tcpWake.finished, "shared TCP wake completion"); err != nil {
			t.Fatalf("TCP shared wake: %v", err)
		}
	}
	// The worker may consume just one queued request after its active call.
	for range 2 {
		if err := receiveLoadEvent(t, udpWake.finished, "bounded UDP wake completion"); err != nil {
			t.Fatal(err)
		}
	}
	if udpWake.calls.Load() != 2 || udpWake.maximum.Load() != 1 {
		t.Fatalf("UDP work calls=%d max=%d, want one active plus one queued", udpWake.calls.Load(), udpWake.maximum.Load())
	}

	// This timer models deliberately delayed game startup after successful
	// API recovery. It is not a guess about when another goroutine will run.
	coldStart := time.NewTimer(200 * time.Millisecond)
	defer coldStart.Stop()
	<-coldStart.C
	game, err := lc.Listen(listenerCtx, "tcp", upstreamAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	gameDone := make(chan struct{})
	go func() {
		defer close(gameDone)
		var echoes sync.WaitGroup
		defer echoes.Wait()
		for range tcpPlayers {
			conn, acceptErr := game.Accept()
			if acceptErr != nil {
				return
			}
			echoes.Add(1)
			go func() {
				defer echoes.Done()
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	var maxLatency, totalLatency time.Duration
	connected := make([]net.Conn, 0, tcpPlayers)
	for range tcpPlayers {
		player := receiveLoadEvent(t, players, "TCP game handoff and echo")
		if player.err != nil {
			t.Fatalf("TCP handoff: %v", player.err)
		}
		connected = append(connected, player.conn)
		totalLatency += player.latency
		if player.latency > maxLatency {
			maxLatency = player.latency
		}
	}
	if patches.Load() != 2 || apiMaximum.Load() != 1 {
		t.Fatalf("API attempts=%d maximum concurrency=%d, want 2 and 1", patches.Load(), apiMaximum.Load())
	}
	// Waiting for the actual last recorded join deadline, rather than an
	// estimated goroutine start time, verifies every session outlives it.
	deadlineElapsed := time.NewTimer(time.Until(latestDeadline) + time.Millisecond)
	defer deadlineElapsed.Stop()
	<-deadlineElapsed.C
	for _, conn := range connected {
		if err := exchangeLoadPayload(conn, "after join deadline"); err != nil {
			t.Fatalf("gameplay beyond join budget: %v", err)
		}
	}

	stopListeners()
	waitLoadEvent(t, tcpDone, "TCP listener shutdown")
	waitLoadEvent(t, udpDone, "UDP listener and worker shutdown")
	drainDone := make(chan struct{})
	go func() {
		sessions.drain(loadSafetyTimeout, stopSessions)
		close(drainDone)
	}()
	// Established sessions retain the drain context after listeners stop.
	for _, conn := range connected {
		if err := exchangeLoadPayload(conn, "during shutdown drain"); err != nil {
			t.Fatalf("draining gameplay: %v", err)
		}
		_ = conn.Close()
	}
	waitLoadEvent(t, drainDone, "session drain")
	waitLoadEvent(t, gameDone, "upstream echo shutdown")
	if sessions.active.Load() != 0 || udpWake.active.Load() != 0 || apiActive.Load() != 0 {
		t.Fatalf("shutdown left sessions=%d UDP wakes=%d API requests=%d", sessions.active.Load(), udpWake.active.Load(), apiActive.Load())
	}
	select {
	case err := <-errCh:
		t.Fatalf("listener failure: %v", err)
	default:
	}
	t.Logf("mixed load: TCP attempts=%d handoffs=%d UDP sources=%d received=%d API PATCH attempts=%d max_API_concurrency=%d UDP wake calls=%d max_UDP_wake_concurrency=%d UDP_receive_elapsed=%s handoff_mean=%s handoff_max=%s total_elapsed=%s", tcpPlayers, len(connected), len(udpClients), udpSources+2, patches.Load(), apiMaximum.Load(), udpWake.calls.Load(), udpWake.maximum.Load(), udpElapsed, totalLatency/tcpPlayers, maxLatency, time.Since(started))
}

func exchangeLoadPayload(conn net.Conn, payload string) error {
	if _, err := io.WriteString(conn, payload); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return fmt.Errorf("read echo: %w", err)
	}
	if string(got) != payload {
		return fmt.Errorf("echo = %q, want %q", got, payload)
	}
	return nil
}
