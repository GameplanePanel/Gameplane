//go:build unix

package controlplane

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMasterKeyRotationPreservesCredentialsAndRejectsStaleWriter(t *testing.T) {
	store, oldClient, oldPath := managementFixture(t)
	created, err := oldClient.Secrets("panel").Create(t.Context(), credential("provider"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(t.TempDir(), "keys", "rotated.key")
	if err := RotateKey(t.Context(), store, oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	if err := RotateKey(t.Context(), store, oldPath, newPath); err != nil {
		t.Fatalf("retry of committed rotation: %v", err)
	}
	if _, err := New(t.Context(), store, oldPath); err == nil {
		t.Fatal("old key opened rotated store")
	}
	newClient, err := New(t.Context(), store, newPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := newClient.Secrets("panel").Get(t.Context(), "provider", metav1.GetOptions{})
	if err != nil || !bytes.Equal(got.Data["token"], created.Data["token"]) || got.UID != created.UID || got.ResourceVersion != created.ResourceVersion {
		t.Fatalf("rotation changed credential identity/content: %v", err)
	}
	if _, err := oldClient.Secrets("panel").Create(t.Context(), credential("stale"), metav1.CreateOptions{}); err == nil {
		t.Fatal("stale running writer committed old-key ciphertext")
	}
	secret, registration := registrationObjects()
	if _, err := oldClient.RegisterCluster(t.Context(), "panel", secret, registration); err == nil {
		t.Fatal("stale atomic registration writer committed old-key ciphertext")
	}
	if _, err := newClient.Secrets("panel").Create(t.Context(), credential("fresh"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("rotation removed backup key")
	}
}

func TestMasterKeyRotationRollbackRetainsAllOldCiphertext(t *testing.T) {
	store, client, oldPath := managementFixture(t)
	before := map[string]string{}
	for _, name := range []string{"a", "b"} {
		if _, err := client.Secrets("panel").Create(t.Context(), credential(name), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		var payload string
		if err := store.DB.QueryRowContext(t.Context(), `SELECT payload FROM management_objects WHERE name = ?`, name).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		before[name] = payload
	}
	if _, err := store.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_rotation BEFORE UPDATE OF payload ON management_objects WHEN NEW.name = 'b' BEGIN SELECT RAISE(ABORT, 'interrupted rotation'); END`); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(t.TempDir(), "rotated.key")
	if err := RotateKey(t.Context(), store, oldPath, newPath); err == nil {
		t.Fatal("rotation ignored database interruption")
	}
	for name, want := range before {
		var got string
		if err := store.DB.QueryRowContext(t.Context(), `SELECT payload FROM management_objects WHERE name = ?`, name).Scan(&got); err != nil || got != want {
			t.Fatalf("partial rotation persisted for %s: %v", name, err)
		}
	}
	if _, err := New(t.Context(), store, oldPath); err != nil {
		t.Fatalf("rollback lost old key: %v", err)
	}
	if _, err := New(t.Context(), store, newPath); err == nil {
		t.Fatal("rollback activated new key")
	}
	if _, err := store.DB.ExecContext(t.Context(), `DROP TRIGGER reject_rotation`); err != nil {
		t.Fatal(err)
	}
	if err := RotateKey(t.Context(), store, oldPath, newPath); err != nil {
		t.Fatalf("retry with preserved new key: %v", err)
	}
}

func TestMasterKeyRotationRejectsCorruptSourceBeforeCreatingNewKey(t *testing.T) {
	store, client, oldPath := managementFixture(t)
	if _, err := client.Secrets("panel").Create(t.Context(), credential("provider"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(t.Context(), `UPDATE management_objects SET payload = 'corrupt'`); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(t.TempDir(), "new.key")
	if err := RotateKey(t.Context(), store, oldPath, newPath); err == nil {
		t.Fatal("rotated corrupt source")
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("corrupt source caused new key publication")
	}
	var payload string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT payload FROM management_objects WHERE name = 'provider'`).Scan(&payload); err != nil || payload != "corrupt" {
		t.Fatal("rotation changed corrupt source instead of failing closed")
	}
}

func TestMasterKeyRotationAuthenticatesLegacyCiphertext(t *testing.T) {
	store, client, oldPath := managementFixture(t)
	secret, err := client.Secrets("panel").Create(t.Context(), credential("legacy"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	aad := []byte("secrets\x00panel\x00legacy\x00" + string(secret.UID) + "\x001")
	legacy := base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, data, aad))
	if _, err := store.DB.ExecContext(t.Context(), `UPDATE management_objects SET payload = ? WHERE name = 'legacy'`, legacy); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(t.TempDir(), "rotated.key")
	if err := RotateKey(t.Context(), store, oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT payload FROM management_objects WHERE name = 'legacy'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload, "gpk1.") || payload == legacy {
		t.Fatal("rotation did not migrate legacy credential format")
	}
	newClient, err := New(t.Context(), store, newPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newClient.Secrets("panel").Get(t.Context(), "legacy", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestMasterKeyRotationRejectsWrongAndIdenticalKeys(t *testing.T) {
	store, _, oldPath := managementFixture(t)
	wrongPath := filepath.Join(t.TempDir(), "wrong.key")
	if err := os.WriteFile(wrongPath, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(t.TempDir(), "new.key")
	if err := RotateKey(t.Context(), store, wrongPath, newPath); err == nil {
		t.Fatal("wrong old key rotated empty established store")
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("invalid old key caused new key creation")
	}
	if err := RotateKey(t.Context(), store, oldPath, oldPath); err == nil {
		t.Fatal("same key path accepted")
	}
	samePath := filepath.Join(t.TempDir(), "copy.key")
	key, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(samePath, key, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RotateKey(t.Context(), store, oldPath, samePath); err == nil {
		t.Fatal("identical key bytes accepted")
	}
}

func TestMasterKeyRotationSupportsEmptyStoreAndProvisionedNewKey(t *testing.T) {
	store, oldClient, oldPath := managementFixture(t)
	newPath := filepath.Join(t.TempDir(), "external.key")
	newKey := bytes.Repeat([]byte{42}, 32)
	if err := os.WriteFile(newPath, newKey, 0400); err != nil {
		t.Fatal(err)
	}
	if err := RotateKeyWithOptions(t.Context(), store, oldPath, KeyOptions{File: newPath, Provisioned: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := oldClient.Secrets("panel").Create(t.Context(), credential("stale"), metav1.CreateOptions{}); err == nil {
		t.Fatal("empty-store rotation allowed stale writer")
	}
	if _, err := NewWithOptions(t.Context(), store, KeyOptions{File: newPath, Provisioned: true}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(newPath)
	if err != nil || !bytes.Equal(got, newKey) {
		t.Fatal("rotation changed externally provisioned key")
	}
}

func TestEstablishedEmptyStoreNeverReplacesMissingKey(t *testing.T) {
	store, _, path := managementFixture(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := New(t.Context(), store, path); err == nil {
		t.Fatal("replaced missing key of established empty store")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing established key was recreated")
	}
}

func TestCiphertextHeaderCannotBeStrippedOrChangeKeyIdentity(t *testing.T) {
	for _, change := range []string{"strip", "identity"} {
		t.Run(change, func(t *testing.T) {
			store, client, path := managementFixture(t)
			if _, err := client.Secrets("panel").Create(t.Context(), credential("provider"), metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			var payload string
			if err := store.DB.QueryRowContext(t.Context(), `SELECT payload FROM management_objects WHERE name = 'provider'`).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(payload, ".")
			if len(parts) != 3 {
				t.Fatal("credential has no versioned key identity")
			}
			if change == "strip" {
				payload = parts[2]
			} else {
				payload = parts[0] + "." + strings.Repeat("0", 64) + "." + parts[2]
			}
			if _, err := store.DB.ExecContext(t.Context(), `UPDATE management_objects SET payload = ? WHERE name = 'provider'`, payload); err != nil {
				t.Fatal(err)
			}
			if _, err := New(t.Context(), store, path); err == nil {
				t.Fatal("ciphertext header tampering authenticated")
			}
		})
	}
}
