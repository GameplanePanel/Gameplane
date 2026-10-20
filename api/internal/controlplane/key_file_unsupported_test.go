//go:build !unix

package controlplane

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyAccessFailsClosedWithoutPlatformPermissionValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.key")
	if err := os.WriteFile(path, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readKey(path); err == nil {
		t.Fatal("accepted key without platform access validation")
	}
}
