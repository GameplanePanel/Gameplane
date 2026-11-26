package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDump replaces runDump. It writes src to stdout, text to stderr, and
// returns dumpErr after the writes.
func fakeDump(src []byte, stderrText string, dumpErr error) func(context.Context, string, io.Writer, io.Writer) error {
	return func(_ context.Context, _ string, stdout, stderr io.Writer) error {
		if stderrText != "" {
			_, _ = io.WriteString(stderr, stderrText)
		}
		if len(src) > 0 {
			if _, err := stdout.Write(src); err != nil {
				return err
			}
		}
		return dumpErr
	}
}

func setRunDump(t *testing.T, fn func(context.Context, string, io.Writer, io.Writer) error) {
	t.Helper()
	prev := runDump
	runDump = fn
	t.Cleanup(func() { runDump = prev })
}

func newTestExporter(format string, abort func()) *exporter {
	return newExporter(exportConfig{Snapshot: "snap1", Format: format, Manifest: testManifest()}, abort)
}

func exportRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/export/uid", nil)
}

func TestExporterServesTarGz(t *testing.T) {
	src := buildSource(t, fullSource())
	setRunDump(t, fakeDump(src, "", nil))

	x := newTestExporter("tar.gz", nil)
	rec := httptest.NewRecorder()
	x.ServeHTTP(rec, exportRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	hdrs, _ := readTarGz(t, rec.Body.Bytes())
	if len(hdrs) != 8 || hdrs[0].Name != ManifestName {
		t.Errorf("archive entries = %d, first %q", len(hdrs), hdrs[0].Name)
	}

	res := x.result()
	if res.Error != "" || res.Entries != 8 || res.Skipped != 5 || res.BytesSent != int64(rec.Body.Len()) {
		t.Errorf("result = %+v, body %d bytes", res, rec.Body.Len())
	}
}

func TestExporterServesZip(t *testing.T) {
	setRunDump(t, fakeDump(buildSource(t, fullSource()), "", nil))

	x := newTestExporter("zip", nil)
	rec := httptest.NewRecorder()
	x.ServeHTTP(rec, exportRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	files := readZip(t, rec.Body.Bytes())
	if len(files) != 7 || files[0].Name != ManifestName {
		t.Errorf("zip entries = %v", zipNames(files))
	}
	if res := x.result(); res.Error != "" || res.Skipped != 6 {
		t.Errorf("result = %+v", res)
	}
}

func TestExporterStreamsLargeArchiveWithFlushes(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	body := make([]byte, 2<<20)
	_, _ = rng.Read(body)
	src := buildSource(t, []srcEntry{
		{hdr: tar.Header{Name: "data/random.bin", Typeflag: tar.TypeReg, Mode: 0o644}, body: string(body)},
	})
	setRunDump(t, fakeDump(src, "", nil))

	x := newTestExporter("zip", nil)
	rec := httptest.NewRecorder()
	x.ServeHTTP(rec, exportRequest())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	files := readZip(t, rec.Body.Bytes())
	found := false
	for _, f := range files {
		if f.Name == "data/random.bin" {
			found = true
			if got := zipBody(t, f); got != string(body) {
				t.Errorf("random.bin content mismatch: %d bytes", len(got))
			}
		}
	}
	if !found {
		t.Error("data/random.bin missing from archive")
	}
}

func TestExporterFailureBeforeFirstByteIs502(t *testing.T) {
	setRunDump(t, fakeDump(nil, "repo locked", errors.New("exit status 1")))

	aborted := false
	x := newTestExporter("tar.gz", func() { aborted = true })
	rec := httptest.NewRecorder()
	x.ServeHTTP(rec, exportRequest())

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "repo locked") {
		t.Error("restic stderr must not reach the client")
	}
	if rec.Header().Get("Content-Disposition") != "" || rec.Header().Get("Content-Type") == "application/gzip" {
		t.Errorf("archive headers must be cleared on failure, got %v", rec.Header())
	}
	res := x.result()
	if !strings.Contains(res.Error, "repo locked") || !strings.Contains(res.Error, "exit status 1") {
		t.Errorf("result error = %q, want restic stderr and exit status", res.Error)
	}
	if !aborted {
		t.Error("abort was not called on failure")
	}
}

func TestExporterMidStreamFailureAbortsConnection(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	body := make([]byte, 2<<20)
	_, _ = rng.Read(body)
	src := buildSource(t, []srcEntry{
		{hdr: tar.Header{Name: "data/random.bin", Typeflag: tar.TypeReg, Mode: 0o644}, body: string(body)},
	})
	setRunDump(t, fakeDump(src, "", errors.New("restic exploded")))

	aborted := false
	x := newTestExporter("tar.gz", func() { aborted = true })
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if r := recover(); r != http.ErrAbortHandler {
				t.Errorf("recovered %v, want http.ErrAbortHandler", r)
			}
		}()
		x.ServeHTTP(rec, exportRequest())
		t.Error("ServeHTTP returned; want a panic with http.ErrAbortHandler")
	}()
	if rec.Body.Len() == 0 {
		t.Error("client should have received bytes before the abort")
	}
	if !aborted {
		t.Error("abort was not called on failure")
	}
	if res := x.result(); !strings.Contains(res.Error, "restic exploded") || res.BytesSent != int64(rec.Body.Len()) {
		t.Errorf("result = %+v, body %d bytes", res, rec.Body.Len())
	}
}

// brokenWriter accepts the headers but rejects every body write, as a client
// that went away would.
type brokenWriter struct {
	*httptest.ResponseRecorder
}

func (brokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("client gone")
}

func TestExporterClientWriteFailure(t *testing.T) {
	setRunDump(t, fakeDump(buildSource(t, fullSource()), "", nil))

	x := newTestExporter("tar.gz", nil)
	w := brokenWriter{httptest.NewRecorder()}
	x.ServeHTTP(w, exportRequest())

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when no byte reached the client", w.Code)
	}
	if res := x.result(); !strings.Contains(res.Error, "client gone") {
		t.Errorf("result error = %q", res.Error)
	}
}

func TestExporterRejectsNonGET(t *testing.T) {
	called := false
	setRunDump(t, func(context.Context, string, io.Writer, io.Writer) error {
		called = true
		return nil
	})
	x := newTestExporter("zip", nil)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodHead} {
		rec := httptest.NewRecorder()
		x.ServeHTTP(rec, httptest.NewRequest(method, "/export/uid", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, rec.Code)
		}
		if rec.Header().Get("Allow") != http.MethodGet {
			t.Errorf("%s Allow = %q", method, rec.Header().Get("Allow"))
		}
	}
	if called {
		t.Error("runDump must not run for non-GET requests")
	}
}

func TestExportConfigFromEnv(t *testing.T) {
	good := map[string]string{
		"ARCHIVE_SNAPSHOT_ID": "abc123",
		"ARCHIVE_FORMAT":      "zip",
		"ARCHIVE_MANIFEST":    `{"formatVersion":1,"writer":"op"}`,
	}
	env := func(overrides map[string]string) func(string) string {
		return func(key string) string {
			if v, ok := overrides[key]; ok {
				return v
			}
			return good[key]
		}
	}

	cfg, err := exportConfigFromEnv(env(nil))
	if err != nil {
		t.Fatalf("valid env: %v", err)
	}
	if cfg.Snapshot != "abc123" || cfg.Format != "zip" || cfg.Manifest.Writer != "op" {
		t.Errorf("config = %+v", cfg)
	}

	tests := []struct {
		name      string
		overrides map[string]string
		wantErr   string
	}{
		{"missing snapshot", map[string]string{"ARCHIVE_SNAPSHOT_ID": ""}, "ARCHIVE_SNAPSHOT_ID is required"},
		{"option-like snapshot", map[string]string{"ARCHIVE_SNAPSHOT_ID": "-x"}, "must not start with '-'"},
		{"bad format", map[string]string{"ARCHIVE_FORMAT": "rar"}, "ARCHIVE_FORMAT"},
		{"missing manifest", map[string]string{"ARCHIVE_MANIFEST": ""}, "ARCHIVE_MANIFEST is required"},
		{"bad manifest json", map[string]string{"ARCHIVE_MANIFEST": "{nope"}, "ARCHIVE_MANIFEST"},
		{"manifest without version", map[string]string{"ARCHIVE_MANIFEST": `{}`}, "formatVersion missing"},
		{"newer manifest", map[string]string{"ARCHIVE_MANIFEST": `{"formatVersion":99}`}, "newer Gameplane"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := exportConfigFromEnv(env(tt.overrides))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestTailBufferKeepsLastBytes(t *testing.T) {
	var tb tailBuffer
	_, _ = tb.Write([]byte(strings.Repeat("a", 10000)))
	_, _ = tb.Write([]byte("END\n"))
	got := tb.String()
	if len(got) != stderrTailBytes || !strings.HasSuffix(got, "END") {
		t.Errorf("tail length %d, suffix %q", len(got), got[max(0, len(got)-5):])
	}
	var empty tailBuffer
	if empty.String() != "" {
		t.Error("empty tail should be empty")
	}
}

func TestReportExportTruncatesAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	var stderr bytes.Buffer
	res := exportResult{BytesSent: 42, Entries: 3, Skipped: 1, Error: strings.Repeat("e", 5000)}
	if code := reportExport(Options{TerminationLog: path}, res, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read termination log: %v", err)
	}
	var got exportResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode termination log: %v", err)
	}
	if len(got.Error) != maxTerminationErrorBytes || got.BytesSent != 42 || got.Entries != 3 || got.Skipped != 1 {
		t.Errorf("termination = %+v", got)
	}
}

func TestReportExportSuccessIsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	var stderr bytes.Buffer
	if code := reportExport(Options{TerminationLog: path}, exportResult{BytesSent: 7}, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty on success", stderr.String())
	}
}

func TestReportExportWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "termination-log")
	var stderr bytes.Buffer
	if code := reportExport(Options{TerminationLog: path}, exportResult{}, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Error("write failure should be reported on stderr")
	}
}

func TestRunExportConfigErrorWritesTermination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	var stderr bytes.Buffer
	opts := Options{UID: "uid", TerminationLog: path}
	code := runExport(context.Background(), opts, func(string) string { return "" }, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "ARCHIVE_SNAPSHOT_ID") {
		t.Errorf("termination log = %q, %v", raw, err)
	}
}

func TestRunExportTLSErrorWritesTermination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	var stderr bytes.Buffer
	opts := Options{UID: "uid", TerminationLog: path, Cert: "/nonexistent/tls.crt", Key: "/nonexistent/tls.key", ClientCA: "/nonexistent/ca.crt"}
	if code := runExport(context.Background(), opts, validEnv(), &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "load TLS") {
		t.Errorf("termination log = %q, %v", raw, err)
	}
}

func validEnv() func(string) string {
	env := map[string]string{
		"ARCHIVE_SNAPSHOT_ID": "snap1",
		"ARCHIVE_FORMAT":      "tar.gz",
		"ARCHIVE_MANIFEST":    `{"formatVersion":1}`,
	}
	return func(key string) string { return env[key] }
}

// filePKI is a throwaway CA with a server and a client certificate, written
// to disk for auth.ServerTLS.
type filePKI struct {
	caFile, certFile, keyFile string
	clientCert                tls.Certificate
	pool                      *x509.CertPool
}

func writeFilePKI(t *testing.T) filePKI {
	t.Helper()
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "archive test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(crand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(usage x509.ExtKeyUsage) ([]byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: "archive test"},
			DNSNames:     []string{"localhost"},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(crand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	}
	serverCert, serverKey := issue(x509.ExtKeyUsageServerAuth)
	clientCert, clientKey := issue(x509.ExtKeyUsageClientAuth)

	p := filePKI{
		caFile:   filepath.Join(dir, "ca.crt"),
		certFile: filepath.Join(dir, "tls.crt"),
		keyFile:  filepath.Join(dir, "tls.key"),
		pool:     x509.NewCertPool(),
	}
	p.pool.AddCert(caCert)
	if err := os.WriteFile(p.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.certFile, serverCert, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.keyFile, serverKey, 0o600); err != nil {
		t.Fatal(err)
	}
	cc, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	p.clientCert = cc
	return p
}

// startExport runs runExport in the background and returns the address it
// listens on and a channel with its exit code.
func startExport(t *testing.T, ctx context.Context, opts Options, getenv func(string) string) (string, <-chan int, *bytes.Buffer) {
	t.Helper()
	lnCh := captureListener(t)

	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runExport(ctx, opts, getenv, &stderr) }()

	select {
	case ln := <-lnCh:
		return ln.Addr().String(), done, &stderr
	case code := <-done:
		t.Fatalf("runExport returned %d before listening; stderr %q", code, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("runExport did not start listening")
	}
	return "", nil, nil
}

func exportClient(p filePKI) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      p.pool,
				Certificates: []tls.Certificate{p.clientCert},
				MinVersion:   tls.VersionTLS12,
			},
		},
	}
}

func TestRunExportEndToEnd(t *testing.T) {
	pki := writeFilePKI(t)
	src := buildSource(t, fullSource())
	setRunDump(t, fakeDump(src, "", nil))

	path := filepath.Join(t.TempDir(), "termination-log")
	opts := Options{
		UID: "uid", Addr: "127.0.0.1:0", Cert: pki.certFile, Key: pki.keyFile, ClientCA: pki.caFile,
		IdleTimeout: 30 * time.Second, TerminationLog: path,
	}
	addr, done, stderr := startExport(t, context.Background(), opts, validEnv())

	resp, err := exportClient(pki).Get("https://" + addr + "/export/uid")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	hdrs, _ := readTarGz(t, body)
	if len(hdrs) != 8 {
		t.Errorf("archive entries = %d, want 8", len(hdrs))
	}

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, stderr %q", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runExport did not return after a successful export")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read termination log: %v", err)
	}
	var res exportResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode termination: %v", err)
	}
	if res.BytesSent != int64(len(body)) || res.Entries != 8 || res.Skipped != 5 || res.Error != "" {
		t.Errorf("termination = %+v, body %d bytes", res, len(body))
	}
}

func TestRunExportDumpFailureExitsOne(t *testing.T) {
	pki := writeFilePKI(t)
	setRunDump(t, fakeDump(nil, "no snapshot", errors.New("exit status 10")))

	path := filepath.Join(t.TempDir(), "termination-log")
	opts := Options{
		UID: "uid", Addr: "127.0.0.1:0", Cert: pki.certFile, Key: pki.keyFile, ClientCA: pki.caFile,
		IdleTimeout: 30 * time.Second, TerminationLog: path,
	}
	addr, done, _ := startExport(t, context.Background(), opts, validEnv())

	resp, err := exportClient(pki).Get("https://" + addr + "/export/uid")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}

	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runExport did not return after a failed export")
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "exit status 10") {
		t.Errorf("termination log = %q", raw)
	}
}

func TestRunExportIdleTimeoutExitsOne(t *testing.T) {
	pki := writeFilePKI(t)
	path := filepath.Join(t.TempDir(), "termination-log")
	opts := Options{
		UID: "uid", Addr: "127.0.0.1:0", Cert: pki.certFile, Key: pki.keyFile, ClientCA: pki.caFile,
		IdleTimeout: 50 * time.Millisecond, TerminationLog: path,
	}
	_, done, _ := startExport(t, context.Background(), opts, validEnv())

	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runExport did not return after the idle timeout")
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), ErrIdleTimeout.Error()) {
		t.Errorf("termination log = %q, want idle timeout", raw)
	}
}
