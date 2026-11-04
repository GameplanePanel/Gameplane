package mods

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// openModsRoot anchors removal to the configured data volume. Open each mods
// directory component without accepting symlink aliases, then retain its handle
// so a later rename cannot redirect removal or its manifest update.
func (h *handler) openModsRoot() (*os.Root, error) {
	rel, err := filepath.Rel(h.dataRoot, h.dir)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, ErrEscapesRoot
	}
	root, err := os.OpenRoot(h.dataRoot)
	if err != nil {
		return nil, fmt.Errorf("open mod data root: %w", err)
	}
	if rel == "." {
		return root, nil
	}
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		next, err := openModDirectory(root, component)
		_ = root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

func openModDirectory(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect mod directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, ErrEscapesRoot
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open mod directory: %w", err)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, ErrEscapesRoot
	}
	return root, nil
}

// removeModEntry never resolves the final name to a symlink's referent. Existing
// links are rejected; a link swapped in after Lstat is only unlinked by Remove
// or RemoveAll. Recursive removal likewise never follows child symlinks.
func removeModEntry(root *os.Root, name string, recursive bool) error {
	name, err := safeName(name)
	if err != nil {
		return fmt.Errorf("invalid mod name: %w", err)
	}
	info, err := root.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect mod entry: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrEscapesRoot
	}
	if recursive {
		return root.RemoveAll(name)
	}
	return root.Remove(name)
}
