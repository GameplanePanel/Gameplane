package usage

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
)

func TestAllocatedDiskUsage(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "server")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "region")
	if err := os.WriteFile(file, []byte("world"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, 64<<20); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, file+"-link"); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "sibling")
	if err := os.WriteFile(outside, make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "external")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, path := range []string{root, file, link} {
		var st unix.Stat_t
		if err := unix.Lstat(path, &st); err != nil {
			t.Fatal(err)
		}
		want += st.Blocks * 512
	}
	got, err := volumeUsage(context.Background(), root, 100)
	if err != nil || got.used != want {
		t.Fatalf("size=%v, want %d; err=%v", got, want, err)
	}
	rootLink := filepath.Join(parent, "data")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}
	if got, err := volumeUsage(context.Background(), rootLink, 100); err != nil || got.used != want {
		t.Fatalf("trusted root symlink: size=%v, want %d; err=%v", got, want, err)
	}
	if _, err = volumeUsage(context.Background(), root, 2); err == nil {
		t.Fatal("published incomplete scan")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = volumeUsage(ctx, root, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err = volumeUsage(context.Background(), outside+"-missing", 100); err == nil {
		t.Fatal("missing directory returned a sample")
	}
}
func TestDiskScanDoesNotBlockHeartbeat(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	r := New(Config{Root: t.TempDir(), DataDir: "/data"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.monitorDisk(ctx, func(ctx context.Context, _ string, _ int) (diskSample, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return diskSample{}, ctx.Err()
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	result := make(chan Sample, 1)
	go func() { result <- r.Read() }()
	select {
	case s := <-result:
		if s.DiskKnown {
			t.Fatal("partial scan published")
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat blocked by disk scan")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestCephDirectoryCounters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]int64
		want   diskSample
		ok     bool
	}{
		{"quota", map[string]int64{"ceph.dir.rbytes": 0, "ceph.quota.max_bytes": 9000}, diskSample{0, 9000}, true},
		{"no local quota", map[string]int64{"ceph.dir.rbytes": 20}, diskSample{20, 0}, true},
		{"missing counters", nil, diskSample{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cephDiskUsage(func(key string) (int64, error) {
				v, exists := tc.values[key]
				if !exists {
					return 0, unix.ENODATA
				}
				return v, nil
			})
			if ok != tc.ok || got != tc.want {
				t.Fatalf("%+v %v", got, ok)
			}
		})
	}
}

func TestDiskWalkDoesNotDescendIntoProcMount(t *testing.T) {
	root, err := os.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var proc unix.Statfs_t
	if unix.Statfs("/proc", &proc) != nil || proc.Type != unix.PROC_SUPER_MAGIC {
		t.Skip("procfs not mounted")
	}
	walk := diskWalk{ctx: context.Background(), remaining: 0}
	if err := walk.entry(int(root.Fd()), "proc", 0); err != nil || walk.bytes != 0 {
		t.Fatalf("walk crossed mount: bytes=%d, err=%v", walk.bytes, err)
	}
}
func TestDiskWalkExcludesSameDeviceBind(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mount := filepath.Join(root, "nested")
	if err := os.Mkdir(mount, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "foreign"), make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount(outside, mount, "", unix.MS_BIND, ""); err != nil {
		if errors.Is(err, unix.EPERM) {
			t.Skip("bind mount needs a private privileged test namespace")
		}
		t.Fatal(err)
	}
	defer unix.Unmount(mount, 0)
	var st unix.Stat_t
	if err := unix.Stat(root, &st); err != nil {
		t.Fatal(err)
	}
	got, err := volumeUsage(context.Background(), root, 100)
	if err != nil || got.used != st.Blocks*512 {
		t.Fatalf("nested bind was counted: %+v %v", got, err)
	}
}

func TestUnlinkDuringDirectoryScan(t *testing.T) {
	root, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	walk := diskWalk{ctx: context.Background(), remaining: 1}
	if err := walk.entry(int(root.Fd()), "already-removed", 0); err != nil || walk.bytes != 0 {
		t.Fatalf("removed entry: %d %v", walk.bytes, err)
	}
}

func TestDiskMonitorPublishesOnlyFreshCompleteScans(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "region")
	if err := os.WriteFile(file, []byte("world"), 0o600); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		r := New(Config{Root: root, DataDir: root})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		finish := make(chan struct{})
		var starts int
		go r.monitorDisk(ctx, func(ctx context.Context, root string, limit int) (diskSample, error) {
			starts++
			s, err := volumeUsage(ctx, root, limit)
			<-finish
			return s, err
		})
		synctest.Wait()
		if starts != 1 || r.Read().DiskKnown {
			t.Fatal("published first scan before completion")
		}
		time.Sleep(diskTimeout / 2)
		finish <- struct{}{}
		synctest.Wait()
		if s := r.Read(); !s.DiskKnown || s.DiskUsedBytes <= 0 || s.DiskTotalKnown {
			t.Fatalf("complete scan: %+v", s)
		}
		time.Sleep(diskInterval)
		synctest.Wait()
		if starts != 2 || !r.Read().DiskKnown {
			t.Fatal("fresh prior measurement disappeared during second scan")
		}
		time.Sleep(diskTimeout)
		if r.Read().DiskKnown {
			t.Fatal("published expired sample while the second scan was blocked")
		}
		finish <- struct{}{}
		synctest.Wait()
		if r.Read().DiskKnown {
			t.Fatal("late scan result resurrected a stale measurement")
		}
	})
}

func TestPublicDiskMonitorAndFailedRefresh(t *testing.T) {
	root := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		New(Config{}).MonitorDisk(ctx)
		r := New(Config{Root: root, DataDir: root})
		go r.MonitorDisk(ctx)
		synctest.Wait()
		if !r.Read().DiskKnown {
			t.Fatal("missing initial measurement")
		}
		if err := os.Remove(root); err != nil {
			t.Fatal(err)
		}
		time.Sleep(diskInterval)
		synctest.Wait()
		if r.Read().DiskKnown {
			t.Fatal("measurement from a removed data directory remained visible")
		}
	})
}

func TestDiskUsageRecursesAndRejectsDeepTrees(t *testing.T) {
	root := t.TempDir()
	where := root
	var want int64
	for i := 0; i < 4; i++ {
		var st unix.Stat_t
		if err := unix.Stat(where, &st); err != nil {
			t.Fatal(err)
		}
		want += st.Blocks * 512
		child := filepath.Join(where, "child")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		where = child
	}
	file := filepath.Join(where, "data")
	if err := os.WriteFile(file, make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{where, file} {
		var st unix.Stat_t
		if err := unix.Stat(path, &st); err != nil {
			t.Fatal(err)
		}
		want += st.Blocks * 512
	}
	if got, err := volumeUsage(t.Context(), root, maxDiskEntries); err != nil || got.used != want {
		t.Fatalf("recursive size: %+v want %d, err=%v", got, want, err)
	}
	for i := 4; i <= maxDiskDepth; i++ {
		where = filepath.Join(where, "d")
		if err := os.Mkdir(where, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := volumeUsage(t.Context(), root, maxDiskEntries); err == nil || got.used != 0 {
		t.Fatalf("deep scan published %+v (%v)", got, err)
	}
}

func TestDiskUsageReadFailures(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("allocated"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if bytes, err := allocatedDiskUsage(t.Context(), f, 50); !errors.Is(err, unix.ENOTDIR) || bytes != 0 {
		t.Fatalf("non-directory produced %d bytes, %v", bytes, err)
	}
	w := diskWalk{ctx: t.Context(), bytes: math.MaxInt64}
	if err := w.count(int(f.Fd())); err == nil {
		t.Fatal("overflow produced a finite measurement")
	}
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.entry(int(dir.Fd()), "file", 0); err == nil {
		t.Fatal("entry overflow did not abort scan")
	}
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes, err := allocatedDiskUsage(t.Context(), dir, 0); !errors.Is(err, unix.EBADF) || bytes != 0 {
		t.Fatalf("closed root produced %d bytes, %v", bytes, err)
	}
	if err := w.entry(-1, "child", 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid parent: %v", err)
	}
}

func TestDiskUsageUnreadableSubtree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode checks")
	}
	root := t.TempDir()
	child := filepath.Join(root, "save")
	if err := os.Mkdir(child, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(child, 0o700) })
	got, err := volumeUsage(t.Context(), root, 100)
	if !errors.Is(err, unix.EACCES) || got.used != 0 {
		t.Fatalf("unreadable subtree produced %+v (%v)", got, err)
	}
}

func TestDiskAttributesFromPinnedDirectory(t *testing.T) {
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	fd := int(dir.Fd())
	set := func(k, v string) {
		t.Helper()
		if err := unix.Fsetxattr(fd, "user."+k, []byte(v), 0); errors.Is(err, unix.ENOTSUP) {
			t.Skip("filesystem does not support user xattrs")
		} else if err != nil {
			t.Fatal(err)
		}
	}
	// The same descriptor-based getxattr and parser used on kernel CephFS
	// can be exercised on an ordinary Linux filesystem with user attributes.
	read := func(key string) (int64, error) { return diskAttribute(fd, "user."+key) }
	set("ceph.dir.rbytes", "123456")
	if got, ok := cephDiskUsage(read); !ok || got != (diskSample{used: 123456}) {
		t.Fatalf("missing quota: %+v %t", got, ok)
	}
	set("ceph.quota.max_bytes", "204800")
	if got, ok := cephDiskUsage(read); !ok || got != (diskSample{used: 123456, capacity: 204800}) {
		t.Fatalf("measured quota: %+v %t", got, ok)
	}
	set("ceph.dir.rbytes", "invalid")
	if _, ok := cephDiskUsage(read); ok {
		t.Fatal("malformed counter accepted")
	}
}
