package archive

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// gapAcceptFailListener is a listener whose socket has failed: every Accept
// returns err.
type gapAcceptFailListener struct {
	err error
}

func (l gapAcceptFailListener) Accept() (net.Conn, error) { return nil, l.err }
func (gapAcceptFailListener) Close() error                { return nil }
func (gapAcceptFailListener) Addr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func TestNotifyDoesNotBlockWhenSignalPending(t *testing.T) {
	ch := make(chan struct{}, 1)
	notify(ch)
	notify(ch) // the channel is full: the second signal is dropped, not blocked on
	if len(ch) != 1 {
		t.Fatalf("pending signals = %d, want 1", len(ch))
	}
}

func TestServeReturnsListenerFailure(t *testing.T) {
	errAccept := errors.New("socket broke")
	orig := listen
	listen = func(string, string) (net.Listener, error) {
		return gapAcceptFailListener{err: errAccept}, nil
	}
	t.Cleanup(func() { listen = orig })

	pki := newTestPKI(t)
	cfg := ServerConfig{Addr: "127.0.0.1:0", TLS: pki.serverTLS, UID: "u", IdleTimeout: time.Minute}
	err := Serve(context.Background(), cfg, http.HandlerFunc(noop))
	if !errors.Is(err, errAccept) {
		t.Fatalf("Serve error = %v, want the listener failure", err)
	}
}
