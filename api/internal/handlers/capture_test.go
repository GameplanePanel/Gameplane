package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// These are unit tests for the enable/disable handlers only (T050); they
// run against a fake dynamic client, never envtest — the start/stop/list/
// get/download happy paths already have envtest coverage in
// capture_envtest_test.go.

// fakeCaptureClient builds a kube.Client wired to a fake dynamic client
// that knows about both GameServers and NetworkCaptures.
// fakeKubeClient (modules_fake_test.go) doesn't register the
// NetworkCapture GVR, and captureDisable's stop-active-captures path
// needs it.
func fakeCaptureClient(objs ...runtime.Object) *kube.Client {
	scm := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{
		kube.GVRs["servers"]:   "GameServerList",
		kube.GVRNetworkCapture: "NetworkCaptureList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scm, gvkr, objs...)
	return &kube.Client{Dynamic: dyn, Typed: kubefake.NewClientset()}
}

// newCaptureServerObj builds a GameServer fixture with spec.capture.enabled
// set as requested.
func newCaptureServerObj(name string, captureEnabled bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": scope.DefaultNamespace},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"capture":     map[string]any{"enabled": captureEnabled},
		},
	}}
}

// newCaptureNetworkCapture builds a NetworkCapture fixture in the given
// phase, referencing serverName.
func newCaptureNetworkCapture(name, serverName, phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "NetworkCapture",
		"metadata":   map[string]any{"name": name, "namespace": scope.DefaultNamespace},
		"spec":       map[string]any{"serverRef": map[string]any{"name": serverName}},
		"status":     map[string]any{"phase": phase},
	}}
}

// newCompletedNetworkCapture builds a Completed NetworkCapture fixture whose
// status.completionTime is completedAgo in the past and whose
// spec.ttlSecondsAfterFinished is ttlSeconds, so callers can position it on
// either side of its retention deadline. This exercises the wall-clock
// expiry check (captureHandler.isExpired/expiryDeadline) independently of
// the operator's own phase transition to Expired — the whole point of T070
// is that a capture past its deadline is refused even while its phase is
// still Completed, ahead of the GC reconciler catching up.
func newCompletedNetworkCapture(t *testing.T, name, serverName string, completedAgo time.Duration, ttlSeconds int64) *unstructured.Unstructured {
	t.Helper()
	nc := newCaptureNetworkCapture(name, serverName, "Completed")
	completionTime := time.Now().UTC().Add(-completedAgo).Format(time.RFC3339)
	if err := unstructured.SetNestedField(nc.Object, completionTime, "status", "completionTime"); err != nil {
		t.Fatalf("seed status.completionTime: %v", err)
	}
	if err := unstructured.SetNestedField(nc.Object, ttlSeconds, "spec", "ttlSecondsAfterFinished"); err != nil {
		t.Fatalf("seed spec.ttlSecondsAfterFinished: %v", err)
	}
	return nc
}

func newCaptureAuditor(t *testing.T) *audit.Auditor {
	t.Helper()
	return audit.New(newTestStore(t))
}

func mountCaptureTestRouter(k *kube.Client, cfg CaptureConfig, auditor *audit.Auditor) *chi.Mux {
	r := chi.NewRouter()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	MountCapture(r, reg, auditor, cfg, "", "", "")
	return r
}

func decodeCaptureToggle(t *testing.T, rr *httptest.ResponseRecorder) captureToggleResp {
	t.Helper()
	var resp captureToggleResp
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rr.Body)
	}
	return resp
}

// TestCaptureEnable_Success verifies the happy path: 200, the canonical
// status.capture body shape, and that spec.capture.enabled was actually
// patched to true.
func TestCaptureEnable_Success(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("alpha", false))
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/alpha:capture-enable", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want 200; body=%s", rr.Code, rr.Body)
	}

	resp := decodeCaptureToggle(t, rr)
	if resp.Name != "alpha" {
		t.Errorf("name = %q, want alpha", resp.Name)
	}
	if resp.Status.Capture.Ready {
		t.Error("ready should be false immediately after enable")
	}
	if resp.Status.Capture.ActiveCapture != nil {
		t.Errorf("activeCapture = %v, want nil", *resp.Status.Capture.ActiveCapture)
	}
	if resp.Status.Capture.LastCaptureTime != nil {
		t.Errorf("lastCaptureTime = %v, want nil", *resp.Status.Capture.LastCaptureTime)
	}
	if resp.Status.Capture.SidecarRestarts != 0 {
		t.Errorf("sidecarRestarts = %d, want 0", resp.Status.Capture.SidecarRestarts)
	}

	obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace(scope.DefaultNamespace).Get(t.Context(), "alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	enabled, _, _ := unstructured.NestedBool(obj.Object, "spec", "capture", "enabled")
	if !enabled {
		t.Error("spec.capture.enabled should be true after enable")
	}
}

// TestCaptureEnable_AlreadyEnabledIsIdempotent verifies that re-enabling
// an already-enabled server is not an error — the contract's precondition
// list for enable carries no "already enabled" condition, unlike disable.
func TestCaptureEnable_AlreadyEnabledIsIdempotent(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("idem", true))
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/idem:capture-enable", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-enable status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
}

// TestCaptureEnable_Terminating verifies a GameServer with a
// deletionTimestamp set 409s rather than being patched.
func TestCaptureEnable_Terminating(t *testing.T) {
	srv := newCaptureServerObj("dying", false)
	now := metav1.Now()
	srv.SetDeletionTimestamp(&now)
	k := fakeCaptureClient(srv)
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/dying:capture-enable", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("enable on terminating server status = %d, want 409; body=%s", rr.Code, rr.Body)
	}
}

// TestCaptureEnable_FeatureDisabled verifies the cluster-wide 501 gate.
// httperr.WriteCode never echoes a caller-composed message for a >=500
// status (see its doc comment), so the body here is the generic
// http.StatusText, not the handler's own message string — matching the
// same shape as the existing clusterOps.enabled 501 precedent
// (cluster_actions_test.go's TestClusterActions_DisabledReturns501).
func TestCaptureEnable_FeatureDisabled(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("beta", false))
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: false}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/beta:capture-enable", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("enable with feature disabled status = %d, want 501; body=%s", rr.Code, rr.Body)
	}
}

// TestCaptureEnable_NotFound verifies capture-enable on a nonexistent
// server 404s.
func TestCaptureEnable_NotFound(t *testing.T) {
	k := fakeCaptureClient()
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/ghost:capture-enable", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("enable on missing server status = %d, want 404; body=%s", rr.Code, rr.Body)
	}
}

// TestCaptureDisable_Success verifies the happy path: 200, ready forced
// false, activeCapture forced null (even though status.capture was
// pre-seeded with stale Running-looking values), spec.capture.enabled
// patched to false, and the still-Running NetworkCapture stopped.
func TestCaptureDisable_Success(t *testing.T) {
	srv := newCaptureServerObj("gamma", true)
	if err := unstructured.SetNestedField(srv.Object, true, "status", "capture", "ready"); err != nil {
		t.Fatalf("seed status.capture.ready: %v", err)
	}
	if err := unstructured.SetNestedField(srv.Object, "cap-stale", "status", "capture", "activeCapture"); err != nil {
		t.Fatalf("seed status.capture.activeCapture: %v", err)
	}
	nc := newCaptureNetworkCapture("cap-stale", "gamma", "Running")

	k := fakeCaptureClient(srv, nc)
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/gamma:capture-disable", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200; body=%s", rr.Code, rr.Body)
	}

	resp := decodeCaptureToggle(t, rr)
	if resp.Status.Capture.Ready {
		t.Error("ready should be forced false on disable")
	}
	if resp.Status.Capture.ActiveCapture != nil {
		t.Errorf("activeCapture = %v, want nil (disable forces it, even though status.capture was pre-seeded)", *resp.Status.Capture.ActiveCapture)
	}

	obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace(scope.DefaultNamespace).Get(t.Context(), "gamma", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	enabled, _, _ := unstructured.NestedBool(obj.Object, "spec", "capture", "enabled")
	if enabled {
		t.Error("spec.capture.enabled should be false after disable")
	}

	ncAfter, err := k.Dynamic.Resource(kube.GVRNetworkCapture).
		Namespace(scope.DefaultNamespace).Get(t.Context(), "cap-stale", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get network capture: %v", err)
	}
	// F-259 (maintainer decision 2026-09-25): disable only requests the
	// stop; the operator completes the capture once its sidecar stops.
	phase, _, _ := unstructured.NestedString(ncAfter.Object, "status", "phase")
	if phase != "Running" {
		t.Errorf("active capture phase = %q, want Running (the API no longer writes Completed)", phase)
	}
	if _, ok := ncAfter.GetAnnotations()[kube.CaptureStopRequestedAnnotation]; !ok {
		t.Errorf("active capture annotations = %v, want %s set after disable", ncAfter.GetAnnotations(), kube.CaptureStopRequestedAnnotation)
	}
}

// TestCaptureDisable_AlreadyDisabled verifies disabling a server that
// never had capture enabled 409s (unlike enable, disable's precondition
// list does have an "already disabled" condition).
func TestCaptureDisable_AlreadyDisabled(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("delta", false))
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/delta:capture-disable", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("disable already-disabled status = %d, want 409; body=%s", rr.Code, rr.Body)
	}
}

// TestCaptureDisable_NotFound verifies capture-disable on a nonexistent
// server 404s.
func TestCaptureDisable_NotFound(t *testing.T) {
	k := fakeCaptureClient()
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/ghost:capture-disable", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("disable on missing server status = %d, want 404; body=%s", rr.Code, rr.Body)
	}
}

// TestCaptureEnableDisable_RBAC_OperatorForbidden proves the RBAC
// ordering doesn't regress: an OPERATOR-role token holds servers:write
// but must NOT be able to reach capture-enable/disable, which require
// captures:manage (rbac.go's rule table, ordered before the servers:write
// catch-all). This is the specific token that would have caught the
// original hole — not merely "some non-admin token" (rbac_test.go's own
// comment on the equivalent rule-table test makes the same point).
func TestCaptureEnableDisable_RBAC_OperatorForbidden(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("epsilon", false))
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)

	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountCapture(r, reg, newCaptureAuditor(t), CaptureConfig{FeatureEnabled: true}, "", "", "")

	operator := &auth.User{
		ID:       7,
		Username: "op",
		Role:     rbac.RoleOperator,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {"servers:write": {}}},
		},
	}

	for _, path := range []string{"/servers/epsilon:capture-enable", "/servers/epsilon:capture-disable"} {
		req := httptest.NewRequestWithContext(auth.WithUser(t.Context(), operator), http.MethodPost, path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("operator %s status = %d, want 403 (servers:write must not substitute for captures:manage); body=%s", path, rr.Code, rr.Body)
		}
	}
}

// captureTestCfg is the cluster-wide capture config shared by the T069/T070
// tests below: default retention 24h, cluster max 7 days.
var captureTestCfg = CaptureConfig{
	FeatureEnabled:          true,
	DefaultRetentionSeconds: 86400,
	MaxRetentionSeconds:     604800,
}

func decodeCaptureStart(t *testing.T, rr *httptest.ResponseRecorder) captureStartResp {
	t.Helper()
	var resp captureStartResp
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rr.Body)
	}
	return resp
}

// TestCaptureStart_TTLOmittedDefaultsTo24h (T069) verifies an omitted
// ttlSecondsAfterFinished defaults to the cluster's DefaultRetentionSeconds
// (86400s / 24h, FR-007) when the GameServer carries no retentionSeconds
// override.
func TestCaptureStart_TTLOmittedDefaultsTo24h(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("ttl-default", true))
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	body := captureStartReq{MaxDurationSeconds: 60, MaxSizeBytes: 1024}
	rr := do(t, r, http.MethodPost, "/servers/ttl-default:capture-start", body)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want 202; body=%s", rr.Code, rr.Body)
	}

	resp := decodeCaptureStart(t, rr)
	if resp.TTLSecondsAfterFinish != 86400 {
		t.Errorf("ttlSecondsAfterFinished = %d, want 86400 (cluster default)", resp.TTLSecondsAfterFinish)
	}
}

// TestCaptureStart_TTLUnderMaxPasses (T069) verifies a client-supplied TTL
// under the cluster maximum is accepted and echoed back unchanged.
func TestCaptureStart_TTLUnderMaxPasses(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("ttl-under", true))
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	body := captureStartReq{MaxDurationSeconds: 60, MaxSizeBytes: 1024, TTLSecondsAfterFinish: 3600}
	rr := do(t, r, http.MethodPost, "/servers/ttl-under:capture-start", body)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want 202; body=%s", rr.Code, rr.Body)
	}

	resp := decodeCaptureStart(t, rr)
	if resp.TTLSecondsAfterFinish != 3600 {
		t.Errorf("ttlSecondsAfterFinished = %d, want 3600 (requested value, under cluster max)", resp.TTLSecondsAfterFinish)
	}
}

// TestCaptureStart_TTLOverMaxRejected (T069) verifies a client-supplied TTL
// exceeding the cluster maximum is rejected with 400 and the contract's
// documented message shape, per rest-api.md's "requested retention Xs
// exceeds cluster maximum Ys" — a hard rejection, not a silent clamp.
func TestCaptureStart_TTLOverMaxRejected(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("ttl-over", true))
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	body := captureStartReq{MaxDurationSeconds: 60, MaxSizeBytes: 1024, TTLSecondsAfterFinish: 700000}
	rr := do(t, r, http.MethodPost, "/servers/ttl-over:capture-start", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("start status = %d, want 400; body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "exceeds cluster maximum") {
		t.Errorf("body = %q, want it to mention exceeding the cluster maximum", rr.Body.String())
	}

	captures, err := k.ListNetworkCaptures(t.Context(), scope.DefaultNamespace, "ttl-over")
	if err != nil {
		t.Fatalf("list network captures: %v", err)
	}
	if len(captures) != 0 {
		t.Errorf("got %d NetworkCaptures created, want 0 (rejected before creation)", len(captures))
	}
}

// TestCaptureStart_TTLOmittedHonorsPerServerOverride (T069) verifies that
// when the request omits ttlSecondsAfterFinished, a GameServer with
// spec.capture.retentionSeconds set uses that value as the default instead
// of the cluster-wide DefaultRetentionSeconds — the override is still
// bounded by the cluster maximum but replaces the fallback, not the ceiling.
func TestCaptureStart_TTLOmittedHonorsPerServerOverride(t *testing.T) {
	srv := newCaptureServerObj("ttl-override", true)
	if err := unstructured.SetNestedField(srv.Object, int64(7200), "spec", "capture", "retentionSeconds"); err != nil {
		t.Fatalf("seed spec.capture.retentionSeconds: %v", err)
	}
	k := fakeCaptureClient(srv)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	body := captureStartReq{MaxDurationSeconds: 60, MaxSizeBytes: 1024}
	rr := do(t, r, http.MethodPost, "/servers/ttl-override:capture-start", body)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want 202; body=%s", rr.Code, rr.Body)
	}

	resp := decodeCaptureStart(t, rr)
	if resp.TTLSecondsAfterFinish != 7200 {
		t.Errorf("ttlSecondsAfterFinished = %d, want 7200 (GameServer's retentionSeconds override, not the 86400 cluster default)", resp.TTLSecondsAfterFinish)
	}
}

// TestCaptureList_OmitsExpiredAndPastTTL (T070) verifies List omits both an
// Expired-phase capture and a Completed capture that is already past its
// own retention deadline but hasn't been reconciled to Expired yet — the
// wall-clock check, not phase alone, is what makes the second one disappear
// immediately instead of only after the operator's GC pass.
func TestCaptureList_OmitsExpiredAndPastTTL(t *testing.T) {
	srv := newCaptureServerObj("list-expiry", true)
	expiredPhase := newCaptureNetworkCapture("cap-expired-phase", "list-expiry", "Expired")
	pastTTL := newCompletedNetworkCapture(t, "cap-past-ttl", "list-expiry", 2*time.Hour, 3600)
	reachable := newCompletedNetworkCapture(t, "cap-reachable", "list-expiry", time.Minute, 86400)

	k := fakeCaptureClient(srv, expiredPhase, pastTTL, reachable)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	rr := do(t, r, http.MethodGet, "/servers/list-expiry:captures", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rr.Code, rr.Body)
	}

	var resp captureListResp
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rr.Body)
	}
	if len(resp.Captures) != 1 || resp.Captures[0].CaptureID != "cap-reachable" {
		ids := make([]string, len(resp.Captures))
		for i, c := range resp.Captures {
			ids[i] = c.CaptureID
		}
		t.Errorf("captures = %v, want exactly [cap-reachable]", ids)
	}
}

// TestCaptureGet_ExpiredPhaseRefused (T070) verifies Get 404s a capture the
// operator has already transitioned to phase Expired.
func TestCaptureGet_ExpiredPhaseRefused(t *testing.T) {
	srv := newCaptureServerObj("get-expired-phase", true)
	nc := newCaptureNetworkCapture("cap-x", "get-expired-phase", "Expired")
	k := fakeCaptureClient(srv, nc)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	rr := do(t, r, http.MethodGet, "/servers/get-expired-phase:capture?id=cap-x", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get status = %d, want 404; body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "not found or has expired") {
		t.Errorf("body = %q, want the contract's not-found-or-expired message", rr.Body.String())
	}
}

// TestCaptureGet_PastTTLRefused (T070) verifies Get 404s a Completed
// capture whose retention deadline has already passed even though its
// phase has not yet been reconciled to Expired — the gap T070 exists to
// close: inaccessible immediately, not merely after the GC pass.
func TestCaptureGet_PastTTLRefused(t *testing.T) {
	srv := newCaptureServerObj("get-past-ttl", true)
	nc := newCompletedNetworkCapture(t, "cap-x", "get-past-ttl", 2*time.Hour, 3600)
	k := fakeCaptureClient(srv, nc)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	rr := do(t, r, http.MethodGet, "/servers/get-past-ttl:capture?id=cap-x", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get status = %d, want 404 (past retention deadline, still phase Completed); body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "not found or has expired") {
		t.Errorf("body = %q, want the contract's not-found-or-expired message", rr.Body.String())
	}
}

// TestCaptureGet_ReachableBeforeExpiry (T070) verifies a Completed capture
// still inside its retention window is returned normally.
func TestCaptureGet_ReachableBeforeExpiry(t *testing.T) {
	srv := newCaptureServerObj("get-reachable", true)
	nc := newCompletedNetworkCapture(t, "cap-x", "get-reachable", time.Minute, 86400)
	k := fakeCaptureClient(srv, nc)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	rr := do(t, r, http.MethodGet, "/servers/get-reachable:capture?id=cap-x", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", rr.Code, rr.Body)
	}

	var resp captureGetResp
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rr.Body)
	}
	if resp.CaptureID != "cap-x" {
		t.Errorf("captureId = %q, want cap-x", resp.CaptureID)
	}
}

// TestCaptureDownload_ExpiredPhaseRefused (T070) verifies Download 404s a
// capture the operator has already transitioned to phase Expired.
func TestCaptureDownload_ExpiredPhaseRefused(t *testing.T) {
	srv := newCaptureServerObj("dl-expired-phase", true)
	nc := newCaptureNetworkCapture("cap-x", "dl-expired-phase", "Expired")
	k := fakeCaptureClient(srv, nc)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	rr := do(t, r, http.MethodGet, "/servers/dl-expired-phase:capture-file?id=cap-x", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("download status = %d, want 404; body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "not found or has expired") {
		t.Errorf("body = %q, want the contract's not-found-or-expired message", rr.Body.String())
	}
}

// TestCaptureDownload_PastTTLRefused (T070) verifies Download 404s a
// Completed capture past its own retention deadline even before the
// operator's GC pass has reconciled it to phase Expired.
func TestCaptureDownload_PastTTLRefused(t *testing.T) {
	srv := newCaptureServerObj("dl-past-ttl", true)
	nc := newCompletedNetworkCapture(t, "cap-x", "dl-past-ttl", 2*time.Hour, 3600)
	k := fakeCaptureClient(srv, nc)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	rr := do(t, r, http.MethodGet, "/servers/dl-past-ttl:capture-file?id=cap-x", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("download status = %d, want 404 (past retention deadline, still phase Completed); body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "not found or has expired") {
		t.Errorf("body = %q, want the contract's not-found-or-expired message", rr.Body.String())
	}
}

// TestCaptureDownload_ReachableBeforeExpiryReachesProxy (T070) verifies a
// still-Completed, not-yet-expired capture clears the expiry gate: with no
// mTLS client configured in this fake-client unit test, the handler reaches
// its proxy step and reports 503 ("agent mTLS not configured") rather than
// 404 — proving the expiry/phase checks did not block it. The full
// sidecar-proxied download success path is covered by capture_envtest_test.go
// against a real sidecar.
func TestCaptureDownload_ReachableBeforeExpiryReachesProxy(t *testing.T) {
	srv := newCaptureServerObj("dl-reachable", true)
	nc := newCompletedNetworkCapture(t, "cap-x", "dl-reachable", time.Minute, 86400)
	k := fakeCaptureClient(srv, nc)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	rr := do(t, r, http.MethodGet, "/servers/dl-reachable:capture-file?id=cap-x", nil)
	if rr.Code == http.StatusNotFound {
		t.Fatalf("download status = %d, want != 404 (capture is not expired); body=%s", rr.Code, rr.Body)
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("download status = %d, want 503 (no mTLS client configured in this unit test); body=%s", rr.Code, rr.Body)
	}
}

// TestCaptureStart_InvalidBPFFilterRejected (T075) verifies a start request
// with a syntactically invalid BPF filter is rejected 400 before any
// NetworkCapture CR is created. The validateFilter function rejects filters
// containing characters outside the allowed set (alphanumerics, spaces, and
// a small punctuation set), so we test with a disallowed character like @.
func TestCaptureStart_InvalidBPFFilterRejected(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("invalid-filter", true))
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	body := captureStartReq{
		Filter:             "tcp port 80 @invalid",
		MaxDurationSeconds: 60,
		MaxSizeBytes:       1024,
	}
	rr := do(t, r, http.MethodPost, "/servers/invalid-filter:capture-start", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("start with invalid filter status = %d, want 400; body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "invalid filter") {
		t.Errorf("body = %q, want it to mention invalid filter", rr.Body.String())
	}

	// Verify no NetworkCapture CR was created
	captures, err := k.ListNetworkCaptures(t.Context(), scope.DefaultNamespace, "invalid-filter")
	if err != nil {
		t.Fatalf("list network captures: %v", err)
	}
	if len(captures) != 0 {
		t.Errorf("got %d NetworkCaptures created, want 0 (rejected before creation)", len(captures))
	}
}

// TestCaptureStart_ConcurrentCaptureRejected (T075) verifies a start request
// against a GameServer whose status.capture.activeCapture is already set is
// rejected 409 immediately without a Kubernetes create call. This tests the
// API-tier fast-path concurrency check; the authoritative lock lives in the
// operator's NetworkCaptureReconciler.
func TestCaptureStart_ConcurrentCaptureRejected(t *testing.T) {
	srv := newCaptureServerObj("concurrent-test", true)
	// Seed the server with an active capture in status
	if err := unstructured.SetNestedField(srv.Object, "cap-existing", "status", "capture", "activeCapture"); err != nil {
		t.Fatalf("seed status.capture.activeCapture: %v", err)
	}
	// Also create the actual NetworkCapture CR that the status refers to
	nc := newCaptureNetworkCapture("cap-existing", "concurrent-test", "Running")

	k := fakeCaptureClient(srv, nc)
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	body := captureStartReq{
		MaxDurationSeconds: 60,
		MaxSizeBytes:       1024,
	}
	rr := do(t, r, http.MethodPost, "/servers/concurrent-test:capture-start", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("start with active capture status = %d, want 409; body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "already in progress") {
		t.Errorf("body = %q, want it to mention capture already in progress", rr.Body.String())
	}

	// Verify no new NetworkCapture CR was created (only the pre-existing one)
	captures, err := k.ListNetworkCaptures(t.Context(), scope.DefaultNamespace, "concurrent-test")
	if err != nil {
		t.Fatalf("list network captures: %v", err)
	}
	if len(captures) != 1 {
		t.Errorf("got %d NetworkCaptures, want 1 (the pre-existing one only)", len(captures))
	}
	if captures[0].Name != "cap-existing" {
		t.Errorf("capture name = %q, want cap-existing", captures[0].Name)
	}
}

// TestCaptureStart_EmptyFilterAccepted (FR-003) verifies that a capture
// request with no filter expression (omitted, not explicitly empty string)
// is accepted by the API handler. The default filter restricting to the
// server's advertised ports is synthesized by the operator when the
// NetworkCapture has a nil Filter. The API's job is to pass nil through
// without rejecting it — this documents that the happy path works.
func TestCaptureStart_EmptyFilterAccepted(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("empty-filter", true))
	r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

	body := captureStartReq{
		Filter:             "",
		MaxDurationSeconds: 60,
		MaxSizeBytes:       1024,
	}
	rr := do(t, r, http.MethodPost, "/servers/empty-filter:capture-start", body)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start with empty filter status = %d, want 202; body=%s", rr.Code, rr.Body)
	}

	// Verify a NetworkCapture was created (filter was not rejected)
	captures, err := k.ListNetworkCaptures(t.Context(), scope.DefaultNamespace, "empty-filter")
	if err != nil {
		t.Fatalf("list network captures: %v", err)
	}
	if len(captures) != 1 {
		t.Errorf("got %d NetworkCaptures created, want 1", len(captures))
	}
	if captures[0].Spec.Filter != nil {
		t.Errorf("capture filter = %q, want nil (empty filter passed through)", *captures[0].Spec.Filter)
	}
}

// TestCaptureStart_TTLTooSmallRejected verifies that a TTL in the range
// [1..59] is rejected with 400 and a message naming the minimum, per
// FR-007 and the CRD's Minimum=60 constraint on TTLSecondsAfterFinished.
// This catches both client-supplied values and per-server retentionSeconds
// overrides before they reach CRD admission, so the user gets actionable
// feedback instead of an opaque validation error.
func TestCaptureStart_TTLTooSmallRejected(t *testing.T) {
	tests := []struct {
		name           string
		ttl            int64
		serverOverride *int64
	}{
		{
			name: "explicit request at boundary minus 1",
			ttl:  59,
		},
		{
			name: "explicit request at boundary",
			ttl:  60,
		},
		{
			name:           "server override below minimum",
			ttl:            0,
			serverOverride: ptrInt64(30),
		},
		{
			name:           "server override at boundary minus 1",
			ttl:            0,
			serverOverride: ptrInt64(59),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := newCaptureServerObj("ttl-small", true)
			if test.serverOverride != nil {
				if err := unstructured.SetNestedField(srv.Object, *test.serverOverride, "spec", "capture", "retentionSeconds"); err != nil {
					t.Fatalf("seed spec.capture.retentionSeconds: %v", err)
				}
			}

			k := fakeCaptureClient(srv)
			r := mountCaptureTestRouter(k, captureTestCfg, newCaptureAuditor(t))

			body := captureStartReq{
				MaxDurationSeconds:    60,
				MaxSizeBytes:          1024,
				TTLSecondsAfterFinish: test.ttl,
			}
			rr := do(t, r, http.MethodPost, "/servers/ttl-small:capture-start", body)

			if test.ttl >= 60 && test.serverOverride == nil {
				if rr.Code != http.StatusAccepted {
					t.Errorf("TTL %d: expected 202, got %d; body=%s", test.ttl, rr.Code, rr.Body)
				}
			} else {
				if rr.Code != http.StatusBadRequest {
					t.Errorf("TTL %d: expected 400, got %d; body=%s", test.ttl, rr.Code, rr.Body)
				}
				if !strings.Contains(rr.Body.String(), "must be 60") {
					t.Errorf("body = %q, want it to mention TTL must be 60", rr.Body.String())
				}
			}

			// Verify no capture was created for the rejected case
			captures, err := k.ListNetworkCaptures(t.Context(), scope.DefaultNamespace, "ttl-small")
			if err != nil {
				t.Fatalf("list network captures: %v", err)
			}
			if test.ttl >= 60 && test.serverOverride == nil {
				if len(captures) != 1 {
					t.Errorf("expected 1 NetworkCapture created, got %d", len(captures))
				}
			} else {
				if len(captures) != 0 {
					t.Errorf("expected 0 NetworkCaptures (rejected before creation), got %d", len(captures))
				}
			}
		})
	}
}

func ptrInt64(i int64) *int64 {
	return &i
}
