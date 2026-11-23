package ws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// scriptedFilesTransport answers agent requests with the scripted status codes, then with
// fallback (200 when zero). It records every agent path it was asked for.
type scriptedFilesTransport struct {
	statuses []int
	fallback int
	paths    []string
}

func (s *scriptedFilesTransport) Do(_ context.Context, op agentRequest) (*http.Response, error) {
	s.paths = append(s.paths, op.path)
	code := s.fallback
	if code == 0 {
		code = http.StatusOK
	}
	if len(s.statuses) > 0 {
		code, s.statuses = s.statuses[0], s.statuses[1:]
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (*scriptedFilesTransport) Dial(context.Context, agentTarget, string, string) (*websocket.Conn, *http.Response, error) {
	return nil, nil, errors.New("dial not used by file routes")
}

// fakeFilesClock is the injected clock: sleeping advances time without blocking.
type fakeFilesClock struct{ now time.Time }

func (c *fakeFilesClock) Now() time.Time        { return c.now }
func (c *fakeFilesClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

var filesTestStart = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

const filesTestUID = "uid-alpha"

func filesTestServer(annotations map[string]any, suspend bool) *unstructured.Unstructured {
	meta := map[string]any{"name": "alpha", "namespace": scope.DefaultNamespace, "uid": filesTestUID}
	if annotations != nil {
		meta["annotations"] = annotations
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   meta,
		"spec":       map[string]any{"suspend": suspend},
	}}
}

func gamePodNamed(name string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name + "-0", Namespace: scope.DefaultNamespace}}
}

type filesFixture struct {
	proxy   *proxy
	typed   *k8sfake.Clientset
	tr      *scriptedFilesTransport
	patches []string
}

// newFilesFixture wires a proxy to fake dynamic and typed clients. Every GameServer patch is
// recorded and then applied by the fake tracker, as the API server would.
func newFilesFixture(t *testing.T, gs *unstructured.Unstructured, tr *scriptedFilesTransport, pods ...*corev1.Pod) *filesFixture {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kube.GVRs["servers"]: "GameServerList"}, gs)
	objects := make([]runtime.Object, 0, len(pods))
	for _, p := range pods {
		objects = append(objects, p)
	}
	f := &filesFixture{typed: k8sfake.NewSimpleClientset(objects...), tr: tr}
	dyn.PrependReactor("patch", "gameservers", func(action k8stesting.Action) (bool, runtime.Object, error) {
		f.patches = append(f.patches, string(action.(k8stesting.PatchAction).GetPatch()))
		return false, nil, nil
	})
	f.proxy = &proxy{
		k:         &kube.Client{Dynamic: dyn, Typed: f.typed},
		transport: tr,
		remoteUID: filesTestUID,
	}
	return f
}

func TestEnsureFilesRunningServerIsNotTouched(t *testing.T) {
	f := newFilesFixture(t, filesTestServer(nil, false), &scriptedFilesTransport{})
	clock := &fakeFilesClock{now: filesTestStart}
	if err := f.proxy.ensureFilesWith(t.Context(), scope.DefaultNamespace, "alpha", clock.Now, clock.Sleep); err != nil {
		t.Fatalf("running server: %v", err)
	}
	if len(f.patches) != 0 {
		t.Fatalf("running server was patched: %v", f.patches)
	}
	if len(f.tr.paths) != 0 {
		t.Fatalf("running server reached the agent: %v", f.tr.paths)
	}
}

func TestEnsureFilesSuspendedServerRequestsOnePod(t *testing.T) {
	f := newFilesFixture(t, filesTestServer(nil, true), &scriptedFilesTransport{})
	clock := &fakeFilesClock{now: filesTestStart}
	if err := f.proxy.ensureFilesWith(t.Context(), scope.DefaultNamespace, "alpha", clock.Now, clock.Sleep); err != nil {
		t.Fatalf("suspended server: %v", err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(f.patches))
	}
	var body struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(f.patches[0]), &body); err != nil {
		t.Fatalf("patch is not JSON: %v", err)
	}
	// Only the files stamp is written: no wake annotation and no spec change.
	if len(body.Metadata.Annotations) != 1 {
		t.Fatalf("patch annotations = %v, want only files-requested-at", body.Metadata.Annotations)
	}
	if got := body.Metadata.Annotations[filesRequestedAtAnnotation]; got != filesTestStart.Format(time.RFC3339) {
		t.Fatalf("files-requested-at = %q, want %q", got, filesTestStart.Format(time.RFC3339))
	}
	if strings.Contains(f.patches[0], "spec") || strings.Contains(f.patches[0], "idle-wake") {
		t.Fatalf("patch touches spec or a wake annotation: %s", f.patches[0])
	}
}

func TestEnsureFilesFreshStampSkipsPatch(t *testing.T) {
	stamp := filesTestStart.Add(-30 * time.Second).Format(time.RFC3339)
	tr := &scriptedFilesTransport{}
	f := newFilesFixture(t, filesTestServer(map[string]any{filesRequestedAtAnnotation: stamp}, true), tr)
	clock := &fakeFilesClock{now: filesTestStart}
	if err := f.proxy.ensureFilesWith(t.Context(), scope.DefaultNamespace, "alpha", clock.Now, clock.Sleep); err != nil {
		t.Fatalf("fresh stamp: %v", err)
	}
	if len(f.patches) != 0 {
		t.Fatalf("fresh stamp patched %d times", len(f.patches))
	}
	if len(tr.paths) != 1 {
		t.Fatalf("agent calls = %d, want 1 readiness check", len(tr.paths))
	}
}

func TestEnsureFilesStaleStampPatches(t *testing.T) {
	stamp := filesTestStart.Add(-90 * time.Second).Format(time.RFC3339)
	f := newFilesFixture(t, filesTestServer(map[string]any{filesRequestedAtAnnotation: stamp}, true), &scriptedFilesTransport{})
	clock := &fakeFilesClock{now: filesTestStart}
	if err := f.proxy.ensureFilesWith(t.Context(), scope.DefaultNamespace, "alpha", clock.Now, clock.Sleep); err != nil {
		t.Fatalf("stale stamp: %v", err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("stale stamp patches = %d, want 1", len(f.patches))
	}
}

func TestEnsureFilesAsleepCountsAsStopped(t *testing.T) {
	asleep := map[string]any{filesIdleAsleepAnnotation: filesTestStart.Add(-time.Hour).Format(time.RFC3339)}
	f := newFilesFixture(t, filesTestServer(asleep, false), &scriptedFilesTransport{})
	clock := &fakeFilesClock{now: filesTestStart}
	if err := f.proxy.ensureFilesWith(t.Context(), scope.DefaultNamespace, "alpha", clock.Now, clock.Sleep); err != nil {
		t.Fatalf("asleep server: %v", err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("asleep server patches = %d, want 1", len(f.patches))
	}
	if strings.Contains(f.patches[0], "idle-wake") || strings.Contains(f.patches[0], "suspend") {
		t.Fatalf("asleep server wake or spec written: %s", f.patches[0])
	}
}

func TestEnsureFilesWaitsForGamePodThenAgent(t *testing.T) {
	// The game pod is still there at the first check, the agent is not ready twice, then it is.
	tr := &scriptedFilesTransport{statuses: []int{http.StatusBadGateway, http.StatusBadGateway, http.StatusOK}}
	f := newFilesFixture(t, filesTestServer(nil, true), tr, gamePodNamed("alpha"))
	clock := &fakeFilesClock{now: filesTestStart}
	deleted := false
	sleep := func(d time.Duration) {
		clock.Sleep(d)
		if !deleted {
			deleted = true
			_ = f.typed.CoreV1().Pods(scope.DefaultNamespace).Delete(t.Context(), "alpha-0", metav1.DeleteOptions{})
		}
	}
	if err := f.proxy.ensureFilesWith(t.Context(), scope.DefaultNamespace, "alpha", clock.Now, sleep); err != nil {
		t.Fatalf("waiting server: %v", err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(f.patches))
	}
	if len(tr.paths) != 3 {
		t.Fatalf("agent calls = %d, want 3 (pod gone is checked first)", len(tr.paths))
	}
	for _, p := range tr.paths {
		if p != "/files/stat" {
			t.Fatalf("readiness path = %q, want /files/stat", p)
		}
	}
}

func TestEnsureFilesStartingAfterDeadline(t *testing.T) {
	tr := &scriptedFilesTransport{fallback: http.StatusBadGateway}
	f := newFilesFixture(t, filesTestServer(nil, true), tr)
	clock := &fakeFilesClock{now: filesTestStart}
	err := f.proxy.ensureFilesWith(t.Context(), scope.DefaultNamespace, "alpha", clock.Now, clock.Sleep)
	if !errors.Is(err, errFilesStarting) {
		t.Fatalf("err = %v, want errFilesStarting", err)
	}
	if elapsed := clock.now.Sub(filesTestStart); elapsed < filesReadyTimeout {
		t.Fatalf("gave up after %v, before %v", elapsed, filesReadyTimeout)
	}
}

func TestEnsureFilesWrites503WithStartingID(t *testing.T) {
	tr := &scriptedFilesTransport{fallback: http.StatusBadGateway}
	f := newFilesFixture(t, filesTestServer(nil, true), tr)
	clock := &fakeFilesClock{now: filesTestStart}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/files/stat", nil)
	req.Header.Set("Accept", "application/problem+json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "alpha")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	if f.proxy.ensureFilesClocked(rec, req, clock.Now, clock.Sleep) {
		t.Fatal("ensureFiles let a request through while files were starting")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "server.files.starting") || !strings.Contains(body, "files are starting") {
		t.Fatalf("503 body lost its message id or text: %s", body)
	}
}

func TestFilesRequestFreshBoundaries(t *testing.T) {
	now := filesTestStart
	cases := []struct {
		name  string
		stamp string
		want  bool
	}{
		{"missing", "", false},
		{"unparsable", "yesterday", false},
		{"just written", now.Format(time.RFC3339), true},
		{"just under an interval", now.Add(-filesRequestInterval + time.Second).Format(time.RFC3339), true},
		{"exactly an interval", now.Add(-filesRequestInterval).Format(time.RFC3339), false},
		{"far future", now.Add(time.Hour).Format(time.RFC3339), false},
	}
	for _, tc := range cases {
		if got := filesRequestFresh(tc.stamp, now); got != tc.want {
			t.Errorf("%s: fresh = %v, want %v", tc.name, got, tc.want)
		}
	}
}
