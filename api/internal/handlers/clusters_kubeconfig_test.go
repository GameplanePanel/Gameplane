package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/controlplane"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func registeredRotationFixture(t *testing.T) (*kube.Client, *kube.Registry, http.Handler, *unstructured.Unstructured) {
	t.Helper()
	_, home, reg, _ := standaloneHandlerStore(t)
	router := standaloneClustersRouter(home, reg)
	if rr := doClusters(t, router, http.MethodPost, "/clusters/", clusterCreateReq{Name: "remote", DisplayName: "Remote games", Kubeconfig: standaloneTestKubeconfig}); rr.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rr.Code, rr.Body)
	}
	registration, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return home, reg, router, registration
}

func TestKubeconfigRotationPreservesRegistrationAndImmediatelyReplacesClient(t *testing.T) {
	store, home, reg, keyPath := standaloneHandlerStore(t)
	router := standaloneClustersRouter(home, reg)
	if rr := doClusters(t, router, http.MethodPost, "/clusters/", clusterCreateReq{Name: "remote", DisplayName: "Remote games", Kubeconfig: standaloneTestKubeconfig}); rr.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rr.Code, rr.Body)
	}
	registration, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	registration.Object["spec"].(map[string]any)["agentGateway"] = map[string]any{"url": "https://gateway.example.invalid", "tlsSecretRef": map[string]any{"name": "externally-managed-gateway"}}
	registration, err = home.Clusters().Update(t.Context(), registration, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldClient := fakeKubeClient()
	reg.SetWithUID("remote", registration.GetUID(), oldClient)
	userID := seedUser(t, store, "rotation-reader", "viewer", "")
	if _, err := store.DB.ExecContext(t.Context(), `INSERT INTO user_role_bindings(user_id,role_name,cluster,namespace) VALUES (?, 'viewer', 'remote', 'gameplane-games')`, userID); err != nil {
		t.Fatal(err)
	}
	rotated := strings.ReplaceAll(standaloneTestKubeconfig, "secret-standalone-registration-token", "rotated-registration-token")
	rr := doClusters(t, router, http.MethodPut, "/clusters/remote/kubeconfig", map[string]string{"kubeconfig": rotated})
	if rr.Code != http.StatusNoContent || rr.Body.Len() != 0 {
		t.Fatalf("rotate: %d %s", rr.Code, rr.Body)
	}
	if client, ok := reg.Get("remote"); ok && client == oldClient {
		t.Fatal("old workload client remains available after rotation")
	}
	current, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil || current.GetUID() != registration.GetUID() || gatewayCredentialName(current) != "externally-managed-gateway" {
		t.Fatal("rotation changed registration identity or gateway")
	}
	var bindings int
	if err := store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM user_role_bindings WHERE user_id = ? AND cluster = 'remote'`, userID).Scan(&bindings); err != nil || bindings != 1 {
		t.Fatal("rotation removed the existing workload grant")
	}
	name, _, _ := unstructured.NestedString(current.Object, "spec", "kubeconfigSecret", "name")
	assertManagementCiphertext(t, store, name, rotated)
	reopened, err := controlplane.New(t.Context(), store, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := reopened.Secrets(standaloneTestNamespace).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil || string(secret.Data["kubeconfig"]) != rotated || secret.Immutable == nil || !*secret.Immutable {
		t.Fatal("immutable rotated credential did not survive restart")
	}
	if rr := doClusters(t, router, http.MethodDelete, "/clusters/remote", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete rotated cluster: %d %s", rr.Code, rr.Body)
	}
	if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("cluster deletion stranded the versioned credential: %v", err)
	}
}

func TestKubeconfigRotationRequiresCentralClusterManagement(t *testing.T) {
	_, _, router, _ := registeredRotationFixture(t)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/clusters/remote/kubeconfig", strings.NewReader(`{"kubeconfig":"private-input"}`))
	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, request)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated credential rotation: %d", unauthenticated.Code)
	}
	for _, permission := range []string{"modules:manage", "templates:write", "servers:write", "config:manage"} {
		body, err := json.Marshal(map[string]string{"kubeconfig": standaloneTestKubeconfig})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequestWithContext(auth.WithUser(t.Context(), inventoryUser("remote", "*", permission)), http.MethodPut, "/clusters/remote/kubeconfig", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, request)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s granted credential rotation: %d %s", permission, rr.Code, rr.Body)
		}
	}
	for _, tc := range []struct{ name, config string }{{"local", standaloneTestKubeconfig}, {"remote", "invalid-private-kubeconfig"}, {"remote", ""}} {
		rr := doClusters(t, router, http.MethodPut, "/clusters/"+tc.name+"/kubeconfig", map[string]string{"kubeconfig": tc.config})
		if rr.Code != http.StatusBadRequest || strings.Contains(rr.Body.String(), "invalid-private-kubeconfig") {
			t.Fatalf("invalid rotation leaked/accepted input: %d %s", rr.Code, rr.Body)
		}
	}
	body, err := json.Marshal(map[string]string{"kubeconfig": standaloneTestKubeconfig})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequestWithContext(auth.WithUser(t.Context(), inventoryUser("local", "*", "cluster:manage")), http.MethodPut, "/clusters/remote/kubeconfig", bytes.NewReader(body))
	allowed := httptest.NewRecorder()
	router.ServeHTTP(allowed, request)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("central cluster-management grant denied rotation: %d %s", allowed.Code, allowed.Body)
	}
}

func TestKubeconfigRotationPreservesExternalCredential(t *testing.T) {
	home, _, router, registration := registeredRotationFixture(t)
	external := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "external-kubeconfig", Labels: map[string]string{kube.ClusterKubeconfigLabel: "true"}}, Data: map[string][]byte{"kubeconfig": []byte(standaloneTestKubeconfig)}}
	if _, err := home.Secrets(standaloneTestNamespace).Create(t.Context(), external, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedField(registration.Object, external.Name, "spec", "kubeconfigSecret", "name")
	if _, err := home.Clusters().Update(t.Context(), registration, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/kubeconfig", map[string]string{"kubeconfig": standaloneTestKubeconfig}); rr.Code != http.StatusNoContent {
		t.Fatalf("rotate external reference: %d %s", rr.Code, rr.Body)
	}
	if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), external.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("removed external credential: %v", err)
	}
}

func TestConcurrentKubeconfigRotationsPublishOnlyOneCompleteCredential(t *testing.T) {
	home, _, router, _ := registeredRotationFixture(t)
	first := strings.ReplaceAll(standaloneTestKubeconfig, "secret-standalone-registration-token", "first-rotation-token")
	second := strings.ReplaceAll(standaloneTestKubeconfig, "secret-standalone-registration-token", "second-rotation-token")
	interleaved := false
	home.ClusterStore = gatewayInterleavingClusters{ClusterStore: home.ClusterStore, beforeUpdate: func() {
		if interleaved {
			return
		}
		interleaved = true
		if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/kubeconfig", map[string]string{"kubeconfig": second}); rr.Code != http.StatusNoContent {
			t.Fatalf("concurrent rotate: %d %s", rr.Code, rr.Body)
		}
	}}
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/kubeconfig", map[string]string{"kubeconfig": first}); rr.Code != http.StatusConflict {
		t.Fatalf("stale rotate: %d %s", rr.Code, rr.Body)
	}
	current, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	name, _, _ := unstructured.NestedString(current.Object, "spec", "kubeconfigSecret", "name")
	secret, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil || string(secret.Data["kubeconfig"]) != second {
		t.Fatal("pointer and credential came from different rotations")
	}
	secrets, err := home.Secrets(standaloneTestNamespace).List(t.Context(), metav1.ListOptions{LabelSelector: kube.ClusterKubeconfigLabel + "=true"})
	if err != nil || len(secrets.Items) != 1 {
		t.Fatalf("rotation stranded detached credentials: %v", err)
	}
}

func TestKubeconfigRotationCannotCrossRegistrationDeletionAndRecreation(t *testing.T) {
	home, reg, router, original := registeredRotationFixture(t)
	interleaved := false
	home.ClusterStore = gatewayInterleavingClusters{ClusterStore: home.ClusterStore, beforeUpdate: func() {
		if interleaved {
			return
		}
		interleaved = true
		if rr := doClusters(t, router, http.MethodDelete, "/clusters/remote", nil); rr.Code != http.StatusNoContent {
			t.Fatalf("concurrent remove: %d %s", rr.Code, rr.Body)
		}
		if rr := doClusters(t, router, http.MethodPost, "/clusters/", clusterCreateReq{Name: "remote", Kubeconfig: standaloneTestKubeconfig}); rr.Code != http.StatusCreated {
			t.Fatalf("concurrent recreate: %d %s", rr.Code, rr.Body)
		}
		current, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		reg.SetWithUID("remote", current.GetUID(), fakeKubeClient())
	}}
	rr := doClusters(t, router, http.MethodPut, "/clusters/remote/kubeconfig", map[string]string{"kubeconfig": strings.ReplaceAll(standaloneTestKubeconfig, "secret-standalone-registration-token", "stale-private-token")})
	if rr.Code != http.StatusConflict && rr.Code != http.StatusNotFound {
		t.Fatalf("rotation crossed recreation: %d %s", rr.Code, rr.Body)
	}
	current, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil || current.GetUID() == original.GetUID() {
		t.Fatal("replacement registration missing")
	}
	name, _, _ := unstructured.NestedString(current.Object, "spec", "kubeconfigSecret", "name")
	if name != clusterKubeconfigSecretName("remote") {
		t.Fatal("rotation replaced the recreated registration's credential")
	}
	if _, ok := reg.Get("remote"); !ok {
		t.Fatal("stale rotation removed the replacement client")
	}
}
