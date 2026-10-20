package controlplane

import (
	"bytes"
	"context"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"modernc.org/sqlite"
)

func registrationObjects() (*corev1.Secret, *unstructured.Unstructured) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cluster-remote-kubeconfig", Namespace: "panel", Labels: map[string]string{kube.ClusterKubeconfigLabel: "true", "gameplane.local/managed-by": "gameplane-api"}}, Data: map[string][]byte{"kubeconfig": []byte("registration-credential")}}
	registration := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "Cluster", "metadata": map[string]any{"name": "remote"}, "spec": map[string]any{"kubeconfigSecret": map[string]any{"name": secret.Name, "key": "kubeconfig"}}}}
	return secret, registration
}

func TestRegistrationCancellationAfterCredentialWriteRollsBack(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	function := fmt.Sprintf("cancel_registration_%d", time.Now().UnixNano())
	inserted := false
	if err := sqlite.RegisterScalarFunction(function, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		inserted = true
		cancel()
		return int64(0), nil
	}); err != nil {
		t.Fatal(err)
	}
	store, client, _ := managementFixture(t)
	if _, err := store.DB.ExecContext(t.Context(), `CREATE TRIGGER cancel_registration AFTER INSERT ON management_objects WHEN NEW.kind = 'secrets' BEGIN SELECT `+function+`(); END`); err != nil {
		t.Fatal(err)
	}
	secret, registration := registrationObjects()
	if _, err := client.RegisterCluster(ctx, "panel", secret, registration); err == nil || !inserted {
		t.Fatalf("registration must cancel after the credential insert: inserted=%v err=%v", inserted, err)
	}
	var count int
	if err := store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM management_objects`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cancelled transaction persisted objects: count=%d err=%v", count, err)
	}
}

func TestRegistrationTransactionRollsBackCredentialOnRegistrationFailure(t *testing.T) {
	store, client, _ := managementFixture(t)
	// Fail the second database write, after the credential has been inserted.
	if _, err := store.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_registration BEFORE INSERT ON management_objects WHEN NEW.kind = 'clusters' BEGIN SELECT RAISE(ABORT, 'registration rejected'); END`); err != nil {
		t.Fatal(err)
	}
	secret, registration := registrationObjects()
	if _, err := client.RegisterCluster(t.Context(), "panel", secret, registration); err == nil {
		t.Fatal("registration unexpectedly succeeded")
	}
	if _, err := client.Secrets("panel").Get(t.Context(), secret.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("transaction left an orphan credential: %v", err)
	}
	if _, err := client.Clusters().Get(t.Context(), "remote", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("transaction left a registration: %v", err)
	}
}

func TestRegistrationCancellationAndConflictLeaveNoCredential(t *testing.T) {
	_, client, _ := managementFixture(t)
	secret, registration := registrationObjects()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.RegisterCluster(ctx, "panel", secret, registration); err == nil {
		t.Fatal("cancelled registration succeeded")
	}
	if _, err := client.Clusters().Create(t.Context(), registration, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RegisterCluster(t.Context(), "panel", secret, registration); !apierrors.IsAlreadyExists(err) {
		t.Fatalf("existing registration must conflict: %v", err)
	}
	if _, err := client.Secrets("panel").Get(t.Context(), secret.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("cancel/conflict left a credential: %v", err)
	}
}

func TestRegistrationRecoversOnlyMatchingLegacyOwnedOrphan(t *testing.T) {
	for _, mutation := range []string{"matching", "external", "different-credential", "different-name"} {
		t.Run(mutation, func(t *testing.T) {
			_, client, _ := managementFixture(t)
			secret, registration := registrationObjects()
			orphan := secret.DeepCopy()
			switch mutation {
			case "external":
				delete(orphan.Labels, "gameplane.local/managed-by")
			case "different-credential":
				orphan.Data["kubeconfig"] = []byte("unrelated-credential")
			case "different-name":
				secret.Name, orphan.Name = "external-kubeconfig", "external-kubeconfig"
				_ = unstructured.SetNestedField(registration.Object, secret.Name, "spec", "kubeconfigSecret", "name")
			}
			created, err := client.Secrets("panel").Create(t.Context(), orphan, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			registered, err := client.RegisterCluster(t.Context(), "panel", secret, registration)
			if mutation == "matching" {
				if err != nil || registered.GetUID() == "" {
					t.Fatalf("legacy orphan was not recovered: %v", err)
				}
			} else if err == nil {
				t.Fatal("adopted unrelated credential")
			}
			current, err := client.Secrets("panel").Get(t.Context(), orphan.Name, metav1.GetOptions{})
			if err != nil || current.UID != created.UID || current.ResourceVersion != created.ResourceVersion || !bytes.Equal(current.Data["kubeconfig"], created.Data["kubeconfig"]) {
				t.Fatal("orphan recovery modified the existing secret")
			}
		})
	}
}
