package console

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
)

type fakeRcon struct {
	gotCmd string
	out    string
	err    error
}

func (f *fakeRcon) Exec(cmd string) (string, error) {
	f.gotCmd = cmd
	return f.out, f.err
}

func newServer(t *testing.T, rc Rcon) (*httptest.Server, string) {
	t.Helper()
	r := chi.NewRouter()
	Mount(r, rc)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/console"
	return srv, wsURL
}

func dial(t *testing.T, wsURL string) (*websocket.Conn, context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		t.Cleanup(func() { _ = dialResp.Body.Close() })
	}
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		cancel()
	})
	return conn, ctx, cancel
}

func TestConsole_RoundTrip(t *testing.T) {
	rc := &fakeRcon{out: "pong"}
	_, wsURL := newServer(t, rc)
	conn, ctx, _ := dial(t, wsURL)

	if err := wsjson.Write(ctx, conn, Envelope{Kind: "cmd", Body: "ping"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got Envelope
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Kind != "out" || got.Body != "pong" {
		t.Fatalf("got %+v", got)
	}
	if rc.gotCmd != "ping" {
		t.Fatalf("rcon got %q", rc.gotCmd)
	}
}

func TestConsole_RconError(t *testing.T) {
	_, wsURL := newServer(t, &fakeRcon{err: errors.New("rcon offline")})
	conn, ctx, _ := dial(t, wsURL)

	_ = wsjson.Write(ctx, conn, Envelope{Kind: "cmd", Body: "list"})
	var got Envelope
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	// Error responses carry a generic message, not the upstream error details,
	// to avoid leaking RCON secrets or connection information to the client.
	// The detailed error is logged separately by the handler.
	if got.Kind != "err" || got.Body != "upstream unavailable" {
		t.Fatalf("got %+v, expected Body='upstream unavailable'", got)
	}
}

// TestConsole_UnclassifiedErrorOmitsSecret confirms the warning logged for an
// unclassified Exec failure does not contain the submitted command or error
// details, since the command may carry a secret and error text from RCON
// servers sometimes contains arguments or other unclassified data.
func TestConsole_UnclassifiedErrorOmitsSecret(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const cmd = `setpassword "hunter2"`
	rc := &fakeRcon{err: errors.New("connection dropped with arg hunter2")}
	_, wsURL := newServer(t, rc)
	conn, ctx, _ := dial(t, wsURL)

	if err := wsjson.Write(ctx, conn, Envelope{Kind: "cmd", Body: cmd}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got Envelope
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Kind != "err" || got.Body != "upstream unavailable" {
		t.Fatalf("got %+v", got)
	}
	logged := buf.String()
	if strings.Contains(logged, "hunter2") {
		t.Fatalf("log contains the command password: %q", logged)
	}
	if strings.Contains(logged, "connection dropped") {
		t.Fatalf("log contains unclassified error details: %q", logged)
	}
	if !strings.Contains(logged, "unclassified error") {
		t.Fatalf("log should indicate redaction: %q", logged)
	}
}

func TestConsole_IgnoresEmptyAndNonCmd(t *testing.T) {
	rc := &fakeRcon{out: "ok"}
	_, wsURL := newServer(t, rc)
	conn, ctx, _ := dial(t, wsURL)

	// Non-"cmd" kind: server should not call Rcon and should not respond.
	_ = wsjson.Write(ctx, conn, Envelope{Kind: "noop", Body: "x"})
	// Empty body cmd: also ignored.
	_ = wsjson.Write(ctx, conn, Envelope{Kind: "cmd", Body: ""})
	// Real one: must respond.
	_ = wsjson.Write(ctx, conn, Envelope{Kind: "cmd", Body: "go"})

	var got Envelope
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Body != "ok" || rc.gotCmd != "go" {
		t.Fatalf("got=%+v rcon=%q", got, rc.gotCmd)
	}
}

// TestConsole_NonWebSocketRequestRejected covers the websocket.Accept
// failure branch in serve(): a plain GET without the Upgrade/Connection
// handshake headers fails the WS handshake, and the handler must return
// immediately rather than entering the read/write loop.
func TestConsole_NonWebSocketRequestRejected(t *testing.T) {
	srv, _ := newServer(t, &fakeRcon{})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/console", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("status=%d, want a 4xx handshake rejection", resp.StatusCode)
	}
}

func TestConsole_ClientCloseEndsLoop(t *testing.T) {
	_, wsURL := newServer(t, &fakeRcon{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := conn.Close(websocket.StatusNormalClosure, "bye"); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Give the server loop a moment to observe the close. Nothing to
	// assert beyond "no panic, no goroutine leak"; coverage of the
	// read-error return path is the goal.
	time.Sleep(50 * time.Millisecond)
}

type queuedConsole struct {
	*rcon.Commands
	waiting  chan struct{}
	finished chan error
}

func (q queuedConsole) ExecContext(ctx context.Context, cmd string) (string, error) {
	close(q.waiting)
	out, err := q.Commands.ExecContext(ctx, cmd)
	q.finished <- err
	return out, err
}

func TestConsole_DisconnectCancelsQueuedCommand(t *testing.T) {
	rc := &fakeRcon{}
	q := queuedConsole{rcon.NewCommands(rc), make(chan struct{}), make(chan error, 1)}
	_, wsURL := newServer(t, q)
	conn, ctx, _ := dial(t, wsURL)
	err := q.Sequence(ctx, func(rcon.Executor) error {
		if err := wsjson.Write(ctx, conn, Envelope{Kind: "cmd", Body: "stop"}); err != nil {
			return err
		}
		select {
		case <-q.waiting:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := conn.CloseNow(); err != nil {
			return err
		}
		select {
		case err := <-q.finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("disconnected command returned %v", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
	if err != nil || rc.gotCmd != "" {
		t.Fatalf("command after disconnect: %q, %v", rc.gotCmd, err)
	}
}
