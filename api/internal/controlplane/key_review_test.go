//go:build unix

package controlplane

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGeneratedKeyRejectsSupplementaryReadGroup(t *testing.T) {
	if err := validateKeyAccess(1000, 2000, 0640, 1000, 1000, []int{1000, 2000}); err == nil {
		t.Fatal("accepted generated key readable by a supplementary group")
	}
	if err := validateKeyAccess(0, 2000, 0440, 1000, 1000, []int{1000, 2000}); err != nil {
		t.Fatalf("rejected root-owned fsGroup projection: %v", err)
	}
}

func TestGeneratedKeyRecoversOnlyMatchingPublicationAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.key")
	key := bytes.Repeat([]byte{7}, 32)
	if err := os.WriteFile(path, key, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, ".management-key-stale")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(dir, ".management-key-active")
	if err := os.WriteFile(unrelated, bytes.Repeat([]byte{8}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := loadKey(path, true); err != nil || !bytes.Equal(got, key) {
		t.Fatalf("recover key: %v", err)
	}
	if _, err := os.Stat(alias); !os.IsNotExist(err) {
		t.Fatal("stale key alias survived")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("removed an unrelated publisher's temporary file")
	}
}

func TestGeneratedKeyConcurrentPublishersRetainOneKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.key")
	var workers sync.WaitGroup
	keys := make(chan []byte, 8)
	errors := make(chan error, 8)
	for range 8 {
		workers.Go(func() { key, err := loadKey(path, false); keys <- key; errors <- err })
	}
	workers.Wait()
	close(keys)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first []byte
	for key := range keys {
		if first == nil {
			first = key
		}
		if !bytes.Equal(first, key) {
			t.Fatal("publishers loaded different keys")
		}
	}
	if _, err := readKey(path); err != nil {
		t.Fatalf("published key access: %v", err)
	}
}

func TestRotationRejectsUninitializedEmptyStoreWithoutMutation(t *testing.T) {
	store, _, oldPath := managementFixture(t)
	if _, err := store.DB.ExecContext(t.Context(), `DELETE FROM management_key_state`); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(t.TempDir(), "new.key")
	if err := RotateKey(t.Context(), store, oldPath, newPath); err == nil {
		t.Fatal("adopted an uninitialized empty database")
	}
	var count int
	if err := store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM management_key_state`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed rotation changed key state")
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("failed source validation created a new key")
	}
}

func TestRotationAuthenticatesPopulatedStoreWithoutKeyState(t *testing.T) {
	store, client, oldPath := managementFixture(t)
	if _, err := client.Secrets("panel").Create(t.Context(), credential("legacy-state"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(t.Context(), `DELETE FROM management_key_state`); err != nil {
		t.Fatal(err)
	}
	if err := RotateKey(t.Context(), store, oldPath, filepath.Join(t.TempDir(), "new.key")); err != nil {
		t.Fatalf("authenticated migration: %v", err)
	}
}

func TestStartupCredentialErrorIdentifiesObjectWithoutPayload(t *testing.T) {
	store, client, keyPath := managementFixture(t)
	if _, err := client.Secrets("panel").Create(t.Context(), credential("broken-credential"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(t.Context(), `UPDATE management_objects SET payload = 'private-broken-payload' WHERE name = 'broken-credential'`); err != nil {
		t.Fatal(err)
	}
	_, err := New(t.Context(), store, keyPath)
	if err == nil || !strings.Contains(err.Error(), "secrets/panel/broken-credential") || strings.Contains(err.Error(), "private-broken-payload") {
		t.Fatalf("unsafe or unidentified failure: %v", err)
	}
	var permanent interface{ PermanentCredentialFailure() bool }
	if !errors.As(err, &permanent) || !permanent.PermanentCredentialFailure() {
		t.Fatal("authenticated storage corruption was classified as transient")
	}
}

func TestTypedCredentialCorruptionIsPermanent(t *testing.T) {
	_, err := asSecret(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "malformed", "namespace": "panel"},
		"data":     map[string]any{"kubeconfig": 42},
	}}, nil)
	var permanent interface{ PermanentCredentialFailure() bool }
	if err == nil || !errors.As(err, &permanent) || !permanent.PermanentCredentialFailure() {
		t.Fatalf("typed credential corruption was classified as transient: %v", err)
	}
}
