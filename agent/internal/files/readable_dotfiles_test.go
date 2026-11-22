package files

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestReadAllowsPackManifestDotfiles(t *testing.T) {
	srvURL, root := newServer(t)
	for _, name := range []string{".modrinth-modpack-manifest.json", ".curseforge-manifest.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(`{"versionId":"v1"}`), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		for _, p := range []string{name, "/" + name} {
			resp := get(t, srvURL, "/files/read", url.Values{"path": {p}})
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK || string(body) != `{"versionId":"v1"}` {
				t.Fatalf("read %q = %d %q", p, resp.StatusCode, body)
			}
		}
	}
}

func TestReadStillRejectsOtherDotfiles(t *testing.T) {
	srvURL, root := newServer(t)
	for _, name := range []string{".mods-manifest.json", ".env"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", ".curseforge-manifest.json"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write nested: %v", err)
	}
	for _, p := range []string{".mods-manifest.json", ".env", "sub/.curseforge-manifest.json", "/.curseforge-manifest.json/../.env"} {
		resp := get(t, srvURL, "/files/read", url.Values{"path": {p}})
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("read %q = %d, want 400", p, resp.StatusCode)
		}
	}
}

func TestReadManifestDotfileSymlinkNotFollowed(t *testing.T) {
	srvURL, root := newServer(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".curseforge-manifest.json")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	resp := get(t, srvURL, "/files/read", url.Values{"path": {".curseforge-manifest.json"}})
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK || string(body) == "secret" {
		t.Fatalf("symlinked manifest served: %d %q", resp.StatusCode, body)
	}
}

func TestManifestDotfilesNotWritableOrDeletable(t *testing.T) {
	srvURL, root := newServer(t)
	name := ".curseforge-manifest.json"
	if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path="+name, nil)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete = %d, want 400", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("manifest removed: %v", err)
	}
}
