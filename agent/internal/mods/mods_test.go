package mods

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

// allowLoopback lets the SSRF dial guard reach httptest servers (which
// bind to 127.0.0.1) for the duration of a test.
func allowLoopback(t *testing.T) {
	t.Helper()
	prev := ssrfPolicy
	ssrfPolicy = func(net.IP) bool { return true }
	t.Cleanup(func() { ssrfPolicy = prev })
}

func newSrv(t *testing.T, dataRoot string, spec *caps.Mods) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	Mount(r, dataRoot, spec)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func modsSpec(dir string, install *caps.ModInstall) *caps.Mods {
	return &caps.Mods{Path: dir, Extensions: []string{".jar"}, Install: install}
}

func TestList(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.jar", "b.jar", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, "mods", f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := newSrv(t, root, modsSpec("mods", nil))
	status, body := do(t, srv, http.MethodGet, "/mods", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	var got []Mod
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	// Only .jar files (extension filter) — notes.txt excluded.
	if len(got) != 2 {
		t.Fatalf("got %d mods: %+v", len(got), got)
	}
}

func TestList_MissingDirIsEmpty(t *testing.T) {
	srv := newSrv(t, t.TempDir(), modsSpec("mods", nil))
	status, body := do(t, srv, http.MethodGet, "/mods", nil)
	if status != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestList_Unconfigured(t *testing.T) {
	srv := newSrv(t, t.TempDir(), nil)
	status, body := do(t, srv, http.MethodGet, "/mods", nil)
	if status != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestList_SkipsSubdirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mods", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mods", "a.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newSrv(t, root, modsSpec("mods", nil))
	_, body := do(t, srv, http.MethodGet, "/mods", nil)
	var got []Mod
	_ = json.Unmarshal(body, &got)
	if len(got) != 1 || got[0].Name != "a.jar" {
		t.Fatalf("got %+v, want only a.jar", got)
	}
}

func TestList_NoExtensionFilterListsAll(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.jar", "readme.txt", "config.yml"} {
		if err := os.WriteFile(filepath.Join(root, "plugins", f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// No Extensions → every file counts as a mod.
	srv := newSrv(t, root, &caps.Mods{Path: "plugins"})
	_, body := do(t, srv, http.MethodGet, "/mods", nil)
	var got []Mod
	_ = json.Unmarshal(body, &got)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3: %+v", len(got), got)
	}
}

func TestRemove_NonEmptyDirErrors(t *testing.T) {
	root := t.TempDir()
	// A non-empty subdirectory named like a mod can't be os.Remove'd,
	// exercising the generic error branch.
	if err := os.MkdirAll(filepath.Join(root, "mods", "pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mods", "pack", "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newSrv(t, root, modsSpec("mods", nil))
	status, _ := do(t, srv, http.MethodDelete, "/mods?name=pack", nil)
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", status)
	}
}

func TestNewHandler_PathEscapeDisables(t *testing.T) {
	// Defense in depth: a traversal path disables mods entirely.
	h := newHandler(t.TempDir(), &caps.Mods{Path: "../../escape"})
	if h.dir != "" {
		t.Fatalf("dir = %q, want empty (disabled)", h.dir)
	}
}

func TestInstall_HappyPath(t *testing.T) {
	allowLoopback(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("JARDATA"))
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	root := t.TempDir()
	srv := newSrv(t, root, modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{host}}))

	status, body := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/cool.jar"})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	data, err := os.ReadFile(filepath.Join(root, "mods", "cool.jar"))
	if err != nil || string(data) != "JARDATA" {
		t.Fatalf("file = %q err=%v", data, err)
	}
}

func TestInstall_ExtractArchive(t *testing.T) {
	allowLoopback(t)

	// Build a Thunderstore-style zip: a plugin .dll plus package metadata.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"plugins/Cool.dll": "DLLBYTES",
		"manifest.json":    `{"name":"Cool"}`,
		"README.md":        "hi",
	} {
		f, _ := zw.Create(name)
		_, _ = f.Write([]byte(content))
	}
	_ = zw.Close()
	zipBytes := buf.Bytes()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	root := t.TempDir()
	spec := &caps.Mods{
		Path:       "plugins",
		Extensions: []string{".zip"},
		Extract:    true,
		Install:    &caps.ModInstall{AllowedHosts: []string{host}},
	}
	srv := newSrv(t, root, spec)

	// Install → unpacks into plugins/<name>/.
	status, body := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/x.zip", "name": "Owner-Cool-1.0.0.zip"})
	if status != http.StatusOK {
		t.Fatalf("install status=%d body=%s", status, body)
	}
	dll := filepath.Join(root, "plugins", "Owner-Cool-1.0.0", "plugins", "Cool.dll")
	if data, err := os.ReadFile(dll); err != nil || string(data) != "DLLBYTES" {
		t.Fatalf("extracted dll = %q err=%v", data, err)
	}

	// List → the per-mod folder shows as one mod.
	status, body = do(t, srv, http.MethodGet, "/mods", nil)
	if status != http.StatusOK {
		t.Fatalf("list status=%d", status)
	}
	var mods []Mod
	if err := json.Unmarshal(body, &mods); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(mods) != 1 || mods[0].Name != "Owner-Cool-1.0.0" {
		t.Fatalf("list = %+v, want one folder Owner-Cool-1.0.0", mods)
	}

	// Remove → the whole folder is gone.
	status, _ = do(t, srv, http.MethodDelete, "/mods?name=Owner-Cool-1.0.0", nil)
	if status != http.StatusNoContent {
		t.Fatalf("remove status=%d", status)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "Owner-Cool-1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("folder should be gone, stat err=%v", err)
	}
}

func TestInstall_ExplicitName(t *testing.T) {
	allowLoopback(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("DATA"))
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	root := t.TempDir()
	srv := newSrv(t, root, modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{host}}))
	// URL path has no usable basename; an explicit name is used instead.
	status, body := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/download?id=42", "name": "renamed.jar"})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if _, err := os.Stat(filepath.Join(root, "mods", "renamed.jar")); err != nil {
		t.Fatalf("renamed.jar missing: %v", err)
	}
}

func TestInstall_ContentLengthOverCap(t *testing.T) {
	allowLoopback(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Advertise a huge size up front so the cap rejects before reading.
		w.Header().Set("Content-Length", "999999999")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	srv := newSrv(t, t.TempDir(), modsSpec("mods",
		&caps.ModInstall{AllowedHosts: []string{host}, MaxSizeMB: 1}))
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/big.jar"})
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", status)
	}
}

func TestInstall_UpstreamError(t *testing.T) {
	allowLoopback(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	srv := newSrv(t, t.TempDir(), modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{host}}))
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/missing.jar"})
	if status != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", status)
	}
}

func TestInstall_RedirectToDisallowedHostBlocked(t *testing.T) {
	allowLoopback(t)
	// Redirect to a host not on the allowlist must be refused by
	// CheckRedirect even though the initial host is allowed.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "http://evil.example.com/x.jar", http.StatusFound)
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	srv := newSrv(t, t.TempDir(), modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{host}}))
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/start.jar"})
	if status != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", status)
	}
}

func TestRemove_Unconfigured(t *testing.T) {
	srv := newSrv(t, t.TempDir(), nil)
	status, _ := do(t, srv, http.MethodDelete, "/mods?name=x.jar", nil)
	if status != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", status)
	}
}

func TestInstall_HostNotAllowed(t *testing.T) {
	allowLoopback(t)
	srv := newSrv(t, t.TempDir(), modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{"cdn.example.com"}}))
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": "https://evil.example.net/x.jar"})
	if status != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", status)
	}
}

func TestInstall_BlocksLoopbackBySSRFGuard(t *testing.T) {
	// No allowLoopback: the default guard must refuse a 127.0.0.1 target
	// even though the host is on the allowlist.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	srv := newSrv(t, t.TempDir(), modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{host}}))
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/x.jar"})
	if status != http.StatusForbidden {
		t.Fatalf("status=%d, want 403 (SSRF guard)", status)
	}
}

func TestInstall_SizeCap(t *testing.T) {
	allowLoopback(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("A"), 5<<20)) // 5 MiB
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	srv := newSrv(t, t.TempDir(), modsSpec("mods",
		&caps.ModInstall{AllowedHosts: []string{host}, MaxSizeMB: 1}))
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/big.jar"})
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", status)
	}
}

func TestInstall_WrongExtension(t *testing.T) {
	allowLoopback(t)
	srv := newSrv(t, t.TempDir(), modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{"cdn.example.com"}}))
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": "https://cdn.example.com/notamod.zip"})
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", status)
	}
}

func TestInstall_BadURL(t *testing.T) {
	srv := newSrv(t, t.TempDir(), modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{"x"}}))
	for _, u := range []string{"", "ftp://x/y.jar", "/relative.jar", "not a url"} {
		status, _ := do(t, srv, http.MethodPost, "/mods/install", map[string]string{"url": u})
		if status != http.StatusBadRequest {
			t.Errorf("url %q: status=%d, want 400", u, status)
		}
	}
}

func TestInstall_Disabled(t *testing.T) {
	// Mods configured but no install policy → installs are 501.
	srv := newSrv(t, t.TempDir(), modsSpec("mods", nil))
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": "https://cdn.example.com/x.jar"})
	if status != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", status)
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "mods", "gone.jar")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newSrv(t, root, modsSpec("mods", nil))

	if status, _ := do(t, srv, http.MethodDelete, "/mods?name=gone.jar", nil); status != http.StatusNoContent {
		t.Fatalf("delete status=%d", status)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}
	if status, _ := do(t, srv, http.MethodDelete, "/mods?name=missing.jar", nil); status != http.StatusNotFound {
		t.Fatalf("missing delete status=%d, want 404", status)
	}
}

func TestRemove_RejectsTraversal(t *testing.T) {
	srv := newSrv(t, t.TempDir(), modsSpec("mods", nil))
	for _, n := range []string{"../escape", "a/b", "..", ".hidden", ""} {
		status, _ := do(t, srv, http.MethodDelete, "/mods?name="+n, nil)
		if status != http.StatusBadRequest {
			t.Errorf("name %q: status=%d, want 400", n, status)
		}
	}
}

func TestSafeName(t *testing.T) {
	bad := []string{"", "..", ".", "a/b", "a\\b", "../x", ".dotfile", "x\ny", strings.Repeat("a", 201)}
	for _, n := range bad {
		if _, err := safeName(n); err == nil {
			t.Errorf("safeName(%q) should fail", n)
		}
	}
	good := []string{"cool.jar", "Mod_1.2.3.jar", "a-b_c.zip"}
	for _, n := range good {
		if got, err := safeName(n); err != nil || got != n {
			t.Errorf("safeName(%q) = %q, %v", n, got, err)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	allow := []string{"cdn.modrinth.com", ".curseforge.com"}
	yes := []string{"cdn.modrinth.com", "CDN.Modrinth.com", "edge.curseforge.com", "curseforge.com"}
	no := []string{"modrinth.com", "evil.com", "curseforge.com.evil.com", "fakecdn.modrinth.com.evil"}
	for _, h := range yes {
		if !hostAllowed(h, allow) {
			t.Errorf("hostAllowed(%q) = false, want true", h)
		}
	}
	for _, h := range no {
		if hostAllowed(h, allow) {
			t.Errorf("hostAllowed(%q) = true, want false", h)
		}
	}
}

func TestArchiveFolderName(t *testing.T) {
	cases := map[string]string{
		"Owner-Mod-1.0.0.zip":    "Owner-Mod-1.0.0",
		"Owner-Mod-1.0.0.tar.gz": "Owner-Mod-1.0.0",
		"Owner-Mod-1.0.0.tgz":    "Owner-Mod-1.0.0",
		"Owner-Mod-1.0.0.TGZ":    "Owner-Mod-1.0.0", // case-insensitive
		"plainname":              "plainname",       // no known archive ext
	}
	for in, want := range cases {
		if got := archiveFolderName(in); got != want {
			t.Errorf("archiveFolderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func makeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnzipInto_RejectsZipSlip(t *testing.T) {
	zipPath := makeZip(t, map[string]string{"good.txt": "ok", "../evil.txt": "pwned"})
	dst := filepath.Join(t.TempDir(), "out")
	// After refactoring to use ConfinePath, unzipInto now returns an error
	// immediately on any traversal entry (rather than silently skipping).
	// This makes the guard explicit to CodeQL's taint analysis.
	err := unzipInto(zipPath, dst, 1<<20)
	if err == nil {
		t.Fatalf("unzipInto should error on traversal entry, not skip it")
	}
	// No files should be extracted if a traversal is detected early.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("evil.txt should not have escaped: %v", err)
	}
}

func TestUnzipInto_DirectoriesAreWorldTraversable(t *testing.T) {
	zipPath := makeZip(t, map[string]string{"world/region/r.0.0.mca": "x"})
	dst := filepath.Join(t.TempDir(), "out")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unzipInto(zipPath, dst, 1<<20); err != nil {
		t.Fatalf("unzipInto: %v", err)
	}
	for _, p := range []string{"world", filepath.Join("world", "region")} {
		fi, err := os.Stat(filepath.Join(dst, p))
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := fi.Mode().Perm(); perm&0o005 != 0o005 {
			t.Errorf("dir %s mode=%03o, want other r-x (0o755)", p, perm)
		}
	}
}

func TestUnzipInto_SizeCap(t *testing.T) {
	zipPath := makeZip(t, map[string]string{"big.bin": strings.Repeat("A", 4096)})
	dst := filepath.Join(t.TempDir(), "out")
	if err := unzipInto(zipPath, dst, 1024); !errors.Is(err, errTooLarge) {
		t.Fatalf("unzipInto over cap = %v, want errTooLarge", err)
	}
}

func TestUnzipInto_BadArchive(t *testing.T) {
	dir := t.TempDir()
	notZip := filepath.Join(dir, "x.zip")
	if err := os.WriteFile(notZip, []byte("definitely not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unzipInto(notZip, filepath.Join(dir, "out"), 1<<20); !errors.Is(err, errBadArchive) {
		t.Fatalf("unzipInto bad archive = %v, want errBadArchive", err)
	}
}

func TestUnzipInto_RejectsSymlinks(t *testing.T) {
	// Create a temp directory for the zip
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "symlink.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	// Create a zip with a symlink entry
	zw := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "escape-link", Method: zip.Store}
	h.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("../../../../etc/passwd")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// unzipInto should reject the symlink entry
	dst := filepath.Join(dir, "out")
	if err := unzipInto(zipPath, dst, 1<<20); !errors.Is(err, errSymlinkEntry) {
		t.Fatalf("unzipInto symlink = %v, want errSymlinkEntry", err)
	}

	// The "escape-link" entry should not exist after rejection
	escapeLinkPath := filepath.Join(dst, "escape-link")
	if _, err := os.Lstat(escapeLinkPath); !os.IsNotExist(err) {
		t.Fatalf("escape-link should not exist after rejection: %v", err)
	}
}

// TestUnzipInto_RejectsSymlinksWithoutUnixCreator covers the adversarial
// shape: an entry carrying the Unix symlink type bits in ExternalAttrs but
// leaving CreatorVersion zero, so archive/zip's FileHeader.Mode falls back to
// the MS-DOS attribute bits and never reports os.ModeSymlink. A guard that
// trusted Mode() alone would let this through.
func TestUnzipInto_RejectsSymlinksWithoutUnixCreator(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "symlink-nocreator.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	zw := zip.NewWriter(f)
	// Note: ExternalAttrs is set directly and CreatorVersion is left zero,
	// unlike SetMode which would stamp the Unix creator byte.
	h := &zip.FileHeader{Name: "escape-link", Method: zip.Store, ExternalAttrs: 0o120777 << 16}
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("../../../../etc/passwd")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Guard the premise: if a future Go release taught Mode() to decode these
	// bits without the creator byte, this test would silently stop covering
	// the fallback branch it exists for.
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := zr.File[0].Mode(); got&os.ModeSymlink != 0 {
		t.Fatalf("premise broken: Mode() = %v already reports ModeSymlink without a Unix creator", got)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "out")
	if err := unzipInto(zipPath, dst, 1<<20); !errors.Is(err, errSymlinkEntry) {
		t.Fatalf("unzipInto symlink without unix creator = %v, want errSymlinkEntry", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "escape-link")); !os.IsNotExist(err) {
		t.Fatalf("escape-link should not exist after rejection: %v", err)
	}
}

func TestInstall_ExtractBadArchive(t *testing.T) {
	allowLoopback(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("definitely not a zip"))
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	root := t.TempDir()
	spec := &caps.Mods{
		Path:       "plugins",
		Extensions: []string{".zip"},
		Extract:    true,
		Install:    &caps.ModInstall{AllowedHosts: []string{host}},
	}
	srv := newSrv(t, root, spec)
	status, _ := do(t, srv, http.MethodPost, "/mods/install",
		map[string]string{"url": upstream.URL + "/x.zip", "name": "Owner-Bad-1.0.0.zip"})
	if status != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502 (bad archive)", status)
	}
}

// The SSRF IP-classification table (isPublic / IsAllowed) now lives in the
// shared netguard module's TestIsPublic; the agent tests cover only its use
// of the guard (default-refuses loopback, redirect host re-check).

// --- Tests for ConfinePath-based refactors (T006) ---

// TestRemoveEntry_ConfinePath verifies that removeEntry uses ConfinePath
// to validate the target and rejects path traversal attempts.
func TestRemoveEntry_ConfinePath(t *testing.T) {
	root := t.TempDir()
	modsDir := filepath.Join(root, "mods")
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a file to remove.
	target := filepath.Join(modsDir, "test.jar")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newHandler(root, modsSpec("mods", nil))

	// removeEntry should accept a valid name and delete the file.
	if err := h.removeEntry("test.jar"); err != nil {
		t.Errorf("removeEntry(valid) = %v, want nil", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be deleted but still exists")
	}

	// removeEntry should reject path traversal attempts.
	for _, bad := range []string{"../escape", "a/b", "..", ".hidden"} {
		if err := h.removeEntry(bad); err == nil {
			t.Errorf("removeEntry(%q) should fail on traversal", bad)
		}
	}
}

// TestDownload_ConfinePath verifies that download uses ConfinePath to validate
// the destination and rejects traversal attempts.
func TestDownload_ConfinePath(t *testing.T) {
	allowLoopback(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("TESTDATA"))
	}))
	defer upstream.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))

	root := t.TempDir()
	h := newHandler(root, modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{host}}))

	// download should accept a valid name and save the file.
	n, err := h.download(t.Context(), upstream.URL+"/test.jar", "valid.jar")
	if err != nil || n != 8 {
		t.Errorf("download(valid) = %v, %d, want nil, 8", err, n)
	}
	if _, err := os.Stat(filepath.Join(root, "mods", "valid.jar")); err != nil {
		t.Errorf("file should exist: %v", err)
	}

	// download should reject traversal attempts by ConfinePath.
	for _, bad := range []string{"../escape", "a/b"} {
		_, err := h.download(t.Context(), upstream.URL+"/x.jar", bad)
		if err == nil {
			t.Errorf("download(%q) should fail on traversal", bad)
		}
	}
}

// TestSwapInArchive_ConfinePath verifies that swapInArchive uses ConfinePath
// to validate the destination and rejects traversal attempts.
func TestSwapInArchive_ConfinePath(t *testing.T) {
	root := t.TempDir()
	modsDir := filepath.Join(root, "mods")
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	h := newHandler(root, &caps.Mods{Path: "mods", Extract: true})

	// Create a valid zip to extract.
	validZip := makeZip(t, map[string]string{
		"plugin.dll": "DLLBYTES",
		"config.yml": "key: value",
	})

	// swapInArchive should accept a valid folder name.
	if err := h.swapInArchive(validZip, "MyPlugin", 1<<20); err != nil {
		t.Errorf("swapInArchive(valid) = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(modsDir, "MyPlugin", "plugin.dll")); err != nil {
		t.Errorf("extracted file should exist: %v", err)
	}

	// swapInArchive should reject traversal attempts by ConfinePath.
	for _, bad := range []string{"../escape", "a/b", "..", ".hidden"} {
		badZip := makeZip(t, map[string]string{"file.txt": "data"})
		if err := h.swapInArchive(badZip, bad, 1<<20); err == nil {
			t.Errorf("swapInArchive(%q) should fail on traversal", bad)
		}
	}
}

// TestRemoveWithConfinePath verifies that the remove handler uses ConfinePath
// to validate the name parameter and rejects traversal attempts.
func TestRemoveWithConfinePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "mods", "gone.jar")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newSrv(t, root, modsSpec("mods", nil))

	// remove should work for a valid name.
	if status, _ := do(t, srv, http.MethodDelete, "/mods?name=gone.jar", nil); status != http.StatusNoContent {
		t.Errorf("delete status=%d, want 204", status)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}

	// remove should reject traversal attempts via ConfinePath validation.
	for _, bad := range []string{"../escape", "a/b", ".."} {
		status, _ := do(t, srv, http.MethodDelete, "/mods?name="+bad, nil)
		if status != http.StatusBadRequest {
			t.Errorf("delete name=%q status=%d, want 400", bad, status)
		}
	}
}

// TestUnzipInto_RejectsTraversalWithConfinePath verifies that unzipInto now
// rejects archive entries containing traversal attempts (rather than silently
// skipping them) by returning an error immediately.
func TestUnzipInto_RejectsTraversalWithConfinePath(t *testing.T) {
	// Create an archive with a traversal entry.
	zipPath := makeZip(t, map[string]string{
		"../evil.txt": "should not extract",
	})
	dst := filepath.Join(t.TempDir(), "out")

	// unzipInto should now reject the traversal entry and return an error.
	err := unzipInto(zipPath, dst, 1<<20)
	if err == nil {
		t.Fatal("unzipInto should error on traversal entry, not skip it")
	}

	// Verify the evil file escaped nowhere.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("evil.txt should not have escaped")
	}
}

// TestUnzipInto_AcceptsValidEntriesWithConfinePath verifies that unzipInto
// still extracts valid entries correctly after the ConfinePath refactor.
func TestUnzipInto_AcceptsValidEntriesWithConfinePath(t *testing.T) {
	// Create an archive with only valid entries.
	zipPath := makeZip(t, map[string]string{
		"plugins/Cool.dll": "DLLBYTES",
		"config/data.json": `{"version":1}`,
		"readme.txt":       "Instructions",
	})
	dst := filepath.Join(t.TempDir(), "out")

	// unzipInto should succeed.
	if err := unzipInto(zipPath, dst, 1<<20); err != nil {
		t.Fatalf("unzipInto valid archive: %v", err)
	}

	// All files should be extracted.
	for _, path := range []string{
		filepath.Join(dst, "plugins", "Cool.dll"),
		filepath.Join(dst, "config", "data.json"),
		filepath.Join(dst, "readme.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file %q should exist: %v", filepath.Base(path), err)
		}
	}
}

// TestUnzipInto_AcceptsDotfilesAndNestedEntries verifies that unzipInto
// accepts archives with dotfiles and nested directories (using ConfineRelPath).
// This is the key regression test for DEFECT 2 — archives legitimately contain
// nested paths and dotfiles, which ConfinePath would have rejected.
func TestUnzipInto_AcceptsDotfilesAndNestedEntries(t *testing.T) {
	// Create an archive with dotfiles and nested paths (typical for real archives).
	zipPath := makeZip(t, map[string]string{
		".gitkeep":           "",              // dotfile at root
		"config/.gitignore":  "*.tmp\n",       // dotfile in subdirectory
		"plugins/Cool.dll":   "DLLBYTES",      // nested file
		"src/main/.settings": "debug=false\n", // nested dotfile
	})
	dst := filepath.Join(t.TempDir(), "out")

	// unzipInto should succeed with ConfineRelPath.
	if err := unzipInto(zipPath, dst, 1<<20); err != nil {
		t.Fatalf("unzipInto archive with dotfiles: %v", err)
	}

	// All files and dotfiles should be extracted.
	for _, path := range []string{
		filepath.Join(dst, ".gitkeep"),
		filepath.Join(dst, "config", ".gitignore"),
		filepath.Join(dst, "plugins", "Cool.dll"),
		filepath.Join(dst, "src", "main", ".settings"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file %q should exist: %v", path, err)
		}
	}
}

func TestUpload_BadArchive(t *testing.T) {
	root := t.TempDir()
	spec := &caps.Mods{
		Path:       "plugins",
		Extensions: []string{".zip"},
		Extract:    true,
	}
	srv := newSrv(t, root, spec)

	// Create a bad zip file and upload it
	badZipPath := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(badZipPath, []byte("definitely not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Open the file and send it as multipart upload
	zipFile, err := os.Open(badZipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zipFile.Close()

	// Build the multipart form body.
	client := &http.Client{}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "bad.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(part, zipFile)
	writer.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mods/upload", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for bad archive", resp.StatusCode)
	}
}

func TestUpload_DefaultSizeCap(t *testing.T) {
	root := t.TempDir()
	spec := &caps.Mods{
		Path:       "plugins",
		Extensions: []string{".zip"},
	}
	srv := newSrv(t, root, spec)

	// Create a large file (exceeds default cap)
	largeData := bytes.Repeat([]byte("A"), 300<<20) // 300 MiB, exceeds 256 MiB default
	client := &http.Client{}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "large.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(part, bytes.NewReader(largeData))
	writer.Close()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mods/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413 for oversized upload", resp.StatusCode)
	}
}

func TestUpload_WrongExtension(t *testing.T) {
	root := t.TempDir()
	spec := &caps.Mods{
		Path:       "plugins",
		Extensions: []string{".dll"},
	}
	srv := newSrv(t, root, spec)

	client := &http.Client{}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "plugin.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("zipdata"))
	writer.Close()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mods/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for wrong extension", resp.StatusCode)
	}
}

func TestUpload_InvalidForm(t *testing.T) {
	root := t.TempDir()
	spec := &caps.Mods{Path: "plugins"}
	srv := newSrv(t, root, spec)

	// Send a POST without proper multipart form
	client := &http.Client{}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mods/upload",
		bytes.NewReader([]byte("not a form")))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for invalid form", resp.StatusCode)
	}
}

func TestRemove_MissingMod(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := newSrv(t, root, modsSpec("mods", nil))

	// Try to remove a mod that doesn't exist
	status, body := do(t, srv, http.MethodDelete, "/mods?name=nonexistent.jar", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", status, body)
	}
}

func TestList_Empty(t *testing.T) {
	root := t.TempDir()
	// Create the mods directory but leave it empty
	if err := os.MkdirAll(filepath.Join(root, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := newSrv(t, root, modsSpec("mods", nil))

	status, body := do(t, srv, http.MethodGet, "/mods", nil)
	if status != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestRedactURLErr_StripsQuery(t *testing.T) {
	orig := &url.Error{
		Op:  "Get",
		URL: "https://mods.example.com/download?username=alice&token=secret",
		Err: errors.New("connection refused"),
	}
	got := redactURLErr(orig)

	var ue *url.Error
	if !errors.As(got, &ue) {
		t.Fatalf("redactURLErr should preserve the *url.Error in the chain, got %v", got)
	}
	if ue.URL != "https://mods.example.com/download" {
		t.Errorf("URL = %q, want query stripped", ue.URL)
	}
	// The same pointer identity is mutated in place.
	if !errors.Is(got, error(orig)) {
		t.Errorf("redactURLErr should return the same error value it mutated")
	}
}

// TestRedactURLErr_NonURLError covers the pass-through branch: an error
// that isn't a *url.Error (e.g. a plain wrapped error) is returned
// unchanged.
func TestRedactURLErr_NonURLError(t *testing.T) {
	plain := errors.New("boom")
	got := redactURLErr(plain)
	if !errors.Is(got, plain) {
		t.Errorf("redactURLErr should pass through a non-*url.Error unchanged, got %v", got)
	}
}

// TestRedactURLErr_UnparsableEmbeddedURL covers the perr != nil branch:
// when the *url.Error's own URL string fails to re-parse, redactURLErr
// leaves it untouched rather than panicking or corrupting it.
func TestRedactURLErr_UnparsableEmbeddedURL(t *testing.T) {
	// A URL containing a raw control byte fails url.Parse.
	badURL := "https://example.com/\x7f?token=secret"
	orig := &url.Error{Op: "Get", URL: badURL, Err: errors.New("bad")}
	got := redactURLErr(orig)

	var ue *url.Error
	if !errors.As(got, &ue) {
		t.Fatalf("expected *url.Error in chain, got %v", got)
	}
	if ue.URL != badURL {
		t.Errorf("URL = %q, want unchanged %q when re-parse fails", ue.URL, badURL)
	}
}

// TestDirSize sums regular file sizes recursively, skipping directories,
// and returns 0 for an empty or nonexistent directory (WalkDir's error is
// swallowed).
func TestDirSize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("1234567890"), 0o600); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	got := dirSize(root)
	if got != 15 {
		t.Errorf("dirSize = %d, want 15 (5 + 10, directories excluded)", got)
	}
}

func TestDirSize_Empty(t *testing.T) {
	if got := dirSize(t.TempDir()); got != 0 {
		t.Errorf("dirSize of empty dir = %d, want 0", got)
	}
}

func TestDirSize_Nonexistent(t *testing.T) {
	if got := dirSize(filepath.Join(t.TempDir(), "does-not-exist")); got != 0 {
		t.Errorf("dirSize of nonexistent dir = %d, want 0", got)
	}
}

// TestExtAllowed covers handler.extAllowed's two branches: no configured
// extensions permits everything, and a configured allowlist matches
// case-insensitively on suffix while rejecting anything else.
func TestExtAllowed(t *testing.T) {
	h := &handler{}
	if !h.extAllowed("anything.exe") {
		t.Errorf("no configured extensions should allow everything")
	}

	h = &handler{exts: []string{".jar", ".zip"}}
	cases := []struct {
		name string
		want bool
	}{
		{"plugin.jar", true},
		{"PLUGIN.JAR", true}, // case-insensitive
		{"archive.zip", true},
		{"readme.txt", false},
		{"noext", false},
	}
	for _, c := range cases {
		if got := h.extAllowed(c.name); got != c.want {
			t.Errorf("extAllowed(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// mockHTTPClient returns an *http.Client that responds to all requests with
// the given content.
func mockHTTPClient(content string) *http.Client {
	return &http.Client{
		Transport: &mockTransport{content: content},
	}
}

type mockTransport struct {
	content string
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(m.content)),
		Request:    req,
	}, nil
}

// TestDownload_FilePermissions verifies that downloaded mod files are 0o644
// so the game container (different uid, shared fsGroup) can read them.
func TestDownload_FilePermissions(t *testing.T) {
	root := t.TempDir()
	h := newHandler(root, &caps.Mods{
		Path: "mods",
		Install: &caps.ModInstall{
			AllowedHosts: []string{"example.com"},
			MaxSizeMB:    256,
		},
	})
	h.client = mockHTTPClient("test-mod-content")

	_, err := h.download(t.Context(), "http://example.com/test.jar", "test.jar")
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	fi, err := os.Stat(filepath.Join(root, "mods", "test.jar"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o644 {
		t.Errorf("file mode=%03o, want 0o644", perm)
	}
}

// TestUpload_FilePermissions verifies that uploaded mod files are 0o644
// so the game container (different uid, shared fsGroup) can read them.
func TestUpload_FilePermissions(t *testing.T) {
	root := t.TempDir()
	spec := &caps.Mods{Path: "mods", Extensions: []string{".jar"}}
	srv := newSrv(t, root, spec)

	client := &http.Client{}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "test.jar")
	if err != nil {
		t.Fatalf("createformfile: %v", err)
	}
	if _, err := part.Write([]byte("test-content")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	mw.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mods/upload", &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	fi, err := os.Stat(filepath.Join(root, "mods", "test.jar"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o644 {
		t.Errorf("file mode=%03o, want 0o644", perm)
	}
}
