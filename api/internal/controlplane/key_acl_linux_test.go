//go:build linux

package controlplane

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func installNamedReadACL(t *testing.T, f *os.File, tag uint16) {
	t.Helper()
	installNamedACL(t, f, tag, "system.posix_acl_access")
}

func installNamedACL(t *testing.T, f *os.File, tag uint16, attribute string) {
	t.Helper()
	// Linux's POSIX ACL xattr ABI: version 2 and little-endian 8-byte entries.
	type aclEntry struct {
		tag, permission uint16
		id              uint32
	}
	id := uint32(1)
	if (tag == 2 && os.Geteuid() == 1) || (tag == 8 && os.Getegid() == 1) {
		id = 2
	}
	entries := []aclEntry{{1, 6, ^uint32(0)}} // owner rw
	if tag == 2 {
		entries = append(entries, aclEntry{tag, 4, id})
	}
	entries = append(entries, aclEntry{4, 4, ^uint32(0)}) // owning group r
	if tag == 8 {
		entries = append(entries, aclEntry{tag, 4, id})
	}
	entries = append(entries,
		aclEntry{16, 4, ^uint32(0)}, // mask r: st_mode still reports 0640
		aclEntry{32, 0, ^uint32(0)}, // other none
	)
	acl := make([]byte, 4+8*len(entries))
	binary.LittleEndian.PutUint32(acl, 2)
	for i, entry := range entries {
		b := acl[4+i*8:]
		binary.LittleEndian.PutUint16(b, entry.tag)
		binary.LittleEndian.PutUint16(b[2:], entry.permission)
		binary.LittleEndian.PutUint32(b[4:], entry.id)
	}
	if err := unix.Fsetxattr(int(f.Fd()), attribute, acl, 0); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skip("test filesystem cannot install POSIX access ACLs")
		}
		t.Fatalf("install named read ACL: %v", err)
	}
}

func TestGeneratedKeyRejectsInheritedExtendedAccessACL(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	installNamedACL(t, f, 2, "system.posix_acl_default")
	path := filepath.Join(dir, "panel.key")
	if _, err := loadKey(path, false); err == nil || !strings.Contains(err.Error(), "ACL") {
		t.Fatalf("generated key bypassed ACL validation: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("fixture must publish a private-mode key with inherited ACL: %v", err)
	}
	if _, err := readKey(path); err == nil {
		t.Fatal("inherited ACL would also bypass subsequent startup validation")
	}
}

func TestExistingKeyRejectsNamedACLReadersWithSafeModeAndGroup(t *testing.T) {
	for _, tag := range []uint16{2, 8} { // named user and named group
		t.Run(map[uint16]string{2: "user", 8: "group"}[tag], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "panel.key")
			f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if _, err := f.Write(make([]byte, 32)); err != nil {
				t.Fatal(err)
			}
			installNamedReadACL(t, f, tag)
			info, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0640 || validateKeyFile(info) != nil {
				t.Fatal("ACL fixture must pass the prior mode/ownership checks")
			}
			if _, err := readKey(path); err == nil || !strings.Contains(err.Error(), "ACL") {
				t.Fatalf("accepted a named ACL reader: %v", err)
			}
		})
	}
}

func TestKeyACLValidationUsesOpenedInode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.key")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	installNamedReadACL(t, f, 2)
	if err := os.Rename(path, filepath.Join(dir, "replaced.key")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateKeyACL(f); err == nil {
		t.Fatal("ACL inspection followed the replaced path instead of its handle")
	}
	if _, err := readKey(path); err != nil {
		t.Fatalf("safe replacement key rejected: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validateKeyACL(f); err == nil {
		t.Fatal("uninspectable key ACL was accepted")
	}
}

func TestProvisionedProjectedKeyWithoutExtendedACLRemainsReadable(t *testing.T) {
	dir := t.TempDir()
	versionDir := filepath.Join(dir, "..version")
	if err := os.Mkdir(versionDir, 0700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{42}, 32)
	if err := os.WriteFile(filepath.Join(versionDir, "panel.key"), key, 0440); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..version", filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "panel.key")
	if err := os.Symlink("..data/panel.key", path); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfiguredKey(KeyOptions{File: path, Provisioned: true}, true)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("projected provisioned key rejected: %v", err)
	}
}
