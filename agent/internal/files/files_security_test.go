package files

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// secMkfifo creates a named pipe at rel below root. A FIFO is neither a
// directory nor a regular file, so the file-browser must refuse it.
func secMkfifo(t *testing.T, root, rel string) {
	t.Helper()
	if err := syscall.Mkfifo(filepath.Join(root, rel), 0o600); err != nil {
		t.Fatalf("mkfifo %s: %v", rel, err)
	}
}

// TestRead_RefusesFIFO checks that /files/read answers 400 for a named pipe
// instead of serving it. The open is non-blocking, so the request returns
// without a writer ever attaching to the FIFO.
func TestRead_RefusesFIFO(t *testing.T) {
	srvURL, root := newServer(t)
	secMkfifo(t, root, "pipe")
	resp := get(t, srvURL, "/files/read", url.Values{"path": []string{"/pipe"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("read FIFO: got %d, want 400", resp.StatusCode)
	}
}

// TestDownload_RefusesFIFO checks that /files/download answers 400 for a
// named pipe nested below the root, not only for one at the top level.
func TestDownload_RefusesFIFO(t *testing.T) {
	srvURL, root := newServer(t)
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	secMkfifo(t, root, filepath.Join("sub", "pipe"))
	resp := get(t, srvURL, "/files/download", url.Values{"path": []string{"/sub/pipe"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("download FIFO: got %d, want 400", resp.StatusCode)
	}
}
