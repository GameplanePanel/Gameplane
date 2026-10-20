package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// nonFlushingWriter is an http.ResponseWriter that does NOT implement
// http.Flusher, used to deterministically hit the 500 branch in
// eventsHandler. (httptest.ResponseRecorder implements Flusher, so it
// would route into the streaming path.)
type nonFlushingWriter struct {
	header http.Header
	code   int
}

func (n *nonFlushingWriter) Header() http.Header         { return n.header }
func (n *nonFlushingWriter) Write(b []byte) (int, error) { return len(b), nil }
func (n *nonFlushingWriter) WriteHeader(c int)           { n.code = c }

// MountEvents wires the SSE handler under /events and depends on a real
// http.Flusher to send frames. The streaming-success path is exercised
// by the e2e tier (real cluster + real kube-watch); here we only assert
// the no-Flusher 500 branch deterministically.
func TestEvents_StreamingUnsupported(t *testing.T) {
	k := fakeKubeClient()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	w := &nonFlushingWriter{header: http.Header{}}
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/events", nil)
	eventsHandler(reg)(w, req)
	if w.code != http.StatusInternalServerError {
		t.Fatalf("got %d", w.code)
	}
}

// TestEvents_RejectsForbiddenNamespace — the stream must run requests
// through scope.Resolve before watching anything. A namespace the caller
// may not use is rejected (403) rather than silently watched, closing the
// old metav1.NamespaceAll cross-namespace leak.
func TestEvents_RejectsForbiddenNamespace(t *testing.T) {
	k := fakeKubeClient()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	rr := httptest.NewRecorder() // implements http.Flusher → past the 500 branch
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/events?namespace=forbidden", nil)
	eventsHandler(reg)(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403 for a non-permitted namespace", rr.Code)
	}
}
