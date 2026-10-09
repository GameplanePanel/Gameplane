package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/controlplane"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

const standaloneTestNamespace = "gameplane-system"
const standaloneTestKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: remote
  cluster:
    server: https://remote.example.invalid:6443
contexts:
- name: remote
  context:
    cluster: remote
    user: panel
current-context: remote
users:
- name: panel
  user:
    token: secret-standalone-registration-token
`

func standaloneHandlerStore(t *testing.T) (*db.Store, *kube.Client, *kube.Registry, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(t.Context(), "sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "credentials.key")
	home, err := controlplane.New(t.Context(), store, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	reg := kube.NewRegistry("local")
	reg.SetManagement(home)
	return store, home, reg, keyPath
}

func standaloneClustersRouter(home *kube.Client, reg *kube.Registry) http.Handler {
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountClusters(r, reg, home, standaloneTestNamespace)
	return r
}

func assertManagementCiphertext(t *testing.T, store *db.Store, name string, sensitive ...string) {
	t.Helper()
	var payload string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT payload FROM management_objects WHERE kind = 'secrets' AND namespace = ? AND name = ?`, standaloneTestNamespace, name).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if json.Valid([]byte(payload)) {
		t.Fatal("credential stored as plaintext JSON")
	}
	for _, value := range sensitive {
		if strings.Contains(payload, value) || strings.Contains(payload, base64.StdEncoding.EncodeToString([]byte(value))) {
			t.Fatal("credential content stored without encryption")
		}
	}
}

func TestStandaloneClusterHTTPRegistrationPersistsEncryptedAndDeletesCleanly(t *testing.T) {
	store, home, reg, keyPath := standaloneHandlerStore(t)
	router := standaloneClustersRouter(home, reg)
	empty := doClusters(t, router, http.MethodGet, "/clusters/", nil)
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != `{"items":[]}` {
		t.Fatalf("new standalone discovery: %d %s", empty.Code, empty.Body)
	}
	created := doClusters(t, router, http.MethodPost, "/clusters/", clusterCreateReq{Name: "remote", DisplayName: "Remote cluster", Kubeconfig: standaloneTestKubeconfig})
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	if strings.Contains(created.Body.String(), "secret-standalone") || strings.Contains(created.Body.String(), "kubeconfig") {
		t.Fatal("registration response exposed credentials")
	}
	assertManagementCiphertext(t, store, clusterKubeconfigSecretName("remote"), standaloneTestKubeconfig, "secret-standalone-registration-token")
	first, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	home, err = controlplane.New(t.Context(), store, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	reg = kube.NewRegistry("local")
	reg.SetManagement(home)
	router = standaloneClustersRouter(home, reg)
	stored, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), clusterKubeconfigSecretName("remote"), metav1.GetOptions{})
	if err != nil || string(stored.Data["kubeconfig"]) != standaloneTestKubeconfig {
		t.Fatalf("reopened credential: %v", err)
	}
	registration, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil || registration.GetUID() != first.GetUID() {
		t.Fatalf("reopened registration identity: %v", err)
	}
	listed := doClusters(t, router, http.MethodGet, "/clusters/", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listed.Code, listed.Body)
	}
	var out clustersListResp
	if err := json.Unmarshal(listed.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].Name != "remote" || out.Items[0].DisplayName != "Remote cluster" {
		t.Fatalf("unexpected discovery: %+v", out.Items)
	}
	for _, forbidden := range []string{"secret-standalone", "kubeconfig", "remote.example.invalid", `"name":"local"`} {
		if strings.Contains(listed.Body.String(), forbidden) {
			t.Fatalf("discovery leaked %q", forbidden)
		}
	}
	if reg.Default() != nil || home.Typed != nil || home.Dynamic != nil {
		t.Fatal("management backend became a workload cluster")
	}
	deleted := doClusters(t, router, http.MethodDelete, "/clusters/remote", nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body)
	}
	if _, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("registration survived delete: %v", err)
	}
	if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), clusterKubeconfigSecretName("remote"), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("kubeconfig survived delete: %v", err)
	}
}

func TestStandaloneRegistrationRejectsRemoteOnlyWorkloadPermissions(t *testing.T) {
	for _, permission := range []string{"servers:write", "cluster:read"} {
		t.Run(permission, func(t *testing.T) {
			_, home, reg, _ := standaloneHandlerStore(t)
			router := standaloneClustersRouter(home, reg)
			created := doClusters(t, router, http.MethodPost, "/clusters/", clusterCreateReq{Name: "remote", Kubeconfig: standaloneTestKubeconfig})
			if created.Code != http.StatusCreated {
				t.Fatalf("fixture registration: %d %s", created.Code, created.Body)
			}
			user := &auth.User{Perms: map[string]map[string]map[string]struct{}{"remote": {"*": {permission: {}}}}}
			for _, request := range []struct{ method, path string }{
				{http.MethodPost, "/clusters/?cluster=remote"},
				{http.MethodDelete, "/clusters/remote?cluster=remote"},
				{http.MethodPut, "/clusters/remote/gateway?cluster=remote"},
				{http.MethodDelete, "/clusters/remote/gateway?cluster=remote"},
			} {
				// Intentionally invalid mutation body: authorization must run first.
				req := httptest.NewRequestWithContext(auth.WithUser(t.Context(), user), request.method, request.path, bytes.NewBufferString(`{}`))
				rr := httptest.NewRecorder()
				router.ServeHTTP(rr, req)
				if rr.Code != http.StatusForbidden {
					t.Errorf("remote-only %s allowed %s %s: %d %s", permission, request.method, request.path, rr.Code, rr.Body)
				}
			}
			if _, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{}); err != nil {
				t.Fatalf("denied mutation changed registration: %v", err)
			}
		})
	}
}

type replaceCredentialBeforeDelete struct {
	kube.SecretStore
	replace func(context.Context, string) error
}

func (s *replaceCredentialBeforeDelete) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	if err := s.replace(ctx, name); err != nil {
		return err
	}
	return s.SecretStore.Delete(ctx, name, opts)
}

func TestStandaloneCredentialCleanupDoesNotDeleteConcurrentReplacement(t *testing.T) {
	for _, feature := range []string{clusterGatewayLabel, kube.ClusterKubeconfigLabel} {
		t.Run(feature, func(t *testing.T) {
			_, home, _, _ := standaloneHandlerStore(t)
			name := gatewaySecretName("remote")
			if feature == kube.ClusterKubeconfigLabel {
				name = clusterKubeconfigSecretName("remote")
			}
			underlying := home.Secrets(standaloneTestNamespace)
			_, err := underlying.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{feature: "true", ManagedByLabel: managedByValue}}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			home.SecretStore = func(string) kube.SecretStore {
				return &replaceCredentialBeforeDelete{SecretStore: underlying, replace: func(ctx context.Context, name string) error {
					if err := underlying.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
						return err
					}
					_, err := underlying.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name}, StringData: map[string]string{"owner": "external-replacement"}}, metav1.CreateOptions{})
					return err
				}}
			}
			if feature == kube.ClusterKubeconfigLabel {
				err = deleteClusterKubeconfigSecret(t.Context(), home, standaloneTestNamespace, "remote", name)
			} else {
				err = deleteManagedSecret(t.Context(), home, standaloneTestNamespace, name, feature)
			}
			if !apierrors.IsConflict(err) {
				t.Fatalf("cleanup did not reject replaced credential: %v", err)
			}
			replacement, err := underlying.Get(t.Context(), name, metav1.GetOptions{})
			if err != nil || string(replacement.Data["owner"]) != "external-replacement" {
				t.Fatalf("replacement was deleted: %v", err)
			}
		})
	}
}
