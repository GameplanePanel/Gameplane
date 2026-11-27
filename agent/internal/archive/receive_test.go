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
	"strings"
	"testing"
	"time"
)

// newTestReceiver returns a receiver for a body of size declared bytes in a
// fresh staging directory.
func newTestReceiver(t *testing.T, format string, declared int64) *receiver {
	t.Helper()
	return newReceiver(receiveConfig{
		Format:        format,
		DeclaredBytes: declared,
		MaxUnpacked:   1 << 20,
		StagingDir:    t.TempDir(),
	}, func() {})
}

// plainBody is a small plain tar.gz upload with one file.
func plainBody(t *testing.T) []byte {
	return makeTarGzBytes(t, []tarEnt{{name: "a.txt", body: "hello"}})
}

func TestReceiveRequiresContentLength(t *testing.T) {
	rv := newTestReceiver(t, "tar.gz", 10)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", io.MultiReader(strings.NewReader("0123456789")))
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, req)
	if rec.Code != http.StatusLengthRequired {
		t.Fatalf("status = %d, want 411", rec.Code)
	}
	if rv.result().Error == "" {
		t.Error("summary error not set")
	}
}

func TestReceiveRejectsSizeMismatch(t *testing.T) {
	rv := newTestReceiver(t, "tar.gz", 20)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", strings.NewReader("0123456789"))
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rv.result().Error, "declared") {
		t.Errorf("error = %q, want the size mismatch", rv.result().Error)
	}
}

func TestReceiveAbortsWhenBodyOverflows(t *testing.T) {
	rv := newTestReceiver(t, "tar.gz", 10)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", strings.NewReader("0123456789ABCDEFGHIJ"))
	req.ContentLength = 10 // the declared length, while the body keeps going
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("no panic, want http.ErrAbortHandler")
			} else if err, ok := r.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
				t.Fatalf("recover = %v, want http.ErrAbortHandler", r)
			}
		}()
		rv.ServeHTTP(rec, req)
	}()
	if !strings.Contains(rv.result().Error, errUploadOverflow.Error()) {
		t.Errorf("error = %q, want the overflow", rv.result().Error)
	}
}

func TestReceiveRejectsWrongMethod(t *testing.T) {
	rv := newTestReceiver(t, "tar.gz", 10)
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload/u", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if rv.result().Error != "" {
		t.Errorf("wrong method should not end the run, error = %q", rv.result().Error)
	}
}

func TestReceiveSuccessUnpacksAndWritesSummary(t *testing.T) {
	body := plainBody(t)
	rv := newTestReceiver(t, "tar.gz", int64(len(body)))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if _, err := os.Stat(rv.uploadPath); err != nil {
		t.Errorf("upload not stored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rv.unpackDir, "volumes", "data", "a.txt")); err != nil {
		t.Errorf("file not unpacked: %v", err)
	}
	sum := rv.result()
	if sum.Error != "" || sum.Kind != kindPlain || sum.UnpackedBytes != 5 {
		t.Fatalf("summary = %+v, want plain, 5 bytes, no error", sum)
	}
	if len(sum.Volumes) != 1 || sum.Volumes[0] != "data" {
		t.Errorf("Volumes = %v, want [data]", sum.Volumes)
	}
}

func TestReceiveUnpackFailureIsRecorded(t *testing.T) {
	body := makeTarGzBytes(t, []tarEnt{{name: "/etc/passwd", body: "x"}})
	rv := newTestReceiver(t, "tar.gz", int64(len(body)))
	rec := httptest.NewRecorder()
	rv.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/upload/u", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the upload was received)", rec.Code)
	}
	if !strings.Contains(rv.result().Error, "/etc/passwd") {
		t.Errorf("error = %q, want the refused entry", rv.result().Error)
	}
}

func TestReceiveServesOnlyTheUploadPath(t *testing.T) {
	pki := newTestPKI(t)
	body := plainBody(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rv := newReceiver(receiveConfig{
		Format:        "tar.gz",
		DeclaredBytes: int64(len(body)),
		MaxUnpacked:   1 << 20,
		StagingDir:    t.TempDir(),
	}, cancel)

	result, addr := startServe(ctx, t, ServerConfig{
		Addr:        "127.0.0.1:0",
		TLS:         pki.serverTLS,
		Prefix:      uploadPrefix,
		UID:         "u1",
		IdleTimeout: 5 * time.Second,
	}, rv)
	c := pki.client()

	if code, err := fetch(c, "https://"+addr+"/upload/other"); err != nil || code != http.StatusNotFound {
		t.Fatalf("wrong UID: status = %d, err = %v, want 404", code, err)
	}
	if code, err := fetch(c, "https://"+addr+"/upload"); err != nil || code != http.StatusNotFound {
		t.Fatalf("bare prefix: status = %d, err = %v, want 404", code, err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "https://"+addr+"/upload/u1", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}
	if err := await(t, result); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if sum := rv.result(); sum.Error != "" || sum.Kind != kindPlain {
		t.Errorf("summary = %+v, want plain without error", sum)
	}
}

func TestReportReceiveWritesSummary(t *testing.T) {
	term := filepath.Join(t.TempDir(), "term")
	var buf bytes.Buffer
	sum := receiveSummary{Kind: kindPlain, UnpackedBytes: 5, Volumes: []string{"data"}}
	if code := reportReceive(Options{TerminationLog: term}, sum, &buf); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	b, err := os.ReadFile(term)
	if err != nil {
		t.Fatal(err)
	}
	var got receiveSummary
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("termination message is not JSON: %v", err)
	}
	if got.Kind != kindPlain || got.UnpackedBytes != 5 || got.Error != "" || len(got.Volumes) != 1 {
		t.Errorf("termination = %+v", got)
	}
}

func TestReportReceiveErrorExitsOne(t *testing.T) {
	term := filepath.Join(t.TempDir(), "term")
	var buf bytes.Buffer
	if code := reportReceive(Options{TerminationLog: term}, receiveSummary{Volumes: []string{}, Error: "boom"}, &buf); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("stderr = %q, want the error", buf.String())
	}
}

func TestReceiveConfigFromEnv(t *testing.T) {
	good := map[string]string{
		"ARCHIVE_FORMAT":             "zip",
		"ARCHIVE_DECLARED_BYTES":     "10",
		"ARCHIVE_MAX_UNPACKED_BYTES": "100",
		"ARCHIVE_PLAIN_TEMPLATE":     "yes",
	}
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cfg, err := receiveConfigFromEnv(env(good))
	if err != nil {
		t.Fatalf("receiveConfigFromEnv: %v", err)
	}
	if cfg.StagingDir != "/staging" || !cfg.Plain || cfg.DeclaredBytes != 10 || cfg.MaxUnpacked != 100 {
		t.Errorf("cfg = %+v", cfg)
	}
	bad := map[string]string{"ARCHIVE_FORMAT": "rar", "ARCHIVE_DECLARED_BYTES": "1", "ARCHIVE_MAX_UNPACKED_BYTES": "1"}
	if _, err := receiveConfigFromEnv(env(bad)); err == nil {
		t.Error("unsupported format: want error")
	}
	zero := map[string]string{"ARCHIVE_FORMAT": "zip", "ARCHIVE_DECLARED_BYTES": "0", "ARCHIVE_MAX_UNPACKED_BYTES": "1"}
	if _, err := receiveConfigFromEnv(env(zero)); err == nil {
		t.Error("zero declared size: want error")
	}
}
