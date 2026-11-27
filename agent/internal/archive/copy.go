package archive

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// targetsDir holds one mount per restored volume: /targets/<logicalName>.
	targetsDir = "/targets"
)

// copyResult is the termination message of a copy run.
type copyResult struct {
	CopiedVolumes []string `json:"copiedVolumes"`
	Error         string   `json:"error,omitempty"`
}

// copyConfig is the input of one copy run.
type copyConfig struct {
	StagingDir string
	Volumes    []string
}

// copyConfigFromEnv reads ARCHIVE_COPY_VOLUMES and ARCHIVE_STAGING_DIR through
// getenv and validates them.
func copyConfigFromEnv(getenv func(string) string) (copyConfig, error) {
	raw := getenv("ARCHIVE_COPY_VOLUMES")
	if raw == "" {
		return copyConfig{}, errors.New("ARCHIVE_COPY_VOLUMES is required")
	}
	var vols []string
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if !volumeNamePattern.MatchString(name) {
			return copyConfig{}, fmt.Errorf("ARCHIVE_COPY_VOLUMES: invalid volume name %q", name)
		}
		vols = append(vols, name)
	}
	staging := getenv("ARCHIVE_STAGING_DIR")
	if staging == "" {
		staging = defaultStagingDir
	}
	return copyConfig{StagingDir: staging, Volumes: vols}, nil
}

// runCopy replaces the contents of each target volume with the staged tree and
// returns the process exit code: 0 on success, 1 otherwise. The outcome is
// written to the termination log either way.
func runCopy(opts Options, getenv func(string) string, stderr io.Writer) int {
	cfg, err := copyConfigFromEnv(getenv)
	if err != nil {
		return reportCopy(opts, copyResult{CopiedVolumes: []string{}, Error: err.Error()}, stderr)
	}
	copied, err := copyVolumes(filepath.Join(cfg.StagingDir, "unpacked"), targetsDir, cfg.Volumes)
	res := copyResult{CopiedVolumes: copied}
	if err != nil {
		res.Error = truncateError(err.Error())
	}
	return reportCopy(opts, res, stderr)
}

// reportCopy writes the termination message and returns the exit code.
func reportCopy(opts Options, res copyResult, stderr io.Writer) int {
	if res.Error != "" {
		_, _ = fmt.Fprintln(stderr, "copy: "+res.Error)
	}
	if err := WriteTermination(opts.TerminationLog, res); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if res.Error != "" {
		return 1
	}
	return 0
}

// copyVolumes copies <stagingUnpacked>/volumes/<name> over <targets>/<name> for
// each name, in order. It returns the names copied before any failure.
func copyVolumes(stagingUnpacked, targets string, names []string) ([]string, error) {
	copied := []string{}
	for _, name := range names {
		if err := copyVolume(filepath.Join(stagingUnpacked, "volumes", name), filepath.Join(targets, name)); err != nil {
			return copied, fmt.Errorf("volume %q: %w", name, err)
		}
		copied = append(copied, name)
	}
	return copied, nil
}

// dirTime is a directory whose mode and times are set after its contents. path
// is relative to the root of the directory it belongs to.
type dirTime struct {
	path string
	info fs.FileInfo
}

// copyVolume empties dst, which must be an existing mount, and copies the tree
// at src into it. Modes, symlinks and modification times are preserved; owner
// is left to the pod's fsGroup. The walk never follows symlinks. Every
// filesystem operation on either side goes through a root-scoped handle, so no
// name can resolve outside src or dst.
func copyVolume(src, dst string) error {
	// os.OpenRoot follows a symlink at the top level; refuse one explicitly so a
	// staged volume folder can never be redirected elsewhere.
	if li, err := os.Lstat(src); err == nil && li.Mode()&fs.ModeSymlink != 0 {
		return errors.New("staged volume is a symlink")
	}
	srcRoot, err := os.OpenRoot(src)
	if err != nil {
		return fmt.Errorf("staged volume: %w", err)
	}
	defer func() { _ = srcRoot.Close() }()
	si, err := srcRoot.Stat(".")
	if err != nil {
		return fmt.Errorf("staged volume: %w", err)
	}
	if !si.IsDir() {
		return errors.New("staged volume is not a directory")
	}
	dstRoot, err := os.OpenRoot(dst)
	if err != nil {
		return fmt.Errorf("target mount: %w", err)
	}
	defer func() { _ = dstRoot.Close() }()
	di, err := dstRoot.Stat(".")
	if err != nil {
		return fmt.Errorf("target mount: %w", err)
	}
	if !di.IsDir() {
		return errors.New("target mount is not a directory")
	}
	if err := clearDir(dstRoot); err != nil {
		return err
	}

	var dirs []dirTime
	err = fs.WalkDir(srcRoot.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case rel == ".":
			dirs = append(dirs, dirTime{path: ".", info: info})
			return nil
		case info.IsDir():
			if err := dstRoot.Mkdir(rel, 0o700); err != nil {
				return fmt.Errorf("create directory %q: %w", rel, err)
			}
			dirs = append(dirs, dirTime{path: rel, info: info})
			return nil
		case info.Mode().IsRegular():
			return copyFile(srcRoot, dstRoot, rel, info)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := srcRoot.Readlink(rel)
			if err != nil {
				return fmt.Errorf("read symlink %q: %w", rel, err)
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("symlink %q has an absolute target", rel)
			}
			if err := dstRoot.Symlink(target, rel); err != nil {
				return fmt.Errorf("create symlink %q: %w", rel, err)
			}
			return nil
		}
		return fmt.Errorf("entry %q: unsupported file type", rel)
	})
	if err != nil {
		return err
	}
	// Deepest directories first, so a read-only parent is fixed after its children.
	for i := len(dirs) - 1; i >= 0; i-- {
		dt := dirs[i]
		if err := dstRoot.Chmod(dt.path, dt.info.Mode().Perm()); err != nil {
			return fmt.Errorf("set directory mode: %w", err)
		}
		if err := dstRoot.Chtimes(dt.path, dt.info.ModTime(), dt.info.ModTime()); err != nil {
			return fmt.Errorf("set directory times: %w", err)
		}
	}
	return nil
}

// clearDir removes every entry inside dir but keeps dir itself, the mount point.
// Entries are removed through dir, so a symlink is removed rather than followed.
func clearDir(dir *os.Root) error {
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return fmt.Errorf("read target mount: %w", err)
	}
	for _, e := range entries {
		if err := dir.RemoveAll(e.Name()); err != nil {
			return fmt.Errorf("clear target mount: %w", err)
		}
	}
	return nil
}

// copyFile copies one regular file named rel from srcRoot to dstRoot, keeping
// its permission bits and mtime.
func copyFile(srcRoot, dstRoot *os.Root, rel string, info fs.FileInfo) error {
	in, err := srcRoot.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open %q: %w", filepath.Base(rel), err)
	}
	defer func() { _ = in.Close() }()
	out, err := dstRoot.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %q: %w", filepath.Base(rel), err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy %q: %w", filepath.Base(rel), err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %q: %w", filepath.Base(rel), err)
	}
	if err := dstRoot.Chmod(rel, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set mode of %q: %w", filepath.Base(rel), err)
	}
	return dstRoot.Chtimes(rel, info.ModTime(), info.ModTime())
}
