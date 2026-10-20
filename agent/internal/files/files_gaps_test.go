package files

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestList_NotADirectory exercises os.ReadDir on a regular file.
func TestList_NotADirectory(t *testing.T) {
	srvURL, root := newServer(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp := get(t, srvURL, "/files/list", url.Values{"path": []string{"/f.txt"}})
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("listing a regular file should fail")
	}
}

// TestWrite_BodyTooLarge confirms the MaxBytesReader cap fires.
func TestWrite_BodyTooLarge(t *testing.T) {
	srvURL, _ := newServer(t)
	body := bytes.NewReader(bytes.Repeat([]byte("a"), maxWriteBytes+1))
	resp, err := testPost(t, srvURL+"/files/write?path=/big.txt", "application/octet-stream", body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized write status=%d, want 413", resp.StatusCode)
	}
}

// TestWrite_BadResolve hits the resolve-error branch on /files/write.
func TestWrite_BadResolve(t *testing.T) {
	srvURL, root := newServer(t)
	resp, err := testPost(t, srvURL+"/files/write?path="+notADirPath(t, root), "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

// errReader fails every read, exercising savePart's copy-error branch.
type errReader struct{}

var errRead = errors.New("read boom")

func (errReader) Read([]byte) (int, error) { return 0, errRead }

// TestSavePart_CreateError points savePart at a directory path that is
// really a regular file, so os.Create fails.
func TestSavePart_CreateError(t *testing.T) {
	dir := t.TempDir()
	blocking := filepath.Join(dir, "blocking")
	if err := os.WriteFile(blocking, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := savePart(blocking, blocking, "x.txt", strings.NewReader("y"), 16); err == nil {
		t.Fatal("expected create error")
	}
}

// TestSavePart_CopyError confirms a mid-stream read failure is wrapped.
func TestSavePart_CopyError(t *testing.T) {
	dir := t.TempDir()
	err := savePart(dir, dir, "x.txt", errReader{}, 16)
	if !errors.Is(err, errRead) {
		t.Fatalf("got %v, want wrapped %v", err, errRead)
	}
}

// TestUpload_SkipsNonFileParts confirms plain form fields are ignored
// while attachments in the same body are still stored.
func TestUpload_SkipsNonFileParts(t *testing.T) {
	srvURL, root := newServer(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("comment", "hello"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	fw, err := mw.CreateFormFile("files", "a.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("alpha")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	resp, err := testPost(t, srvURL+"/files/upload?path=/mixed", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(root, "mixed", "a.txt"))
	if err != nil || string(got) != "alpha" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "mixed", "comment")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("form field should not be written: %v", err)
	}
}

// TestUpload_MalformedBody hits the NextPart error branch with a body
// that never contains the declared boundary.
func TestUpload_MalformedBody(t *testing.T) {
	srvURL, _ := newServer(t)
	resp, err := testPost(t, srvURL+"/files/upload?path=/bad", "multipart/form-data; boundary=zzz", strings.NewReader("garbage\r\n"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

// TestMkdir_ExistingFile triggers the os.MkdirAll error branch by
// asking it to create a directory whose path is already a file.
func TestMkdir_ExistingFile(t *testing.T) {
	srvURL, root := newServer(t)
	conflict := filepath.Join(root, "blocking")
	if err := os.WriteFile(conflict, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := testPost(t, srvURL+"/files/mkdir?path=/blocking/sub", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		t.Fatal("mkdir over existing file should fail")
	}
}

// TestUpload_ResolveError exercises the resolve-fail branch on /files/upload.
func TestUpload_ResolveError(t *testing.T) {
	srvURL, root := newServer(t)
	buf, ct := multipartBody(t, map[string]string{"x.txt": "y"})
	resp, err := testPost(t, srvURL+"/files/upload?path="+notADirPath(t, root), ct, buf)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

// TestUpload_TruncatedBody is a regression test for the multipart EOF
// handling bug: a request that successfully parses one file part but is then
// truncated before the closing boundary must be rejected (400), not silently
// accepted as complete (204). The fix uses direct 'err == io.EOF' comparison
// to distinguish a bare EOF (genuine closing boundary) from a wrapped EOF
// (truncated body), since errors.Is unwraps both and treats them as equal.
func TestUpload_TruncatedBody(t *testing.T) {
	srvURL, root := newServer(t)

	// Build a multipart body with one valid file part, then truncate it
	// before writing the closing boundary. This simulates a client that sends
	// a complete part header + content but then abruptly closes the connection.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("files", "partial.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("content")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	// DO NOT call mw.Close() — that writes the closing boundary.
	// Instead, just capture the raw buffer with an incomplete body.
	truncatedBody := buf.String()

	// Upload the truncated multipart.
	resp, err := testPost(t, srvURL+"/files/upload?path=/trunc", mw.FormDataContentType(), strings.NewReader(truncatedBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	// The handler must reject a truncated body with 400, not 204.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("truncated multipart should return 400, got %d", resp.StatusCode)
	}

	// Verify no file was written (upload should have been rejected).
	if _, err := os.Stat(filepath.Join(root, "trunc", "partial.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should not exist after truncated upload: %v", err)
	}
}
