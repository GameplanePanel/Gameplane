//go:build unix

package controlplane

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestExistingKeyRejectsUnsafePermissionsAndNonRegularFiles(t *testing.T) {
	for _, mode := range []os.FileMode{0644, 0660, 0700, 0444} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "panel.key")
			if err := os.WriteFile(path, make([]byte, 32), mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := readKey(path); err == nil {
				t.Fatal("accepted unsafe key permissions")
			}
		})
	}
	for _, kind := range []string{"directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "panel.key")
			var err error
			if kind == "directory" {
				err = os.Mkdir(path, 0700)
			} else {
				err = unix.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readKey(path); err == nil {
				t.Fatal("accepted nonregular key")
			}
		})
	}
}

func TestExistingKeyRequiresExactly32Bytes(t *testing.T) {
	for _, size := range []int{0, 31, 33, 1 << 20} {
		path := filepath.Join(t.TempDir(), "panel.key")
		if err := os.WriteFile(path, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readKey(path); err == nil {
			t.Fatalf("accepted key size %d", size)
		}
	}
}

func TestProjectedKeySymlinksRemainConfinedAndReadable(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "..2026")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "panel.key"), make([]byte, 32), 0440); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..2026", filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "panel.key")
	if err := os.Symlink("..data/panel.key", path); err != nil {
		t.Fatal(err)
	}
	if key, err := readKey(path); err != nil || len(key) != 32 {
		t.Fatalf("projected key: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.key")
	if err := os.WriteFile(outside, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readKey(path); err == nil {
		t.Fatal("followed a key symlink outside configured directory")
	}
}

func TestKeyOwnershipAllowsProjectedRootOwnedFSGroup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		uid, gid uint32
		mode     os.FileMode
		wantOK   bool
	}{
		{"private-process-key", 65532, 65532, 0600, true},
		{"projected-root-fsgroup", 0, 65532, 0440, true},
		{"foreign-owner", 1234, 65532, 0440, false},
		{"foreign-group", 0, 1234, 0440, false},
		{"world-readable", 0, 65532, 0444, false},
		{"group-writable", 0, 65532, 0460, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateKeyAccess(tc.uid, tc.gid, tc.mode, 65532, []int{65532})
			if (err == nil) != tc.wantOK {
				t.Fatalf("key access accepted=%v want=%v", err == nil, tc.wantOK)
			}
		})
	}
}
