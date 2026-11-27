package archive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRunCopyReportsMissingStagedVolume(t *testing.T) {
	term := filepath.Join(t.TempDir(), "termination-log")
	var stderr bytes.Buffer
	code := runCopy(Options{TerminationLog: term}, gapEnv(map[string]string{
		"ARCHIVE_COPY_VOLUMES": "world",
		"ARCHIVE_STAGING_DIR":  t.TempDir(),
	}), &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `volume "world"`) {
		t.Errorf("stderr = %q, want the failing volume", stderr.String())
	}
	raw, err := os.ReadFile(term)
	if err != nil {
		t.Fatal(err)
	}
	var res copyResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("termination message is not JSON: %v", err)
	}
	if !strings.Contains(res.Error, `volume "world"`) || len(res.CopiedVolumes) != 0 {
		t.Errorf("termination = %+v", res)
	}
}

func TestRunCopyRejectsBadConfig(t *testing.T) {
	term := filepath.Join(t.TempDir(), "termination-log")
	var stderr bytes.Buffer
	if code := runCopy(Options{TerminationLog: term}, gapEnv(nil), &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	raw, err := os.ReadFile(term)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ARCHIVE_COPY_VOLUMES is required") {
		t.Errorf("termination = %q", raw)
	}
}

func TestReportCopyCleanRunExitsZero(t *testing.T) {
	term := filepath.Join(t.TempDir(), "termination-log")
	var stderr bytes.Buffer
	res := copyResult{CopiedVolumes: []string{"world"}}
	if code := reportCopy(Options{TerminationLog: term}, res, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
}

func TestReportCopyUnwritableTerminationExitsOne(t *testing.T) {
	term := filepath.Join(t.TempDir(), "missing", "termination-log")
	var stderr bytes.Buffer
	if code := reportCopy(Options{TerminationLog: term}, copyResult{CopiedVolumes: []string{}}, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want the write failure")
	}
}

func TestCopyVolumesCopiesEveryNamedVolume(t *testing.T) {
	root := stagedVolume(t, "world")
	targets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(targets, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	copied, err := copyVolumes(root, targets, []string{"world"})
	if err != nil {
		t.Fatalf("copyVolumes: %v", err)
	}
	if len(copied) != 1 || copied[0] != "world" {
		t.Errorf("copied = %v, want [world]", copied)
	}
	b, err := os.ReadFile(filepath.Join(targets, "world", "a.txt"))
	if err != nil || string(b) != "new" {
		t.Errorf("a.txt = %q, %v", b, err)
	}
}

func TestCopyVolumeRefusesSymlinkedStagedVolume(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "volumes"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "volumes", "world")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := copyVolume(link, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want a staged symlink refusal", err)
	}
}

func TestCopyVolumeMissingTargetMountRefused(t *testing.T) {
	root := stagedVolume(t, "world")
	err := copyVolume(filepath.Join(root, "volumes", "world"), filepath.Join(t.TempDir(), "absent"))
	if err == nil || !strings.Contains(err.Error(), "target mount") {
		t.Fatalf("error = %v, want target mount failure", err)
	}
}

func TestCopyVolumeRefusesFIFOInStagedTree(t *testing.T) {
	root := stagedVolume(t, "world")
	vol := filepath.Join(root, "volumes", "world")
	if err := syscall.Mkfifo(filepath.Join(vol, "pipe"), 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	err := copyVolume(vol, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsupported file type") {
		t.Fatalf("error = %v, want unsupported file type", err)
	}
}
