package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

type fakeCaptureGateway struct {
	caps                ws.CaptureGatewayCapabilities
	downloaded, deleted *gatewayprotocol.CaptureTarget
	deleteErr           error
	deleteStatus        int
}

func (f *fakeCaptureGateway) Capabilities(context.Context, string, string, string) (ws.CaptureGatewayCapabilities, error) {
	return f.caps, nil
}
func (f *fakeCaptureGateway) Download(_ context.Context, target gatewayprotocol.CaptureTarget, _ http.Header) (*http.Response, error) {
	f.downloaded = &target
	return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": {"bytes 0-3/4"}}, Body: io.NopCloser(strings.NewReader("pcap"))}, nil
}
func (f *fakeCaptureGateway) Delete(_ context.Context, target gatewayprotocol.CaptureTarget) (*http.Response, error) {
	f.deleted = &target
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	status := f.deleteStatus
	if status == 0 {
		status = 204
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestRemoteCaptureDownloadAndRetriableDeleteStayBound(t *testing.T) {
	for _, cleanupErr := range []error{nil, errors.New("gateway unavailable")} {
		t.Run("cleanup", func(t *testing.T) {
			server := newCaptureServerObj("alpha", true)
			server.SetUID("remote-server")
			nc := newCompletedNetworkCapture(t, "cap-one", "alpha", time.Second, 120)
			nc.SetUID("remote-capture")
			nc.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: "alpha", UID: server.GetUID()}})
			remote := fakeCaptureClient(server, nc)
			home := fakeCaptureClient(newCaptureServerObj("alpha", true))
			reg := kube.NewRegistry(scope.DefaultCluster)
			reg.Set(scope.DefaultCluster, home)
			reg.Set("remote", remote)
			gateway := &fakeCaptureGateway{deleteErr: cleanupErr}
			h := &captureHandler{reg: reg, cfg: captureTestCfg, auditor: newCaptureAuditor(t), gateway: gateway}
			r := chi.NewRouter()
			r.Get("/servers/{name}:capture-file", h.captureDownload)
			r.Delete("/servers/{name}:capture", h.captureDelete)
			rr := do(t, r, http.MethodGet, "/servers/alpha:capture-file?id=cap-one&cluster=remote", nil)
			if rr.Code != 206 || rr.Body.String() != "pcap" || gateway.downloaded == nil || gateway.downloaded.UID != "remote-server" || gateway.downloaded.CaptureUID != "remote-capture" {
				t.Fatalf("download=%d %s target=%+v", rr.Code, rr.Body, gateway.downloaded)
			}
			rr = do(t, r, http.MethodDelete, "/servers/alpha:capture?id=cap-one&cluster=remote", nil)
			want := 200
			if cleanupErr != nil {
				want = 503
			}
			if rr.Code != want || gateway.deleted == nil || gateway.deleted.CaptureUID != "remote-capture" {
				t.Fatalf("delete=%d %s target=%+v", rr.Code, rr.Body, gateway.deleted)
			}
			if calls := kubeClientCalls(t, home); calls != 0 {
				t.Fatalf("home client touched %d times", calls)
			}
			found := false
			for _, action := range remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
				if action.GetVerb() == "delete" {
					opts := action.(interface{ GetDeleteOptions() metav1.DeleteOptions }).GetDeleteOptions()
					if opts.Preconditions == nil || opts.Preconditions.UID == nil || *opts.Preconditions.UID != "remote-capture" {
						t.Fatal("delete lost UID precondition")
					}
					found = true
				}
			}
			if found != (cleanupErr == nil) {
				t.Fatalf("CR deletion=%v cleanupErr=%v", found, cleanupErr)
			}
		})
	}
}

func TestRemoteCaptureUsesSelectedFeatureAndLimits(t *testing.T) {
	caps := ws.CaptureGatewayCapabilities{Protocol: "v1", TargetUID: true, CaptureEnabled: true, CaptureFiles: true, DefaultRetentionSeconds: 120, MaxRetentionSeconds: 240, DefaultMaxDurationSecs: 15, DefaultMaxSizeBytes: 1024}
	for _, enabled := range []bool{true, false} {
		t.Run("feature", func(t *testing.T) {
			server := newCaptureServerObj("alpha", true)
			server.SetUID("remote-server")
			remote := fakeCaptureClient(server)
			reg := kube.NewRegistry(scope.DefaultCluster)
			reg.Set(scope.DefaultCluster, fakeCaptureClient())
			reg.Set("remote", remote)
			gateway := &fakeCaptureGateway{caps: caps}
			gateway.caps.CaptureEnabled = enabled
			cfg := captureTestCfg
			cfg.FeatureEnabled = !enabled
			h := &captureHandler{reg: reg, cfg: cfg, auditor: newCaptureAuditor(t), gateway: gateway}
			r := chi.NewRouter()
			r.Post("/servers/{name}:capture-enable", h.captureEnable)
			r.Post("/servers/{name}:capture-start", h.captureStart)
			for _, operation := range []string{"enable", "start"} {
				rr := do(t, r, http.MethodPost, "/servers/alpha:capture-"+operation+"?cluster=remote", map[string]any{})
				if !enabled {
					if rr.Code != 501 {
						t.Fatalf("disabled %s=%d %s", operation, rr.Code, rr.Body)
					}
					continue
				}
				if rr.Code < 200 || rr.Code >= 300 {
					t.Fatalf("enabled %s=%d %s", operation, rr.Code, rr.Body)
				}
			}
			if enabled {
				list, err := remote.ListNetworkCaptures(t.Context(), scope.DefaultNamespace, "alpha")
				if err != nil || len(list) != 1 {
					t.Fatalf("captures=%v err=%v", list, err)
				}
				nc := list[0]
				if nc.Spec.MaxDuration.Duration != 15*time.Second || nc.Spec.MaxSize.Value() != 1024 || *nc.Spec.TTLSecondsAfterFinished != 120 {
					t.Fatalf("wrong site defaults: %+v", nc.Spec)
				}
			}
		})
	}
}

func TestCaptureStreamPreservesRangeFailuresAndDetectsInterruptedBody(t *testing.T) {
	rr := httptest.NewRecorder()
	status := streamCaptureResponse(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), &http.Response{StatusCode: 416, Header: http.Header{"Content-Range": {"bytes */4"}, "Content-Disposition": {"secret"}}, Body: io.NopCloser(strings.NewReader("backend secret"))}, "cap-one")
	if status != 416 || rr.Header().Get("Content-Range") != "bytes */4" || rr.Header().Get("Content-Disposition") != "" || strings.Contains(rr.Body.String(), "backend secret") {
		t.Fatalf("range failure: %d %v %s", status, rr.Header(), rr.Body)
	}
	rr = httptest.NewRecorder()
	status = streamCaptureResponse(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(failingCaptureReader{})}, "cap-one")
	if status != 502 {
		t.Fatalf("interrupted stream status=%d", status)
	}
}

type failingCaptureReader struct{}

func (failingCaptureReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRemoteCleanupRequiresPositiveBoundAcknowledgement(t *testing.T) {
	for _, status := range []int{200, 204, 404, 409, 410, 502, 503} {
		h := &captureHandler{gateway: &fakeCaptureGateway{deleteStatus: status}}
		err := h.remoteCaptureCleanup(httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/", nil), gatewayprotocol.CaptureTarget{})
		if (err == nil) != (status == 204 || status == 410) {
			t.Errorf("status=%d err=%v", status, err)
		}
	}
}

func TestRemoteCaptureDeleteRequiresCleanupOrNeverStartedProof(t *testing.T) {
	for _, tc := range []struct {
		name, phase, conditionStatus, reason string
		observed                             int64
		pod, wrongOwner, noCompletion        bool
		cleanupStatus, want                  int
		wantGateway                          bool
	}{
		{name: "bound cleanup", phase: "Completed", cleanupStatus: 204, want: 200, wantGateway: true},
		{name: "confirmed absence", phase: "Completed", cleanupStatus: 410, want: 200, wantGateway: true},
		{name: "ambiguous missing", phase: "Completed", cleanupStatus: 404, want: 503, wantGateway: true},
		{name: "busy", phase: "Completed", cleanupStatus: 409, want: 503, wantGateway: true},
		{name: "bad gateway", phase: "Completed", cleanupStatus: 502, want: 503, wantGateway: true},
		{name: "outage", phase: "Completed", cleanupStatus: 503, want: 503, wantGateway: true},
		{name: "never started", phase: "Completed", conditionStatus: "True", reason: "never_started", observed: 2, want: 200},
		{name: "stale proof", phase: "Completed", conditionStatus: "True", reason: "never_started", observed: 1, want: 503, wantGateway: true},
		{name: "false proof", phase: "Completed", conditionStatus: "False", reason: "never_started", observed: 2, want: 503, wantGateway: true},
		{name: "different reason", phase: "Completed", conditionStatus: "True", reason: "stopped", observed: 2, want: 503, wantGateway: true},
		{name: "recorded pod", phase: "Completed", conditionStatus: "True", reason: "never_started", observed: 2, pod: true, want: 503, wantGateway: true},
		{name: "wrong owner", phase: "Completed", conditionStatus: "True", reason: "never_started", observed: 2, wrongOwner: true, want: 404},
		{name: "pending", phase: "Pending", conditionStatus: "True", reason: "never_started", observed: 2, want: 409},
		{name: "unreconciled", phase: "", cleanupStatus: 410, want: 409},
		{name: "unknown phase", phase: "Unknown", cleanupStatus: 410, want: 409},
		{name: "running", phase: "Running", want: 409},
		{name: "failed without completion", phase: "Failed", noCompletion: true, want: 503, wantGateway: true},
		{name: "failed with proof", phase: "Failed", conditionStatus: "True", reason: "never_started", observed: 2, want: 503, wantGateway: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newCaptureServerObj("alpha", true)
			server.SetUID("remote-server")
			nc := newCompletedNetworkCapture(t, "cap-one", "alpha", time.Second, 120)
			nc.SetUID("remote-capture")
			nc.SetGeneration(2)
			owner := server.GetUID()
			if tc.wrongOwner {
				owner = "replacement-server"
			}
			nc.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: "alpha", UID: owner}})
			if tc.pod {
				nc.SetAnnotations(map[string]string{"gameplane.local/capture-pod-uid": "recorded-pod"})
			}
			if err := unstructured.SetNestedField(nc.Object, tc.phase, "status", "phase"); err != nil {
				t.Fatal(err)
			}
			if tc.noCompletion {
				unstructured.RemoveNestedField(nc.Object, "status", "completionTime")
			}
			if tc.conditionStatus != "" {
				if err := unstructured.SetNestedSlice(nc.Object, []any{map[string]any{"type": "SidecarStopped", "status": tc.conditionStatus, "reason": tc.reason, "observedGeneration": tc.observed, "lastTransitionTime": time.Now().UTC().Format(time.RFC3339), "message": "operator result"}}, "status", "conditions"); err != nil {
					t.Fatal(err)
				}
			}
			remote := fakeCaptureClient(server, nc)
			reg := kube.NewRegistry(scope.DefaultCluster)
			reg.Set("remote", remote)
			gateway := &fakeCaptureGateway{deleteStatus: tc.cleanupStatus}
			if tc.cleanupStatus == 0 {
				gateway.deleteErr = errors.New("gateway unavailable")
			}
			h := &captureHandler{reg: reg, cfg: captureTestCfg, auditor: newCaptureAuditor(t), gateway: gateway}
			r := chi.NewRouter()
			r.Delete("/servers/{name}:capture", h.captureDelete)
			rr := do(t, r, http.MethodDelete, "/servers/alpha:capture?id=cap-one&cluster=remote", nil)
			if rr.Code != tc.want || (gateway.deleted != nil) != tc.wantGateway {
				t.Fatalf("delete=%d %s gateway=%+v; want=%d gateway=%v", rr.Code, rr.Body, gateway.deleted, tc.want, tc.wantGateway)
			}
			deleted := false
			for _, action := range remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
				if action.GetVerb() != "delete" {
					continue
				}
				opts := action.(interface{ GetDeleteOptions() metav1.DeleteOptions }).GetDeleteOptions()
				if opts.Preconditions == nil || opts.Preconditions.UID == nil || *opts.Preconditions.UID != nc.GetUID() {
					t.Fatal("delete lost original capture UID")
				}
				deleted = true
			}
			if deleted != (tc.want == 200) {
				t.Fatalf("CR deletion=%v status=%d", deleted, rr.Code)
			}
		})
	}
}

func TestRemoteCaptureMetadataNeverUsesCentralRetentionClamp(t *testing.T) {
	server := newCaptureServerObj("alpha", true)
	nc := newCompletedNetworkCapture(t, "cap-one", "alpha", 2*time.Minute, 3600)
	remote := fakeCaptureClient(server, nc)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, fakeCaptureClient())
	reg.Set("remote", remote)
	cfg := captureTestCfg
	cfg.DefaultRetentionSeconds = 60
	cfg.MaxRetentionSeconds = 60
	h := &captureHandler{reg: reg, cfg: cfg, auditor: newCaptureAuditor(t), gateway: &fakeCaptureGateway{caps: ws.CaptureGatewayCapabilities{Protocol: "v1", TargetUID: true, DefaultRetentionSeconds: 3600, MaxRetentionSeconds: 7200}}}
	r := chi.NewRouter()
	r.Get("/servers/{name}:capture", h.captureGet)
	r.Get("/servers/{name}:captures", h.captureList)
	for _, outage := range []bool{false, true} {
		if outage {
			h.gateway = nil
		}
		for _, path := range []string{"/servers/alpha:capture?cluster=remote&id=cap-one", "/servers/alpha:captures?cluster=remote"} {
			rr := do(t, r, http.MethodGet, path, nil)
			if rr.Code != 200 || !strings.Contains(rr.Body.String(), "cap-one") {
				t.Fatalf("outage=%v metadata hidden by central TTL: %d %s", outage, rr.Code, rr.Body)
			}
		}
	}
}

func TestRemoteCaptureCleanupCannotDeleteRecreatedRecord(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusGone} {
		server := newCaptureServerObj("alpha", true)
		server.SetUID("remote-server")
		nc := newCompletedNetworkCapture(t, "cap-one", "alpha", time.Second, 120)
		nc.SetUID("original-capture")
		nc.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: "alpha", UID: server.GetUID()}})
		remote := fakeCaptureClient(server, nc)
		client := remote.Dynamic.(*dynamicfake.FakeDynamicClient)
		client.PrependReactor("delete", "networkcaptures", func(action clienttesting.Action) (bool, runtime.Object, error) {
			// Model recreation between gateway acknowledgement and Kubernetes DELETE.
			replacement := nc.DeepCopy()
			replacement.SetUID("replacement-capture")
			if err := client.Tracker().Update(kube.GVRNetworkCapture, replacement, nc.GetNamespace()); err != nil {
				t.Fatal(err)
			}
			opts := action.(clienttesting.DeleteAction).GetDeleteOptions()
			if opts.Preconditions == nil || opts.Preconditions.UID == nil || *opts.Preconditions.UID != nc.GetUID() {
				t.Fatal("delete can remove replacement without original UID precondition")
			}
			return true, nil, apierrors.NewConflict(kube.GVRNetworkCapture.GroupResource(), nc.GetName(), errors.New("UID precondition failed"))
		})
		reg := kube.NewRegistry(scope.DefaultCluster)
		reg.Set("remote", remote)
		gateway := &fakeCaptureGateway{deleteStatus: status}
		h := &captureHandler{reg: reg, cfg: captureTestCfg, auditor: newCaptureAuditor(t), gateway: gateway}
		r := chi.NewRouter()
		r.Delete("/servers/{name}:capture", h.captureDelete)
		rr := do(t, r, http.MethodDelete, "/servers/alpha:capture?id=cap-one&cluster=remote", nil)
		if rr.Code != http.StatusConflict || gateway.deleted == nil || gateway.deleted.CaptureUID != string(nc.GetUID()) {
			t.Fatalf("cleanup=%d delete=%d target=%+v", status, rr.Code, gateway.deleted)
		}
		remaining, err := client.Resource(kube.GVRNetworkCapture).Namespace(nc.GetNamespace()).Get(t.Context(), nc.GetName(), metav1.GetOptions{})
		if err != nil || remaining.GetUID() != "replacement-capture" {
			t.Fatalf("replacement lost: %v %v", remaining, err)
		}
	}
}
