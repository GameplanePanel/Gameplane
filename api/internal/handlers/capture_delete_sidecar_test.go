package handlers

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// roundTripFunc adapts a function to http.RoundTripper, letting tests stub
// the transport a *http.Client uses without a real listener.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestCaptureDelete_CallsSidecarToFreeVolumeBudget is the regression test for
// F-261: deleting a NetworkCapture through the API must also ask the sidecar
// to remove its backing file, not just delete the CR - otherwise the file
// keeps counting against the sidecar's volume budget (CAPTURE_VOLUME_BUDGET_BYTES,
// F-187) until the pod restarts, even though the dashboard shows the capture
// as gone.
func TestCaptureDelete_CallsSidecarToFreeVolumeBudget(t *testing.T) {
	srv := newCaptureServerObj("del-sidecar", true)
	nc := newCompletedNetworkCapture(t, "cap-del", "del-sidecar", time.Minute, 86400)
	k := fakeCaptureClient(srv, nc)

	var gotMethod, gotPath, gotHost string
	tlsClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		gotHost = req.URL.Host
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}

	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	h := &captureHandler{reg: reg, auditor: newCaptureAuditor(t), cfg: captureTestCfg, tlsClient: tlsClient}
	r := chi.NewRouter()
	r.Delete("/servers/{name}:capture", h.captureDelete)

	rr := do(t, r, http.MethodDelete, "/servers/del-sidecar:capture?id=cap-del", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", rr.Code, rr.Body)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("sidecar call method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/captures/cap-del" {
		t.Errorf("sidecar call path = %q, want /captures/cap-del", gotPath)
	}
	if !strings.Contains(gotHost, "del-sidecar") {
		t.Errorf("sidecar call host = %q, want it to name the server del-sidecar", gotHost)
	}

	// The CR itself must be gone regardless of the sidecar call's outcome -
	// the sidecar cleanup is a courtesy alongside the CR delete, not a
	// precondition for it.
	got, err := k.GetNetworkCapture(t.Context(), scope.DefaultNamespace, "cap-del")
	if err != nil {
		t.Fatalf("get network capture after delete: %v", err)
	}
	if got != nil {
		t.Fatal("NetworkCapture CR still exists after delete")
	}
}

// TestCaptureDelete_SidecarUnreachableStillDeletesCR verifies that a sidecar
// that cannot be reached (pod already gone, network error) does not block
// deleting the CR - the cleanup call is best-effort, and a delete that
// always failed on a torn-down pod would make normal cleanup impossible.
func TestCaptureDelete_SidecarUnreachableStillDeletesCR(t *testing.T) {
	srv := newCaptureServerObj("del-unreachable", true)
	nc := newCompletedNetworkCapture(t, "cap-del", "del-unreachable", time.Minute, 86400)
	k := fakeCaptureClient(srv, nc)

	tlsClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errSimulatedSidecarUnreachable
	})}

	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	h := &captureHandler{reg: reg, auditor: newCaptureAuditor(t), cfg: captureTestCfg, tlsClient: tlsClient}
	r := chi.NewRouter()
	r.Delete("/servers/{name}:capture", h.captureDelete)

	rr := do(t, r, http.MethodDelete, "/servers/del-unreachable:capture?id=cap-del", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 even though the sidecar is unreachable; body=%s", rr.Code, rr.Body)
	}

	got, err := k.GetNetworkCapture(t.Context(), scope.DefaultNamespace, "cap-del")
	if err != nil {
		t.Fatalf("get network capture after delete: %v", err)
	}
	if got != nil {
		t.Fatal("NetworkCapture CR still exists after delete")
	}
}

// errSimulatedSidecarUnreachable is a sentinel error the transport stub
// above returns to simulate an unreachable sidecar (e.g. the pod is already
// gone by the time the API tries to clean up its capture file).
var errSimulatedSidecarUnreachable = errors.New("simulated: sidecar unreachable")
