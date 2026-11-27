package source

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localsrcTree builds a root with one top-level file and a nested module
// directory, and returns the root.
func localsrcTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "root.txt"), []byte("top\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(root, "mods", "minecraft")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "module.yaml"), []byte("name: minecraft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLocalLoaderDisabledWithoutRoot(t *testing.T) {
	t.Parallel()
	load, loc, err := LocalLoader("", "mods")
	if err == nil || !strings.Contains(err.Error(), "local sources are disabled") {
		t.Fatalf("LocalLoader with empty root: error = %v", err)
	}
	if load != nil || loc != "" {
		t.Fatalf("LocalLoader with empty root returned load=%v loc=%q", load != nil, loc)
	}
}

func TestLocalLoaderRejectsEscapes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	tests := []struct {
		name string
		sub  string
	}{
		{"parent", ".."},
		{"parent with slash", "../"},
		{"parent child", "../x"},
		{"climb after descent", "a/../../x"},
		{"sibling sharing name prefix", "../" + filepath.Base(root) + "-evil"},
		{"double parent", "../../"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			load, loc, err := LocalLoader(root, tc.sub)
			if err == nil || !strings.Contains(err.Error(), "escapes the module root") {
				t.Fatalf("LocalLoader(%q) error = %v, want escape refusal", tc.sub, err)
			}
			if load != nil || loc != "" {
				t.Fatalf("LocalLoader(%q) returned load=%v loc=%q on refusal", tc.sub, load != nil, loc)
			}
		})
	}
}

func TestLocalLoaderServes(t *testing.T) {
	t.Parallel()
	root := localsrcTree(t)
	tests := []struct {
		name     string
		root     string
		sub      string
		wantLoc  string
		readFile string
		wantBody string
	}{
		{"empty sub is root", root, "", ".", "root.txt", "top\n"},
		{"dot sub is root", root, ".", ".", "root.txt", "top\n"},
		{"nested sub", root, "mods/minecraft", "mods/minecraft", "module.yaml", "name: minecraft\n"},
		{"sub with inner climb stays inside", root, "mods/../mods/minecraft", "mods/../mods/minecraft", "module.yaml", "name: minecraft\n"},
		{"unclean root is cleaned", root + string(os.PathSeparator) + ".", "mods/minecraft", "mods/minecraft", "module.yaml", "name: minecraft\n"},
		{"trailing slash root is cleaned", root + string(os.PathSeparator), "mods/minecraft", "mods/minecraft", "module.yaml", "name: minecraft\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			load, loc, err := LocalLoader(tc.root, tc.sub)
			if err != nil {
				t.Fatalf("LocalLoader: %v", err)
			}
			if loc != tc.wantLoc {
				t.Fatalf("location = %q, want %q", loc, tc.wantLoc)
			}
			fsys, digest, err := load(t.Context())
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if digest != "" {
				t.Fatalf("digest = %q, want empty for local sources", digest)
			}
			body, err := fs.ReadFile(fsys, tc.readFile)
			if err != nil || string(body) != tc.wantBody {
				t.Fatalf("read %s = %q, %v; want %q", tc.readFile, body, err, tc.wantBody)
			}
		})
	}
}

func TestLocalLoaderLoadFailures(t *testing.T) {
	t.Parallel()
	root := localsrcTree(t)
	tests := []struct {
		name     string
		sub      string
		wantErr  string
		notExist bool
	}{
		{"missing directory", "absent", "module directory", true},
		{"path is a file", "root.txt", "is not a directory", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			load, _, err := LocalLoader(root, tc.sub)
			if err != nil {
				t.Fatalf("LocalLoader(%q): %v", tc.sub, err)
			}
			fsys, _, err := load(t.Context())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("load error = %v, want substring %q", err, tc.wantErr)
			}
			if tc.notExist && !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("load error = %v, want fs.ErrNotExist", err)
			}
			if fsys != nil {
				t.Fatal("failed load returned a filesystem")
			}
		})
	}
}
