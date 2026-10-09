package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func managementFixture(t *testing.T) (*db.Store, *kube.Client, string) {
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
	keyFile := filepath.Join(dir, "management.key")
	client, err := New(t.Context(), store, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	return store, client, keyFile
}

func credential(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"managed": "true"}},
		StringData: map[string]string{"token": "very-private-test-credential"},
	}
}

func TestManagementKeyRejectsInvalidFilesWithoutReplacingThem(t *testing.T) {
	for _, size := range []int{0, 31, 33} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "panel.key")
			original := make([]byte, size)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadKey(path, false); err == nil {
				t.Fatal("accepted an invalid existing key")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(original) {
				t.Fatal("replaced invalid key instead of failing closed")
			}
		})
	}
}

func TestManagementKeyCreatesNestedDirectoryAndReusesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "keys", "panel.key")
	first, err := loadKey(path, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadKey(path, true)
	if err != nil || len(first) != 32 || string(second) != string(first) {
		t.Fatalf("key did not survive reopen: %v", err)
	}
}

func TestCredentialsPersistEncryptedAndRemainNamespaceIsolated(t *testing.T) {
	store, client, keyFile := managementFixture(t)
	created, err := client.Secrets("panel").Create(t.Context(), credential("provider"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if created.UID == "" || created.ResourceVersion != "1" || len(created.StringData) != 0 {
		t.Fatalf("missing persisted identity: %#v", created.ObjectMeta)
	}
	var payload string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT payload FROM management_objects WHERE name = 'provider'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"very-private-test-credential", base64.StdEncoding.EncodeToString(created.Data["token"]), `"token"`} {
		if strings.Contains(payload, forbidden) {
			t.Fatal("credential data stored without encryption")
		}
	}
	keyBefore, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := New(t.Context(), store, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	keyAfter, err := os.ReadFile(keyFile)
	if err != nil || string(keyBefore) != string(keyAfter) {
		t.Fatal("startup changed the encryption key")
	}
	got, err := reopened.Secrets("panel").Get(t.Context(), "provider", metav1.GetOptions{})
	if err != nil || string(got.Data["token"]) != "very-private-test-credential" || got.UID != created.UID {
		t.Fatalf("credential did not survive reopen: %v", err)
	}
	if _, err := reopened.Secrets("other").Get(t.Context(), "provider", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("namespace leak: %v", err)
	}
	if _, err := reopened.Secrets("other").Create(t.Context(), credential("provider"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	list, err := reopened.Secrets("panel").List(t.Context(), metav1.ListOptions{LabelSelector: "managed=true"})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("label/namespace selection failed: %v", err)
	}
}

func TestCredentialUpdatesAndDeletesUseOptimisticConcurrency(t *testing.T) {
	_, client, _ := managementFixture(t)
	secrets := client.Secrets("panel")
	first, err := secrets.Create(t.Context(), credential("provider"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Create(t.Context(), credential("provider"), metav1.CreateOptions{}); !apierrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create: %v", err)
	}
	changed := first.DeepCopy()
	changed.Data["token"] = []byte("rotated")
	second, err := secrets.Update(t.Context(), changed, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.UID != first.UID || second.ResourceVersion != "2" {
		t.Fatal("update lost identity/version")
	}
	if _, err := secrets.Update(t.Context(), first, metav1.UpdateOptions{}); !apierrors.IsConflict(err) {
		t.Fatalf("stale update accepted: %v", err)
	}
	if err := secrets.Delete(t.Context(), "provider", metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &first.ResourceVersion}}); !apierrors.IsConflict(err) {
		t.Fatalf("stale delete accepted: %v", err)
	}
	wrong := types.UID("wrong")
	if err := secrets.Delete(t.Context(), "provider", metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &wrong}}); !apierrors.IsConflict(err) {
		t.Fatalf("wrong identity delete accepted: %v", err)
	}
	if err := secrets.Delete(t.Context(), "provider", metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &second.UID, ResourceVersion: &second.ResourceVersion}}); err != nil {
		t.Fatal(err)
	}
	recreated, err := secrets.Create(t.Context(), credential("provider"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if recreated.UID == first.UID {
		t.Fatal("recreation reused deleted identity")
	}
	if _, err := secrets.Update(t.Context(), first, metav1.UpdateOptions{}); !apierrors.IsConflict(err) {
		t.Fatalf("old object replaced recreated credential: %v", err)
	}
}

func TestStartupFailsClosedOnMissingWrongKeyAndCorruptCiphertext(t *testing.T) {
	for _, damage := range []string{"missing-key", "wrong-key", "ciphertext"} {
		t.Run(damage, func(t *testing.T) {
			store, client, keyFile := managementFixture(t)
			if _, err := client.Secrets("panel").Create(t.Context(), credential("provider"), metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "missing-key":
				if err := os.Remove(keyFile); err != nil {
					t.Fatal(err)
				}
			case "wrong-key":
				if err := os.WriteFile(keyFile, make([]byte, 32), 0600); err != nil {
					t.Fatal(err)
				}
			case "ciphertext":
				if _, err := store.DB.ExecContext(t.Context(), `UPDATE management_objects SET payload = 'corrupt'`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := New(t.Context(), store, keyFile); err == nil {
				t.Fatal("startup accepted inaccessible credential state")
			}
			if damage == "missing-key" {
				if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
					t.Fatal("startup replaced a missing key over existing credentials")
				}
			}
		})
	}
}

func TestCiphertextCannotBeMovedBetweenCredentialIdentities(t *testing.T) {
	store, client, _ := managementFixture(t)
	for _, name := range []string{"one", "two"} {
		if _, err := client.Secrets("panel").Create(t.Context(), credential(name), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB.ExecContext(t.Context(), `UPDATE management_objects SET payload = (SELECT payload FROM management_objects WHERE name = 'one') WHERE name = 'two'`); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Secrets("panel").Get(t.Context(), "two", metav1.GetOptions{}); err == nil {
		t.Fatal("swapped ciphertext authenticated")
	}
}

func registration(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "Cluster",
		"metadata": map[string]any{"name": name},
		"spec":     map[string]any{"kubeconfigSecret": map[string]any{"name": "remote-config", "key": "kubeconfig"}},
	}}
}

func TestStandaloneRegistryIsEmptyAndRegistrationsPersistWithPagination(t *testing.T) {
	store, client, keyFile := managementFixture(t)
	reg := kube.NewRegistry("")
	reg.SetManagement(client)
	if len(reg.IDs()) != 0 || reg.Default() != nil || reg.Management() != client || client.Typed != nil || client.Dynamic != nil || client.Config != nil {
		t.Fatal("standalone panel became a workload cluster")
	}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := client.Clusters().Create(t.Context(), registration(name), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := New(t.Context(), store, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reopened.Clusters().List(t.Context(), metav1.ListOptions{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].GetName() != "alpha" || first.GetContinue() == "" {
		t.Fatalf("first page: %v", err)
	}
	second, err := reopened.Clusters().List(t.Context(), metav1.ListOptions{Limit: 1, Continue: first.GetContinue()})
	if err != nil || len(second.Items) != 1 || second.Items[0].GetName() != "beta" || second.GetContinue() != "" {
		t.Fatalf("second page: %v", err)
	}
	stale := first.Items[0].DeepCopy()
	current := stale.DeepCopy()
	current.Object["status"] = map[string]any{"phase": "Healthy"}
	if _, err := reopened.Clusters().Update(t.Context(), current, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Clusters().Update(t.Context(), stale, metav1.UpdateOptions{}); !apierrors.IsConflict(err) {
		t.Fatalf("stale registration update: %v", err)
	}
}

func TestStandaloneWatcherReloadsAndPersistsRemoteHealth(t *testing.T) {
	store, client, keyFile := managementFixture(t)
	server := privateTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			t.Errorf("unexpected remote path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"gitVersion":"v1.35.0"}`))
	}))
	defer server.Close()
	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	config := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: remote
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: remote
  context:
    cluster: remote
    user: panel
current-context: remote
users:
- name: panel
  user:
    token: private-remote-token
`, server.URL, ca)
	if _, err := kube.ValidateStandaloneKubeconfig([]byte(config)); err != nil {
		t.Fatalf("remote health fixture violates standalone access policy: %v", err)
	}
	_, err := client.Secrets("panel").Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "remote-config", Labels: map[string]string{kube.ClusterKubeconfigLabel: "true"}},
		Data:       map[string][]byte{"kubeconfig": []byte(config)},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Clusters().Create(t.Context(), registration("remote"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	client, err = New(t.Context(), store, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	reg := kube.NewRegistry("")
	reg.SetManagement(client)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); kube.WatchClusters(ctx, client, reg, "panel") }()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("watcher did not restore registration and health")
		case <-ticker.C:
			obj, err := client.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			if phase != "Healthy" {
				continue
			}
			version, _, _ := unstructured.NestedString(obj.Object, "status", "serverVersion")
			if version != "v1.35.0" {
				t.Fatalf("wrong remote version: %s", version)
			}
			if _, ok := reg.Get("remote"); !ok {
				continue
			}
			if reg.Default() != nil {
				t.Fatal("watcher installed a local workload client")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("watcher did not stop on cancellation")
			}
			return
		}
	}
}
