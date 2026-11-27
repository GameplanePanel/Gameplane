package archive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stagedVolume builds <root>/volumes/<name> with a file, a subdirectory and a
// relative symlink, and returns the unpacked root.
func stagedVolume(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	vol := filepath.Join(root, "volumes", name)
	if err := os.MkdirAll(filepath.Join(vol, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(vol, "a.txt")
	if err := os.WriteFile(file, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vol, "sub", "b.txt"), []byte("bee"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../a.txt", filepath.Join(vol, "sub", "link")); err != nil {
		t.Fatal(err)
	}
	mt := time.Unix(1700000000, 0)
	if err := os.Chtimes(file, mt, mt); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCopyVolumeReplacesContentsKeepsMount(t *testing.T) {
	root := stagedVolume(t, "world")
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "olddir"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}

	if err := copyVolume(filepath.Join(root, "volumes", "world"), dst); err != nil {
		t.Fatalf("copyVolume: %v", err)
	}

	after, err := os.Lstat(dst)
	if err != nil || !after.IsDir() || !os.SameFile(before, after) {
		t.Fatalf("mount point was replaced: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old file kept: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "olddir")); !os.IsNotExist(err) {
		t.Errorf("old directory kept: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil || string(b) != "new" {
		t.Fatalf("a.txt = %q, %v", b, err)
	}
	fi, err := os.Stat(filepath.Join(dst, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", fi.Mode().Perm())
	}
	if !fi.ModTime().Equal(time.Unix(1700000000, 0)) {
		t.Errorf("mtime = %v, want the staged mtime", fi.ModTime())
	}
	if b, err := os.ReadFile(filepath.Join(dst, "sub", "b.txt")); err != nil || string(b) != "bee" {
		t.Errorf("sub/b.txt = %q, %v", b, err)
	}
	target, err := os.Readlink(filepath.Join(dst, "sub", "link"))
	if err != nil || target != "../a.txt" {
		t.Errorf("symlink target = %q, %v, want ../a.txt", target, err)
	}
}

func TestCopyVolumeRefusesAbsoluteSymlink(t *testing.T) {
	root := stagedVolume(t, "world")
	vol := filepath.Join(root, "volumes", "world")
	if err := os.Symlink("/etc/passwd", filepath.Join(vol, "abs")); err != nil {
		t.Fatal(err)
	}
	if err := copyVolume(vol, t.TempDir()); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("error = %v, want an absolute symlink refusal", err)
	}
}

func TestCopyVolumeMissingStagedVolume(t *testing.T) {
	if err := copyVolume(filepath.Join(t.TempDir(), "nope"), t.TempDir()); err == nil {
		t.Fatal("copyVolume succeeded for a missing staged volume")
	}
}

func TestCopyVolumesStopsAtFirstFailure(t *testing.T) {
	root := stagedVolume(t, "world")
	targets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(targets, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(targets, "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	copied, err := copyVolumes(filepath.Join(root), targets, []string{"world", "gone", "missing"})
	if err == nil || !strings.Contains(err.Error(), `"gone"`) {
		t.Fatalf("error = %v, want the failure for gone", err)
	}
	if len(copied) != 1 || copied[0] != "world" {
		t.Errorf("copied = %v, want [world]", copied)
	}
}

func TestCopyConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	if _, err := copyConfigFromEnv(env(map[string]string{})); err == nil {
		t.Error("missing ARCHIVE_COPY_VOLUMES: want error")
	}
	if _, err := copyConfigFromEnv(env(map[string]string{"ARCHIVE_COPY_VOLUMES": "../etc"})); err == nil {
		t.Error("invalid volume name: want error")
	}
	cfg, err := copyConfigFromEnv(env(map[string]string{"ARCHIVE_COPY_VOLUMES": "world, data"}))
	if err != nil {
		t.Fatalf("copyConfigFromEnv: %v", err)
	}
	if cfg.StagingDir != "/staging" || len(cfg.Volumes) != 2 || cfg.Volumes[1] != "data" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestReportCopyFailureWritesError(t *testing.T) {
	term := filepath.Join(t.TempDir(), "term")
	var buf bytes.Buffer
	res := copyResult{CopiedVolumes: []string{"world"}, Error: "boom"}
	if code := reportCopy(Options{TerminationLog: term}, res, &buf); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	b, err := os.ReadFile(term)
	if err != nil {
		t.Fatal(err)
	}
	var got copyResult
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("termination message is not JSON: %v", err)
	}
	if got.Error != "boom" || len(got.CopiedVolumes) != 1 {
		t.Errorf("termination = %+v", got)
	}
}
