package source

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LocalLoader returns the Loader for a directory mounted into the process,
// plus its location label ("." for the root, else sub). root is the
// operator-trusted base directory (the --module-local-root flag, wired via Helm
// values together with the volume mount); sub is the source's relative path
// under it and must not escape it.
func LocalLoader(root, sub string) (Loader, string, error) {
	if root == "" {
		return nil, "", errors.New("local sources are disabled: operator started without --module-local-root")
	}
	root = filepath.Clean(root)
	dir := filepath.Clean(filepath.Join(root, filepath.FromSlash(sub)))
	// CRD validation already rejects absolute paths and "..", but the
	// flag value is operator-trusted input only after this re-check.
	if dir != root && !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
		return nil, "", fmt.Errorf("path %q escapes the module root", sub)
	}
	load := func(context.Context) (fs.FS, string, error) {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, "", fmt.Errorf("module directory: %w", err)
		}
		if !info.IsDir() {
			return nil, "", fmt.Errorf("module path %q is not a directory", dir)
		}
		return os.DirFS(dir), "", nil
	}
	loc := sub
	if loc == "" {
		loc = "."
	}
	return load, loc, nil
}
