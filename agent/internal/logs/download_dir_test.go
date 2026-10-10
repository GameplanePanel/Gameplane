package logs

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A configured log path that points at a directory is a misconfiguration:
// the download handler must 500 rather than try to serve a directory.
func TestDownload_PathIsDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "logs")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	url := mountServer(t, root, path) // the path is a directory below root
	resp, err := testGet(t, url+"/logs/download")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", resp.StatusCode)
	}
}
