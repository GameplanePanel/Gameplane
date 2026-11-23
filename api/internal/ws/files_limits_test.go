package ws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// readingTransport drains the request body like a real upstream copy and fails with the body's
// own error, wrapped, as net/http does when the cap trips mid-copy.
type readingTransport struct {
	mu    sync.Mutex
	calls int
	got   int
	err   error
}

func (r *readingTransport) Do(_ context.Context, op agentRequest) (*http.Response, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	var (
		n   int64
		err error
	)
	if op.body != nil {
		n, err = io.Copy(io.Discard, op.body)
	}
	r.mu.Lock()
	r.got = int(n)
	r.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("upstream copy: %w", err)
	}
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (*readingTransport) Dial(context.Context, agentTarget, string, string) (*websocket.Conn, *http.Response, error) {
	return nil, nil, errors.New("dial not used")
}

func (r *readingTransport) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func limitRequest(t *testing.T, h http.Handler, body string, contentLength int64) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Put("/servers/{name}/files/stream", h.ServeHTTP)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/servers/alpha/files/stream?path=/a", io.NopCloser(strings.NewReader(body)))
	req.ContentLength = contentLength
	req.Header.Set("Accept", "application/problem+json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHTTPProxyLimitDeclaredLengthOverCapIs413(t *testing.T) {
	tr := &readingTransport{}
	p := &proxy{transport: tr}
	rec := limitRequest(t, p.httpProxyLimit("/files/stream", 16), strings.Repeat("x", 64), 64)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "server.generic.413") {
		t.Fatalf("413 lost its message id: %s", rec.Body.String())
	}
	if tr.callCount() != 0 {
		t.Fatal("an over-limit upload reached the agent transport")
	}
}

func TestHTTPProxyLimitUnknownLengthOverCapIs413(t *testing.T) {
	tr := &readingTransport{}
	p := &proxy{transport: tr}
	// No Content-Length (-1), so the cap trips during the upstream copy.
	rec := limitRequest(t, p.httpProxyLimit("/files/stream", 16), strings.Repeat("x", 64), -1)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
	if tr.callCount() != 1 {
		t.Fatalf("transport calls = %d, want 1", tr.callCount())
	}
}

func TestHTTPProxyLimitWithinCapAndOtherErrors(t *testing.T) {
	tr := &readingTransport{}
	p := &proxy{transport: tr}
	if rec := limitRequest(t, p.httpProxyLimit("/files/stream", 16), strings.Repeat("x", 16), 16); rec.Code != http.StatusOK {
		t.Fatalf("at-limit upload status = %d, want 200", rec.Code)
	}
	if rec := limitRequest(t, p.httpProxyLimit("/files/stream", 16), strings.Repeat("x", 16), -1); rec.Code != http.StatusOK {
		t.Fatalf("at-limit unknown-length upload status = %d, want 200", rec.Code)
	}
	// A transport failure that is not the body cap stays a gateway error.
	tr.err = errors.New("connection refused")
	if rec := limitRequest(t, p.httpProxyLimit("/files/stream", 16), "x", 1); rec.Code != http.StatusBadGateway {
		t.Fatalf("transport failure status = %d, want 502", rec.Code)
	}
}

// refreshFixture is a stopped (or running) GameServer behind fake clients. Patches are counted
// through a channel, so a refresher goroutine can be observed without a data race.
type refreshFixture struct {
	proxy   *proxy
	patched chan string
}

func newRefreshFixture(t *testing.T, suspend bool, patchErr error) *refreshFixture {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kube.GVRs["servers"]: "GameServerList"}, filesTestServer(nil, suspend))
	f := &refreshFixture{patched: make(chan string, 64)}
	dyn.PrependReactor("patch", "gameservers", func(action k8stesting.Action) (bool, runtime.Object, error) {
		f.patched <- string(action.(k8stesting.PatchAction).GetPatch())
		if patchErr != nil {
			return true, nil, patchErr
		}
		return false, nil, nil
	})
	f.proxy = &proxy{
		k:         &kube.Client{Dynamic: dyn, Typed: k8sfake.NewSimpleClientset()},
		transport: &readingTransport{},
		remoteUID: filesTestUID,
	}
	return f
}

// advancingClock moves two minutes on every call, so each refresh sees a stale stamp.
func advancingClock() func() time.Time {
	var mu sync.Mutex
	at := filesTestStart
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at = at.Add(2 * time.Minute)
		return at
	}
}

func filesRefreshRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "alpha")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func waitPatch(t *testing.T, f *refreshFixture) {
	t.Helper()
	select {
	case <-f.patched:
	case <-time.After(10 * time.Second):
		t.Fatal("no files-requested-at refresh arrived")
	}
}

func TestKeepFilesRequestedRefreshesUntilStopped(t *testing.T) {
	f := newRefreshFixture(t, true, nil)
	req := filesRefreshRequest(t, "/servers/alpha/files/download?path=/a")
	stop := f.proxy.keepFilesRequested(t.Context(), req, 5*time.Millisecond, advancingClock())

	// The stamp is rewritten again and again while the request is in flight.
	waitPatch(t, f)
	waitPatch(t, f)
	waitPatch(t, f)
	stop()

	// stop waits for the goroutine, so nothing lands afterwards.
	for len(f.patched) > 0 {
		<-f.patched
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(f.patched); n != 0 {
		t.Fatalf("%d refreshes after stop", n)
	}
}

func TestKeepFilesRequestedStopsWithItsContext(t *testing.T) {
	f := newRefreshFixture(t, true, nil)
	req := filesRefreshRequest(t, "/servers/alpha/files/download?path=/a")
	ctx, cancel := context.WithCancel(t.Context())
	stop := f.proxy.keepFilesRequested(ctx, req, 5*time.Millisecond, advancingClock())
	waitPatch(t, f)
	cancel()
	stop()
}

func TestKeepFilesRequestedLeavesRunningServerAlone(t *testing.T) {
	f := newRefreshFixture(t, false, nil)
	req := filesRefreshRequest(t, "/servers/alpha/files/download?path=/a")
	stop := f.proxy.keepFilesRequested(t.Context(), req, 2*time.Millisecond, advancingClock())
	time.Sleep(60 * time.Millisecond)
	stop()
	if n := len(f.patched); n != 0 {
		t.Fatalf("running server patched %d times", n)
	}
}

func TestKeepFilesRequestedNoopWithoutClusterOrNamespace(t *testing.T) {
	req := filesRefreshRequest(t, "/servers/alpha/files/download?path=/a")
	(&proxy{}).keepFilesRequested(t.Context(), req, time.Millisecond, time.Now)()

	f := newRefreshFixture(t, true, nil)
	bad := filesRefreshRequest(t, "/servers/alpha/files/download?namespace=not-an-allowed-namespace")
	f.proxy.keepFilesRequested(t.Context(), bad, time.Millisecond, time.Now)()
	if n := len(f.patched); n != 0 {
		t.Fatalf("refresher ran for a forbidden namespace (%d patches)", n)
	}
}

func TestRefreshFilesStampFailuresAreSwallowed(t *testing.T) {
	// A failing patch is logged, not returned.
	f := newRefreshFixture(t, true, errors.New("patch refused"))
	f.proxy.refreshFilesStamp(t.Context(), scope.DefaultNamespace, "alpha", filesTestStart)
	waitPatch(t, f)

	// A server that is gone, and one replaced under the same name, are both left alone.
	f.proxy.refreshFilesStamp(t.Context(), scope.DefaultNamespace, "missing", filesTestStart)
	f.proxy.remoteUID = "another-uid"
	f.proxy.refreshFilesStamp(t.Context(), scope.DefaultNamespace, "alpha", filesTestStart)
	if n := len(f.patched); n != 0 {
		t.Fatalf("%d unexpected patches", n)
	}

	// A cancelled context hides the error log but still returns quietly.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.proxy.remoteUID = filesTestUID
	f.proxy.refreshFilesStamp(ctx, scope.DefaultNamespace, "alpha", filesTestStart)
}

func TestFilesRouteServesStoppedServerAndStopsRefresher(t *testing.T) {
	f := newRefreshFixture(t, true, nil)
	router := chi.NewRouter()
	router.Get("/servers/{name}/files/download", f.proxy.filesRoute("/files/download", defaultBodyLimit))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/files/download?path=/a", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// The request stamped the server once on entry.
	waitPatch(t, f)
}

func TestMountWithGatewayNamespaceOption(t *testing.T) {
	reg := kube.NewRegistry(scope.DefaultCluster)
	r := chi.NewRouter()
	Mount(r, reg, "", "", "", AgentGatewayOptions{Namespace: "gameplane-system", MaxUploadBytes: 1 << 20})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/files/stat?path=/", nil))
	// Without mTLS material or a cluster client the route answers 503 instead of panicking.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
