//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/controlplane"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRotatePanelKeyRejectsIncompleteArgumentsBeforeDatabaseWrites(t *testing.T) {
	t.Setenv("GAMEPLANE_PANEL_KEY_FILE", "")
	for _, args := range [][]string{
		{},
		{"--old-key-file=old.key"},
		{"--new-key-file=new.key"},
		{"--old-key-file=old.key", "--new-key-file=new.key", "unexpected"},
	} {
		path := filepath.Join(t.TempDir(), "panel.db")
		full := append([]string{"--db-driver=sqlite", "--db-dsn=" + path}, args...)
		if err := rotatePanelKey(t.Context(), full); err == nil {
			t.Fatal("accepted incomplete rotation arguments")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("invalid command arguments caused a database write")
		}
	}
}

func TestRotatePanelKeyCommandUsesConfiguredDatabaseAndSeparateKeyPaths(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "panel.db")
	oldPath := filepath.Join(dir, "old.key")
	newPath := filepath.Join(dir, "new.key")
	store, err := db.Open(t.Context(), "sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	client, err := controlplane.New(t.Context(), store, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Secrets("panel").Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "provider"}, StringData: map[string]string{"token": "private-test-token"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAMEPLANE_DB_DRIVER", "sqlite")
	t.Setenv("GAMEPLANE_DB_DSN", dbPath)
	args := []string{"--old-key-file=" + oldPath, "--new-key-file=" + newPath}
	if err := rotatePanelKey(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	rotated, err := controlplane.New(t.Context(), store, newPath)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := rotated.Secrets("panel").Get(t.Context(), "provider", metav1.GetOptions{})
	if err != nil || string(secret.Data["token"]) != "private-test-token" {
		t.Fatalf("command failed to preserve credential: %v", err)
	}
}

func TestRotatePanelKeyMissingProvisionedReplacementLeavesOldStateUsable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "panel.db")
	oldPath := filepath.Join(dir, "old.key")
	newPath := filepath.Join(dir, "external", "new.key")
	store, err := db.Open(t.Context(), "sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := controlplane.New(t.Context(), store, oldPath); err != nil {
		t.Fatal(err)
	}
	args := []string{"--db-driver=sqlite", "--db-dsn=" + dbPath, "--old-key-file=" + oldPath, "--new-key-file=" + newPath, "--new-key-provisioned"}
	if err := rotatePanelKey(t.Context(), args); err == nil {
		t.Fatal("generated missing provisioned replacement key")
	}
	if _, err := os.Stat(filepath.Dir(newPath)); !os.IsNotExist(err) {
		t.Fatal("provisioned rotation created key directory")
	}
	if _, err := controlplane.New(t.Context(), store, oldPath); err != nil {
		t.Fatalf("failed provisioned rotation retired old key: %v", err)
	}
}
