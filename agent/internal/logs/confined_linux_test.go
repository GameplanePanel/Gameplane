package logs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/sys/unix"
)

func testSource(t *testing.T, root, path string) *logSource {
	t.Helper()
	source, err := newHandler(root, path).openSource()
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	t.Cleanup(source.close)
	return source
}

func writeLogFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireTailFailure(t *testing.T, serverURL, query string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(serverURL, "http")+"/logs/tail"+query, nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	_, body, err := conn.Read(ctx)
	if err == nil {
		t.Fatalf("unsafe log read delivered data: %q", body)
	}
	var closeError websocket.CloseError
	if !errors.As(err, &closeError) || closeError.Code != websocket.StatusInternalError {
		t.Fatalf("want prompt internal-error close, got %v", err)
	}
	if strings.Contains(closeError.Reason, "open") || strings.Contains(closeError.Reason, "/") {
		t.Fatalf("filesystem detail leaked in close: %q", closeError.Reason)
	}
}

// Removing no-follow protection at either the final or an ancestor component
// must expose the sentinel and fail these real HTTP and WebSocket requests.
func TestLogs_RejectEscapedSymlinks(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		name := "final"
		if ancestor {
			name = "ancestor"
		}
		t.Run(name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			secret := filepath.Join(outside, "tls.key")
			writeLogFixture(t, secret, "agent-only-secret\n")
			path := filepath.Join(root, "latest.log")
			if ancestor {
				if err := os.Symlink(outside, filepath.Join(root, "logs")); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(root, "logs", "tls.key")
			} else if err := os.Symlink(secret, path); err != nil {
				t.Fatal(err)
			}
			url := mountServer(t, root, path)
			response, err := testGet(t, url+"/logs/download")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusInternalServerError || strings.Contains(string(body), "agent-only-secret") || strings.Contains(string(body), root) || strings.Contains(string(body), outside) {
				t.Fatalf("unsafe download status=%d body=%q", response.StatusCode, body)
			}
			for _, query := range []string{"?from=start", "", "?tail=1"} {
				requireTailFailure(t, url, query)
			}
		})
	}
}

// Both configuration forms must resolve beneath the explicit root. A prefix
// check or deriving the root from the configured path would read the secret.
func TestLogs_RejectOutsidePaths(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(parent, "data-secret.log")
	writeLogFixture(t, secret, "secret\n")
	for _, path := range []string{secret, "../data-secret.log", root} {
		url := mountServer(t, root, path)
		response, err := testGet(t, url+"/logs/download")
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusInternalServerError {
			t.Fatalf("path=%q status=%d", path, response.StatusCode)
		}
		requireTailFailure(t, url, "?from=start")
	}
}

func TestLogs_AbsoluteAndRelativePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "logs", "latest.log")
	writeLogFixture(t, path, "safe log\n")
	for _, configured := range []string{path, "logs/latest.log"} {
		url := mountServer(t, root, configured)
		response, err := testGet(t, url+"/logs/download")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "safe log\n" {
			t.Fatalf("path=%q status=%d body=%q err=%v", configured, response.StatusCode, body, err)
		}
	}
}

// Header is first consulted after the handler opens and stats the log. Swapping
// here deterministically catches a later ServeFile/path reopen without a hook
// in production code.
type swapOnHeader struct {
	*httptest.ResponseRecorder
	swap func()
}

func (w *swapOnHeader) Header() http.Header {
	if w.swap != nil {
		swap := w.swap
		w.swap = nil
		swap()
	}
	return w.ResponseRecorder.Header()
}

func TestDownload_PinsOpenedFileAcrossPathSwap(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path, secret := filepath.Join(root, "latest.log"), filepath.Join(outside, "tls.key")
	writeLogFixture(t, path, "original log\n")
	writeLogFixture(t, secret, "agent-only-secret\n")
	w := &swapOnHeader{ResponseRecorder: httptest.NewRecorder(), swap: func() {
		if err := os.Rename(path, path+".1"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(secret, path); err != nil {
			t.Fatal(err)
		}
	}}
	newHandler(root, path).download(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/logs/download", nil))
	if w.Code != http.StatusOK || w.Body.String() != "original log\n" {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
}

// Reading the first frame synchronizes with a successful initial open. The
// next probe/reopen must reject a replaced final component or log directory.
func TestTail_RejectSymlinkRotation(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(map[bool]string{false: "final", true: "ancestor"}[ancestor], func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			logDir := filepath.Join(root, "logs")
			if err := os.Mkdir(logDir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(logDir, "latest.log")
			secret := filepath.Join(outside, "latest.log")
			writeLogFixture(t, path, "first\n")
			writeLogFixture(t, secret, "agent-only-secret\n")
			url := mountServer(t, root, path)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http")+"/logs/tail?from=start", nil)
			if response != nil && response.Body != nil {
				defer response.Body.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			_, body, err := conn.Read(ctx)
			if err != nil || string(body) != "first\n" {
				t.Fatalf("initial read=%q err=%v", body, err)
			}
			old, target := path, secret
			if ancestor {
				old, target = logDir, outside
			}
			if err := os.Rename(old, old+".1"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, old); err != nil {
				t.Fatal(err)
			}
			_, body, err = conn.Read(ctx)
			if err == nil || websocket.CloseStatus(err) != websocket.StatusInternalError {
				t.Fatalf("unsafe rotation read=%q err=%v", body, err)
			}
		})
	}
}

// Keeping a parent FD between probes would miss a legitimate directory rename
// and later follow the wrong log. The rotation probe must walk from root again.
func TestCheckRotation_RewalksReplacedDirectory(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	if err := os.Mkdir(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logDir, "latest.log")
	writeLogFixture(t, path, "old\n")
	source := testSource(t, root, path)
	f, err := source.open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(logDir, logDir+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLogFixture(t, path, "new\n")
	rotated, err := checkRotation(source, f)
	if err != nil || !rotated {
		t.Fatalf("rotated=%v err=%v", rotated, err)
	}
	reopened, err := source.open()
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	body, err := io.ReadAll(reopened)
	if err != nil || string(body) != "new\n" {
		t.Fatalf("new log=%q err=%v", body, err)
	}
}

// Even a successful rotation probe cannot authorize a later path reopen: a
// symlink swapped in between those two operations must still be refused.
func TestRotation_ReopenRejectsSwapAfterProbe(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path, secret := filepath.Join(root, "latest.log"), filepath.Join(outside, "tls.key")
	writeLogFixture(t, path, "old\n")
	writeLogFixture(t, secret, "agent-only-secret\n")
	source := testSource(t, root, path)
	f, err := source.open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeLogFixture(t, path, "new\n")
	rotated, err := checkRotation(source, f)
	if err != nil || !rotated {
		t.Fatalf("rotated=%v err=%v", rotated, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, path); err != nil {
		t.Fatal(err)
	}
	if err := streamFile(t.Context(), nil, source, false, 0); err == nil {
		t.Fatal("reopen accepted the symlink after a successful rotation probe")
	}
}

// The trusted root may be an operator-managed symlink, but changing that link
// after a request starts must not change where its later opens resolve.
func TestLogSource_PinsTrustedRoot(t *testing.T) {
	parent, actual, outside := t.TempDir(), t.TempDir(), t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.Symlink(actual, root); err != nil {
		t.Fatal(err)
	}
	writeLogFixture(t, filepath.Join(actual, "latest.log"), "safe\n")
	writeLogFixture(t, filepath.Join(outside, "latest.log"), "agent-only-secret\n")
	source := testSource(t, root, "latest.log")
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	f, err := source.open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil || string(body) != "safe\n" {
		t.Fatalf("pinned root log=%q err=%v", body, err)
	}
}

func TestLogs_RejectNonregularFilesWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"directory", "fifo", "device"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "latest.log")
			switch kind {
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "device":
				// Use an existing device with an explicit trusted root so this
				// guard is exercised even when CI cannot create device nodes.
				root, path = "/dev", "/dev/null"
				fi, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				if fi.Mode()&os.ModeCharDevice == 0 {
					t.Fatal("device fixture is not a character device")
				}
			}
			source := testSource(t, root, path)
			done := make(chan error, 1)
			go func() {
				f, err := source.open()
				if f != nil {
					_ = f.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("nonregular log accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("opening nonregular log blocked")
			}
			url := mountServer(t, root, path)
			response, err := testGet(t, url+"/logs/download")
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status=%d", response.StatusCode)
			}
			requireTailFailure(t, url, "?from=start")
		})
	}
}
