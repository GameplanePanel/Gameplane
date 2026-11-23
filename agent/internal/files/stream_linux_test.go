package files

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errStreamCancel stands in for a client that drops the connection mid-upload.
var errStreamCancel = errors.New("client canceled upload")

// cancelAfter yields its data and then fails, like a dropped connection.
type cancelAfter struct{ data []byte }

func (c *cancelAfter) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, errStreamCancel
	}
	n := copy(p, c.data)
	c.data = c.data[n:]
	return n, nil
}

// assertNoStreamTemp fails when any ".stream-" temporary file remains under root.
func assertNoStreamTemp(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".stream-") {
			t.Fatalf("temporary file remains: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func streamTo(t *testing.T, h *handler, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, h.stream, http.MethodPut, target, body)
}

func TestStream_NewFileOverHTTP(t *testing.T) {
	srvURL, root := newServer(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut,
		srvURL+"/files/stream?path=/new.txt", strings.NewReader("streamed"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", resp.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil || string(got) != "streamed" {
		t.Fatalf("content=%q err=%v", got, err)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_OverwriteExisting(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	put(t, filepath.Join(root, "f.txt"), "old-content")
	resp := streamTo(t, h, "/files/stream?path=/f.txt", strings.NewReader("new"))
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", resp.Code)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(got) != "new" {
		t.Fatalf("content=%q, want new", got)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_ResumeAppendsAfterOffset(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	put(t, filepath.Join(root, "f.txt"), "abcdef")
	resp := streamTo(t, h, "/files/stream?path=/f.txt&offset=3", strings.NewReader("XYZ"))
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", resp.Code)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(got) != "abcXYZ" {
		t.Fatalf("content=%q, want abcXYZ", got)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_OffsetBeyondSizeRejected(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	put(t, filepath.Join(root, "f.txt"), "abc")
	resp := streamTo(t, h, "/files/stream?path=/f.txt&offset=10", strings.NewReader("XYZ"))
	if resp.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status=%d, want 416", resp.Code)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(got) != "abc" {
		t.Fatalf("existing file changed: %q", got)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_OffsetMissingFileNotFound(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	resp := streamTo(t, h, "/files/stream?path=/missing.txt&offset=2", strings.NewReader("XY"))
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.Code)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_InvalidOffsetRejected(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	for _, off := range []string{"-1", "abc"} {
		resp := streamTo(t, h, "/files/stream?path=/f.txt&offset="+off, strings.NewReader("x"))
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("offset=%s: status=%d, want 400", off, resp.Code)
		}
	}
	assertNoStreamTemp(t, root)
}

func TestStream_SizeLimitBody(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = 4
	put(t, filepath.Join(root, "f.txt"), "keep")
	resp := streamTo(t, h, "/files/stream?path=/f.txt", strings.NewReader("12345"))
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", resp.Code)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(got) != "keep" {
		t.Fatalf("existing file changed: %q", got)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_SizeLimitCountsOffset(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = 4
	put(t, filepath.Join(root, "f.txt"), "abcdef")
	resp := streamTo(t, h, "/files/stream?path=/f.txt&offset=3", strings.NewReader("XY"))
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", resp.Code)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_FreeSpaceRefusedBeforeWriting(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	h.freeBytes = func(int) (uint64, error) { return 2, nil }
	resp := streamTo(t, h, "/files/stream?path=/new.txt", strings.NewReader("12345"))
	if resp.Code != http.StatusInsufficientStorage {
		t.Fatalf("status=%d, want 507", resp.Code)
	}
	if _, err := os.Lstat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("file created despite refusal: err=%v", err)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_ClientCancelKeepsExistingFile(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	put(t, filepath.Join(root, "f.txt"), "original")
	resp := streamTo(t, h, "/files/stream?path=/f.txt", &cancelAfter{data: []byte("abc")})
	if resp.Code < 400 {
		t.Fatalf("status=%d, want an error status", resp.Code)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(got) != "original" {
		t.Fatalf("existing file changed after cancel: %q", got)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_DotfileRejected(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	resp := streamTo(t, h, "/files/stream?path=/.env", strings.NewReader("x"))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.Code)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_RootRejected(t *testing.T) {
	h, _, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	resp := streamTo(t, h, "/files/stream?path=/", strings.NewReader("x"))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.Code)
	}
}

func TestStream_SymlinkDestinationRefused(t *testing.T) {
	h, root, outside := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	resp := streamTo(t, h, "/files/stream?path=/link.txt", strings.NewReader("pwned"))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.Code)
	}
	assertOutsideUntouched(t, outside)
	assertNoStreamTemp(t, root)
}

func TestStream_AncestorSwappedForSymlinkRejected(t *testing.T) {
	h, root, outside := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raceAt(t, h, "open", "", "sub", 1, func() {
		swapForSymlink(t, filepath.Join(root, "sub"), outside)
	})
	resp := streamTo(t, h, "/files/stream?path=/sub/new.txt", strings.NewReader("payload"))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.Code)
	}
	assertOutsideUntouched(t, outside)
}
