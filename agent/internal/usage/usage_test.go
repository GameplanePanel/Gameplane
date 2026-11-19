package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// TestRead_CPURate verifies the cumulative usage_usec counter is turned
// into a millicores rate using the wall-clock delta, and that the first
// read (no prior sample) reports an unknown rate while still reading the
// limit.
func TestRead_CPURate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cpu.stat", "usage_usec 1000000\nuser_usec 1\nsystem_usec 1\n")
	writeFile(t, dir, "cpu.max", "200000 100000\n")

	clock := &fakeClock{t: time.Unix(100, 0)}
	r := New(Config{Root: dir, now: clock.now})

	s1 := r.Read()
	if s1.CPUKnown {
		t.Fatalf("first read should report unknown CPU rate, got %d", s1.CPUMillicores)
	}
	if !s1.CPULimitKnown || s1.CPULimitMillicores != 2000 {
		t.Fatalf("cpu limit = %d known=%v, want 2000", s1.CPULimitMillicores, s1.CPULimitKnown)
	}

	// 2 CPU-seconds consumed over 2 wall-seconds = exactly 1 core.
	clock.t = clock.t.Add(2 * time.Second)
	writeFile(t, dir, "cpu.stat", "usage_usec 3000000\n")
	s2 := r.Read()
	if !s2.CPUKnown || s2.CPUMillicores != 1000 {
		t.Fatalf("cpu millicores = %d known=%v, want 1000", s2.CPUMillicores, s2.CPUKnown)
	}
}

// TestRead_CPUNoWallAdvance covers the guard where the clock did not move
// between samples — no rate can be computed.
func TestRead_CPUNoWallAdvance(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cpu.stat", "usage_usec 5\n")
	clock := &fakeClock{t: time.Unix(100, 0)}
	r := New(Config{Root: dir, now: clock.now})

	r.Read() // seed prev
	writeFile(t, dir, "cpu.stat", "usage_usec 10\n")
	s := r.Read() // same clock value
	if s.CPUKnown {
		t.Fatalf("no wall advance should leave CPU unknown, got %d", s.CPUMillicores)
	}
}

// TestRead_CPUCounterReset covers the guard where the cumulative counter
// goes backwards (a cgroup/container reset) — no rate is emitted rather
// than a wild negative-turned-huge value.
func TestRead_CPUCounterReset(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cpu.stat", "usage_usec 5000000\n")
	clock := &fakeClock{t: time.Unix(100, 0)}
	r := New(Config{Root: dir, now: clock.now})

	r.Read() // seed prev at 5,000,000
	clock.t = clock.t.Add(2 * time.Second)
	writeFile(t, dir, "cpu.stat", "usage_usec 1000000\n") // counter went backwards
	s := r.Read()
	if s.CPUKnown {
		t.Fatalf("a backwards usage counter should leave CPU unknown, got %d", s.CPUMillicores)
	}
}

func TestRead_CPUMaxUnlimited(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cpu.max", "max 100000\n")
	s := New(Config{Root: dir}).Read()
	if s.CPULimitKnown {
		t.Fatalf("unlimited cpu.max should report unknown limit")
	}
}

func TestRead_Memory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "memory.current", "536870912\n")
	writeFile(t, dir, "memory.max", "1073741824\n")
	s := New(Config{Root: dir}).Read()
	if !s.MemoryKnown || s.MemoryBytes != 536870912 {
		t.Fatalf("memory = %d known=%v", s.MemoryBytes, s.MemoryKnown)
	}
	if !s.MemoryLimitKnown || s.MemoryLimitBytes != 1073741824 {
		t.Fatalf("memory limit = %d known=%v", s.MemoryLimitBytes, s.MemoryLimitKnown)
	}
}

func TestRead_MemoryMaxUnlimited(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "memory.current", "1024\n")
	writeFile(t, dir, "memory.max", "max\n")
	s := New(Config{Root: dir}).Read()
	if !s.MemoryKnown || s.MemoryBytes != 1024 {
		t.Fatalf("memory = %d known=%v", s.MemoryBytes, s.MemoryKnown)
	}
	if s.MemoryLimitKnown {
		t.Fatalf("unlimited memory.max should report unknown limit")
	}
}

func TestRead_DiskDisabledWhenNoDataDir(t *testing.T) {
	s := New(Config{Root: t.TempDir()}).Read()
	if s.DiskKnown {
		t.Fatalf("disk should be disabled without a DataDir")
	}
}

func TestRead_AllUnknownWhenAbsent(t *testing.T) {
	s := New(Config{Root: t.TempDir()}).Read()
	if s.CPUKnown || s.CPULimitKnown || s.MemoryKnown || s.MemoryLimitKnown || s.DiskKnown {
		t.Fatalf("everything should be unknown on an empty cgroup root: %+v", s)
	}
}

func TestRead_MalformedValues(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cpu.stat", "user_usec 1\nusage_usec notanumber\n")
	writeFile(t, dir, "cpu.max", "abc def\n")
	writeFile(t, dir, "memory.current", "xyz\n")
	s := New(Config{Root: dir}).Read()
	if s.CPUKnown || s.CPULimitKnown || s.MemoryKnown {
		t.Fatalf("malformed values should be unknown: %+v", s)
	}
}

func TestRead_CPUStatWithoutUsageLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cpu.stat", "user_usec 5\nsystem_usec 3\n")
	s := New(Config{Root: dir}).Read()
	if s.CPUKnown {
		t.Fatalf("cpu.stat without usage_usec should be unknown")
	}
}

func TestRead_CPUMaxWrongFieldCount(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cpu.max", "100000\n")
	s := New(Config{Root: dir}).Read()
	if s.CPULimitKnown {
		t.Fatalf("malformed cpu.max should be unknown")
	}
}

func TestRead_CPUMaxZeroPeriod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cpu.max", "100000 0\n")
	s := New(Config{Root: dir}).Read()
	if s.CPULimitKnown {
		t.Fatalf("zero period should be unknown")
	}
}

func TestNew_DefaultsRoot(t *testing.T) {
	r := New(Config{})
	if r.cfg.Root != defaultCgroupRoot {
		t.Fatalf("default root = %q, want %q", r.cfg.Root, defaultCgroupRoot)
	}
	if r.cfg.now == nil {
		t.Fatalf("clock should default to non-nil")
	}
}

func TestNew_ProcDefaults(t *testing.T) {
	r := New(Config{ProcMode: true})
	if r.cfg.ProcRoot != defaultProcRoot {
		t.Fatalf("default proc root = %q, want %q", r.cfg.ProcRoot, defaultProcRoot)
	}
	if r.cfg.clkTck != userHZ {
		t.Fatalf("default clkTck = %d, want %d", r.cfg.clkTck, userHZ)
	}
	if r.cfg.selfPID != os.Getpid() {
		t.Fatalf("default selfPID = %d, want %d", r.cfg.selfPID, os.Getpid())
	}
}

// writeProc lays down a fake /proc/<pid>/{stat,statm} pair. The stat layout
// keeps the comm in parens (field 2) and places ppid at field 4 with utime
// (field 14) carrying all the ticks and stime (field 15) zero — the reader
// sums the two. Nine zero placeholders sit between ppid and utime.
func writeProc(t *testing.T, procRoot string, pid, ppid int, comm string, ticks uint64, rssPages int64) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir proc/%d: %v", pid, err)
	}
	writeFile(t, dir, "stat", fmt.Sprintf("%d (%s) S %d 0 0 0 0 0 0 0 0 0 %d 0 0 0\n", pid, comm, ppid, ticks))
	writeFile(t, dir, "statm", fmt.Sprintf("1000 %d 0 0 0 0 0\n", rssPages))
}

// TestRead_ProcMode covers the shared-PID-namespace path: CPU/memory are
// summed across the game process(es), excluding pid 1 (pause) and the agent's
// own subtree, and the CPU/memory limits come from the operator's hints.
func TestRead_ProcMode(t *testing.T) {
	proc := t.TempDir()
	const self = 50
	// excluded (pid 1)
	writeProc(t, proc, 1, 0, "pause", 999, 999)
	// excluded (selfPID)
	writeProc(t, proc, self, 1, "agent", 500, 500)
	// excluded (agent subtree)
	writeProc(t, proc, 60, self, "agent-tls", 300, 300)
	// counted
	writeProc(t, proc, 100, 1, "javaserver", 100, 10)
	// counted (child of game)
	writeProc(t, proc, 101, 100, "java-gc", 0, 5)

	clock := &fakeClock{t: time.Unix(100, 0)}
	r := New(Config{
		ProcMode:           true,
		ProcRoot:           proc,
		selfPID:            self,
		clkTck:             100,
		now:                clock.now,
		CPULimitMillicores: 4000,
		MemLimitBytes:      8 << 30,
	})

	// First read: no prior CPU counter to diff, but memory and the limit
	// hints are immediately known.
	s1 := r.Read()
	if s1.CPUKnown {
		t.Fatalf("first proc read should report unknown CPU rate, got %d", s1.CPUMillicores)
	}
	wantRSS := int64(15) * int64(os.Getpagesize()) // 10 + 5 game pages
	if !s1.MemoryKnown || s1.MemoryBytes != wantRSS {
		t.Fatalf("proc memory = %d known=%v, want %d", s1.MemoryBytes, s1.MemoryKnown, wantRSS)
	}
	if !s1.CPULimitKnown || s1.CPULimitMillicores != 4000 {
		t.Fatalf("cpu limit = %d known=%v, want 4000", s1.CPULimitMillicores, s1.CPULimitKnown)
	}
	if !s1.MemoryLimitKnown || s1.MemoryLimitBytes != 8<<30 {
		t.Fatalf("mem limit = %d known=%v, want %d", s1.MemoryLimitBytes, s1.MemoryLimitKnown, int64(8<<30))
	}

	// One wall-second later the game burned 100 more ticks (1 CPU-second at
	// clkTck 100) → exactly one core.
	clock.t = clock.t.Add(time.Second)
	writeProc(t, proc, 100, 1, "javaserver", 200, 10)
	s2 := r.Read()
	if !s2.CPUKnown || s2.CPUMillicores != 1000 {
		t.Fatalf("proc cpu millicores = %d known=%v, want 1000", s2.CPUMillicores, s2.CPUKnown)
	}
}

func TestRead_ProcModeNoProcesses(t *testing.T) {
	// An empty procfs has nothing to sum → usage unknown, but the operator's
	// limit hint still reports.
	r := New(Config{ProcMode: true, ProcRoot: t.TempDir(), selfPID: 50, CPULimitMillicores: 1000})
	s := r.Read()
	if s.CPUKnown || s.MemoryKnown {
		t.Fatalf("no processes should leave usage unknown: %+v", s)
	}
	if !s.CPULimitKnown || s.CPULimitMillicores != 1000 {
		t.Fatalf("cpu limit hint should still report: %+v", s)
	}
}

func TestRead_ProcModeUnreadableRoot(t *testing.T) {
	r := New(Config{ProcMode: true, ProcRoot: filepath.Join(t.TempDir(), "absent"), selfPID: 50})
	s := r.Read()
	if s.CPUKnown || s.MemoryKnown {
		t.Fatalf("unreadable proc root should leave usage unknown: %+v", s)
	}
}

func TestReadProcStat(t *testing.T) {
	dir := t.TempDir()
	// A comm with spaces and nested parens must not confuse field parsing —
	// the reader keys off the final ')'.
	writeFile(t, dir, "ok", "100 (my (weird) game) S 7 0 0 0 0 0 0 0 0 0 11 4 0 0\n")
	if ppid, ticks, ok := readProcStat(filepath.Join(dir, "ok"), dir); !ok || ppid != 7 || ticks != 15 {
		t.Fatalf("readProcStat ok = (%d,%d,%v), want (7,15,true)", ppid, ticks, ok)
	}
	writeFile(t, dir, "short", "100 (x) S 1 2 3\n") // too few fields after comm
	if _, _, ok := readProcStat(filepath.Join(dir, "short"), dir); ok {
		t.Fatalf("short stat should not parse")
	}
	writeFile(t, dir, "noparen", "100 x S 1\n") // no closing paren
	if _, _, ok := readProcStat(filepath.Join(dir, "noparen"), dir); ok {
		t.Fatalf("missing ) should not parse")
	}
	writeFile(t, dir, "badppid", "100 (x) S z 0 0 0 0 0 0 0 0 0 1 1 0 0\n")
	if _, _, ok := readProcStat(filepath.Join(dir, "badppid"), dir); ok {
		t.Fatalf("non-numeric ppid should not parse")
	}
	writeFile(t, dir, "badtime", "100 (x) S 7 0 0 0 0 0 0 0 0 0 a b 0 0\n")
	if _, _, ok := readProcStat(filepath.Join(dir, "badtime"), dir); ok {
		t.Fatalf("non-numeric times should not parse")
	}
	if _, _, ok := readProcStat(filepath.Join(dir, "absent"), dir); ok {
		t.Fatalf("absent stat should not parse")
	}
}

func TestReadProcStatmRSS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok", "1000 42 7 0 0 0 0\n")
	if got := readProcStatmRSS(filepath.Join(dir, "ok"), dir); got != 42 {
		t.Fatalf("rss = %d, want 42", got)
	}
	writeFile(t, dir, "short", "1000\n")
	if got := readProcStatmRSS(filepath.Join(dir, "short"), dir); got != 0 {
		t.Fatalf("short statm rss = %d, want 0", got)
	}
	writeFile(t, dir, "bad", "1000 notanum\n")
	if got := readProcStatmRSS(filepath.Join(dir, "bad"), dir); got != 0 {
		t.Fatalf("bad statm rss = %d, want 0", got)
	}
	if got := readProcStatmRSS(filepath.Join(dir, "absent"), dir); got != 0 {
		t.Fatalf("absent statm rss = %d, want 0", got)
	}
}
