package usage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

const (
	diskInterval   = 5 * time.Minute
	diskTimeout    = time.Minute
	maxDiskEntries = 2_000_000
	maxDiskDepth   = 128
)

// MonitorDisk runs one scan at a time until ctx is canceled. A shared
// filesystem has no per-directory capacity. The reader publishes allocated
// bytes only after the entire scan completes, and clears a failed sample.
func (r *Reader) MonitorDisk(ctx context.Context) { r.monitorDisk(ctx, volumeUsage) }

func (r *Reader) monitorDisk(ctx context.Context, scan func(context.Context, string, int) (diskSample, error)) {
	if r.cfg.DataDir == "" {
		return
	}
	for ctx.Err() == nil {
		scanCtx, cancel := context.WithTimeout(ctx, diskTimeout)
		sample, err := scan(scanCtx, r.cfg.DataDir, maxDiskEntries)
		// A scan and its timer can become runnable together. Observe the
		// deadline itself even if cancellation has not been delivered yet.
		deadline, _ := scanCtx.Deadline()
		completed := err == nil && scanCtx.Err() == nil && time.Now().Before(deadline)
		cancel()
		r.mu.Lock()
		r.diskMeasuredAt = time.Time{}
		if completed {
			r.diskBytes = sample.used
			r.diskCapacity = sample.capacity
			r.diskMeasuredAt = r.cfg.now()
		}
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(diskInterval):
		}
	}
}

func (r *Reader) readDisk(s *Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.diskMeasuredAt.IsZero() && r.cfg.now().Sub(r.diskMeasuredAt) < diskInterval+diskTimeout {
		s.DiskUsedBytes = r.diskBytes
		s.DiskKnown = true
		s.DiskTotalBytes = r.diskCapacity
		s.DiskTotalKnown = r.diskCapacity > 0
	}
}

type diskSample struct{ used, capacity int64 }

func volumeUsage(ctx context.Context, root string, limit int) (diskSample, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return diskSample{}, err
	}
	dir := os.NewFile(uintptr(fd), root)
	defer func() { _ = dir.Close() }()
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err == nil && fs.Type == unix.CEPH_SUPER_MAGIC {
		// Kernel CephFS exposes recursive usage and quotas as virtual xattrs on
		// this directory. An inherited/absent quota cannot establish capacity.
		if s, ok := cephDiskUsage(func(key string) (int64, error) { return diskAttribute(fd, key) }); ok {
			return s, nil
		}
	}
	used, err := allocatedDiskUsage(ctx, dir, limit)
	return diskSample{used: used}, err
}

// CephFS exposes the counters as decimal integers on the opened directory.
func diskAttribute(fd int, key string) (int64, error) {
	var value [32]byte
	n, err := unix.Fgetxattr(fd, key, value[:])
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(string(value[:n]), 10, 64)
}

func cephDiskUsage(read func(string) (int64, error)) (diskSample, bool) {
	used, err := read("ceph.dir.rbytes")
	if err != nil || used < 0 {
		return diskSample{}, false
	}
	quota, err := read("ceph.quota.max_bytes")
	if err != nil || quota < 0 {
		quota = 0
	}
	return diskSample{used: used, capacity: quota}, true
}

type diskWalk struct {
	ctx       context.Context
	remaining int
	// Single-link files need no entry. NO_XDEV confines inode numbers to one mount.
	hardlinks map[uint64]struct{}
	bytes     int64
}

func allocatedDiskUsage(ctx context.Context, dir *os.File, limit int) (int64, error) {
	fd := int(dir.Fd())
	walk := diskWalk{ctx: ctx, remaining: limit, hardlinks: make(map[uint64]struct{})}
	if err := walk.count(fd); err != nil {
		return 0, err
	}
	if err := walk.dir(dir, 0); err != nil {
		return 0, err
	}
	return walk.bytes, nil
}

func (w *diskWalk) count(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR && st.Nlink > 1 {
		if _, ok := w.hardlinks[st.Ino]; ok {
			return nil
		}
		w.hardlinks[st.Ino] = struct{}{}
	}
	if st.Blocks < 0 || st.Blocks > (math.MaxInt64-w.bytes)/512 {
		return errors.New("allocated byte count overflow")
	}
	w.bytes += st.Blocks * 512
	return nil
}

func (w *diskWalk) dir(dir *os.File, depth int) error {
	if depth > maxDiskDepth {
		return errors.New("disk scan depth exceeded")
	}
	for {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		names, err := dir.Readdirnames(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, name := range names {
			if err := w.ctx.Err(); err != nil {
				return err
			}
			w.remaining--
			if w.remaining < 0 {
				return errors.New("disk scan entry limit exceeded")
			}
			if err := w.entry(int(dir.Fd()), name, depth); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func (w *diskWalk) entry(parent int, name string, depth int) error {
	// O_PATH avoids opening FIFOs or devices for I/O. NO_XDEV also excludes
	// bind mounts on the same device; NOFOLLOW counts the link itself.
	fd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV | unix.RESOLVE_NO_MAGICLINKS})
	// Entries that disappeared after readdir no longer occupy this path.
	if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open disk entry: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := w.count(fd); err != nil {
		return err
	}
	// Reopen the pinned inode as a readable directory. ENOTDIR identifies
	// leaves without trusting the directory entry type or reopening its name.
	child, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOTDIR) {
		return nil
	}
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(child), name)
	defer func() { _ = dir.Close() }()
	return w.dir(dir, depth+1)
}
