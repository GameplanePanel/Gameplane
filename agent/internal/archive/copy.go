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

// dirTime is a directory whose mode and times are set after its contents.
type dirTime struct {
	path string
	info fs.FileInfo
}

// copyVolume empties dst, which must be an existing mount, and copies the tree
// at src into it. Modes, symlinks and modification times are preserved; owner
// is left to the pod's fsGroup. The walk never follows symlinks.
func copyVolume(src, dst string) error {
	si, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("staged volume: %w", err)
	}
	if !si.IsDir() {
		return errors.New("staged volume is not a directory")
	}
	di, err := os.Lstat(dst)
	if err != nil {
		return fmt.Errorf("target mount: %w", err)
	}
	if !di.IsDir() {
		return errors.New("target mount is not a directory")
	}
	if err := clearDir(dst); err != nil {
		return err
	}

	var dirs []dirTime
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case rel == ".":
			dirs = append(dirs, dirTime{path: dst, info: info})
			return nil
		case info.IsDir():
			if err := os.Mkdir(out, 0o700); err != nil {
				return fmt.Errorf("create directory %q: %w", rel, err)
			}
			dirs = append(dirs, dirTime{path: out, info: info})
			return nil
		case info.Mode().IsRegular():
			return copyFile(p, out, info)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return fmt.Errorf("read symlink %q: %w", rel, err)
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("symlink %q has an absolute target", rel)
			}
			if err := os.Symlink(target, out); err != nil {
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
		if err := os.Chmod(dt.path, dt.info.Mode().Perm()); err != nil {
			return fmt.Errorf("set directory mode: %w", err)
		}
		if err := os.Chtimes(dt.path, dt.info.ModTime(), dt.info.ModTime()); err != nil {
			return fmt.Errorf("set directory times: %w", err)
		}
	}
	return nil
}

// clearDir removes every entry inside dir but keeps dir itself, the mount point.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read target mount: %w", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("clear target mount: %w", err)
		}
	}
	return nil
}

// copyFile copies one regular file, keeping its permission bits and mtime.
func copyFile(src, dst string, info fs.FileInfo) error {
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open %q: %w", filepath.Base(src), err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %q: %w", filepath.Base(dst), err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy %q: %w", filepath.Base(src), err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %q: %w", filepath.Base(dst), err)
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set mode of %q: %w", filepath.Base(dst), err)
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}
