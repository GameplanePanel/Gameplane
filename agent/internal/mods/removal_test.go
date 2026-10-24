package mods

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

func TestRemove_RejectsSymlinkEntries(t *testing.T) {
	for _, extract := range []bool{false, true} {
		for _, target := range []string{"inside", "outside", "root", "dangling", "loop"} {
			t.Run(target+map[bool]string{false: "/file", true: "/extracted"}[extract], func(t *testing.T) {
				data := t.TempDir()
				dir := filepath.Join(data, "mods")
				outside := t.TempDir()
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, parent := range []string{dir, outside} {
					if err := os.WriteFile(filepath.Join(parent, "kept.jar"), []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				ledger := []byte(`{"version":1,"mods":{"alias":{"provider":"upload"}}}`)
				if err := os.WriteFile(filepath.Join(dir, manifestName), ledger, 0o600); err != nil {
					t.Fatal(err)
				}
				destination := map[string]string{"inside": "kept.jar", "outside": filepath.Join(outside, "kept.jar"), "root": ".", "dangling": "missing.jar", "loop": "alias"}[target]
				if err := os.Symlink(destination, filepath.Join(dir, "alias")); err != nil {
					t.Fatal(err)
				}
				srv := newSrv(t, data, &caps.Mods{Path: "mods", Extract: extract})
				if status, _ := do(t, srv, http.MethodDelete, "/mods?name=alias", nil); status != http.StatusBadRequest {
					t.Fatalf("delete symlink: status=%d, want 400", status)
				}
				for _, parent := range []string{dir, outside} {
					assertRemovalFile(t, filepath.Join(parent, "kept.jar"), "keep")
				}
				assertRemovalFile(t, filepath.Join(dir, manifestName), string(ledger))
				if info, err := os.Lstat(filepath.Join(dir, "alias")); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("rejected link changed: info=%v err=%v", info, err)
				}
			})
		}
	}
}

func TestRemove_RejectsAliasedModsDirectory(t *testing.T) {
	for _, target := range []string{"outside", "root", "inside"} {
		t.Run(target, func(t *testing.T) {
			data, outside := t.TempDir(), t.TempDir()
			inside := filepath.Join(data, "other")
			if err := os.Mkdir(inside, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, parent := range []string{data, outside, inside} {
				if err := os.WriteFile(filepath.Join(parent, "kept.jar"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			destination := map[string]string{"outside": outside, "root": ".", "inside": "other"}[target]
			if err := os.Symlink(destination, filepath.Join(data, "mods")); err != nil {
				t.Fatal(err)
			}
			srv := newSrv(t, data, &caps.Mods{Path: "mods"})
			if status, _ := do(t, srv, http.MethodDelete, "/mods?name=kept.jar", nil); status != http.StatusBadRequest {
				t.Fatalf("delete through aliased root: status=%d, want 400", status)
			}
			for _, parent := range []string{data, outside, inside} {
				assertRemovalFile(t, filepath.Join(parent, "kept.jar"), "keep")
			}
		})
	}
}

func TestRemove_RecursivePreservesSymlinkTargets(t *testing.T) {
	data, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(data, "mods", "pack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "kept.jar"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "child")); err != nil {
		t.Fatal(err)
	}
	srv := newSrv(t, data, &caps.Mods{Path: "mods", Extract: true})
	if status, _ := do(t, srv, http.MethodDelete, "/mods?name=pack", nil); status != http.StatusNoContent {
		t.Fatalf("delete pack: status=%d, want 204", status)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pack remains: %v", err)
	}
	assertRemovalFile(t, filepath.Join(outside, "kept.jar"), "keep")
}

func TestRemove_RootAndManifestSurviveDirectoryReplacement(t *testing.T) {
	data, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(data, "mods")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ledger := `{"version":1,"mods":{"gone.jar":{"provider":"upload"}}}`
	for _, parent := range []string{dir, outside} {
		if err := os.WriteFile(filepath.Join(parent, "gone.jar"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, manifestName), []byte(ledger), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := newHandler(data, &caps.Mods{Path: "mods"})
	root, err := h.openModsRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Deterministically replace the pathname after the operation acquires its
	// root handle. Neither deletion nor metadata may re-open that pathname.
	moved := filepath.Join(data, "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	if err := removeModEntry(root, "gone.jar", false); err != nil {
		t.Fatal(err)
	}
	h.updateManifestInRoot(root, func(mods map[string]*ModMeta) { delete(mods, "gone.jar") })
	if _, err := os.Stat(filepath.Join(moved, "gone.jar")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original entry remains: %v", err)
	}
	if entries := readManifest(t, moved); len(entries) != 0 {
		t.Fatalf("original manifest did not update: %+v", entries)
	}
	assertRemovalFile(t, filepath.Join(outside, "gone.jar"), "keep")
	assertRemovalFile(t, filepath.Join(outside, manifestName), ledger)
}

func TestRemove_ConfiguredDirectories(t *testing.T) {
	for _, path := range []string{"mods", "nested/mods", "/mods", "."} {
		t.Run(path, func(t *testing.T) {
			data := t.TempDir()
			h := newHandler(data, &caps.Mods{Path: path})
			if err := os.MkdirAll(h.dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.dir, "gone.jar"), []byte("remove"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := h.removeEntry("gone.jar"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(h.dir, "gone.jar")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("entry remains: %v", err)
			}
		})
	}
}

func TestRemove_ManifestOutsideSymlinkPreservesTarget(t *testing.T) {
	data, outside := t.TempDir(), t.TempDir()
	h := newHandler(data, &caps.Mods{Path: "mods"})
	if err := os.Mkdir(h.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ledger := `{"version":1,"mods":{"foreign.jar":{"provider":"upload"}}}`
	target := filepath.Join(outside, manifestName)
	if err := os.WriteFile(target, []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(h.dir, manifestName)); err != nil {
		t.Fatal(err)
	}
	root, err := h.openModsRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	h.updateManifestInRoot(root, func(mods map[string]*ModMeta) {
		if len(mods) != 0 {
			t.Fatalf("loaded outside manifest: %+v", mods)
		}
	})
	assertRemovalFile(t, target, ledger)
	if info, err := os.Lstat(filepath.Join(h.dir, manifestName)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("confined manifest was not published: info=%v err=%v", info, err)
	}
}

func TestManifestSaveInRoot_PublishFailureCleansTemporaryFile(t *testing.T) {
	data := t.TempDir()
	// A directory at the ledger path deterministically prevents atomic rename,
	// even when CI runs as root. Its existing contents must remain intact.
	if err := os.Mkdir(filepath.Join(data, manifestName), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(data, manifestName, "previous")
	if err := os.WriteFile(previous, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	m := &manifest{Version: 1, Mods: map[string]*ModMeta{}}
	if err := m.saveInRoot(root); err == nil {
		t.Fatal("publishing over a directory succeeded")
	}
	assertRemovalFile(t, previous, "keep")
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 1 || entries[0].Name() != manifestName {
		t.Fatalf("temporary manifest leaked: entries=%v err=%v", entries, err)
	}
}

func TestRemove_MissingDirectory(t *testing.T) {
	srv := newSrv(t, t.TempDir(), &caps.Mods{Path: "mods"})
	if status, _ := do(t, srv, http.MethodDelete, "/mods?name=missing.jar", nil); status != http.StatusNotFound {
		t.Fatalf("missing mods directory: status=%d, want 404", status)
	}
}

func assertRemovalFile(t *testing.T, name, want string) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil || string(data) != want {
		t.Fatalf("file changed: %s: data=%q err=%v", name, data, err)
	}
}
