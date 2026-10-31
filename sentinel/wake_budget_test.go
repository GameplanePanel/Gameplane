package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

func TestWakeSharedFailureAndImmediateRetry(t *testing.T) {
	w, client := newFakeGameServerWaker(t, "alpha", "games", time.Minute, nil)
	started, release := make(chan struct{}), make(chan struct{})
	var patches atomic.Int32
	failure := errors.New("API unavailable")
	client.PrependReactor("patch", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) {
		if patches.Add(1) == 1 {
			close(started)
			<-release
			return true, nil, failure
		}
		return false, nil, nil
	})
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- w.RequestWake(t.Context()) }()
	<-started
	waiting := &observedWaitContext{Context: t.Context(), observed: make(chan struct{})}
	go func() { second <- w.RequestWake(waiting) }()
	select {
	case err := <-second:
		close(release)
		t.Fatalf("in-flight waiter returned before wake completed: %v", err)
	case <-waiting.observed:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("second wake request never reached its wait")
	}
	close(release)
	for _, result := range []<-chan error{first, second} {
		select {
		case err := <-result:
			if !errors.Is(err, failure) {
				t.Fatalf("got %v, want shared failure", err)
			}
		case <-time.After(time.Second):
			t.Fatal("wake waiter did not finish")
		}
	}
	if err := w.RequestWake(t.Context()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if patches.Load() != 2 {
		t.Fatalf("got %d patches, want failed attempt and retry", patches.Load())
	}
}

// Done is evaluated when RequestWake waits on the shared result. This handshake
// avoids relying on the scheduler running the second goroutine within a sleep.
type observedWaitContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestWakePatchHasIndependentTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		select {
		case <-req.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	w := newWaker(client.Resource(gameServerGVR).Namespace("games"), "alpha", time.Minute)
	w.patchTimeout = 25 * time.Millisecond
	started := time.Now()
	if err := w.RequestWake(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("wake PATCH exceeded its own timeout")
	}
}

type contextBoundWake struct{ started chan struct{} }

func (w *contextBoundWake) RequestWake(ctx context.Context) error {
	close(w.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestJoinBudgetIncludesWakeRequest(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	wake := &contextBoundWake{started: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleJoin(t.Context(), server, bufio.NewReader(server), wake, joinParams{upstreamAddr: "127.0.0.1:1", deadline: 25 * time.Millisecond, pollInterval: time.Millisecond})
	}()
	<-wake.started
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("join deadline did not bound wake request")
	}
}

func TestUDPWakeWorkerCoalescesAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	wake := &contextBoundWake{started: make(chan struct{})}
	worker := newUDPWakeWorker(ctx, wake)
	worker.request()
	<-wake.started
	for range 1000 {
		worker.request()
	}
	if len(worker.requests) > 1 {
		t.Fatal("unbounded UDP wake backlog")
	}
	cancel()
	select {
	case <-worker.done:
	case <-time.After(time.Second):
		t.Fatal("UDP wake worker did not stop")
	}
}

func TestSuccessfulGameplayOutlivesJoinBudget(t *testing.T) {
	upstream, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	accepted := make(chan struct{})
	go func() {
		conn, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		_, _ = io.Copy(conn, conn)
	}()
	server, player := tcpPipe(t)
	defer player.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		handleJoin(ctx, server, bufio.NewReader(server), newCountingWaker(nil), joinParams{upstreamAddr: upstream.Addr().String(), deadline: 100 * time.Millisecond, pollInterval: time.Millisecond})
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("join did not reach game")
	}
	// The session must retain the drain context after the short join context
	// is cancelled, including after its original deadline has elapsed.
	time.Sleep(200 * time.Millisecond)
	_ = player.SetDeadline(time.Now().Add(time.Second))
	if _, err := player.Write([]byte("still playing")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("still playing"))
	if _, err := io.ReadFull(player, got); err != nil || string(got) != "still playing" {
		t.Fatalf("gameplay after join budget: %q, %v", got, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session did not stop on drain cancellation")
	}
}

type observedPacketConn struct {
	net.PacketConn
	reads chan struct{}
}

func (c observedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(p)
	if err == nil {
		c.reads <- struct{}{}
	}
	return n, addr, err
}

func TestUDPReceiveContinuesWhileWakeIsStalled(t *testing.T) {
	listener, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := (&net.Dialer{}).DialContext(t.Context(), "udp", listener.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wake := &contextBoundWake{started: make(chan struct{})}
	reads := make(chan struct{}, 128)
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveUDPPackets(ctx, observedPacketConn{listener, reads}, PortConfig{}, wake, Config{UDPThreshold: 1, UDPWindow: time.Minute, UDPMaxSources: 10}, make(chan error, 1))
	}()
	if _, err := client.Write([]byte("wake")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake.started:
	case <-time.After(time.Second):
		t.Fatal("wake did not start")
	}
	started := time.Now()
	for range 100 {
		if _, err := client.Write([]byte("game packet")); err != nil {
			t.Fatal(err)
		}
	}
	for range 101 {
		select {
		case <-reads:
		case <-time.After(time.Second):
			t.Fatal("UDP receive blocked behind stalled wake PATCH")
		}
	}
	t.Logf("received 101 datagrams while wake blocked in %s", time.Since(started))
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UDP listener/worker did not stop")
	}
}
