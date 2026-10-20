//go:build envtest

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// This suite exercises capture.go against the same envtest router every
// other file in this package uses (suite_envtest_test.go's mountedR/
// apiSrv) — no auth/RBAC middleware is mounted here (see that file's own
// doc comment: those live in their own packages with their own unit
// tests), so these tests cover the handler <-> kube.Client wiring, request
// validation, and the SSRF/streaming/audit shape of the download path.
// captures:manage / operator-403 coverage belongs in api/internal/rbac's
// own tests, which assert against the rule table directly.

// TestCapture_StartStopDownload exercises the start -> stop -> list -> get
// -> download happy path on a single server. The download leg can only be
// verified up to the mTLS boundary in this tier: MountCapture is wired
// with no CA/cert/key material (see suite_envtest_test.go), so a
// Completed capture's download returns 503 "agent mTLS not configured"
// rather than a real file — there is no sidecar pod for envtest to talk
// to. That still proves the SSRF guard, the Completed-phase gate, and the
// pre-stream audit write all ran correctly before the mTLS check.
func TestCapture_StartStopDownload(t *testing.T) {
	serverName := uniqueResourceName("cap-test")
	createCaptureTestServer(t, serverName, true)

	var captureID string

	t.Run("start_capture", func(t *testing.T) {
		startReq := map[string]any{
			"filter":                  "tcp port 25565",
			"maxDurationSeconds":      300,
			"maxSizeBytes":            int64(10485760), // 10 MiB, well below 900 MiB limit
			"ttlSecondsAfterFinished": int64(86400),
		}
		resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-start", startReq)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("start status = %d, want 202; body=%s", resp.StatusCode, readBody(t, resp))
		}

		var captureResp captureStartResp
		if err := json.NewDecoder(resp.Body).Decode(&captureResp); err != nil {
			t.Fatalf("decode start response: %v", err)
		}
		if captureResp.CaptureID == "" {
			t.Errorf("start response missing captureId")
		}
		if captureResp.Phase != "Pending" {
			t.Errorf("start response phase = %s, want Pending", captureResp.Phase)
		}
		if captureResp.ServerName != serverName {
			t.Errorf("start response serverName = %s, want %s", captureResp.ServerName, serverName)
		}
		if captureResp.Filter != "tcp port 25565" {
			t.Errorf("start response filter = %s, want 'tcp port 25565'", captureResp.Filter)
		}
		if captureResp.TTLSecondsAfterFinish != 86400 {
			t.Errorf("start response ttlSecondsAfterFinished = %d, want 86400", captureResp.TTLSecondsAfterFinish)
		}
		captureID = captureResp.CaptureID
	})

	t.Run("concurrent_capture_rejected", func(t *testing.T) {
		startReq := map[string]any{
			"filter":             "tcp port 25565",
			"maxDurationSeconds": 300,
			"maxSizeBytes":       int64(10485760), // 10 MiB, well below 900 MiB limit
		}
		resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-start", startReq)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("second start status = %d, want 409; body=%s", resp.StatusCode, readBody(t, resp))
		}
	})

	t.Run("list_captures", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, "/servers/"+serverName+":captures", nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list status = %d, want 200; body=%s", resp.StatusCode, readBody(t, resp))
		}

		var listResp captureListResp
		if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
			t.Fatalf("decode list response: %v", err)
		}
		if listResp.Total == 0 || len(listResp.Captures) == 0 {
			t.Fatalf("list captures empty, want at least 1")
		}
		found := false
		for _, c := range listResp.Captures {
			if c.CaptureID == captureID {
				found = true
			}
		}
		if !found {
			t.Errorf("list response missing captureId %s", captureID)
		}
	})

	t.Run("get_capture_status", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, "/servers/"+serverName+":capture?id="+captureID, nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("get status = %d, want 200; body=%s", resp.StatusCode, readBody(t, resp))
		}

		var capture captureGetResp
		if err := json.NewDecoder(resp.Body).Decode(&capture); err != nil {
			t.Fatalf("decode get response: %v", err)
		}
		if capture.CaptureID != captureID {
			t.Errorf("get response id = %s, want %s", capture.CaptureID, captureID)
		}
		if capture.Phase != "Pending" {
			t.Errorf("get response phase = %s, want Pending", capture.Phase)
		}
	})

	t.Run("stop_capture", func(t *testing.T) {
		resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-stop", map[string]any{
			"captureId": captureID,
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stop status = %d, want 200; body=%s", resp.StatusCode, readBody(t, resp))
		}

		var stopResp captureStopResp
		if err := json.NewDecoder(resp.Body).Decode(&stopResp); err != nil {
			t.Fatalf("decode stop response: %v", err)
		}
		// F-259 (maintainer decision 2026-09-25): stop only requests the
		// stop via the stop-requested annotation; the operator, not the
		// API, sets phase=Completed once the sidecar has stopped.
		if stopResp.Phase == "Completed" {
			t.Errorf("stop response phase = %s, want the pre-stop phase (the operator completes the capture)", stopResp.Phase)
		}
		if stopResp.StoppingReason != "user_requested" {
			t.Errorf("stop response stoppingReason = %s, want user_requested", stopResp.StoppingReason)
		}

		ncRes := kubeC.Dynamic.Resource(kube.GVRNetworkCapture).Namespace(scope.DefaultNamespace)
		u, err := ncRes.Get(context.Background(), captureID, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get capture after stop: %v", err)
		}
		if _, ok := u.GetAnnotations()[kube.CaptureStopRequestedAnnotation]; !ok {
			t.Fatalf("capture annotations = %v, want %s set", u.GetAnnotations(), kube.CaptureStopRequestedAnnotation)
		}

		// No operator runs in this tier: emulate its reaction to the
		// annotation (sidecar stopped, then phase=Completed) so the
		// remaining subtests see the capture the way a real cluster
		// leaves it.
		now := time.Now().UTC().Format(time.RFC3339)
		if err := unstructured.SetNestedField(u.Object, "Completed", "status", "phase"); err != nil {
			t.Fatalf("set phase: %v", err)
		}
		if err := unstructured.SetNestedField(u.Object, now, "status", "completionTime"); err != nil {
			t.Fatalf("set completionTime: %v", err)
		}
		if _, err := ncRes.UpdateStatus(context.Background(), u, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("emulate operator completing the capture: %v", err)
		}
	})

	t.Run("stop_already_stopped_conflicts", func(t *testing.T) {
		resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-stop", map[string]any{
			"captureId": captureID,
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("re-stop status = %d, want 409; body=%s", resp.StatusCode, readBody(t, resp))
		}
	})

	t.Run("download_reaches_mtls_gate", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, "/servers/"+serverName+":capture-file?id="+captureID, nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("download status = %d, want 503; body=%s", resp.StatusCode, readBody(t, resp))
		}
		// httperr.WriteCode never echoes err.Error() for a >=500 status (it
		// writes the generic http.StatusText instead, to avoid leaking
		// upstream detail) — see httperr.go's doc comment. So the body here
		// is the generic text for 503, not the handler's internal message.
		if body := readBody(t, resp); body != "Service Unavailable" {
			t.Errorf("download body = %q, want %q", body, "Service Unavailable")
		}
	})
}

// TestCapture_InvalidFilter tests that filter expressions containing
// characters no pcap-filter(7) grammar uses are rejected at the handler
// tier before creating a NetworkCapture CR.
func TestCapture_InvalidFilter(t *testing.T) {
	serverName := uniqueResourceName("cap-filter")
	createCaptureTestServer(t, serverName, true)

	resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-start", map[string]any{
		"filter":             "invalid filter expression %%%",
		"maxDurationSeconds": 300,
		"maxSizeBytes":       int64(10485760), // 10 MiB, well below 900 MiB limit
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid filter status = %d, want 400", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !bytes.Contains([]byte(body), []byte("invalid filter")) {
		t.Errorf("error message doesn't mention invalid filter: %s", body)
	}
}

// TestCapture_TTLExceedsMax tests that TTL values exceeding the cluster
// max are rejected and clamped rather than trusted from the client.
func TestCapture_TTLExceedsMax(t *testing.T) {
	serverName := uniqueResourceName("cap-ttl")
	createCaptureTestServer(t, serverName, true)

	resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-start", map[string]any{
		"filter":                  "tcp port 25565",
		"maxDurationSeconds":      300,
		"maxSizeBytes":            int64(10485760), // 10 MiB, well below 900 MiB limit
		"ttlSecondsAfterFinished": int64(10000000), // way over the 604800 max
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ttl exceeded status = %d, want 400", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !bytes.Contains([]byte(body), []byte("exceeds cluster maximum")) {
		t.Errorf("error message doesn't mention cluster maximum: %s", body)
	}
}

// TestCapture_MaxSizeExceedsClusterMax tests that maxSizeBytes above the
// cluster's configured cap (943718400 / 900 MiB in this suite) is rejected.
func TestCapture_MaxSizeExceedsClusterMax(t *testing.T) {
	serverName := uniqueResourceName("cap-size")
	createCaptureTestServer(t, serverName, true)

	resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-start", map[string]any{
		"filter":             "tcp port 25565",
		"maxDurationSeconds": 300,
		"maxSizeBytes":       int64(9999999999999),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized status = %d, want 400; body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !bytes.Contains([]byte(body), []byte("maxSizeBytes")) {
		t.Errorf("error message doesn't mention maxSizeBytes: %s", body)
	}
}

// TestCapture_MissingCaptureEnabled tests that starting a capture on a
// server without capture enabled returns 400.
func TestCapture_MissingCaptureEnabled(t *testing.T) {
	serverName := uniqueResourceName("cap-disabled")
	createCaptureTestServer(t, serverName, false)

	resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-start", map[string]any{
		"filter":             "tcp port 25565",
		"maxDurationSeconds": 300,
		"maxSizeBytes":       int64(10485760), // 10 MiB, well below 900 MiB limit
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("start without capture enabled status = %d, want 400; body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !bytes.Contains([]byte(body), []byte("not enabled")) {
		t.Errorf("error message doesn't mention capture not enabled: %s", body)
	}
}

// TestCapture_ServerNotFound tests that capture-start on a server that
// does not exist 404s (and not a 500 from an unclassified kube error).
func TestCapture_ServerNotFound(t *testing.T) {
	resp := doJSON(t, http.MethodPost, "/servers/"+uniqueResourceName("cap-missing")+":capture-start", map[string]any{
		"filter":             "tcp port 25565",
		"maxDurationSeconds": 300,
		"maxSizeBytes":       int64(10485760), // 10 MiB, well below 900 MiB limit
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing server status = %d, want 404; body=%s", resp.StatusCode, readBody(t, resp))
	}
}

// TestCapture_StopMissingID tests that capture-stop without a captureId
// in the body 400s before any kube call.
func TestCapture_StopMissingID(t *testing.T) {
	serverName := uniqueResourceName("cap-stopnoid")
	createCaptureTestServer(t, serverName, true)

	resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-stop", map[string]any{})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("stop missing id status = %d, want 400; body=%s", resp.StatusCode, readBody(t, resp))
	}
}

// TestCapture_StopUnknownID tests that stopping a capture ID that doesn't
// exist for the server 404s with the capture-specific message.
func TestCapture_StopUnknownID(t *testing.T) {
	serverName := uniqueResourceName("cap-stopunknown")
	createCaptureTestServer(t, serverName, true)

	resp := doJSON(t, http.MethodPost, "/servers/"+serverName+":capture-stop", map[string]any{
		"captureId": "cap-doesnotexist",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("stop unknown id status = %d, want 404; body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !bytes.Contains([]byte(body), []byte("not found")) {
		t.Errorf("error message doesn't mention not found: %s", body)
	}
}

// ---------- helpers ----------

// createCaptureTestServer writes a GameServer fixture for capture tests,
// with spec.capture.enabled set as requested.
func createCaptureTestServer(t *testing.T, name string, captureEnabled bool) {
	t.Helper()
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": scope.DefaultNamespace},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
			"capture":     map[string]any{"enabled": captureEnabled},
		},
	}}
	if _, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Create(context.Background(), gs, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create server fixture: %v", err)
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}
