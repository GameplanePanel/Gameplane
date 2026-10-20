package mods

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

// postUpload sends a multipart upload with the given filename + payload.
func postUpload(t *testing.T, srv *httptest.Server, field, filename string, payload []byte) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mods/upload", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestUpload_HappyPath(t *testing.T) {
	root := t.TempDir()
	// No install (allowlist) block on purpose: uploads must work without one.
	srv := newSrv(t, root, modsSpec("mods", nil))

	status, body := postUpload(t, srv, "file", "custom.jar", []byte("JARDATA"))
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var installed Mod
	if err := json.Unmarshal(body, &installed); err != nil {
		t.Fatal(err)
	}
	if installed.Name != "custom.jar" || installed.Size != int64(len("JARDATA")) {
		t.Fatalf("installed = %+v", installed)
	}
	if installed.Meta == nil || installed.Meta.Provider != "upload" || installed.Meta.InstalledAt == "" {
		t.Fatalf("meta = %+v, want provider upload with installedAt", installed.Meta)
	}
	data, err := os.ReadFile(filepath.Join(root, "mods", "custom.jar"))
	if err != nil || string(data) != "JARDATA" {
		t.Fatalf("file = %q err=%v", data, err)
	}
	// Manifest records the upload.
	entries := readManifest(t, filepath.Join(root, "mods"))
	if m, ok := entries["custom.jar"]; !ok || m.Provider != "upload" {
		t.Fatalf("manifest = %+v", entries)
	}
}

func TestUploadLargeFileWithoutSystemTemp(t *testing.T) {
	root := t.TempDir()
	// Multipart FormFile spills files above 32 MiB to the system temp directory.
	// The sidecar has a read-only root; only the mounted mods directory is writable.
	t.Setenv("TMPDIR", filepath.Join(root, "unavailable"))
	srv := newSrv(t, root, modsSpec("mods", nil))
	payload := bytes.Repeat([]byte("M"), 33<<20)
	status, body := postUpload(t, srv, "file", "large.jar", payload)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	data, err := os.ReadFile(filepath.Join(root, "mods", "large.jar"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("uploaded content differs: err=%v size=%d", err, len(data))
	}
}

func TestUploadRejectsOversizedTrailingFields(t *testing.T) {
	root := t.TempDir()
	h := newHandler(root, modsSpec("mods", &caps.ModInstall{AllowedHosts: []string{"cdn.example.com"}, MaxSizeMB: 1}))
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	file, err := mw.CreateFormFile("file", "small.jar")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("valid mod"))
	field, err := mw.CreateFormField("extra")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = field.Write(bytes.Repeat([]byte("x"), 3<<20))
	_ = mw.Close()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mods/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.upload(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(root, "mods"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected upload left files: entries=%v err=%v", entries, err)
	}
}

func TestUploadRejectsTooManyMultipartParts(t *testing.T) {
	root := t.TempDir()
	h := newHandler(root, modsSpec("mods", nil))
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	file, err := mw.CreateFormFile("file", "small.jar")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("mod"))
	for range 1000 {
		if err := mw.WriteField("extra", ""); err != nil {
			t.Fatal(err)
		}
	}
	_ = mw.Close()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mods/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.upload(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(root, "mods"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected upload left files: entries=%v err=%v", entries, err)
	}
}

func TestUpload_ExtractArchive(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("plugins/Cool.dll")
	_, _ = f.Write([]byte("DLL"))
	_ = zw.Close()

	root := t.TempDir()
	spec := &caps.Mods{Path: "plugins", Extensions: []string{".zip"}, Extract: true}
	srv := newSrv(t, root, spec)

	status, body := postUpload(t, srv, "file", "Owner-Cool-1.0.0.zip", buf.Bytes())
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	dll := filepath.Join(root, "plugins", "Owner-Cool-1.0.0", "plugins", "Cool.dll")
	info, err := os.Stat(filepath.Join(root, "plugins", "Owner-Cool-1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != moduleDirMode {
		t.Fatalf("archive root mode=%o, want %o", info.Mode().Perm(), moduleDirMode)
	}
	if data, err := os.ReadFile(dll); err != nil || string(data) != "DLL" {
		t.Fatalf("extracted = %q err=%v", data, err)
	}
	// The upload temp file is gone; only the folder remains.
	entries, _ := os.ReadDir(filepath.Join(root, "plugins"))
	for _, e := range entries {
		if e.Name() != "Owner-Cool-1.0.0" && e.Name() != manifestName {
			t.Fatalf("unexpected leftover %q", e.Name())
		}
	}
}

func TestUpload_ExtractBadArchive(t *testing.T) {
	root := t.TempDir()
	spec := &caps.Mods{Path: "plugins", Extensions: []string{".zip"}, Extract: true}
	srv := newSrv(t, root, spec)
	status, _ := postUpload(t, srv, "file", "bad.zip", []byte("not a zip"))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", status)
	}
}

func TestUpload_RejectsWrongExtension(t *testing.T) {
	srv := newSrv(t, t.TempDir(), modsSpec("mods", nil)) // .jar only
	status, _ := postUpload(t, srv, "file", "notes.txt", []byte("x"))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", status)
	}
}

func TestUpload_RejectsBadName(t *testing.T) {
	srv := newSrv(t, t.TempDir(), &caps.Mods{Path: "mods"})
	for _, bad := range []string{".hidden", manifestName} {
		status, _ := postUpload(t, srv, "file", bad, []byte("x"))
		if status != http.StatusBadRequest {
			t.Errorf("name %q: status=%d, want 400", bad, status)
		}
	}
}

func TestUpload_SizeCap(t *testing.T) {
	root := t.TempDir()
	srv := newSrv(t, root, modsSpec("mods", &caps.ModInstall{
		AllowedHosts: []string{"cdn.example.com"}, MaxSizeMB: 1,
	}))
	status, _ := postUpload(t, srv, "file", "big.jar", bytes.Repeat([]byte("A"), 2<<20))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", status)
	}
	// Nothing lands in the mods dir.
	if _, err := os.Stat(filepath.Join(root, "mods", "big.jar")); !os.IsNotExist(err) {
		t.Fatalf("oversize upload must not persist, stat err=%v", err)
	}
}

func TestUpload_MissingFileField(t *testing.T) {
	srv := newSrv(t, t.TempDir(), &caps.Mods{Path: "mods"})
	status, _ := postUpload(t, srv, "wrongfield", "x.jar", []byte("x"))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", status)
	}
}

func TestUpload_Unconfigured(t *testing.T) {
	srv := newSrv(t, t.TempDir(), nil)
	status, _ := postUpload(t, srv, "file", "x.jar", []byte("x"))
	if status != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", status)
	}
}

// TestUpload_NonExtractRejectsSymlinkEscape verifies that the non-extract
// upload handler rejects a destination that is a symlink pointing outside
// h.dir, thus covering the ConfinePath symlink-resolution error branch.
// This is the only case that ConfinePath adds beyond safeName: safeName
// already rejects path components with "/" and "..", so symlink resolution
// is the key differentiator that the old filepath.Clean code lacked.
func TestUpload_NonExtractRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	escaped := t.TempDir() // A directory outside root, the attack target

	// Create the mods directory and a symlink inside it pointing outside.
	modsDir := filepath.Join(root, "mods")
	if err := os.MkdirAll(modsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(modsDir, "escape.jar")
	if err := os.Symlink(escaped, linkPath); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	srv := newSrv(t, root, modsSpec("mods", nil))

	// Verify that a valid upload to a normal file works (positive case).
	status, body := postUpload(t, srv, "file", "valid.jar", []byte("VALIDDATA"))
	if status != http.StatusOK {
		t.Errorf("valid upload status=%d, want 200; body=%s", status, body)
	}
	if _, err := os.Stat(filepath.Join(modsDir, "valid.jar")); err != nil {
		t.Errorf("valid file should exist: %v", err)
	}

	// Verify that upload to the symlink name is rejected by ConfinePath
	// due to the symlink pointing outside the mods directory.
	status, _ = postUpload(t, srv, "file", "escape.jar", []byte("BADDATA"))
	if status != http.StatusBadRequest {
		t.Errorf("upload to symlink escape status=%d, want 400", status)
	}

	// Verify the attack target (outside mods dir) was not touched.
	entries, _ := os.ReadDir(escaped)
	if len(entries) != 0 {
		t.Errorf("escape target must remain untouched, but contains %d entries", len(entries))
	}
}
