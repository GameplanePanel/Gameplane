package files

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newServer mounts files on a temp root and returns the server URL + the
// resolved root path (resolved via EvalSymlinks because t.TempDir() may
// return a path containing symlinks on macOS — the production resolver
// would fight us otherwise).
func newServer(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	r := chi.NewRouter()
	Mount(r, resolved)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL, resolved
}

func get(t *testing.T, srvURL, path string, q url.Values) *http.Response {
	t.Helper()
	full := srvURL + path
	if q != nil {
		full += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, full, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return resp
}

func testPost(t *testing.T, url, contentType string, body io.Reader) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	return http.DefaultClient.Do(req)
}

func TestList(t *testing.T) {
	srvURL, root := newServer(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	resp := get(t, srvURL, "/files/list", url.Values{"path": []string{"/"}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var ents []Entry
	if err := json.NewDecoder(resp.Body).Decode(&ents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(ents) != 2 {
		t.Fatalf("got %d entries: %+v", len(ents), ents)
	}
	names := map[string]bool{}
	for _, e := range ents {
		names[e.Name] = e.Dir
	}
	if names["a.txt"] != false || names["sub"] != true {
		t.Fatalf("entries=%+v", ents)
	}
}

func TestList_NotFound(t *testing.T) {
	srvURL, _ := newServer(t)
	resp := get(t, srvURL, "/files/list", url.Values{"path": []string{"/missing"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestList_FiltersDotfiles(t *testing.T) {
	srvURL, root := newServer(t)
	// Create a normal file and a dotfile in the same directory.
	if err := os.WriteFile(filepath.Join(root, "normal.txt"), []byte("visible"), 0o600); err != nil {
		t.Fatalf("write normal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("invisible"), 0o600); err != nil {
		t.Fatalf("write dotfile: %v", err)
	}

	resp := get(t, srvURL, "/files/list", url.Values{"path": []string{"/"}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var ents []Entry
	if err := json.NewDecoder(resp.Body).Decode(&ents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Should have exactly one entry: normal.txt.
	if len(ents) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(ents), ents)
	}
	if ents[0].Name != "normal.txt" {
		t.Fatalf("got entry %q, want 'normal.txt'", ents[0].Name)
	}
	// Verify the dotfile actually exists on disk (to confirm the filter works).
	if _, err := os.Stat(filepath.Join(root, ".hidden")); err != nil {
		t.Fatalf("dotfile does not exist: %v", err)
	}
}

func TestRead(t *testing.T) {
	srvURL, root := newServer(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("success", func(t *testing.T) {
		resp := get(t, srvURL, "/files/read", url.Values{"path": []string{"/a.txt"}})
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "hi" {
			t.Fatalf("body=%q", body)
		}
	})

	t.Run("directory rejected", func(t *testing.T) {
		resp := get(t, srvURL, "/files/read", url.Values{"path": []string{"/"}})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})

	t.Run("not found", func(t *testing.T) {
		resp := get(t, srvURL, "/files/read", url.Values{"path": []string{"/nope"}})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})

	t.Run("too large", func(t *testing.T) {
		big := filepath.Join(root, "big.bin")
		f, err := os.Create(big)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := f.Truncate((2 << 20) + 1); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		_ = f.Close()
		resp := get(t, srvURL, "/files/read", url.Values{"path": []string{"/big.bin"}})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})

	t.Run("bad resolve generic 400", func(t *testing.T) {
		// An ancestor that is a regular file → EvalSymlinks fails with
		// ENOTDIR (not NotExist) → generic "bad request" (not the verbatim
		// errPathOutOfRoot path).
		resp := get(t, srvURL, "/files/read", url.Values{"path": []string{notADirPath(t, root)}})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "bad request") {
			t.Fatalf("body=%q", body)
		}
	})

	t.Run("symlink-escape returns errPathOutOfRoot verbatim", func(t *testing.T) {
		outside := t.TempDir()
		outsideResolved, _ := filepath.EvalSymlinks(outside)
		// Place a real file outside, then a symlink inside root pointing
		// to that file's parent. Reading the symlink will trigger resolve's
		// post-EvalSymlinks prefix check and return errPathOutOfRoot.
		if err := os.WriteFile(filepath.Join(outsideResolved, "secret"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		link := filepath.Join(root, "esc-r")
		if err := os.Symlink(outsideResolved, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
		resp := get(t, srvURL, "/files/read", url.Values{"path": []string{"/esc-r/secret"}})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "escapes root") {
			t.Fatalf("body=%q", body)
		}
	})
}

func TestDownload(t *testing.T) {
	srvURL, root := newServer(t)
	if err := os.WriteFile(filepath.Join(root, "f.bin"), []byte("payload"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("success sets Content-Disposition", func(t *testing.T) {
		resp := get(t, srvURL, "/files/download", url.Values{"path": []string{"/f.bin"}})
		defer resp.Body.Close()
		if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "f.bin") {
			t.Fatalf("Content-Disposition=%q", cd)
		}
	})

	t.Run("directory rejected", func(t *testing.T) {
		resp := get(t, srvURL, "/files/download", url.Values{"path": []string{"/"}})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})

	t.Run("not found", func(t *testing.T) {
		resp := get(t, srvURL, "/files/download", url.Values{"path": []string{"/nope"}})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})
}

func TestWrite(t *testing.T) {
	srvURL, root := newServer(t)

	t.Run("writes file in root", func(t *testing.T) {
		body := bytes.NewBufferString("hello world")
		resp, err := testPost(t, srvURL+"/files/write?path=/a.txt", "text/plain", body)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(resp))
		}
		got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
		if string(got) != "hello world" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("rejects symlink-escape via resolve", func(t *testing.T) {
		// Place a symlink inside root pointing outside; writing through
		// it must be rejected.
		outside := t.TempDir()
		outsideResolved, _ := filepath.EvalSymlinks(outside)
		link := filepath.Join(root, "escape")
		if err := os.Symlink(outsideResolved, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
		// Targeting the symlink itself: resolve will follow and reject.
		resp, err := testPost(t, srvURL+"/files/write?path=/escape", "text/plain", strings.NewReader("x"))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})
}

// errAfterReader returns n bytes of filler data, then a read error — it
// simulates a request body or upload source that fails partway through
// (client disconnect, ENOSPC), unlike errReader (files_gaps_test.go),
// which fails on the very first read.
type errAfterReader struct{ n int }

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, errRead
	}
	if len(p) > r.n {
		p = p[:r.n]
	}
	for i := range p {
		p[i] = 'x'
	}
	r.n -= len(p)
	return len(p), nil
}

// TestWrite_PreservesExistingFileOnCopyError is the F-102 regression test:
// a /files/write whose body fails partway through must leave a
// pre-existing target file untouched (write-to-temp-then-rename), not
// truncated. Calls h.write directly (rather than through newServer's real
// HTTP round trip) so the mid-copy failure is deterministic.
func TestWrite_PreservesExistingFileOnCopyError(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resolved, "keep.txt"), []byte("original content"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := &handler{root: resolved}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/files/write?path=/keep.txt", &errAfterReader{n: 4})
	rr := httptest.NewRecorder()
	h.write(rr, req)
	if rr.Code == http.StatusNoContent {
		t.Fatalf("expected failure status, got %d", rr.Code)
	}
	got, rerr := os.ReadFile(filepath.Join(resolved, "keep.txt"))
	if rerr != nil || string(got) != "original content" {
		t.Fatalf("original file corrupted: got %q err=%v", got, rerr)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatalf("unexpected root contents (stray temp file?): %+v", entries)
	}
}

// TestSavePart_CopyErrorPreservesExistingFile is the other half of the
// F-102 regression test: an upload part whose source fails partway
// through must leave a pre-existing file at that destination name
// untouched, not deleted by the old cleanup-on-error path.
func TestSavePart_CopyErrorPreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(dst, []byte("original"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := savePart(dir, dir, "x.txt", errReader{}, 16); !errors.Is(err, errRead) {
		t.Fatalf("got %v, want wrapped %v", err, errRead)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "original" {
		t.Fatalf("original file corrupted: got %q err=%v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "x.txt" {
		t.Fatalf("unexpected dir contents (stray temp file?): %+v", entries)
	}
}

// TestDelete_SymlinkInRootRemovesLinkNotTarget is the F-108 regression
// test: deleting a symlink that stays inside the root must remove the
// link itself, not the file it points to. The file API can't create a
// symlink (per F-108's evidence), so the symlink is planted directly with
// os.Symlink, as the existing escape-detection tests in this package do.
func TestDelete_SymlinkInRootRemovesLinkNotTarget(t *testing.T) {
	srvURL, root := newServer(t)
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(root, "logs", "a.log")
	if err := os.WriteFile(target, []byte("log content"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(root, "latest.log")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path=/latest.log", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(resp))
	}

	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink still present: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "log content" {
		t.Fatalf("target deleted or corrupted: got %q err=%v", got, err)
	}
}

func TestUpload(t *testing.T) {
	srvURL, root := newServer(t)

	t.Run("happy path", func(t *testing.T) {
		buf, ct := multipartBody(t, map[string]string{"a.txt": "alpha", "b.txt": "beta"})
		resp, err := testPost(t, srvURL+"/files/upload?path=/up", ct, buf)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(resp))
		}
		a, _ := os.ReadFile(filepath.Join(root, "up", "a.txt"))
		b, _ := os.ReadFile(filepath.Join(root, "up", "b.txt"))
		if string(a) != "alpha" || string(b) != "beta" {
			t.Fatalf("a=%q b=%q", a, b)
		}
	})

	t.Run("rejects upload through symlink to outside", func(t *testing.T) {
		outside := t.TempDir()
		outsideResolved, _ := filepath.EvalSymlinks(outside)
		link := filepath.Join(root, "esc-up")
		if err := os.Symlink(outsideResolved, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
		buf, ct := multipartBody(t, map[string]string{"x.txt": "x"})
		resp, err := testPost(t, srvURL+"/files/upload?path=/esc-up", ct, buf)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})

	t.Run("multipart parse error", func(t *testing.T) {
		// Wrong Content-Type (no multipart boundary) → MultipartReader fails.
		resp, err := testPost(t, srvURL+"/files/upload?path=/", "text/plain", strings.NewReader("not multipart"))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})
}

func TestSavePart_RejectsBadFilenames(t *testing.T) {
	dir := t.TempDir()
	cases := []string{".", "..", string(os.PathSeparator)}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			err := savePart(dir, dir, name, strings.NewReader("x"), maxUploadFileBytes)
			if err == nil || !strings.Contains(err.Error(), "invalid filename") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestSavePart_RejectsOversize(t *testing.T) {
	dir := t.TempDir()
	err := savePart(dir, dir, "big", strings.NewReader("abcdef"), 3)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("got %v", err)
	}
}

// resolvedTempDir returns a fresh temp dir with any symlinks in its path
// resolved, matching how the production root is compared.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval temp dir: %v", err)
	}
	return d
}

// TestSavePart_RejectsOutOfRootSymlinkDestination checks that an upload
// whose destination name is a symlink out of root is rejected and leaves
// the target untouched.
func TestSavePart_RejectsOutOfRootSymlinkDestination(t *testing.T) {
	root := resolvedTempDir(t)
	outside := resolvedTempDir(t)
	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("orig"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	linkPath := filepath.Join(root, "esc-link")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := savePart(root, root, "esc-link", strings.NewReader("should-reject"), 1024)
	if !errors.Is(err, errPathOutOfRoot) {
		t.Fatalf("got %v, want %v", err, errPathOutOfRoot)
	}

	got, err := os.ReadFile(target)
	if err != nil || string(got) != "orig" {
		t.Fatalf("outside target modified: got %q err=%v", got, err)
	}
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced with a regular file")
	}
}

// TestSavePart_RejectsDanglingSymlinkDestination checks that a destination
// symlink which cannot be resolved is rejected rather than replaced.
func TestSavePart_RejectsDanglingSymlinkDestination(t *testing.T) {
	root := resolvedTempDir(t)
	linkPath := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(resolvedTempDir(t), "missing", "file"), linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err := savePart(root, root, "dangling", strings.NewReader("x"), 1024)
	if !errors.Is(err, errPathOutOfRoot) {
		t.Fatalf("got %v, want %v", err, errPathOutOfRoot)
	}
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced with a regular file")
	}
}

// TestSavePart_RejectsInRootSymlinkDestination checks that a destination
// symlink is refused even when it points at another folder inside root:
// link traversal is rejected at every component, and both the link and the
// linked file are left as they were.
func TestSavePart_RejectsInRootSymlinkDestination(t *testing.T) {
	root := resolvedTempDir(t)
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	target := filepath.Join(dirB, "y")
	if err := os.WriteFile(target, []byte("orig"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	linkPath := filepath.Join(dirA, "x")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := savePart(root, dirA, "x", strings.NewReader("new"), 1024)
	if !errors.Is(err, errPathOutOfRoot) {
		t.Fatalf("got %v, want %v", err, errPathOutOfRoot)
	}
	fi, err := os.Lstat(linkPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced: fi=%v err=%v", fi, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "orig" {
		t.Fatalf("linked file modified: got %q err=%v", got, err)
	}
}

// TestUpload_RejectsOutOfRootSymlinkDestination checks the handler answers
// a rejected destination with 400 and leaves the target untouched.
func TestUpload_RejectsOutOfRootSymlinkDestination(t *testing.T) {
	srvURL, root := newServer(t)
	outside := resolvedTempDir(t)
	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("orig"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "esc-link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	buf, ct := multipartBody(t, map[string]string{"esc-link": "should-reject"})
	resp, err := testPost(t, srvURL+"/files/upload?path=/", ct, buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(resp))
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "orig" {
		t.Fatalf("outside target modified: got %q err=%v", got, err)
	}
}

// symlinkedRoot returns a data root reached through a symlink (the link
// path) together with the real directory it points at.
func symlinkedRoot(t *testing.T) (string, string) {
	t.Helper()
	realRoot := resolvedTempDir(t)
	link := filepath.Join(resolvedTempDir(t), "data")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatalf("symlink root: %v", err)
	}
	return link, realRoot
}

// TestUpload_SymlinkedRootRejectsInRootSymlinkDestination checks that when
// the data root itself is reached through a symlink (the root path is
// trusted and followed), uploading over an existing symlink whose target is
// inside the root is still rejected and leaves link and target untouched.
func TestUpload_SymlinkedRootRejectsInRootSymlinkDestination(t *testing.T) {
	linkRoot, realRoot := symlinkedRoot(t)
	if err := os.Mkdir(filepath.Join(realRoot, "b"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(realRoot, "b", "y")
	if err := os.WriteFile(target, []byte("orig"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(realRoot, "x")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	r := chi.NewRouter()
	Mount(r, linkRoot)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	buf, ct := multipartBody(t, map[string]string{"x": "new"})
	resp, err := testPost(t, srv.URL+"/files/upload?path=/", ct, buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(resp))
	}
	fi, err := os.Lstat(filepath.Join(realRoot, "x"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced: fi=%v err=%v", fi, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "orig" {
		t.Fatalf("linked file modified: got %q err=%v", got, err)
	}
}

// TestSavePart_SymlinkedRootRejectsOutOfRootSymlinkDestination checks that
// resolving the root's own symlinks does not loosen confinement.
func TestSavePart_SymlinkedRootRejectsOutOfRootSymlinkDestination(t *testing.T) {
	linkRoot, _ := symlinkedRoot(t)
	target := filepath.Join(resolvedTempDir(t), "target.txt")
	if err := os.WriteFile(target, []byte("orig"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(linkRoot, "esc-link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err := savePart(linkRoot, linkRoot, "esc-link", strings.NewReader("x"), 1024)
	if !errors.Is(err, errPathOutOfRoot) {
		t.Fatalf("got %v, want %v", err, errPathOutOfRoot)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "orig" {
		t.Fatalf("outside target modified: got %q err=%v", got, err)
	}
}

func TestUpload_TooManyFiles(t *testing.T) {
	srvURL, _ := newServer(t)
	files := map[string]string{}
	for i := 0; i <= maxUploadFiles; i++ {
		files[filepath.Join("f"+itoa(i)+".txt")] = "x"
	}
	buf, ct := multipartBody(t, files)
	resp, err := testPost(t, srvURL+"/files/upload?path=/many", ct, buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestMkdir(t *testing.T) {
	srvURL, root := newServer(t)
	resp, err := testPost(t, srvURL+"/files/mkdir?path=/single", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(resp))
	}
	fi, err := os.Stat(filepath.Join(root, "single"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
}

// Directories the agent creates must be traversable by the game uid, which on
// templates without an fsGroup reaches the volume through the "other" bits.
func TestMkdir_DirectoriesAreWorldTraversable(t *testing.T) {
	srvURL, root := newServer(t)
	resp, err := testPost(t, srvURL+"/files/mkdir?path=/a/b", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(resp))
	}
	for _, p := range []string{"a", filepath.Join("a", "b")} {
		fi, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		// Only the "other" r-x bits are asserted so the test is stable under any
		// umask that keeps them (the common 022).
		if perm := fi.Mode().Perm(); perm&0o005 != 0o005 {
			t.Errorf("dir %s mode=%03o, want other r-x (0o755)", p, perm)
		}
	}
}

func TestMkdir_BadResolve(t *testing.T) {
	// An ancestor that is a regular file → resolve fails (ENOTDIR). A merely
	// missing parent now resolves so MkdirAll can create the subtree.
	srvURL, root := newServer(t)
	resp, err := testPost(t, srvURL+"/files/mkdir?path="+notADirPath(t, root), "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDelete(t *testing.T) {
	srvURL, root := newServer(t)
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "d", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "sub", "g"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("non-recursive removes file", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path=/f", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status=%d", resp.StatusCode)
		}
		if _, err := os.Stat(filepath.Join(root, "f")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("still exists: %v", err)
		}
	})

	t.Run("non-recursive on non-empty dir errors", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path=/d", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent {
			t.Fatal("expected error on non-empty dir")
		}
	})

	t.Run("recursive removes dir tree", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path=/d&recursive=true", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})

	t.Run("recursive refuses tree containing dotfile", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(root, "w", "sub"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "w", "sub", ".state"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path=/w&recursive=true", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
		if _, err := os.Stat(filepath.Join(root, "w", "sub", ".state")); err != nil {
			t.Fatalf("dotfile removed: %v", err)
		}
	})

	t.Run("recursive on a plain file removes it", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(root, "pf"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path=/pf&recursive=true", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})

	t.Run("refuses to delete root", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path=/&recursive=true", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})

	t.Run("bad resolve on delete (not a directory)", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path="+notADirPath(t, root), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	})
}

func TestResolve_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	outside := t.TempDir()
	resolvedOutside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatalf("eval outside: %v", err)
	}

	// Place a symlink inside root that points outside.
	link := filepath.Join(resolved, "escape")
	if err := os.Symlink(resolvedOutside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	h := &handler{root: resolved}
	rr := httptest.NewRecorder()
	h.list(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/files/list?path=/escape", nil))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "escapes root") {
		t.Fatalf("status=%d body=%q, want 400 escapes root", rr.Code, rr.Body.String())
	}
}

func TestResolve_EmptyPathReturnsRoot(t *testing.T) {
	h := &handler{root: "/data"}
	got, err := h.resolve("")
	if err != nil || got != "/data" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestResolve_ParentMissing(t *testing.T) {
	root := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(root)
	h := &handler{root: resolved}
	// Missing intermediate dirs must resolve (to the literal path) so the
	// handlers' MkdirAll can create the whole subtree on write/upload/mkdir.
	got, err := h.resolve("/no/such/parent/file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(resolved, "no/such/parent/file"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHttpErr_PermissionMappedTo403(t *testing.T) {
	rr := httptest.NewRecorder()
	httpErr(rr, os.ErrPermission)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestHttpErr_BodyLimitMappedTo413(t *testing.T) {
	for _, err := range []error{
		&http.MaxBytesError{Limit: maxWriteBytes},
		fmt.Errorf("write /private/data/file: %w", &http.MaxBytesError{Limit: maxWriteBytes}),
	} {
		rr := httptest.NewRecorder()
		httpErr(rr, err)
		if rr.Code != http.StatusRequestEntityTooLarge || rr.Body.String() != "request body too large\n" {
			t.Fatalf("body limit response: status=%d body=%q", rr.Code, rr.Body.String())
		}
	}
}

func TestHttpErr_GenericMappedTo500(t *testing.T) {
	rr := httptest.NewRecorder()
	httpErr(rr, errors.New("disk on fire"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "disk on fire") {
		t.Fatal("error message leaked")
	}
}

// TestResolve_DotfileAccess rejects dotfile access (paths starting with . or
// containing /.) to prevent access to sensitive files like .gameplane-mods.json
// or .bashrc, as specified in the security contract.
func TestResolve_DotfileAccess(t *testing.T) {
	root := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(root)
	h := &handler{root: resolved}

	tests := []struct {
		path string
		desc string
	}{
		{".gameplane-mods.json", "dotfile at root"},
		{".bashrc", "hidden config file"},
		{".hidden", "hidden directory"},
		{"foo/.hidden", "dotfile in subdirectory"},
		{"foo/.hidden/file", "file inside dotfile directory"},
		{"/.hidden", "dotfile with leading slash"},
		{"./.hidden", "dotfile with leading dot-slash"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			_, err := h.resolve(tt.path)
			if err == nil {
				t.Fatalf("resolve(%q) should reject dotfile access, but succeeded", tt.path)
			}
			if !strings.Contains(err.Error(), "dotfile") {
				t.Errorf("error should mention 'dotfile', got: %v", err)
			}
		})
	}
}

// TestList_DotfileRejected confirms that the list HTTP endpoint rejects
// dotfile requests with a 400 error code.
func TestList_DotfileRejected(t *testing.T) {
	srvURL, _ := newServer(t)
	resp := get(t, srvURL, "/files/list", url.Values{"path": []string{"/.gameplane-mods.json"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	body := readBody(resp)
	if !strings.Contains(body, "dotfile") {
		t.Errorf("error message should mention 'dotfile', got: %q", body)
	}
}

// TestDelete_DotfileRejected confirms that the delete endpoint refuses a
// dot-prefixed path and leaves the file in place.
func TestDelete_DotfileRejected(t *testing.T) {
	srvURL, root := newServer(t)
	target := filepath.Join(root, ".gameplane-mods.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srvURL+"/files/delete?path=/.gameplane-mods.json", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	if body := readBody(resp); !strings.Contains(body, "dotfile") {
		t.Errorf("error message should mention 'dotfile', got: %q", body)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("dotfile was removed: %v", err)
	}
}

// TestUpload_DotfileRejected confirms that an upload whose part filename is
// dot-prefixed is refused and neither creates nor overwrites that file.
func TestUpload_DotfileRejected(t *testing.T) {
	srvURL, root := newServer(t)
	target := filepath.Join(root, ".gameplane-mods.json")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf, ct := multipartBody(t, map[string]string{".gameplane-mods.json": "replaced"})
	resp, err := testPost(t, srvURL+"/files/upload?path=/", ct, buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	if body := readBody(resp); !strings.Contains(body, "dotfile") {
		t.Errorf("error message should mention 'dotfile', got: %q", body)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "original" {
		t.Fatalf("dotfile overwritten: got %q err=%v", got, err)
	}
}

// TestResolve_SymlinkToDotfileRejected confirms a plain-named symlink cannot
// be used to reach a dotfile or a dot-directory inside the root: the dotfile
// check is applied to the symlink-resolved path as well as the requested one.
func TestResolve_SymlinkToDotfileRejected(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resolved, ".gameplane-mods.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Mkdir(filepath.Join(resolved, ".state"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(resolved, ".gameplane-mods.json"), filepath.Join(resolved, "manifest")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(resolved, ".state"), filepath.Join(resolved, "state")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	h := &handler{root: resolved}
	call := func(fn http.HandlerFunc, method, target string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		fn(rr, httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader("x")))
		return rr
	}
	wantDotfile := func(rr *httptest.ResponseRecorder, what string) {
		t.Helper()
		if rr.Code != http.StatusBadRequest || rr.Body.String() != "dotfile access denied\n" {
			t.Fatalf("%s: status=%d body=%q, want 400 dotfile access denied", what, rr.Code, rr.Body.String())
		}
	}

	// Existing target reached through a link to a dotfile.
	wantDotfile(call(h.read, http.MethodGet, "/files/read?path=/manifest"), "read /manifest")
	// New file whose ancestor is a link to a dot-directory.
	wantDotfile(call(h.write, http.MethodPost, "/files/write?path=/state/new.txt"), "write /state/new.txt")
	// Delete of an entry inside a dot-directory reached through a link.
	wantDotfile(call(h.del, http.MethodDelete, "/files/delete?path=/state/x"), "delete /state/x")
	// Deleting the link itself stays allowed: it removes the link, not the
	// dotfile it points to.
	if rr := call(h.del, http.MethodDelete, "/files/delete?path=/manifest"); rr.Code != http.StatusNoContent {
		t.Fatalf("delete /manifest: status=%d body=%q, want 204", rr.Code, rr.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(resolved, "manifest")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(resolved, ".gameplane-mods.json")); err != nil {
		t.Fatalf("dotfile was removed: %v", err)
	}
}

// TestRead_SymlinkToDotfileRejected confirms the read endpoint refuses a
// symlink that points at a dotfile and does not serve its contents.
func TestRead_SymlinkToDotfileRejected(t *testing.T) {
	srvURL, root := newServer(t)
	if err := os.WriteFile(filepath.Join(root, ".gameplane-mods.json"), []byte("secret-manifest"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, ".gameplane-mods.json"), filepath.Join(root, "manifest")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	resp := get(t, srvURL, "/files/read", url.Values{"path": []string{"/manifest"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	body := readBody(resp)
	if body != "dotfile access denied\n" {
		t.Fatalf("body=%q, want %q", body, "dotfile access denied\n")
	}
}

// helpers

func multipartBody(t *testing.T, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, body := range files {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func readBody(r *http.Response) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// TestWrite_FilePermissions verifies that written files default to 0o644
// so the game container (different uid, shared fsGroup) can read them.
func TestWrite_FilePermissions(t *testing.T) {
	srvURL, root := newServer(t)

	// Write a new file
	body := bytes.NewReader([]byte("new content"))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srvURL+"/files/write?path=/new.txt", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	fi, err := os.Stat(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o644 {
		t.Errorf("new file mode=%03o, want 0o644", perm)
	}

	// Overwrite preserves the existing mode
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("orig"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	body = bytes.NewReader([]byte("replaced content"))
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, srvURL+"/files/write?path=/existing.txt", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	fi, err = os.Stat(filepath.Join(root, "existing.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("overwritten file mode=%03o, want 0o600 (preserved)", perm)
	}
}

// TestUpload_FilePermissions verifies that uploaded files default to 0o644
// so the game container (different uid, shared fsGroup) can read them.
func TestUpload_FilePermissions(t *testing.T) {
	srvURL, root := newServer(t)

	// Upload a new file
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", "upload.txt")
	if err != nil {
		t.Fatalf("createformfile: %v", err)
	}
	if _, err := part.Write([]byte("uploaded content")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	mw.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srvURL+"/files/upload?path=/", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	fi, err := os.Stat(filepath.Join(root, "upload.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o644 {
		t.Errorf("uploaded file mode=%03o, want 0o644", perm)
	}

	// Upload over an existing file with a different mode (preserve mode)
	if err := os.WriteFile(filepath.Join(root, "override.txt"), []byte("orig"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	body = new(bytes.Buffer)
	mw = multipart.NewWriter(body)
	part, err = mw.CreateFormFile("file", "override.txt")
	if err != nil {
		t.Fatalf("createformfile: %v", err)
	}
	if _, err := part.Write([]byte("replaced content")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	mw.Close()

	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, srvURL+"/files/upload?path=/", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	fi, err = os.Stat(filepath.Join(root, "override.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("override file mode=%03o, want 0o600 (preserved)", perm)
	}
}

func TestUpload_RejectsSymlinkDestinationInRoot(t *testing.T) {
	root := t.TempDir()

	// Create target file with restricted mode
	targetFile := filepath.Join(root, "target")
	if err := os.WriteFile(targetFile, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create symlink pointing to target
	link := filepath.Join(root, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// Upload refuses the symlink destination.
	src := strings.NewReader("new content")
	if err := savePart(root, root, "link", src, 1000); !errors.Is(err, errPathOutOfRoot) {
		t.Fatalf("savePart err=%v, want %v", err, errPathOutOfRoot)
	}

	// The link is still a symlink and its target is untouched.
	fileStat, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fileStat.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced")
	}
	got, err := os.ReadFile(targetFile)
	if err != nil || string(got) != "original" {
		t.Fatalf("target modified: got %q err=%v", got, err)
	}
	if fi, err := os.Stat(targetFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("target mode changed: fi=%v err=%v", fi, err)
	}
}
