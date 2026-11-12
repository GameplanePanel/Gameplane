package usage

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
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
