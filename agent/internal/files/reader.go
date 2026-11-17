package files

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpenRegularFile opens a regular file beneath root without following symlinks.
// It shares the file browser's descriptor-relative confinement and dotfile
// policy. The caller must close the descriptor and bound any reads.
func OpenRegularFile(root, path string) (*os.File, error) {
	h := &handler{root: filepath.Clean(root)}
	components, err := h.components(path)
	if err != nil {
		return nil, fmt.Errorf("resolve regular file: %w", err)
	}
	f, err := h.openFile(components)
	if err != nil {
		return nil, fmt.Errorf("open regular file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat regular file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("not a regular file: %w", os.ErrInvalid)
	}
	return f, nil
}
