package logs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var (
	errLogPath = errors.New("log path must be inside data root")
	errLogType = errors.New("log file must be regular")
)

// newHandler resolves configuration lexically. Only the operator-supplied root
// is trusted; no mutable log ancestor is consulted to establish confinement.
func newHandler(root, path string) *handler {
	h := &handler{path: path}
	if path == "" {
		return h
	}
	if root == "" {
		h.configErr = errLogPath
		return h
	}
	var err error
	h.root, err = filepath.Abs(root)
	if err != nil {
		h.configErr = errLogPath
		return h
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(h.root, abs)
	}
	h.relative, err = filepath.Rel(h.root, filepath.Clean(abs))
	if err != nil || h.relative == "." || !filepath.IsLocal(h.relative) {
		h.configErr = errLogPath
	}
	return h
}

type logSource struct {
	rootFD   int
	relative string
}

// openSource pins the trusted root for the lifetime of a download or stream.
// The trusted root itself may be reached through an operator-managed symlink;
// every component below it is opened with O_NOFOLLOW.
func (h *handler) openSource() (*logSource, error) {
	if h.configErr != nil {
		return nil, h.configErr
	}
	fd, err := logOpenat(unix.AT_FDCWD, h.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return &logSource{rootFD: fd, relative: h.relative}, nil
}

func (s *logSource) close() {
	_ = unix.Close(s.rootFD)
}

func logOpenat(dirFD int, name string, flags int) (int, error) {
	for {
		fd, err := unix.Openat(dirFD, name, flags, 0)
		if !errors.Is(err, unix.EINTR) {
			return fd, err
		}
	}
}

// open walks from the pinned root on every call, including rotation probes and
// reopens. A directory descriptor pins each ancestor across mutable path swaps.
// O_NONBLOCK prevents a FIFO from blocking before its descriptor type is checked.
func (s *logSource) open() (*os.File, error) {
	const directoryFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	parent, err := logOpenat(s.rootFD, ".", directoryFlags)
	if err != nil {
		return nil, err
	}
	components := strings.Split(s.relative, string(os.PathSeparator))
	for _, name := range components[:len(components)-1] {
		next, err := logOpenat(parent, name, directoryFlags)
		_ = unix.Close(parent)
		if err != nil {
			return nil, err
		}
		parent = next
	}
	defer func() { _ = unix.Close(parent) }()
	name := components[len(components)-1]
	fd, err := logOpenat(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC)
	if err != nil {
		return nil, err
	}
	if fd < 0 {
		return nil, unix.EBADF
	}
	f := os.NewFile(uintptr(fd), name)
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, errLogType
	}
	return f, nil
}
