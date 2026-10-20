package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func clusterTestRegistry(k *kube.Client) *kube.Registry {
	reg := kube.NewRegistry("local")
	reg.Set("local", k)
	return reg
}

func readyNode(name string, ready bool, cpu, mem string) *corev1.Node {
	cond := corev1.ConditionFalse
	if ready {
		cond = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"node-role.kubernetes.io/control-plane": ""},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: cond}},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(cpu),
				corev1.ResourceMemory:           resource.MustParse(mem),
				corev1.ResourcePods:             resource.MustParse("110"),
				corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
			},
		},
	}
}

func boundPV(name, size string, bound bool) *corev1.PersistentVolume {
	phase := corev1.VolumeAvailable
	if bound {
		phase = corev1.VolumeBound
	}
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
		},
		Status: corev1.PersistentVolumeStatus{Phase: phase},
	}
}

func TestCluster_View(t *testing.T) {
	cs := fake.NewSimpleClientset(
		readyNode("cp-0", true, "4", "8Gi"),
		readyNode("worker-0", false, "8", "16Gi"),
	)
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.31.2"}
	k := &kube.Client{Typed: cs}

	r := chi.NewRouter()
	MountCluster(r, clusterTestRegistry(k), nil, "v9.9.9-test", false, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cluster", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var view clusterView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Total != 2 || view.Ready != 1 {
		t.Fatalf("ready/total = %d/%d, want 1/2", view.Ready, view.Total)
	}
	if view.Version != "v1.31.2" {
		t.Fatalf("version = %q, want v1.31.2", view.Version)
	}
	if len(view.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(view.Nodes))
	}
	var cp *clusterNode
	for i := range view.Nodes {
		if view.Nodes[i].Name == "cp-0" {
			cp = &view.Nodes[i]
		}
	}
	if cp == nil || cp.Status != "Ready" || cp.CPU == nil || cp.CPU.Capacity != 4 {
		t.Fatalf("cp-0 mapped wrong: %+v", cp)
	}
	if len(cp.Roles) != 1 || cp.Roles[0] != "control-plane" {
		t.Fatalf("cp-0 roles = %v, want [control-plane]", cp.Roles)
	}
}

func TestCluster_Stats(t *testing.T) {
	cs := fake.NewSimpleClientset(
		readyNode("cp-0", true, "4", "8Gi"),
		boundPV("pv-bound", "10Gi", true),
		boundPV("pv-free", "5Gi", false),
	)
	k := &kube.Client{Typed: cs}

	r := chi.NewRouter()
	MountCluster(r, clusterTestRegistry(k), nil, "v9.9.9-test", false, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cluster/stats", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var stats clusterStats
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Nodes != 1 {
		t.Fatalf("nodes = %d, want 1", stats.Nodes)
	}
	const gi = 1024 * 1024 * 1024
	// Total is the node's disk, not the sum of PV capacity — otherwise every
	// PV being Bound would pin the storage meter at 100%.
	if stats.TotalStorageBytes != 100*gi {
		t.Fatalf("total = %d, want %d", stats.TotalStorageBytes, int64(100*gi))
	}
	if stats.UsedStorageBytes != 10*gi {
		t.Fatalf("used = %d, want %d", stats.UsedStorageBytes, int64(10*gi))
	}
	if stats.UsedStorageBytes >= stats.TotalStorageBytes {
		t.Fatalf("used %d must be below total %d", stats.UsedStorageBytes, stats.TotalStorageBytes)
	}
}

// Regression: bound PVs were counted as both numerator and denominator, so
// used == total on every real cluster and the meter always read 100%.
func TestStorageAccounting(t *testing.T) {
	const gi = 1024 * 1024 * 1024

	nodes := []corev1.Node{*readyNode("a", true, "4", "8Gi"), *readyNode("b", true, "4", "8Gi")}
	if got := nodeStorageCapacity(nodes); got != 200*gi {
		t.Errorf("nodeStorageCapacity = %d, want %d", got, int64(200*gi))
	}

	pvs := []corev1.PersistentVolume{
		*boundPV("bound-a", "10Gi", true),
		*boundPV("bound-b", "5Gi", true),
		*boundPV("spare", "50Gi", false),
	}
	if got := boundVolumeBytes(pvs); got != 15*gi {
		t.Errorf("boundVolumeBytes = %d, want %d (unbound volumes are not usage)", got, int64(15*gi))
	}

	if got := nodeStorageCapacity(nil); got != 0 {
		t.Errorf("nodeStorageCapacity(nil) = %d, want 0", got)
	}
	if got := boundVolumeBytes(nil); got != 0 {
		t.Errorf("boundVolumeBytes(nil) = %d, want 0", got)
	}
}

func TestCluster_Info_NilStore(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.30.0"}
	k := &kube.Client{Typed: cs}

	r := chi.NewRouter()
	MountCluster(r, clusterTestRegistry(k), nil, "v9.9.9-test", false, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cluster/info", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var info clusterInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Version != "v1.30.0" {
		t.Fatalf("version = %q", info.Version)
	}
	if info.GameplaneVersion != "v9.9.9-test" {
		t.Fatalf("gameplaneVersion = %q, want v9.9.9-test", info.GameplaneVersion)
	}
	if info.ClusterName != "" {
		t.Fatalf("clusterName = %q, want empty with nil store", info.ClusterName)
	}
	if info.ClusterOps {
		t.Fatal("clusterOps = true, want false when mounted disabled")
	}
}

// The clusterOps flag and the updates.channel label must surface
// verbatim on /cluster/info so the dashboard can gate the node-join /
// kubeconfig buttons and show which channel the install tracks.
func TestCluster_Info_ReportsClusterOpsAndChannel(t *testing.T) {
	cs := fake.NewSimpleClientset()
	k := &kube.Client{Typed: cs}

	r := chi.NewRouter()
	MountCluster(r, clusterTestRegistry(k), nil, "v9.9.9-test", true, "edge")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cluster/info", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var info clusterInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !info.ClusterOps {
		t.Fatal("clusterOps = false, want true when mounted enabled")
	}
	if info.UpdateChannel != "edge" {
		t.Fatalf("updateChannel = %q, want edge", info.UpdateChannel)
	}
}

// mockAPIServer stands up a real HTTP server serving just enough of the
// Kubernetes API surface (node list, plus metrics.k8s.io when metrics is
// non-nil) for a real *kubernetes.Clientset built from its URL to exercise
// fetchNodeUsage's production REST path end to end.
//
// fake.NewSimpleClientset can't do this: its CoreV1().RESTClient() returns a
// nil *rest.RESTClient wrapped in a non-nil rest.Interface, which panics on
// first field access inside Get() before a request is even built (see
// TestFetchNodeUsage_FakeClientDoesNotPanic and internal/kube/stdin_test.go).
func mockAPIServer(t *testing.T, nodes []corev1.Node, metrics http.HandlerFunc) *kube.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		list := corev1.NodeList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NodeList"},
			Items:    nodes,
		}
		if err := json.NewEncoder(w).Encode(list); err != nil {
			t.Fatalf("encode node list: %v", err)
		}
	})
	if metrics != nil {
		mux.HandleFunc("/apis/metrics.k8s.io/v1beta1/nodes", metrics)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	typed, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("build clientset: %v", err)
	}
	return &kube.Client{Typed: typed}
}

// Regression: the API used to omit `used` unconditionally with a comment
// saying it had "no metrics-server dependency" — even on clusters that DO
// run metrics-server, which rendered CPU/memory as a permanent 0%. Fetch
// the real metrics.k8s.io/v1beta1 payload and populate Used, in the same
// unit (cores, bytes) Capacity already uses.
func TestCluster_View_MetricsAvailable(t *testing.T) {
	k := mockAPIServer(t, []corev1.Node{*readyNode("cp-0", true, "4", "8Gi")},
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// Shape observed on a live cluster: nanocore CPU, Ki memory.
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"cp-0"},"usage":{"cpu":"645424538n","memory":"2717804Ki"}}]}`))
		})

	r := chi.NewRouter()
	MountCluster(r, clusterTestRegistry(k), nil, "v9.9.9-test", false, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cluster", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var view clusterView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(view.Nodes))
	}
	n := view.Nodes[0]
	if n.CPU == nil || n.CPU.Used == nil {
		t.Fatalf("cpu used not populated: %+v", n.CPU)
	}
	// ceil(645424538 nanocores * 1000 / 1e9) = ceil(645.424538) = 646 millicores.
	if *n.CPU.Used != 0.646 {
		t.Fatalf("cpu used = %v, want 0.646 cores", *n.CPU.Used)
	}
	if n.CPU.Capacity != 4 {
		t.Fatalf("cpu capacity = %v, want 4 cores (unit must match used)", n.CPU.Capacity)
	}
	if n.Memory == nil || n.Memory.Used == nil {
		t.Fatalf("memory used not populated: %+v", n.Memory)
	}
	const wantMemBytes = 2717804 * 1024
	if *n.Memory.Used != wantMemBytes {
		t.Fatalf("memory used = %v, want %v", *n.Memory.Used, float64(wantMemBytes))
	}
}

// Graceful degradation: a cluster with no metrics-server installed (or any
// other failure reaching/parsing metrics.k8s.io) must keep serving node
// capacity data with `used` omitted, not fail the request.
func TestCluster_View_MetricsAbsent(t *testing.T) {
	k := mockAPIServer(t, []corev1.Node{*readyNode("cp-0", true, "4", "8Gi")},
		func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r) // metrics.k8s.io not registered — the common case
		})

	r := chi.NewRouter()
	MountCluster(r, clusterTestRegistry(k), nil, "v9.9.9-test", false, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cluster", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var view clusterView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(view.Nodes))
	}
	n := view.Nodes[0]
	if n.CPU == nil || n.CPU.Used != nil {
		t.Fatalf("cpu used = %+v, want nil (no metrics-server)", n.CPU)
	}
	if n.CPU.Capacity != 4 {
		t.Fatalf("cpu capacity = %v, want 4 (capacity must still work without metrics)", n.CPU.Capacity)
	}
	if n.Memory == nil || n.Memory.Used != nil {
		t.Fatalf("memory used = %+v, want nil (no metrics-server)", n.Memory)
	}
}

// fetchNodeUsage must not panic against a fake clientset (used throughout
// this file's other tests): CoreV1().RESTClient() on a fake returns a nil
// *rest.RESTClient wrapped in a non-nil interface, which would otherwise
// panic on first use inside Get().
func TestFetchNodeUsage_FakeClientDoesNotPanic(t *testing.T) {
	cs := fake.NewSimpleClientset()
	h := &clusterHandler{k: &kube.Client{Typed: cs}}
	usage := h.fetchNodeUsage(t.Context())
	if usage != nil {
		t.Fatalf("usage = %+v, want nil against a fake clientset", usage)
	}
}

func TestCluster_PodCount(t *testing.T) {
	cs := fake.NewSimpleClientset(
		readyNode("node-0", true, "4", "8Gi"),
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "app-0", Namespace: "default"},
			Spec:       corev1.PodSpec{NodeName: "node-0"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "app-1", Namespace: "default"},
			Spec:       corev1.PodSpec{NodeName: "node-0"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "completed", Namespace: "default"},
			Spec:       corev1.PodSpec{NodeName: "node-0"},
			Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "unscheduled", Namespace: "default"},
			Spec:       corev1.PodSpec{},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.31.0"}
	k := &kube.Client{Typed: cs}

	r := chi.NewRouter()
	MountCluster(r, clusterTestRegistry(k), nil, "v9.9.9-test", false, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cluster", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var view clusterView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var node *clusterNode
	for i := range view.Nodes {
		if view.Nodes[i].Name == "node-0" {
			node = &view.Nodes[i]
			break
		}
	}
	if node == nil {
		t.Fatalf("node-0 not found")
	}
	if node.Pods == nil || node.Pods.Capacity != 110 {
		t.Fatalf("Pods capacity = %v, want 110", node.Pods)
	}
	if node.Pods.Used == nil || *node.Pods.Used != 2 {
		t.Fatalf("Pods used = %v, want 2", node.Pods.Used)
	}
}
