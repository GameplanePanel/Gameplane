//go:build unix

package controlplane

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProvisionedKeyNeverGeneratesMissingFile(t *testing.T) {
	store, _, _ := managementFixture(t)
	path := filepath.Join(t.TempDir(), "operator", "panel.key")
	if _, err := NewWithOptions(t.Context(), store, KeyOptions{File: path, Provisioned: true}); err == nil {
		t.Fatal("started without required provisioned key")
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("provisioned key mode created a directory")
	}
}

func TestProvisionedKeyAcceptsPrivateReadOnlyInputWithoutChangingIt(t *testing.T) {
	store, _, originalPath := managementFixture(t)
	before, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	// The operator's key mount is read-only; the separate database directory
	// stays writable for startup key-state validation and SQLite journaling.
	path := filepath.Join(t.TempDir(), "panel.key")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(path), 0700) })
	if _, err := NewWithOptions(t.Context(), store, KeyOptions{File: path, Provisioned: true}); err != nil {
		t.Fatalf("read-only provisioned key: %v", err)
	}
	after, err := os.ReadFile(path)
	info, statErr := os.Stat(path)
	if err != nil || statErr != nil || !bytes.Equal(before, after) || info.Mode().Perm() != 0400 {
		t.Fatal("provisioned key mode changed its input")
	}
}

func TestLegacyKeyConstructorStillReusesExistingKey(t *testing.T) {
	store, _, path := managementFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(t.Context(), store, path); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithOptions(t.Context(), store, KeyOptions{File: path}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("constructor changed the encryption key")
	}
}
