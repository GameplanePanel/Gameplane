//go:build unix

package controlplane

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func validateKeyPlatform() error { return validateKeyACLPlatform() }

func openKeyHandle(root *os.Root, name string) (*os.File, error) {
	// FIFOs must not block startup before regular-file validation can run.
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if err := validateKeyACL(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func validateKeyFile(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("management key ownership could not be verified")
	}
	groups, err := os.Getgroups()
	if err != nil {
		return fmt.Errorf("inspect management key process groups: %w", err)
	}
	groups = append(groups, os.Getegid())
	if stat.Nlink != 1 {
		return errors.New("management key must have exactly one filesystem link")
	}
	return validateKeyAccess(stat.Uid, stat.Gid, info.Mode(), os.Geteuid(), os.Getegid(), groups)
}

func validateKeyAccess(uid, gid uint32, mode os.FileMode, euid, egid int, groups []int) error {
	// Root-owned projected Kubernetes keys are legitimate when fsGroup grants
	// this process read access. Unrelated owners, world access, executable keys,
	// and group-write access are never accepted.
	if uid != 0 && int64(uid) != int64(euid) {
		return errors.New("management key must be owned by root or the API user")
	}
	if mode.Perm()&0137 != 0 || mode.Perm()&0400 == 0 {
		return errors.New("management key permissions must be private (0600/0400), or safely group-readable (0440/0640)")
	}
	if mode.Perm()&0040 != 0 {
		// Only operator-owned projections may rely on a supplementary fsGroup.
		// API-owned keys must use the process's primary group, not a shared
		// supplementary group inherited from the host account.
		if uid != 0 && int64(gid) != int64(egid) {
			return errors.New("API-owned management key read group must be the effective process group")
		}
		for _, group := range groups {
			if int64(gid) == int64(group) {
				return nil
			}
		}
		return errors.New("management key read group must be an API process group")
	}
	return nil
}
