package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

var errGapBoom = errors.New("gap test boom")

// gapSetUID rewrites the UID of a fixture object held by the fake dynamic
// client, so a route can carry an identity the apiserver would never issue.
func gapSetUID(t *testing.T, h *handler, gvr schema.GroupVersionResource, name, uid string) {
	t.Helper()
	ctx := t.Context()
	obj, err := h.client.Dynamic.Resource(gvr).Namespace("games").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	obj.SetUID(types.UID(uid))
	if _, err := h.client.Dynamic.Resource(gvr).Namespace("games").Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

// gapFakeDynamic returns the fake dynamic client behind the handler so a test
// can inject apiserver failures for one resource.
func gapFakeDynamic(t *testing.T, h *handler) *dynamicfake.FakeDynamicClient {
	t.Helper()
	fake, ok := h.client.Dynamic.(*dynamicfake.FakeDynamicClient)
	if !ok {
		t.Fatalf("dynamic client is %T, want *dynamicfake.FakeDynamicClient", h.client.Dynamic)
	}
	return fake
}

func gapFailingDial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("connection refused")
}

// gapDeadTransport returns a worker transport whose dial always fails, so no
// request leaves the test process.
func gapDeadTransport(TLSFiles) (*http.Transport, error) {
	return &http.Transport{DialContext: gapFailingDial}, nil
}

// gapBrokenWriter is a client connection that is already gone: every write fails.
type gapBrokenWriter struct{ header http.Header }

func (w *gapBrokenWriter) Header() http.Header       { return w.header }
func (w *gapBrokenWriter) WriteHeader(int)           {}
func (w *gapBrokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// gapWriter lets a test choose whether the client write or the flush fails.
type gapWriter struct {
	writeErr error
	flushErr error
	wrote    int
}

func (w *gapWriter) Header() http.Header { return http.Header{} }
func (w *gapWriter) WriteHeader(int)     {}
func (w *gapWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	w.wrote += len(p)
	return len(p), nil
}

// FlushError is the hook http.NewResponseController uses before http.Flusher.
func (w *gapWriter) FlushError() error { return w.flushErr }

func TestGapBackupExportUnbuildableWorkerURLIsBadGateway(t *testing.T) {
	h, cert := newFixture(t)
	target := backupExportFixture(t, h, "Ready", "exp-svc", "same-name")
	// A control character in the export UID cannot form a request URL, so the
	// gateway must answer 502 before any byte reaches the worker.
	gapSetUID(t, h, kube.GVRBackupExport, "exp-one", "export-uid\x7f")
	target.ExportUID = "export-uid\x7f"
	h.transport = gapDeadTransport
	rr := httptest.NewRecorder()
	h.serveBackupExport(rr, request(t, cert, http.MethodGet, "/unused"), target)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "backup export unavailable") {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestGapBackupExportWorkerDialFailureIsBadGateway(t *testing.T) {
	h, cert := newFixture(t)
	target := backupExportFixture(t, h, "Ready", "exp-svc", "same-name")
	h.transport = gapDeadTransport
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, request(t, cert, http.MethodGet, backupExportPathFor(t, target)))
	if rr.Code != http.StatusBadGateway || rr.Body.String() != "backup export worker unavailable\n" {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestGapBackupExportWorkerRedirectIsBadGateway(t *testing.T) {
	h, cert := newFixture(t)
	target := backupExportFixture(t, h, "Ready", "exp-svc", "same-name")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/somewhere-else", http.StatusFound)
	}))
	defer upstream.Close()
	h.transport = workerTransport(t, upstream)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, request(t, cert, http.MethodGet, backupExportPathFor(t, target)))
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "redirects are not supported") {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "" {
		t.Fatal("worker redirect target was forwarded to the client")
	}
}

// A client that leaves mid-archive is not the worker's fault: the copy stops
// quietly, without the connection abort reserved for truncated worker bodies.
func TestGapBackupExportClientGoneStopsCopyWithoutAbort(t *testing.T) {
	h, cert := newFixture(t)
	target := backupExportFixture(t, h, "Ready", "exp-svc", "same-name")
	var hits atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "archive-bytes")
	}))
	defer upstream.Close()
	h.transport = workerTransport(t, upstream)
	w := &gapBrokenWriter{header: http.Header{}}
	// A panic here would be the http.ErrAbortHandler path; the call must return.
	h.serveBackupExport(w, request(t, cert, http.MethodGet, backupExportPathFor(t, target)), target)
	if hits.Load() != 1 {
		t.Fatalf("worker hits=%d, want 1", hits.Load())
	}
}

func TestGapCopyFlushingReportsClientWriteAndFlushFailures(t *testing.T) {
	workerErr := errors.New("worker reset")
	for _, tc := range []struct {
		name       string
		writer     *gapWriter
		body       io.Reader
		wantClient bool
		wantErr    error
	}{
		{name: "client write fails", writer: &gapWriter{writeErr: errors.New("EPIPE")}, body: strings.NewReader("abc"), wantClient: true},
		{name: "client flush fails", writer: &gapWriter{flushErr: errors.New("flush closed")}, body: strings.NewReader("abc"), wantClient: true},
		{name: "worker body fails", writer: &gapWriter{}, body: io.MultiReader(strings.NewReader("ab"), gapErrReader{err: workerErr}), wantErr: workerErr},
		{name: "clean end", writer: &gapWriter{}, body: strings.NewReader("abc")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := copyFlushing(tc.writer, tc.body)
			if tc.wantClient != errors.Is(err, errClientGone) {
				t.Fatalf("errClientGone=%v want=%v (err=%v)", errors.Is(err, errClientGone), tc.wantClient, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want wrapped %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && !tc.wantClient && err != nil {
				t.Fatalf("clean end returned %v", err)
			}
		})
	}
}

type gapErrReader struct{ err error }

func (r gapErrReader) Read([]byte) (int, error) { return 0, r.err }

func TestGapRestoreUploadOverLimitIsRejectedBeforeLookup(t *testing.T) {
	h, cert := newFixture(t)
	h.cfg.BackupsMaxUploadBytes = 3
	target := restoreUploadFixture(t, h, "Receiving", "rs-svc", "same-name", true)
	fake := gapFakeDynamic(t, h)
	fake.PrependReactor("get", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		t.Error("oversized upload reached the Restore lookup")
		return true, nil, errGapBoom
	})
	req := request(t, cert, http.MethodPut, restoreUploadPathFor(t, target))
	req.Body = io.NopCloser(strings.NewReader(restoreUploadBody))
	req.ContentLength = int64(len(restoreUploadBody))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rr.Body.String(), "too large") {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestGapRestoreUploadLookupFailureIsServiceUnavailable(t *testing.T) {
	h, cert := newFixture(t)
	target := restoreUploadFixture(t, h, "Receiving", "rs-svc", "same-name", true)
	gapFakeDynamic(t, h).PrependReactor("get", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errGapBoom
	})
	req := request(t, cert, http.MethodPut, restoreUploadPathFor(t, target))
	req.Body = io.NopCloser(strings.NewReader(restoreUploadBody))
	req.ContentLength = int64(len(restoreUploadBody))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || rr.Body.String() != "restore lookup unavailable\n" {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestGapRestoreUploadUnbuildableWorkerURLIsBadGateway(t *testing.T) {
	h, cert := newFixture(t)
	target := restoreUploadFixture(t, h, "Receiving", "rs-svc", "same-name", true)
	// The route is built from the real UID first; the handler is then handed a
	// target whose UID cannot form a request URL.
	req := request(t, cert, http.MethodPut, restoreUploadPathFor(t, target))
	gapSetUID(t, h, kube.GVRRestore, "rs-one", "restore-uid\x7f")
	target.RestoreUID = "restore-uid\x7f"
	h.transport = gapDeadTransport
	req.Body = io.NopCloser(strings.NewReader(restoreUploadBody))
	req.ContentLength = int64(len(restoreUploadBody))
	rr := httptest.NewRecorder()
	h.serveRestoreUpload(rr, req, target)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "restore upload unavailable") {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestGapRestoreUploadWorkerRedirectIsBadGateway(t *testing.T) {
	h, cert := newFixture(t)
	target := restoreUploadFixture(t, h, "Receiving", "rs-svc", "same-name", true)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	h.transport = workerTransport(t, upstream)
	req := request(t, cert, http.MethodPut, restoreUploadPathFor(t, target))
	req.Body = io.NopCloser(strings.NewReader(restoreUploadBody))
	req.ContentLength = int64(len(restoreUploadBody))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "redirects are not supported") {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}
