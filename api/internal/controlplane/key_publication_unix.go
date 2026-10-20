//go:build unix

package controlplane

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Generated key publishers share a persistent directory lock. Never unlink
// its file: replacing the inode would permit independent overlapping locks.
func lockKeyPublication(directory string) (func(), error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open key publication root: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(".management-key.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("open key publication lock: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("key publication lock must be a regular file")
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock key publication: %w", err)
	}
	return func() { _ = f.Close() }, nil
}

// The caller holds the publication lock. Different inodes can be active or
// interrupted publications of other filenames, and are never removed.
func recoverKeyAliases(path string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	key, err := openKeyHandle(root, filepath.Base(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = key.Close() }()
	info, err := key.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != 32 {
		return nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".management-key-") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		alias, err := root.OpenFile(entry.Name(), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		aliasInfo, statErr := alias.Stat()
		_ = alias.Close()
		if statErr != nil {
			return statErr
		}
		if os.SameFile(info, aliasInfo) {
			if err := root.Remove(entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}
