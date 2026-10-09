//go:build linux

package controlplane

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func validateKeyACLPlatform() error { return nil }

func validateKeyACL(f *os.File) error {
	// With an extended POSIX ACL, the mode's group bits describe its mask,
	// not its readers. Even 0640 with an API-owned group can expose the key
	// to named unrelated users/groups. Inspect the inode we will read, without
	// allocating an ACL buffer or following its path a second time.
	_, err := unix.Fgetxattr(int(f.Fd()), "system.posix_acl_access", nil)
	if errors.Is(err, unix.ENODATA) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("management key ACL could not be verified: %w", err)
	}
	return errors.New("management key must not have an extended access ACL")
}
