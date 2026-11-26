package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// gapEnv returns a getenv function over m; missing keys read as empty.
func gapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// gapFailingBody is a request body whose connection drops at once.
type gapFailingBody struct{}

func (gapFailingBody) Read([]byte) (int, error) {
	return 0, errors.New("connection reset")
}

func TestReceiveConfigRejectsMissingUnpackLimit(t *testing.T) {
	for _, m := range []map[string]string{
		{"ARCHIVE_FORMAT": "tar.gz", "ARCHIVE_DECLARED_BYTES": "10"},
		{"ARCHIVE_FORMAT": "tar.gz", "ARCHIVE_DECLARED_BYTES": "10", "ARCHIVE_MAX_UNPACKED_BYTES": "0"},
	} {
		if _, err := receiveConfigFromEnv(gapEnv(m)); err == nil {
			t.Errorf("receiveConfigFromEnv(%v): want error", m)
		}
	}
}

func TestReceiveShortBodyRefused(t *testing.T) {
	rv := newTestReceiver(t, "tar.gz", 20)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", strings.NewReader("0123456789"))
	req.ContentLength = 20
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rv.result().Error, "upload is 10 bytes, declared 20") {
		t.Errorf("error = %q, want the short upload", rv.result().Error)
	}
}

func TestReceiveBodyReadFailureRefused(t *testing.T) {
	rv := newTestReceiver(t, "tar.gz", 10)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", gapFailingBody{})
	req.ContentLength = 10
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rv.result().Error, "connection reset") {
		t.Errorf("error = %q, want the read failure", rv.result().Error)
	}
}

func TestReceiveStagingPathIsFileRefused(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "staging")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rv := newReceiver(receiveConfig{Format: "tar.gz", DeclaredBytes: 5, MaxUnpacked: 1 << 20, StagingDir: blocker}, func() {})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rv.result().Error, "create staging directory") {
		t.Errorf("error = %q", rv.result().Error)
	}
}

func TestReceiveUploadPathIsDirectoryRefused(t *testing.T) {
	rv := newTestReceiver(t, "tar.gz", 5)
	if err := os.Mkdir(rv.uploadPath, 0o700); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rv.result().Error, "create upload file") {
		t.Errorf("error = %q", rv.result().Error)
	}
}

func TestTruncateErrorKeepsTheLimit(t *testing.T) {
	long := strings.Repeat("e", maxTerminationErrorBytes+200)
	if got := truncateError(long); len(got) != maxTerminationErrorBytes {
		t.Errorf("len = %d, want %d", len(got), maxTerminationErrorBytes)
	}
	if got := truncateError("short"); got != "short" {
		t.Errorf("truncateError(short) = %q", got)
	}
}

func TestRunReceiveReportsBadConfig(t *testing.T) {
	term := filepath.Join(t.TempDir(), "termination-log")
	var stderr bytes.Buffer
	code := runReceive(t.Context(), Options{TerminationLog: term}, gapEnv(map[string]string{"ARCHIVE_FORMAT": "rar"}), &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	raw, err := os.ReadFile(term)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ARCHIVE_FORMAT") {
		t.Errorf("termination = %q", raw)
	}
}

func TestRunReceiveReportsTLSFailure(t *testing.T) {
	term := filepath.Join(t.TempDir(), "termination-log")
	opts := Options{
		UID: "uid", Addr: "127.0.0.1:0", Cert: "/nonexistent/tls.crt", Key: "/nonexistent/tls.key",
		ClientCA: "/nonexistent/ca.crt", IdleTimeout: time.Second, TerminationLog: term,
	}
	getenv := gapEnv(map[string]string{
		"ARCHIVE_FORMAT": "tar.gz", "ARCHIVE_DECLARED_BYTES": "5",
		"ARCHIVE_MAX_UNPACKED_BYTES": "1048576", "ARCHIVE_STAGING_DIR": t.TempDir(),
	})
	var stderr bytes.Buffer
	if code := runReceive(t.Context(), opts, getenv, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	raw, err := os.ReadFile(term)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "load TLS") {
		t.Errorf("termination = %q, want load TLS", raw)
	}
}

// startReceive runs runReceive in the background and returns the address it
// listens on and a channel with its exit code.
func startReceive(ctx context.Context, t *testing.T, opts Options, getenv func(string) string) (string, <-chan int) {
	t.Helper()
	lnCh := captureListener(t)
	done := make(chan int, 1)
	go func() { done <- runReceive(ctx, opts, getenv, io.Discard) }()
	select {
	case ln := <-lnCh:
		return ln.Addr().String(), done
	case code := <-done:
		t.Fatalf("runReceive returned %d before listening", code)
	case <-time.After(10 * time.Second):
		t.Fatal("runReceive did not start listening")
	}
	return "", nil
}

func gapReceiveOptions(pki filePKI, term string) Options {
	return Options{
		UID: "uid", Addr: "127.0.0.1:0", Cert: pki.certFile, Key: pki.keyFile, ClientCA: pki.caFile,
		IdleTimeout: 30 * time.Second, TerminationLog: term,
	}
}

func TestRunReceiveUnpacksUploadEndToEnd(t *testing.T) {
	pki := writeFilePKI(t)
	body := plainBody(t)
	staging := t.TempDir()
	term := filepath.Join(t.TempDir(), "termination-log")
	getenv := gapEnv(map[string]string{
		"ARCHIVE_FORMAT":             "tar.gz",
		"ARCHIVE_DECLARED_BYTES":     strconv.Itoa(len(body)),
		"ARCHIVE_MAX_UNPACKED_BYTES": "1048576",
		"ARCHIVE_STAGING_DIR":        staging,
	})
	addr, done := startReceive(t.Context(), t, gapReceiveOptions(pki, term), getenv)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "https://"+addr+"/upload/uid", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := exportClient(pki).Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runReceive did not return after a successful upload")
	}
	b, err := os.ReadFile(filepath.Join(staging, "unpacked", "volumes", "data", "a.txt"))
	if err != nil || string(b) != "hello" {
		t.Errorf("unpacked a.txt = %q, %v", b, err)
	}
	raw, err := os.ReadFile(term)
	if err != nil {
		t.Fatal(err)
	}
	var sum receiveSummary
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatalf("termination is not JSON: %v", err)
	}
	if sum.Error != "" || sum.Kind != kindPlain || len(sum.Volumes) != 1 {
		t.Errorf("summary = %+v", sum)
	}
}

func TestRunReceiveIdleTimeoutRecordsError(t *testing.T) {
	pki := writeFilePKI(t)
	term := filepath.Join(t.TempDir(), "termination-log")
	opts := gapReceiveOptions(pki, term)
	opts.IdleTimeout = 50 * time.Millisecond
	getenv := gapEnv(map[string]string{
		"ARCHIVE_FORMAT":             "tar.gz",
		"ARCHIVE_DECLARED_BYTES":     "5",
		"ARCHIVE_MAX_UNPACKED_BYTES": "1048576",
		"ARCHIVE_STAGING_DIR":        t.TempDir(),
	})
	_, done := startReceive(t.Context(), t, opts, getenv)

	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runReceive did not return after the idle timeout")
	}
	raw, err := os.ReadFile(term)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), ErrIdleTimeout.Error()) {
		t.Errorf("termination = %q, want idle timeout", raw)
	}
}

func TestReportReceiveUnwritableTerminationExitsOne(t *testing.T) {
	term := filepath.Join(t.TempDir(), "missing", "termination-log")
	var stderr bytes.Buffer
	if code := reportReceive(Options{TerminationLog: term}, receiveSummary{Volumes: []string{}}, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want the write failure")
	}
}
