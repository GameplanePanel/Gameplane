package logs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

func mountServer(t *testing.T, root, path string) string {
	t.Helper()
	r := chi.NewRouter()
	Mount(r, root, path)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL
}

func testGet(t *testing.T, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return http.DefaultClient.Do(req)
}

func TestTail_NotConfigured(t *testing.T) {
	url := mountServer(t, t.TempDir(), "")
	resp, err := testGet(t, url+"/logs/tail")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDownload_NotConfigured(t *testing.T) {
	url := mountServer(t, t.TempDir(), "")
	resp, err := testGet(t, url+"/logs/download")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDownload_FileMissing(t *testing.T) {
	dir := t.TempDir()
	url := mountServer(t, dir, filepath.Join(dir, "latest.log"))
	resp, err := testGet(t, url+"/logs/download")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDownload_ServesFileAsAttachment(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "latest.log")
	if err := os.WriteFile(logPath, []byte("line one\nline two\n"), 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}

	url := mountServer(t, dir, logPath)
	resp, err := testGet(t, url+"/logs/download")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="latest.log"` {
		t.Fatalf("content-disposition=%q", cd)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "line one\nline two\n" {
		t.Fatalf("body=%q", body)
	}
}

func TestTail_StreamsLinesAndRotates(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "latest.log")
	if err := os.WriteFile(logPath, []byte(""), 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}

	url := mountServer(t, dir, logPath)
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/logs/tail?from=start"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	// Append a line.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	// Read first line.
	mt, b, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.MessageText || string(b) != "hello\n" {
		t.Fatalf("got %d %q", mt, b)
	}

	// Rotate: rename current and create fresh file with a new line.
	rotated := logPath + ".1"
	if err := os.Rename(logPath, rotated); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	f2, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if _, err := f2.WriteString("after-rotate\n"); err != nil {
		t.Fatalf("write2: %v", err)
	}
	_ = f2.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, b, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read after rotate: %v", err)
		}
		if string(b) == "after-rotate\n" {
			return
		}
	}
	t.Fatal("did not see post-rotate line")
}

func TestTail_FileMissingThenAppears(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "latest.log")
	url := mountServer(t, dir, logPath)
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/logs/tail?from=start"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	// Wait briefly so streamFile hits ENOENT once and enters its 1s
	// retry sleep, then create the file.
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(logPath, []byte("late-start\n"), 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, b, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(b) == "late-start\n" {
			return
		}
	}
	t.Fatal("did not see line after file appeared")
}

func TestCheckRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	t.Run("same file", func(t *testing.T) {
		rot, err := checkRotation(testSource(t, dir, path), f)
		if err != nil || rot {
			t.Fatalf("rot=%v err=%v", rot, err)
		}
	})

	t.Run("file deleted", func(t *testing.T) {
		_ = os.Remove(path)
		rot, err := checkRotation(testSource(t, dir, path), f)
		if err != nil || !rot {
			t.Fatalf("rot=%v err=%v", rot, err)
		}
	})

	t.Run("file replaced (rotated)", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("b"), 0o600); err != nil {
			t.Fatalf("recreate: %v", err)
		}
		rot, err := checkRotation(testSource(t, dir, path), f)
		if err != nil || !rot {
			t.Fatalf("rot=%v err=%v", rot, err)
		}
	})
}

func TestSleep_Cancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(ctx, time.Hour); err == nil {
		t.Fatal("expected ctx err")
	}
}

func TestSleep_Elapses(t *testing.T) {
	if err := sleep(context.Background(), 10*time.Millisecond); err != nil {
		t.Fatalf("err=%v", err)
	}
}

// TestTail_DefaultFromEndSkipsExistingContent drives the default (no
// ?from param) path through streamFile's fromEnd branch: pre-existing
// content in the file must not be delivered, only lines appended after
// the WS connects.
func TestTail_DefaultFromEndSkipsExistingContent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "latest.log")
	if err := os.WriteFile(logPath, []byte("already-here\n"), 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}

	url := mountServer(t, dir, logPath)
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/logs/tail"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	// Let the server reach streamFile's Seek(0, io.SeekEnd) before we
	// append; otherwise the seek can land past "fresh\n" and the read
	// below blocks until the context deadline.
	time.Sleep(100 * time.Millisecond)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("fresh\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	mt, b, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.MessageText || string(b) != "fresh\n" {
		t.Fatalf("got %d %q, want only the post-connect line", mt, b)
	}
}

// TestCheckRotation_StatError exercises the rotation probe error branch (not the
// NotExist branch, which TestCheckRotation already covers): make the
// parent directory unsearchable so the descriptor walk fails with EACCES
// while the already-open file handle's own Stat still succeeds.
func TestCheckRotation_StatError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — permissions are bypassed")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(sub, "f")
	if err := os.WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	rot, err := checkRotation(testSource(t, dir, path), f)
	if err == nil {
		t.Fatal("expected a stat error, got nil")
	}
	if rot {
		t.Fatal("rot should be false on a stat error, not treated as a rotation")
	}
}

// TestTail_TailParameterReplaysLastNLines verifies that ?tail=N causes
// the agent to replay the last N lines before following new output.
func TestTail_TailParameterReplaysLastNLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "latest.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\nline3\nline4\nline5\n"), 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}

	url := mountServer(t, dir, logPath)
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/logs/tail?tail=2"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	// Should receive the last 2 lines before any new output.
	mt, b, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if mt != websocket.MessageText || string(b) != "line4\n" {
		t.Fatalf("got %d %q, want line4", mt, b)
	}

	mt, b, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if mt != websocket.MessageText || string(b) != "line5\n" {
		t.Fatalf("got %d %q, want line5", mt, b)
	}

	// Append a new line and verify it's delivered.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("line6\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	mt, b, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read new: %v", err)
	}
	if mt != websocket.MessageText || string(b) != "line6\n" {
		t.Fatalf("got %d %q, want line6", mt, b)
	}
}

// TestTail_InvalidTailParameterIgnored verifies that an invalid ?tail
// parameter (non-numeric or negative) is ignored and falls back to the
// default behavior (from=end).
func TestTail_InvalidTailParameterIgnored(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "latest.log")
	if err := os.WriteFile(logPath, []byte("already-here\n"), 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}

	url := mountServer(t, dir, logPath)
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/logs/tail?tail=invalid"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if dialResp != nil && dialResp.Body != nil {
		defer dialResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	// Wait briefly for seek-to-end to complete.
	time.Sleep(100 * time.Millisecond)

	// Append a new line.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("fresh\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	// Should receive only the new line (old content was skipped).
	mt, b, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.MessageText || string(b) != "fresh\n" {
		t.Fatalf("got %d %q, want only the post-connect line", mt, b)
	}
}
