//go:build linux

package files

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenRegularFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ops.json"), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenRegularFile(root, "ops.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(f)
	if err != nil || string(content) != "[]" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", ".secret", "missing", ".", "pipe"} {
		if opened, err := OpenRegularFile(root, path); err == nil {
			_ = opened.Close()
			t.Errorf("accepted %q", path)
		}
	}
}
