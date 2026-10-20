package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// These tests pin the F-259 stop contract (maintainer decision 2026-09-25,
// api/specs.md "Network capture stop flow"): a user stop only sets the
// gameplane.local/stop-requested annotation, never status, and a repeated
// stop is a no-op that keeps the first request time. The operator is the
// one that stops the sidecar and then sets phase=Completed.

func getCaptureObj(t *testing.T, k *kube.Client, name string) *unstructured.Unstructured {
	t.Helper()
	u, err := k.Dynamic.Resource(kube.GVRNetworkCapture).
		Namespace(scope.DefaultNamespace).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get network capture %s: %v", name, err)
	}
	return u
}

func TestStopNetworkCapture_SetsAnnotationOnly(t *testing.T) {
	k := fakeCaptureClient(newCaptureNetworkCapture("cap-run", "srv", "Running"))

	before := time.Now().UTC().Add(-time.Second)
	nc, err := k.StopNetworkCapture(t.Context(), scope.DefaultNamespace, "cap-run")
	if err != nil {
		t.Fatalf("StopNetworkCapture: %v", err)
	}
	if nc.Status.Phase != kube.CapturePhaseRunning {
		t.Errorf("returned phase = %q, want Running (status untouched)", nc.Status.Phase)
	}
	if !nc.StopRequested() {
		t.Error("returned capture should report StopRequested")
	}

	u := getCaptureObj(t, k, "cap-run")
	val, ok := u.GetAnnotations()[kube.CaptureStopRequestedAnnotation]
	if !ok {
		t.Fatalf("annotations = %v, want %s", u.GetAnnotations(), kube.CaptureStopRequestedAnnotation)
	}
	ts, err := time.Parse(time.RFC3339, val)
	if err != nil {
		t.Fatalf("annotation value %q is not RFC3339: %v", val, err)
	}
	if ts.Before(before.Truncate(time.Second)) {
		t.Errorf("annotation time %s predates the stop call (%s)", ts, before)
	}
	if phase, _, _ := unstructured.NestedString(u.Object, "status", "phase"); phase != "Running" {
		t.Errorf("stored phase = %q, want Running", phase)
	}
	if _, found, _ := unstructured.NestedString(u.Object, "status", "completionTime"); found {
		t.Error("status.completionTime must not be written by the API")
	}
	if _, found, _ := unstructured.NestedString(u.Object, "status", "message"); found {
		t.Error("status.message must not be written by the API")
	}
}

func TestStopNetworkCapture_Idempotent(t *testing.T) {
	const first = "2026-01-02T03:04:05Z"
	nc := newCaptureNetworkCapture("cap-again", "srv", "Running")
	nc.SetAnnotations(map[string]string{kube.CaptureStopRequestedAnnotation: first})
	k := fakeCaptureClient(nc)

	got, err := k.StopNetworkCapture(t.Context(), scope.DefaultNamespace, "cap-again")
	if err != nil {
		t.Fatalf("StopNetworkCapture: %v", err)
	}
	if v := got.Annotations[kube.CaptureStopRequestedAnnotation]; v != first {
		t.Errorf("returned annotation = %q, want the original %q", v, first)
	}
	if v := getCaptureObj(t, k, "cap-again").GetAnnotations()[kube.CaptureStopRequestedAnnotation]; v != first {
		t.Errorf("stored annotation = %q, want the original %q", v, first)
	}
}

func TestStopNetworkCapture_NotFound(t *testing.T) {
	k := fakeCaptureClient()
	if _, err := k.StopNetworkCapture(t.Context(), scope.DefaultNamespace, "ghost"); err == nil {
		t.Fatal("StopNetworkCapture on a missing capture should error")
	}
}

// TestCaptureStop_RepeatIsNoOp drives the HTTP handler: the first stop
// sets the annotation and reports the unchanged (non-Completed) phase; a
// second stop before the operator has completed the capture is a 200
// no-op that keeps the original request time.
func TestCaptureStop_RepeatIsNoOp(t *testing.T) {
	k := fakeCaptureClient(
		newCaptureServerObj("stopper", true),
		newCaptureNetworkCapture("cap-stop", "stopper", "Running"),
	)
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))

	rr := do(t, r, http.MethodPost, "/servers/stopper:capture-stop", map[string]any{"captureId": "cap-stop"})
	if rr.Code != http.StatusOK {
		t.Fatalf("first stop status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var resp captureStopResp
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode stop response: %v", err)
	}
	if resp.Phase != "Running" {
		t.Errorf("stop response phase = %q, want Running (the operator completes it)", resp.Phase)
	}
	if resp.StoppingReason != "user_requested" {
		t.Errorf("stop response stoppingReason = %q, want user_requested", resp.StoppingReason)
	}
	first := getCaptureObj(t, k, "cap-stop").GetAnnotations()[kube.CaptureStopRequestedAnnotation]
	if first == "" {
		t.Fatal("first stop did not set the stop-requested annotation")
	}

	rr = do(t, r, http.MethodPost, "/servers/stopper:capture-stop", map[string]any{"captureId": "cap-stop"})
	if rr.Code != http.StatusOK {
		t.Fatalf("repeat stop status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	if again := getCaptureObj(t, k, "cap-stop").GetAnnotations()[kube.CaptureStopRequestedAnnotation]; again != first {
		t.Errorf("repeat stop changed the annotation: %q -> %q", first, again)
	}
}

func TestCaptureStop_UnreconciledRequestsOperatorStop(t *testing.T) {
	k := fakeCaptureClient(newCaptureServerObj("stopper", true), newCaptureNetworkCapture("cap-new", "stopper", ""))
	r := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, newCaptureAuditor(t))
	rr := do(t, r, http.MethodPost, "/servers/stopper:capture-stop", map[string]any{"captureId": "cap-new"})
	if rr.Code != http.StatusOK {
		t.Fatalf("stop=%d %s", rr.Code, rr.Body)
	}
	nc := getCaptureObj(t, k, "cap-new")
	if nc.GetAnnotations()[kube.CaptureStopRequestedAnnotation] == "" {
		t.Fatal("operator stop not requested")
	}
	if phase, _, _ := unstructured.NestedString(nc.Object, "status", "phase"); phase != "" {
		t.Fatalf("API changed phase to %q", phase)
	}
	if _, found, _ := unstructured.NestedString(nc.Object, "status", "completionTime"); found {
		t.Fatal("API wrote completion time")
	}
}
