package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const (
	podsGameNS         = "gameplane-games"
	podsControlPlaneNS = "gameplane-system"
)

// podsAdminUser holds the admin wildcard on the local cluster.
func podsAdminUser() *auth.User {
	return &auth.User{ID: 1, Username: "admin", Role: "admin", Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {"*": {"*": {}}},
	}}
}

// podsViewerUser holds servers:read cluster-wide on the local cluster only.
func podsViewerUser() *auth.User {
	return &auth.User{ID: 2, Username: "viewer", Role: "viewer", Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {"*": {"servers:read": {}}},
	}}
}

// podsRouter mounts MountPods with the given clusters, serving every request as u.
func podsRouter(u *auth.User, clusters map[string]*kube.Client) http.Handler {
	reg := kube.NewRegistry(scope.DefaultCluster)
	for id, c := range clusters {
		reg.Set(id, c)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), u)))
		})
	})
	MountPods(r, reg, podsControlPlaneNS)
	return r
}

func podsGet(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func podsObj(ns, name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "server"}}},
	}
}

func podsDecodeList(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var list corev1.PodList
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode pod list: %v", err)
	}
	names := make([]string, 0, len(list.Items))
	for _, p := range list.Items {
		names = append(names, p.Name)
	}
	return names
}

func TestPods_ListNamespaceAndLabelSelector(t *testing.T) {
	cs := kubefake.NewSimpleClientset(
		podsObj(podsGameNS, "alpha-0", map[string]string{serverInstanceLabel: "alpha"}),
		podsObj(podsGameNS, "beta-0", map[string]string{serverInstanceLabel: "beta"}),
	)
	h := podsRouter(podsViewerUser(), map[string]*kube.Client{scope.DefaultCluster: {Typed: cs}})

	if got := podsDecodeList(t, podsGet(t, h, "/pods?namespace="+podsGameNS)); len(got) != 2 {
		t.Fatalf("namespace list = %v, want 2 pods", got)
	}
	got := podsDecodeList(t, podsGet(t, h, "/pods?namespace="+podsGameNS+"&labelSelector="+serverInstanceLabel+"%3Dalpha"))
	if len(got) != 1 || got[0] != "alpha-0" {
		t.Fatalf("label-selected list = %v, want [alpha-0]", got)
	}
}

func TestPods_GetPodAndInvalidName(t *testing.T) {
	cs := kubefake.NewSimpleClientset(podsObj(podsGameNS, "alpha-0", nil))
	h := podsRouter(podsViewerUser(), map[string]*kube.Client{scope.DefaultCluster: {Typed: cs}})

	rr := podsGet(t, h, "/pods/alpha-0?namespace="+podsGameNS)
	if rr.Code != http.StatusOK {
		t.Fatalf("get pod: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var pod corev1.Pod
	if err := json.Unmarshal(rr.Body.Bytes(), &pod); err != nil || pod.Name != "alpha-0" {
		t.Fatalf("pod = %+v err=%v", pod, err)
	}
	if rr := podsGet(t, h, "/pods/Bad_Name?namespace="+podsGameNS); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid name: code=%d, want 400", rr.Code)
	}
}

func TestPods_ServerPodsByInstanceLabel(t *testing.T) {
	cs := kubefake.NewSimpleClientset(
		podsObj(podsGameNS, "alpha-0", map[string]string{serverInstanceLabel: "alpha"}),
		podsObj(podsGameNS, "beta-0", map[string]string{serverInstanceLabel: "beta"}),
		podsObj(podsGameNS, "stray-0", nil),
	)
	h := podsRouter(podsViewerUser(), map[string]*kube.Client{scope.DefaultCluster: {Typed: cs}})

	got := podsDecodeList(t, podsGet(t, h, "/servers/alpha/pods?namespace="+podsGameNS))
	if len(got) != 1 || got[0] != "alpha-0" {
		t.Fatalf("server pods = %v, want [alpha-0]", got)
	}
}

func TestPods_ServerLogTail(t *testing.T) {
	cs := kubefake.NewSimpleClientset(
		podsObj(podsGameNS, "alpha-0", map[string]string{serverInstanceLabel: "alpha"}),
		podsObj(podsGameNS, "beta-0", map[string]string{serverInstanceLabel: "beta"}),
	)
	h := podsRouter(podsViewerUser(), map[string]*kube.Client{scope.DefaultCluster: {Typed: cs}})

	// An oversized tailLines is clamped, not rejected.
	rr := podsGet(t, h, "/servers/alpha/logs/pod/tail?namespace="+podsGameNS+"&pod=alpha-0&tailLines=999999")
	if rr.Code != http.StatusOK {
		t.Fatalf("tail: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type = %q, want text/plain", ct)
	}
	// The fake clientset returns a fixed "fake logs" stream for GetLogs.
	if !strings.Contains(rr.Body.String(), "fake logs") {
		t.Fatalf("tail body = %q", rr.Body.String())
	}

	// A pod that belongs to another server is refused.
	other := podsGet(t, h, "/servers/alpha/logs/pod/tail?namespace="+podsGameNS+"&pod=beta-0")
	if other.Code == http.StatusOK || mcpTokenProblemID(t, other) != "server.not_found" {
		t.Fatalf("other server's pod: code=%d body=%s", other.Code, other.Body.String())
	}

	if bad := podsGet(t, h, "/servers/alpha/logs/pod/tail?namespace="+podsGameNS+"&pod=alpha-0&tailLines=abc"); bad.Code != http.StatusBadRequest {
		t.Fatalf("non-integer tailLines: code=%d, want 400", bad.Code)
	}
}

func TestPods_KubeEventsSortedAndTruncated(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cs := kubefake.NewSimpleClientset()
	for i := range 105 {
		ev := &corev1.Event{
			ObjectMeta:    metav1.ObjectMeta{Name: fmt.Sprintf("ev-%03d", i), Namespace: podsGameNS},
			Reason:        "Started",
			LastTimestamp: metav1.NewTime(base.Add(time.Duration(i) * time.Minute)),
		}
		if _, err := cs.CoreV1().Events(podsGameNS).Create(t.Context(), ev, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	h := podsRouter(podsViewerUser(), map[string]*kube.Client{scope.DefaultCluster: {Typed: cs}})

	rr := podsGet(t, h, "/kube-events?namespace="+podsGameNS)
	if rr.Code != http.StatusOK {
		t.Fatalf("kube-events: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		APIVersion string         `json:"apiVersion"`
		Kind       string         `json:"kind"`
		Items      []corev1.Event `json:"items"`
		Truncated  bool           `json:"truncated"`
		Total      int            `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if out.APIVersion != "v1" || out.Kind != "EventList" {
		t.Fatalf("envelope = %q/%q, want v1/EventList", out.APIVersion, out.Kind)
	}
	if !out.Truncated || out.Total != 105 || len(out.Items) != maxKubeEvents {
		t.Fatalf("truncated=%v total=%d items=%d, want true/105/100", out.Truncated, out.Total, len(out.Items))
	}
	if out.Items[0].Name != "ev-104" || out.Items[len(out.Items)-1].Name != "ev-005" {
		t.Fatalf("order: first=%s last=%s, want newest ev-104 first and ev-005 last",
			out.Items[0].Name, out.Items[len(out.Items)-1].Name)
	}
}

func TestPods_ControlPlaneNamespaceAdminOnlyLocal(t *testing.T) {
	cs := kubefake.NewSimpleClientset(podsObj(podsControlPlaneNS, "operator-0", nil))
	clusters := map[string]*kube.Client{
		scope.DefaultCluster: {Typed: cs},
		"remote":             {Typed: kubefake.NewSimpleClientset(podsObj(podsControlPlaneNS, "operator-0", nil))},
	}

	admin := podsRouter(podsAdminUser(), clusters)
	if got := podsDecodeList(t, podsGet(t, admin, "/pods?namespace="+podsControlPlaneNS)); len(got) != 1 {
		t.Fatalf("admin control-plane pods = %v, want one", got)
	}
	if rr := podsGet(t, admin, "/kube-events?namespace="+podsControlPlaneNS); rr.Code != http.StatusOK {
		t.Fatalf("admin control-plane events: code=%d", rr.Code)
	}

	viewer := podsRouter(podsViewerUser(), clusters)
	if rr := podsGet(t, viewer, "/pods?namespace="+podsControlPlaneNS); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer control-plane pods: code=%d, want 403", rr.Code)
	}
	if rr := podsGet(t, viewer, "/kube-events?namespace="+podsControlPlaneNS); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer control-plane events: code=%d, want 403", rr.Code)
	}

	if rr := podsGet(t, admin, "/pods?namespace="+podsControlPlaneNS+"&cluster=remote"); rr.Code != http.StatusForbidden {
		t.Fatalf("admin on remote cluster: code=%d, want 403", rr.Code)
	}
}

func TestPods_StripsManagedFields(t *testing.T) {
	pod := podsObj(podsGameNS, "alpha-0", nil)
	pod.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubectl", Operation: metav1.ManagedFieldsOperationUpdate}}
	cs := kubefake.NewSimpleClientset(pod)
	h := podsRouter(podsViewerUser(), map[string]*kube.Client{scope.DefaultCluster: {Typed: cs}})

	for _, target := range []string{"/pods/alpha-0?namespace=" + podsGameNS, "/pods?namespace=" + podsGameNS} {
		rr := podsGet(t, h, target)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: code=%d body=%s", target, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "managedFields") {
			t.Fatalf("%s: response still carries managedFields", target)
		}
	}
}
