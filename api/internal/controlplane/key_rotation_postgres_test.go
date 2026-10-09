//go:build postgres && unix

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPostgresKeyRotationRejectsStaleWriterAcrossConnections(t *testing.T) {
	dsn := os.Getenv("GAMEPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GAMEPLANE_TEST_POSTGRES_DSN not set")
	}
	admin, err := db.Open(t.Context(), "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "gp_rotation_" + hex.EncodeToString(suffix[:])
	if _, err := admin.DB.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.DB.ExecContext(context.WithoutCancel(t.Context()), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
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
	store, err := db.Open(t.Context(), "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(t.TempDir(), "old.key")
	oldClient, err := New(t.Context(), store, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldClient.Secrets("panel").Create(t.Context(), credential("provider"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	rotator, err := db.Open(t.Context(), "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rotator.Close() })
	newPath := filepath.Join(t.TempDir(), "new.key")
	if err := RotateKey(t.Context(), rotator, oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	if _, err := oldClient.Secrets("panel").Create(t.Context(), credential("stale"), metav1.CreateOptions{}); err == nil {
		t.Fatal("independent stale PostgreSQL writer committed old-key credential")
	}
	newClient, err := New(t.Context(), rotator, newPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newClient.Secrets("panel").Get(t.Context(), "provider", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := RotateKey(t.Context(), rotator, oldPath, newPath); err != nil {
		t.Fatalf("PostgreSQL committed rotation retry: %v", err)
	}
}
