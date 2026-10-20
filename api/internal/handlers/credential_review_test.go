package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

type stalledRotationRefresh struct {
	kube.ClusterStore
	published bool
	stalled   bool
}

func (s *stalledRotationRefresh) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	updated, err := s.ClusterStore.Update(ctx, obj, opts, subresources...)
	if err == nil {
		s.published = true
	}
	return updated, err
}

func (s *stalledRotationRefresh) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if s.published && !s.stalled {
		s.stalled = true
		// Model a queued or blocked refresh consuming the whole cleanup budget.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.ClusterStore.Get(ctx, name, opts, subresources...)
}

func TestKubeconfigCleanupSurvivesExpiredClientRefresh(t *testing.T) {
	t.Parallel()
	home, _, router, registration := registeredRotationFixture(t)
	oldName, _, _ := unstructured.NestedString(registration.Object, "spec", "kubeconfigSecret", "name")
	home.ClusterStore = &stalledRotationRefresh{ClusterStore: home.ClusterStore}
	rotated := strings.ReplaceAll(standaloneTestKubeconfig, "secret-standalone-registration-token", "rotated-cleanup-token")
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/kubeconfig", map[string]string{"kubeconfig": rotated}); rr.Code != http.StatusNoContent {
		t.Fatalf("rotate: %d %s", rr.Code, rr.Body)
	}
	if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), oldName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expired refresh stranded old credential: %v", err)
	}
	if rr := doClusters(t, router, http.MethodDelete, "/clusters/remote", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body)
	}
	if rr := doClusters(t, router, http.MethodPost, "/clusters/", clusterCreateReq{Name: "remote", Kubeconfig: rotated}); rr.Code != http.StatusCreated {
		t.Fatalf("re-registration after timed-out refresh: %d %s", rr.Code, rr.Body)
	}
}

type stalledGatewayCredentialRead struct {
	kube.SecretStore
	gatewayName string
}

func (s stalledGatewayCredentialRead) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Secret, error) {
	if name == s.gatewayName {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.SecretStore.Get(ctx, name, opts)
}

func TestClusterDeletionSeparatesCredentialCleanupBudgets(t *testing.T) {
	t.Parallel()
	home, _, router, registration := registeredRotationFixture(t)
	kubeconfigName, _, _ := unstructured.NestedString(registration.Object, "spec", "kubeconfigSecret", "name")
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", gatewayCertificateRequest(t)); rr.Code != http.StatusNoContent {
		t.Fatalf("configure: %d", rr.Code)
	}
	current, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gatewayName := gatewayCredentialName(current)
	secrets := home.SecretStore
	home.SecretStore = func(namespace string) kube.SecretStore {
		return stalledGatewayCredentialRead{SecretStore: secrets(namespace), gatewayName: gatewayName}
	}
	if rr := doClusters(t, router, http.MethodDelete, "/clusters/remote", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body)
	}
	if _, err := secrets(standaloneTestNamespace).Get(t.Context(), kubeconfigName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("gateway read consumed kubeconfig cleanup budget: %v", err)
	}
}

func TestCombinedGatewayConfigurationRejectsUnsafeDestination(t *testing.T) {
	home := &kube.Client{Typed: kubefake.NewSimpleClientset(), Dynamic: fake.NewSimpleDynamicClient(runtime.NewScheme())}
	reg := kube.NewRegistry("local")
	reg.Set("local", home)
	h := clustersHandler{k: home, reg: reg, namespace: standaloneTestNamespace}
	in := gatewayCertificateRequest(t)
	in.URL = "https://169.254.169.254"
	rr := gatewayReviewRecorder()
	h.configureGateway(rr, gatewayReviewRequest(t.Context(), t, in))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("combined handler accepted metadata origin: %d %s", rr.Code, rr.Body)
	}
}

func TestLegacyKubeconfigRotationDeletesCapturedSecret(t *testing.T) {
	home, _, router, registration := registeredRotationFixture(t)
	name, _, _ := unstructured.NestedString(registration.Object, "spec", "kubeconfigSecret", "name")
	secret, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(secret.Labels, ManagedByLabel)
	if _, err := home.Secrets(standaloneTestNamespace).Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/kubeconfig", map[string]string{"kubeconfig": standaloneTestKubeconfig}); rr.Code != http.StatusNoContent {
		t.Fatalf("rotate: %d %s", rr.Code, rr.Body)
	}
	if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("legacy credential survived: %v", err)
	}
}

type cancelGatewayUpdate struct {
	kube.ClusterStore
	cancel context.CancelFunc
}

type cancelClusterDelete struct {
	kube.ClusterStore
	cancel context.CancelFunc
}

func (s cancelClusterDelete) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	if err := s.ClusterStore.Delete(ctx, name, opts, subresources...); err != nil {
		return err
	}
	s.cancel()
	return nil
}

func TestClusterRemovalCleansCredentialsAfterCallerDisconnects(t *testing.T) {
	home, reg, router, registration := registeredRotationFixture(t)
	kubeconfigName, _, _ := unstructured.NestedString(registration.Object, "spec", "kubeconfigSecret", "name")
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", gatewayCertificateRequest(t)); rr.Code != http.StatusNoContent {
		t.Fatalf("configure: %d", rr.Code)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	home.ClusterStore = cancelClusterDelete{home.ClusterStore, cancel}
	// Cancel only after persisted removal, exercising the cleanup boundary.
	request := gatewayReviewRequest(ctx, t, gatewayCertificateRequest(t))
	request.Method = http.MethodDelete
	h := clustersHandler{k: home, reg: reg, namespace: standaloneTestNamespace}
	rr := gatewayReviewRecorder()
	h.delete(rr, request)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body)
	}
	if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), kubeconfigName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("kubeconfig survived: %v", err)
	}
	if secrets, err := home.Secrets(standaloneTestNamespace).List(t.Context(), metav1.ListOptions{}); err != nil || len(secrets.Items) != 0 {
		t.Fatal("gateway credential survived caller disconnect")
	}
}

func (s cancelGatewayUpdate) Update(ctx context.Context, _ *unstructured.Unstructured, _ metav1.UpdateOptions, _ ...string) (*unstructured.Unstructured, error) {
	s.cancel()
	return nil, ctx.Err()
}

func TestCancelledGatewayConfigurationCleansUnusedCredential(t *testing.T) {
	_, home, reg, _ := standaloneHandlerStore(t)
	if _, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{}, nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	home.ClusterStore = cancelGatewayUpdate{home.ClusterStore, cancel}
	h := clustersHandler{k: home, reg: reg, namespace: standaloneTestNamespace}
	// Drive the handler with a request carrying the cancelled update context.
	req := gatewayReviewRequest(ctx, t, gatewayCertificateRequest(t))
	rr := gatewayReviewRecorder()
	h.configureGateway(rr, req)
	secrets, err := home.Secrets(standaloneTestNamespace).List(t.Context(), metav1.ListOptions{})
	if err != nil || len(secrets.Items) != 0 {
		t.Fatalf("cancelled request stranded credentials: %v %+v", err, secrets)
	}
}

func TestGatewayConfigurationRejectsTerminatingRegistration(t *testing.T) {
	_, home, reg, _ := standaloneHandlerStore(t)
	registration := newCluster("remote", map[string]any{}, nil)
	now := metav1.Now()
	registration.SetDeletionTimestamp(&now)
	if _, err := home.Clusters().Create(t.Context(), registration, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if rr := doClusters(t, standaloneClustersRouter(home, reg), http.MethodPut, "/clusters/remote/gateway", gatewayCertificateRequest(t)); rr.Code != http.StatusNotFound {
		t.Fatalf("terminating registration accepted: %d", rr.Code)
	}
	if secrets, err := home.Secrets(standaloneTestNamespace).List(t.Context(), metav1.ListOptions{}); err != nil || len(secrets.Items) != 0 {
		t.Fatal("terminating registration received a credential")
	}
}

func gatewayReviewRequest(ctx context.Context, t *testing.T, in clusterGatewayRequest) *http.Request {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	route := chi.NewRouteContext()
	route.URLParams.Add("name", "remote")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
	return httptest.NewRequestWithContext(ctx, http.MethodPut, "/clusters/remote/gateway", bytes.NewReader(data))
}

func gatewayReviewRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }
