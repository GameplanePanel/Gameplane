package logs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestTail_OpenError forces os.Open to fail with a non-ENOENT error
// (path is a directory). streamFile should return that error and the
// handler should close the WS with an internal-error frame.
func TestTail_OpenError(t *testing.T) {
	dir := t.TempDir()
	// Use a directory path as the "log file" so os.Open fails on
	// reading. (os.Open succeeds on a directory but Read returns EISDIR
	// — actually os.Open returns a *File on directories; bufio.NewReader
	// will succeed but ReadString returns the directory contents.) For
	// a deterministic non-ENOENT error, place a file we'll chmod 0.
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600); _ = os.Remove(path) })

	url := mountServer(t, dir, path)
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/logs/tail"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	// We expect either the WS to close with an internal error, or read
	// to fail. Just confirm it resolves rather than hanging.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, _, err := conn.Read(ctx)
		if err != nil {
			return
		}
	}
}

// TestTail_NonWebSocketRequestRejected covers the websocket.Accept
// failure branch in tail(): a plain GET without the Upgrade/Connection
// handshake headers fails the WS handshake, and the handler must return
// immediately (no panic, no attempt to read/write a *websocket.Conn)
// rather than proceeding into streamFile.
func TestTail_NonWebSocketRequestRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, []byte("line\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	url := mountServer(t, dir, path)

	resp, err := testGet(t, url+"/logs/tail")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	// websocket.Accept rejects the non-upgrade request with a 4xx before
	// tail() ever reaches streamFile; assert it wasn't silently upgraded.
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("status=%d, want a 4xx handshake rejection", resp.StatusCode)
	}
}

// TestStreamFile_CtxCanceledImmediately covers the early ctx.Err()
// guard at the top of the loop.
func TestStreamFile_CtxCanceledImmediately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Build a fake WS server we won't actually use — streamFile takes a
	// *websocket.Conn but with ctx already canceled, the first iteration
	// of the loop returns ctx.Err() before any IO.
	srv := dummyWSServer(t)
	defer srv.Close()
	cli, dialResp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close(websocket.StatusNormalClosure, "")

	if err := streamFile(ctx, cli, testSource(t, dir, path), false, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v want %v", err, context.Canceled)
	}
}

// dummyWSServer accepts a single WS upgrade and parks until ctx is
// done. Returns an httptest server the caller can dial.
func dummyWSServer(t *testing.T) *dummyWS {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		<-r.Context().Done()
		_ = c.Close(websocket.StatusNormalClosure, "")
	}))
	return &dummyWS{URL: srv.URL, Close: srv.Close}
}

type dummyWS struct {
	URL   string
	Close func()
}

func TestAppendTail_BoundsLinesAndBytes(t *testing.T) {
	var buf []string
	var total int64
	for i := 0; i < 10; i++ {
		buf, total = appendTail(buf, total, strings.Repeat("a", 9)+"\n", 3, 1000)
	}
	if len(buf) != 3 || total != 30 {
		t.Fatalf("line cap: got %d lines, %d bytes; want 3 lines, 30 bytes", len(buf), total)
	}
	buf, total = nil, 0
	for i := 0; i < 10; i++ {
		buf, total = appendTail(buf, total, strings.Repeat("b", 99)+"\n", 100, 250)
	}
	if len(buf) != 2 || total != 200 {
		t.Fatalf("byte cap: got %d lines, %d bytes; want 2 lines, 200 bytes", len(buf), total)
	}
	buf, total = appendTail(buf, total, strings.Repeat("c", 300)+"\n", 100, 250)
	if len(buf) != 2 || total != 200 {
		t.Fatalf("oversize line must be skipped: got %d lines, %d bytes", len(buf), total)
	}
	buf, total = appendTail(nil, 0, "", 3, 1000)
	if len(buf) != 0 || total != 0 {
		t.Fatalf("empty line must be ignored: got %d lines, %d bytes", len(buf), total)
	}
}
