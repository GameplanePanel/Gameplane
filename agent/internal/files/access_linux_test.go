package files

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// A named ACL user need not exist in passwd. Keep the fixture different from
// the current UID without overflow or producing the reserved ACL ID sentinel.
func otherACLUserID() uint32 {
	if os.Getuid() == 1 {
		return 2
	}
	return 1
}

func TestReplacementACLRetainsOriginalOwnerWithoutBroadeningOthers(t *testing.T) {
	for _, mode := range []uint32{0o600, 0o644} {
		st := unix.Stat_t{Uid: 1000, Mode: mode}
		got, err := replacementACL(&st, nil, 65532)
		if err != nil {
			t.Fatal(err)
		}
		// The new mask admits the original owner's rw, while group/other
		// remain exactly as restrictive as the original file.
		want := []aclEntry{{aclUserObj, 6, aclUndefined}, {aclUser, 6, 1000}, {aclGroupObj, uint16(mode >> 3 & 7), aclUndefined}, {aclMask, 6, aclUndefined}, {aclOther, uint16(mode & 7), aclUndefined}}
		entries, err := decodeACL(got)
		if err != nil || !reflect.DeepEqual(entries, want) {
			t.Fatalf("mode %o: entries=%v err=%v", mode, entries, err)
		}
	}
}

func TestReplacementACLPreservesEffectiveNamedPermissions(t *testing.T) {
	// Old mask r-- hides write/execute bits on named entries. Widening the
	// mask for the old owner's rw must not revive those hidden grants.
	original := encodeACL([]aclEntry{{aclUserObj, 6, aclUndefined}, {aclUser, 7, 2000}, {aclGroupObj, 7, aclUndefined}, {aclGroup, 6, 3000}, {aclMask, 4, aclUndefined}, {aclOther, 0, aclUndefined}})
	got, err := replacementACL(&unix.Stat_t{Uid: 1000, Mode: 0o640}, original, 65532)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := decodeACL(got)
	want := []aclEntry{{aclUserObj, 6, aclUndefined}, {aclUser, 6, 1000}, {aclUser, 4, 2000}, {aclGroupObj, 4, aclUndefined}, {aclGroup, 4, 3000}, {aclMask, 6, aclUndefined}, {aclOther, 0, aclUndefined}}
	if err != nil || !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func TestReplacementACLSameOwnerUnchanged(t *testing.T) {
	st := unix.Stat_t{Uid: 1000, Mode: 0o600}
	got, err := replacementACL(&st, nil, 1000)
	if err != nil || len(got) != 0 {
		t.Fatalf("acl=%v err=%v", got, err)
	}
	raw := encodeACL([]aclEntry{{aclUserObj, 6, aclUndefined}, {aclUser, 4, 2000}, {aclGroupObj, 0, aclUndefined}, {aclMask, 4, aclUndefined}, {aclOther, 0, aclUndefined}})
	got, err = replacementACL(&st, raw, 1000)
	if err != nil || !reflect.DeepEqual(got, raw) {
		t.Fatalf("acl changed: %v", err)
	}
}

func TestApplyReplacementACLOnRealFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "replacement")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	// A real unprivileged file owner may install an exact-UID ACL for a
	// different UID without CAP_CHOWN. No privileged test setup is needed.
	access := fileAccess{exists: true, stat: unix.Stat_t{Uid: otherACLUserID(), Gid: uint32(os.Getgid()), Mode: 0o600}}
	if err := access.apply(f); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skip("test filesystem lacks POSIX ACL support")
		}
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, err := unix.Fgetxattr(int(f.Fd()), accessACLName, buf)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := decodeACL(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	want := []aclEntry{{aclUserObj, 6, aclUndefined}, {aclUser, 6, otherACLUserID()}, {aclGroupObj, 0, aclUndefined}, {aclMask, 6, aclUndefined}, {aclOther, 0, aclUndefined}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("on-disk ACL=%v", entries)
	}
}

func TestWriteACLFailureRetainsOriginal(t *testing.T) {
	h, root, _ := raceHandler(t)
	target := filepath.Join(root, "target.txt")
	put(t, target, "original")
	h.preserveAccess = func(*os.File, fileAccess) error { return unix.EOPNOTSUPP }
	err := h.writeFile([]string{"target.txt"}, func(w io.Writer) error { _, e := io.Copy(w, strings.NewReader("new")); return e })
	if !errors.Is(err, errPreserveAccess) {
		t.Fatalf("err=%v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "original" {
		t.Fatalf("data=%q err=%v", got, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v, %v", entries, err)
	}
}

func TestWriteRefusesConcurrentRegularReplacement(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "target.txt"), "original")
	raceAt(t, h, "commit", "", "target.txt", 1, func() {
		put(t, filepath.Join(root, "replacement"), "concurrent")
		if err := os.Rename(filepath.Join(root, "replacement"), filepath.Join(root, "target.txt")); err != nil {
			t.Fatal(err)
		}
	})
	err := h.writeFile([]string{"target.txt"}, func(w io.Writer) error { _, e := io.WriteString(w, "new"); return e })
	if !errors.Is(err, errPreserveAccess) {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "target.txt"))
	if string(got) != "concurrent" {
		t.Fatalf("overwrote replacement: %q", got)
	}
}

func TestDecodeACLRejectsMalformedMetadata(t *testing.T) {
	for _, raw := range [][]byte{nil, {2, 0, 0}, {3, 0, 0, 0}, {2, 0, 0, 0, 1}} {
		if _, err := decodeACL(raw); err == nil {
			t.Fatalf("accepted %v", raw)
		}
	}
	raw := make([]byte, 12)
	binary.LittleEndian.PutUint32(raw, 2)
	binary.LittleEndian.PutUint16(raw[4:], aclUserObj)
	binary.LittleEndian.PutUint16(raw[6:], 8)
	if _, err := decodeACL(raw); err == nil {
		t.Fatal("accepted invalid permission bits")
	}
}
