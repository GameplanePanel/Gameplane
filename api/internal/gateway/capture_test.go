package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func gatewayCaptureFixture(t *testing.T, h *handler, owner, phase string, age time.Duration) gatewayprotocol.CaptureTarget {
	t.Helper()
	nc := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "NetworkCapture", "metadata": map[string]any{"name": "cap-one", "namespace": "games", "uid": "capture-uid", "ownerReferences": []any{map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer", "name": "same-name", "uid": owner}}}, "spec": map[string]any{"serverRef": map[string]any{"name": "same-name"}, "ttlSecondsAfterFinished": int64(60)}, "status": map[string]any{"phase": phase, "completionTime": time.Now().Add(-age).UTC().Format(time.RFC3339)}}}
	if _, err := h.client.Dynamic.Resource(kube.GVRNetworkCapture).Namespace("games").Create(t.Context(), nc, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return gatewayprotocol.CaptureTarget{Target: gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"}, Capture: "cap-one", CaptureUID: "capture-uid"}
}

func TestGatewayCaptureAbsenceStillRequiresCurrentIdentities(t *testing.T) {
	h, cert := newFixture(t)
	target := gatewayCaptureFixture(t, h, "original-uid", "Completed", time.Second)
	var calls atomic.Int64
	front := durationProxy(t.Context(), t, h, cert, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusGone)
	}))
	for _, variant := range []string{"valid", "wrong server", "wrong capture", "missing capture"} {
		t.Run(variant, func(t *testing.T) {
			bound := target
			want := http.StatusNotFound
			switch variant {
			case "valid":
				want = http.StatusGone
			case "wrong server":
				bound.UID = "replacement-server"
			case "wrong capture":
				bound.CaptureUID = "replacement-capture"
			case "missing capture":
				bound.Capture = "missing"
			}
			path, err := gatewayprotocol.CapturePath(bound)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, front.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := front.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != want {
				t.Fatalf("status=%d want=%d", resp.StatusCode, want)
			}
		})
	}
	if calls.Load() != 1 {
		t.Fatalf("invalid targets reached sidecar: %d calls", calls.Load())
	}
}

func TestGatewayCaptureVerifiesOwnershipPhaseAndTTL(t *testing.T) {
	for _, tc := range []struct {
		name, owner, uid, phase, method string
		age                             time.Duration
		status                          int
	}{
		{"foreign owner", "replacement", "capture-uid", "Completed", http.MethodGet, time.Second, 404},
		{"wrong capture UID", "original-uid", "replacement", "Completed", http.MethodDelete, time.Second, 404},
		{"expired", "original-uid", "capture-uid", "Completed", http.MethodGet, 2 * time.Minute, 404},
		{"running", "original-uid", "capture-uid", "Running", http.MethodDelete, time.Second, 409},
		{"unreconciled", "original-uid", "capture-uid", "", http.MethodDelete, time.Second, 409},
		{"unknown phase", "original-uid", "capture-uid", "Unknown", http.MethodDelete, time.Second, 409},
		{"failed", "original-uid", "capture-uid", "Failed", http.MethodGet, time.Second, 404},
		{"write forbidden", "original-uid", "capture-uid", "Completed", http.MethodPost, time.Second, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cert := newFixture(t)
			target := gatewayCaptureFixture(t, h, tc.owner, tc.phase, tc.age)
			target.CaptureUID = tc.uid
			path, err := gatewayprotocol.CapturePath(target)
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, request(t, cert, tc.method, path))
			if rr.Code != tc.status {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
		})
	}
}

func TestGatewayCaptureStreamsOnlyAllowlistedBoundOperation(t *testing.T) {
	h, cert := newFixture(t)
	target := gatewayCaptureFixture(t, h, "original-uid", "Completed", time.Second)
	calls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/targets/original-uid/captures/cap-one/uids/capture-uid/file" || r.Host != "same-name-agent.games.svc.cluster.local:9091" {
			t.Errorf("unexpected target %s %s", r.Host, r.URL.Path)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credentials leaked")
		}
		w.Header().Set("Set-Cookie", "secret=bad")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Header.Get("Range") != "bytes=1-2" {
			t.Error("lost Range")
		}
		w.Header().Set("Content-Range", "bytes 1-2/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("12"))
	}))
	defer upstream.Close()
	h.transport = func(TLSFiles) (*http.Transport, error) {
		tr := upstream.Client().Transport.(*http.Transport).Clone()
		tr.TLSClientConfig.ServerName = "127.0.0.1"
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
		return tr, nil
	}
	path, err := gatewayprotocol.CapturePath(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := request(t, cert, method, path)
		req.Header.Set("Range", "bytes=1-2")
		req.Header.Set("Cookie", "secret")
		req.Header.Set("Authorization", "secret")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		want := http.StatusPartialContent
		if method == http.MethodDelete {
			want = http.StatusNoContent
		}
		if rr.Code != want || rr.Header().Get("Set-Cookie") != "" {
			t.Fatalf("status=%d headers=%v body=%s", rr.Code, rr.Header(), rr.Body)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, request(t, cert, http.MethodGet, path+"?url=https://elsewhere"))
	if rr.Code != 404 || calls != 2 {
		t.Fatal("query reached sidecar")
	}
}
