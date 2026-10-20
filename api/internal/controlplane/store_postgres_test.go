//go:build postgres

package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Exercise management SQL through the actual PostgreSQL driver, including
// placeholder rebinding, optimistic writes and authenticated persistence.
func TestPostgresManagementPersistence(t *testing.T) {
	dsn := os.Getenv("GAMEPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GAMEPLANE_TEST_POSTGRES_DSN not set")
	}
	ctx := t.Context()
	admin, err := db.Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "gp_management_" + hex.EncodeToString(suffix[:])
	if _, err := admin.DB.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.DB.ExecContext(context.WithoutCancel(ctx), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop management schema: %v", err)
		}
	})
	if strings.Contains(dsn, "://") {
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		dsn += separator + "search_path=" + schema
	} else {
		dsn += " search_path=" + schema
	}
	store, err := db.Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "panel.key")
	client, err := New(ctx, store, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Secrets("panel").Create(ctx, credential("provider"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Secrets("panel").Create(ctx, credential("provider"), metav1.CreateOptions{}); !apierrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate credential: %v", err)
	}
	changed := first.DeepCopy()
	changed.Data["token"] = []byte("rotated-postgres-token")
	updated, err := client.Secrets("panel").Update(ctx, changed, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Secrets("panel").Update(ctx, first, metav1.UpdateOptions{}); !apierrors.IsConflict(err) {
		t.Fatalf("stale credential update: %v", err)
	}
	if _, err := client.Clusters().Create(ctx, registration("remote"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(ctx, store, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	list, err := reopened.Secrets("panel").List(ctx, metav1.ListOptions{LabelSelector: "managed=true"})
	if err != nil || len(list.Items) != 1 || string(list.Items[0].Data["token"]) != "rotated-postgres-token" {
		t.Fatalf("reopened credentials: %v", err)
	}
	if _, err := reopened.Secrets("other").Get(ctx, "provider", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("cross-namespace credential: %v", err)
	}
	if _, err := reopened.Clusters().Get(ctx, "remote", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Secrets("panel").Delete(ctx, "provider", metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &first.ResourceVersion}}); !apierrors.IsConflict(err) {
		t.Fatalf("stale credential deletion: %v", err)
	}
	if err := reopened.Secrets("panel").Delete(ctx, "provider", metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &updated.UID, ResourceVersion: &updated.ResourceVersion}}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Clusters().Delete(ctx, "remote", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	registrations, err := reopened.Clusters().List(ctx, metav1.ListOptions{})
	if err != nil || len(registrations.Items) != 0 {
		t.Fatalf("deleted registrations remain: %v", err)
	}
}
