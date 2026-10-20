package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// fakeKubeClientWithClusters builds a kube.Client with custom GVR mappings
// for both dynamic and typed surfaces, including cluster CRDs.
func fakeKubeClientWithClusters(objs ...runtime.Object) *kube.Client {
	scm := runtime.NewScheme()
	_ = scheme.AddToScheme(scm)

	gvkr := map[schema.GroupVersionResource]string{
		kube.GVRCluster: "ClusterList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scm, gvkr, objs...)
	return &kube.Client{Dynamic: dyn, Typed: kubefake.NewClientset()}
}

func newCluster(name string, spec, status map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "Cluster",
			"metadata":   map[string]any{"name": name},
		},
	}
	if spec != nil {
		u.Object["spec"] = spec
	}
	if status != nil {
		u.Object["status"] = status
	}
	return u
}

func mountClustersRouter(k *kube.Client, reg *kube.Registry) http.Handler {
	r := chi.NewRouter()
	MountClusters(r, reg, k, "gameplane-system")
	return r
}

func doClusters(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	u := &auth.User{Perms: map[string]map[string]map[string]struct{}{"*": {"*": {"*": {}}}}}
	req := httptest.NewRequestWithContext(auth.WithUser(t.Context(), u), method, path, buf)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestMountClusters_ListLocal(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters()
	reg.Set("local", k)

	r := mountClustersRouter(k, reg)
	rr := doClusters(t, r, "GET", "/clusters/", nil)

	if rr.Code != 200 {
		t.Fatalf("list local: %d %s", rr.Code, rr.Body)
	}

	var resp clustersListResp
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Should contain the local cluster.
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].Name != "local" {
		t.Fatalf("expected name 'local', got %q", resp.Items[0].Name)
	}
	if resp.Items[0].Phase != "Healthy" {
		t.Fatalf("expected phase 'Healthy', got %q", resp.Items[0].Phase)
	}
}

func TestMountClusters_ListWithRemote(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters(
		newCluster("remote1", map[string]any{
			"displayName": "Remote Cluster 1",
			"kubeconfigSecret": map[string]any{
				"name": "cluster-remote1-kubeconfig",
				"key":  "kubeconfig",
			},
		}, map[string]any{
			"phase":         "Healthy",
			"message":       "Connected",
			"serverVersion": "v1.28.0",
		}),
	)
	reg.Set("local", k)
	reg.Set("remote1", k) // Simulate the watcher having loaded it.

	r := mountClustersRouter(k, reg)
	rr := doClusters(t, r, "GET", "/clusters/", nil)

	if rr.Code != 200 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}

	var resp clustersListResp
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Should have local + remote.
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}

	// Check local cluster.
	if resp.Items[0].Name != "local" {
		t.Fatalf("expected first item name 'local', got %q", resp.Items[0].Name)
	}

	// Check remote cluster.
	remote := resp.Items[1]
	if remote.Name != "remote1" {
		t.Fatalf("expected second item name 'remote1', got %q", remote.Name)
	}
	if remote.DisplayName != "Remote Cluster 1" {
		t.Fatalf("expected displayName 'Remote Cluster 1', got %q", remote.DisplayName)
	}
	if remote.Phase != "Healthy" {
		t.Fatalf("expected phase 'Healthy', got %q", remote.Phase)
	}
}

func TestMountClusters_CreateSuccess(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters()
	reg.Set("local", k)

	r := mountClustersRouter(k, reg)

	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://remote.example.com:6443
  name: remote
contexts:
- context:
    cluster: remote
    user: admin
  name: remote
current-context: remote
users:
- name: admin
  user:
    token: fake-token`

	body := map[string]any{
		"name":        "remote",
		"displayName": "My Remote",
		"kubeconfig":  kubeconfig,
	}

	rr := doClusters(t, r, "POST", "/clusters/", body)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d %s, want 201", rr.Code, rr.Body)
	}

	var resp clusterRegistryView
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Name != "remote" {
		t.Fatalf("expected name 'remote', got %q", resp.Name)
	}
	if resp.DisplayName != "My Remote" {
		t.Fatalf("expected displayName 'My Remote', got %q", resp.DisplayName)
	}
}

func TestMountClusters_CreateBadName(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters()
	reg.Set("local", k)

	r := mountClustersRouter(k, reg)

	t.Run("invalid dns label", func(t *testing.T) {
		body := map[string]any{
			"name":        "UPPERCASE",
			"displayName": "Bad",
			"kubeconfig":  "fake",
		}
		rr := doClusters(t, r, "POST", "/clusters/", body)
		if rr.Code == http.StatusCreated {
			t.Fatal("UPPERCASE should be rejected")
		}
	})

	t.Run("reserved name local", func(t *testing.T) {
		body := map[string]any{
			"name":        "local",
			"displayName": "Bad",
			"kubeconfig":  "fake",
		}
		rr := doClusters(t, r, "POST", "/clusters/", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("reserved name: expected 400, got %d", rr.Code)
		}
	})
}

func TestMountClusters_CreateBadKubeconfig(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters()
	reg.Set("local", k)

	r := mountClustersRouter(k, reg)

	body := map[string]any{
		"name":        "bad",
		"displayName": "Bad Config",
		"kubeconfig":  "not valid yaml",
	}
	rr := doClusters(t, r, "POST", "/clusters/", body)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad kubeconfig: expected 400, got %d", rr.Code)
	}
}

func TestMountClusters_DeleteLocal(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters()
	reg.Set("local", k)

	r := mountClustersRouter(k, reg)

	rr := doClusters(t, r, "DELETE", "/clusters/local", nil)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("delete local: expected 400, got %d", rr.Code)
	}
}

func TestMountClusters_DeleteMissing(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters()
	reg.Set("local", k)

	r := mountClustersRouter(k, reg)

	rr := doClusters(t, r, "DELETE", "/clusters/missing", nil)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("delete missing: expected 404, got %d", rr.Code)
	}
}

func TestMountClusters_DeleteSuccess(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters(
		newCluster("remote", map[string]any{
			"kubeconfigSecret": map[string]any{
				"name": "cluster-remote-kubeconfig",
			},
		}, nil),
	)
	reg.Set("local", k)
	reg.Set("remote", k)

	// Create the kubeconfig Secret.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-remote-kubeconfig",
			Namespace: "gameplane-system",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"kubeconfig": []byte("fake"),
		},
	}
	_, _ = k.Typed.CoreV1().Secrets("gameplane-system").Create(context.Background(), secret, metav1.CreateOptions{})

	r := mountClustersRouter(k, reg)

	rr := doClusters(t, r, "DELETE", "/clusters/remote", nil)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete remote: expected 204, got %d %s", rr.Code, rr.Body)
	}
}

// createClusterTestSecret stores a kubeconfig-shaped Secret with the given
// labels in the control-plane namespace the clusters router is mounted with.
func createClusterTestSecret(t *testing.T, k *kube.Client, name string, labels map[string]string) {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "gameplane-system",
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"kubeconfig": []byte("fake")},
	}
	if _, err := k.Typed.CoreV1().Secrets("gameplane-system").Create(t.Context(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create secret %s: %v", name, err)
	}
}

func TestMountClusters_CreateMarksKubeconfigSecretAsAPIManaged(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters()
	reg.Set("local", k)
	r := mountClustersRouter(k, reg)

	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://remote.example.com:6443
  name: remote
contexts:
- context:
    cluster: remote
    user: admin
  name: remote
current-context: remote
users:
- name: admin
  user:
    token: fake-token`

	rr := doClusters(t, r, "POST", "/clusters/", map[string]any{
		"name":       "remote",
		"kubeconfig": kubeconfig,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d %s, want 201", rr.Code, rr.Body)
	}

	secret, err := k.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "cluster-remote-kubeconfig", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get kubeconfig secret: %v", err)
	}
	if secret.Labels[kube.ClusterKubeconfigLabel] != "true" {
		t.Errorf("label %s = %q, want true", kube.ClusterKubeconfigLabel, secret.Labels[kube.ClusterKubeconfigLabel])
	}
	if secret.Labels[ManagedByLabel] != managedByValue {
		t.Errorf("label %s = %q, want %s", ManagedByLabel, secret.Labels[ManagedByLabel], managedByValue)
	}
}

func TestMountClusters_DeleteRemovesAPIManagedKubeconfigSecret(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters(
		newCluster("remote", map[string]any{
			"kubeconfigSecret": map[string]any{"name": "cluster-remote-kubeconfig"},
		}, nil),
	)
	reg.Set("local", k)
	createClusterTestSecret(t, k, "cluster-remote-kubeconfig", map[string]string{
		kube.ClusterKubeconfigLabel: "true",
		ManagedByLabel:              managedByValue,
	})
	r := mountClustersRouter(k, reg)

	rr := doClusters(t, r, "DELETE", "/clusters/remote", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete remote: expected 204, got %d %s", rr.Code, rr.Body)
	}
	_, err := k.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "cluster-remote-kubeconfig", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("API-managed kubeconfig secret still present after delete (err=%v)", err)
	}
}

func TestMountClusters_DeleteKeepsSecretsNotManagedByTheAPI(t *testing.T) {
	cases := map[string]map[string]string{
		"no labels":             nil,
		"kubeconfig label only": {kube.ClusterKubeconfigLabel: "true"},
		"managed-by only":       {ManagedByLabel: managedByValue},
		"another feature's managed secret": {
			"gameplane.local/notification-sink": "true",
			ManagedByLabel:                      managedByValue,
		},
	}
	for name, labels := range cases {
		t.Run(name, func(t *testing.T) {
			reg := kube.NewRegistry("local")
			k := fakeKubeClientWithClusters(
				newCluster("remote", map[string]any{
					"kubeconfigSecret": map[string]any{"name": "control-plane-secret"},
				}, nil),
			)
			reg.Set("local", k)
			createClusterTestSecret(t, k, "control-plane-secret", labels)
			r := mountClustersRouter(k, reg)

			rr := doClusters(t, r, "DELETE", "/clusters/remote", nil)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("delete remote: expected 204, got %d %s", rr.Code, rr.Body)
			}
			if _, err := k.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "control-plane-secret", metav1.GetOptions{}); err != nil {
				t.Fatalf("secret the API did not create was removed: %v", err)
			}
			if _, err := k.Dynamic.Resource(kube.GVRCluster).Get(t.Context(), "remote", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("cluster registration still present after delete (err=%v)", err)
			}
		})
	}
}

func TestMountClusters_DeleteDropsClusterClientImmediately(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters(
		newCluster("remote", map[string]any{
			"kubeconfigSecret": map[string]any{"name": "cluster-remote-kubeconfig"},
		}, nil),
	)
	reg.Set("local", k)
	reg.Set("remote", k) // As loaded by the cluster watch.
	r := mountClustersRouter(k, reg)

	rr := doClusters(t, r, "DELETE", "/clusters/remote", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete remote: expected 204, got %d %s", rr.Code, rr.Body)
	}
	if _, ok := reg.Get("remote"); ok {
		t.Fatal("cluster client still registered after its registration was deleted")
	}
	if _, ok := reg.Get("local"); !ok {
		t.Fatal("local cluster client removed by a remote cluster delete")
	}
}

func TestMountClusters_DeleteRemovesKubeconfigSecretWithoutManagedByLabel(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters(
		newCluster("remote", map[string]any{
			"kubeconfigSecret": map[string]any{"name": "cluster-remote-kubeconfig"},
		}, nil),
	)
	reg.Set("local", k)
	createClusterTestSecret(t, k, "cluster-remote-kubeconfig", map[string]string{
		kube.ClusterKubeconfigLabel: "true",
	})
	r := mountClustersRouter(k, reg)

	rr := doClusters(t, r, "DELETE", "/clusters/remote", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete remote: expected 204, got %d %s", rr.Code, rr.Body)
	}
	_, err := k.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "cluster-remote-kubeconfig", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("kubeconfig secret without managed-by label should be deleted (err=%v)", err)
	}
}

func TestMountClusters_DeleteKeepsAnotherClustersKubeconfigSecret(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters(
		newCluster("b", map[string]any{
			"kubeconfigSecret": map[string]any{"name": "cluster-a-kubeconfig"},
		}, nil),
	)
	reg.Set("local", k)
	createClusterTestSecret(t, k, "cluster-a-kubeconfig", map[string]string{
		kube.ClusterKubeconfigLabel: "true",
		ManagedByLabel:              managedByValue,
	})
	r := mountClustersRouter(k, reg)

	rr := doClusters(t, r, "DELETE", "/clusters/b", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete b: expected 204, got %d %s", rr.Code, rr.Body)
	}
	_, err := k.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "cluster-a-kubeconfig", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("another cluster's kubeconfig secret should not be deleted: %v", err)
	}
}

func TestMountClusters_DeleteKeepsGeneratedNameSecretWithoutKubeconfigLabel(t *testing.T) {
	reg := kube.NewRegistry("local")
	k := fakeKubeClientWithClusters(
		newCluster("remote", map[string]any{
			"kubeconfigSecret": map[string]any{"name": "cluster-remote-kubeconfig"},
		}, nil),
	)
	reg.Set("local", k)
	createClusterTestSecret(t, k, "cluster-remote-kubeconfig", map[string]string{
		ManagedByLabel: managedByValue,
	})
	r := mountClustersRouter(k, reg)

	rr := doClusters(t, r, "DELETE", "/clusters/remote", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete remote: expected 204, got %d %s", rr.Code, rr.Body)
	}
	_, err := k.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "cluster-remote-kubeconfig", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret without kubeconfig label should not be deleted: %v", err)
	}
}
