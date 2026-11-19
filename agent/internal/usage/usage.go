// Package usage reads the game server's resource consumption from inside
// the pod and exposes it as a Sample for the heartbeat to report.
//
// Everything is sourced in-pod with no cluster-level metrics pipeline
// (Gameplane deliberately has no metrics-server dependency). There are two
// modes:
//
//   - proc mode (default in production): the pod shares its PID namespace
//     (operator sets ShareProcessNamespace), so the agent sums CPU/memory
//     of the game process(es) from /proc — every process except the agent's
//     own subtree and the pause process. This reports the GAME's usage, not
//     the sidecar's. CPU/memory LIMITS come from the operator (the game
//     container's resource limits, passed as env).
//   - cgroup mode (fallback / older pods): CPU and memory come from the
//     pod's cgroup v2 files. Inside a per-container cgroup namespace this
//     reflects only the agent container, so it's a fallback.
//
// Disk is sampled asynchronously within the game data directory. Each value carries
// a "known" flag so callers can patch null ("unknown") rather than a
// misleading zero when a source is unreadable.
package usage

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultCgroupRoot is the cgroup v2 unified-hierarchy mount present in
// every modern pod (Kubernetes 1.28+ defaults to cgroup v2).
const defaultCgroupRoot = "/sys/fs/cgroup"

// defaultProcRoot is the procfs mount.
const defaultProcRoot = "/proc"

// userHZ is the kernel clock-tick rate (CLOCK_TCK) used to convert
// /proc/<pid>/stat jiffies to time. It is 100 on effectively all Linux
// builds; tests override it via Config.clkTck.
const userHZ = 100

// Config selects where the reader looks. Root, DataDir, ProcMode, and the
// limit hints are the public knobs; the rest are injected only by tests.
type Config struct {
	// Root is the cgroup v2 mount point. Empty means defaultCgroupRoot.
	Root string
	// DataDir is the game data volume to measure. Empty
	// disables the disk reading.
	DataDir string

	// ProcMode reads CPU/memory usage from /proc (the game process) instead
	// of the agent's own cgroup. Enabled when the pod shares its PID
	// namespace.
	ProcMode bool
	// CPULimitMillicores / MemLimitBytes are the game container's limits,
	// supplied by the operator in proc mode (the agent can't read another
	// container's cgroup limit). Zero means unknown.
	CPULimitMillicores int64
	MemLimitBytes      int64

	// ProcRoot is the procfs mount; empty means defaultProcRoot.
	ProcRoot string
	// selfPID, clkTck and now are injected only by tests.
	selfPID int
	clkTck  int64
	now     func() time.Time
}

// Sample is one point-in-time reading. Each *Known flag reports whether
// the matching value could be sourced; an unknown value must be reported
// as null, never zero, so the dashboard can render "—".
type Sample struct {
	CPUMillicores      int64
	CPUKnown           bool
	CPULimitMillicores int64
	CPULimitKnown      bool

	MemoryBytes      int64
	MemoryKnown      bool
	MemoryLimitBytes int64
	MemoryLimitKnown bool

	DiskUsedBytes  int64
	DiskTotalBytes int64
	DiskTotalKnown bool
	DiskKnown      bool
}

// Reader produces Samples. It holds the previous CPU usage counter so it
// can turn cumulative CPU time into a rate; keeping that state here (not in
// the heartbeat loop) lets the heartbeat's sendOnce stay a pure function of
// its Config. Read is safe for concurrent use.
type Reader struct {
	cfg Config

	mu             sync.Mutex
	prevUsageUsec  uint64
	prevAt         time.Time
	havePrev       bool
	diskBytes      int64
	diskCapacity   int64
	diskMeasuredAt time.Time
}

// New builds a Reader, applying defaults for any unset Config field.
func New(cfg Config) *Reader {
	if cfg.Root == "" {
		cfg.Root = defaultCgroupRoot
	}
	cfg.Root = filepath.Clean(cfg.Root)
	if cfg.ProcRoot == "" {
		cfg.ProcRoot = defaultProcRoot
	}
	cfg.ProcRoot = filepath.Clean(cfg.ProcRoot)
	if cfg.clkTck == 0 {
		cfg.clkTck = userHZ
	}
	if cfg.selfPID == 0 {
		cfg.selfPID = os.Getpid()
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &Reader{cfg: cfg}
}

// Read takes one sample. It never returns an error: any unreadable source
// leaves its value zero with the corresponding Known flag false. The first
// call after construction reports an unknown CPU rate (no prior counter to
// diff against) — the next call, one heartbeat later, has it.
func (r *Reader) Read() Sample {
	var s Sample
	r.readCPU(&s)
	r.readMemory(&s)
	r.readDisk(&s)
	return s
}

func (r *Reader) readCPU(s *Sample) {
	usageUsec, ok := r.cpuUsageUsec()
	if ok {
		r.mu.Lock()
		now := r.cfg.now()
		if r.havePrev {
			dWall := now.Sub(r.prevAt).Microseconds()
			// Guard against a clock that didn't advance and a counter that
			// went backwards (a process set changing under proc mode, or a
			// counter reset) which would otherwise yield a wild rate.
			if dWall > 0 && usageUsec >= r.prevUsageUsec {
				// Safe: difference is guaranteed positive by the check above, and usageUsec fits in int64.
				diff := usageUsec - r.prevUsageUsec
				if diff > uint64(math.MaxInt64) {
					return
				}
				dUsage := int64(diff)
				s.CPUMillicores = dUsage * 1000 / dWall
				s.CPUKnown = true
			}
		}
		r.prevUsageUsec = usageUsec
		r.prevAt = now
		r.havePrev = true
		r.mu.Unlock()
	}

	if r.cfg.ProcMode {
		if r.cfg.CPULimitMillicores > 0 {
			s.CPULimitMillicores = r.cfg.CPULimitMillicores
			s.CPULimitKnown = true
		}
		return
	}
	if lim, ok := r.cpuLimitMillicores(); ok {
		s.CPULimitMillicores = lim
		s.CPULimitKnown = true
	}
}

// cpuUsageUsec returns cumulative CPU time in microseconds — summed across
// the game process(es) in proc mode, or the cgroup's cpu.stat otherwise.
func (r *Reader) cpuUsageUsec() (uint64, bool) {
	if r.cfg.ProcMode {
		ticks, _, ok := r.procUsage()
		if !ok {
			return 0, false
		}
		// Safe: clkTck is always positive (default 100), never exceeds int32.
		if r.cfg.clkTck <= 0 {
			return 0, false
		}
		return ticks * 1_000_000 / uint64(r.cfg.clkTck), true
	}
	return r.cgroupCPUUsageUsec()
}

// cgroupCPUUsageUsec reads the first "usage_usec" line of cgroup v2's
// cpu.stat.
func (r *Reader) cgroupCPUUsageUsec() (uint64, bool) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(r.cfg.Root, "cpu.stat")))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, false
			}
			return v, true
		}
	}
	return 0, false
}

// cpuLimitMillicores parses cgroup v2's cpu.max ("quota period"). A quota
// of "max" means no limit, which we report as unknown.
func (r *Reader) cpuLimitMillicores() (int64, bool) {
	b, err := os.ReadFile(filepath.Join(r.cfg.Root, "cpu.max"))
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false
	}
	quota, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	period, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || period <= 0 {
		return 0, false
	}
	return quota * 1000 / period, true
}

func (r *Reader) readMemory(s *Sample) {
	if r.cfg.ProcMode {
		if _, rss, ok := r.procUsage(); ok {
			s.MemoryBytes = rss
			s.MemoryKnown = true
		}
		if r.cfg.MemLimitBytes > 0 {
			s.MemoryLimitBytes = r.cfg.MemLimitBytes
			s.MemoryLimitKnown = true
		}
		return
	}
	if v, ok := readUintFile(filepath.Join(r.cfg.Root, "memory.current")); ok {
		// Safe: actual memory usage never exceeds int64.Max in practice.
		if v > uint64(math.MaxInt64) {
			return
		}
		s.MemoryBytes = int64(v)
		s.MemoryKnown = true
	}
	// memory.max is "max" when unlimited, which fails to parse — exactly
	// the "unknown limit" we want to report.
	if v, ok := readUintFile(filepath.Join(r.cfg.Root, "memory.max")); ok {
		// Safe: actual memory limits never exceed int64.Max in practice.
		if v > uint64(math.MaxInt64) {
			return
		}
		s.MemoryLimitBytes = int64(v)
		s.MemoryLimitKnown = true
	}
}

// procUsage sums CPU jiffies and resident memory across every process in
// the shared PID namespace except the agent's own subtree and pid 1 (the
// pause container) — i.e. the game server's processes. ok is false when
// procfs can't be read.
func (r *Reader) procUsage() (cpuTicks uint64, rssBytes int64, ok bool) {
	entries, err := os.ReadDir(r.cfg.ProcRoot)
	if err != nil {
		return 0, 0, false
	}
	type rec struct {
		ppid     int
		ticks    uint64
		rssPages int64
	}
	recs := make(map[int]rec, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a pid dir
		}
		ppid, ticks, sok := readProcStat(filepath.Join(r.cfg.ProcRoot, e.Name(), "stat"), r.cfg.ProcRoot)
		if !sok {
			continue // process exited between ReadDir and read
		}
		rss := readProcStatmRSS(filepath.Join(r.cfg.ProcRoot, e.Name(), "statm"), r.cfg.ProcRoot)
		recs[pid] = rec{ppid: ppid, ticks: ticks, rssPages: rss}
	}
	if len(recs) == 0 {
		return 0, 0, false
	}

	// Exclude the pause process (pid 1) and the agent's own subtree.
	excluded := map[int]bool{1: true, r.cfg.selfPID: true}
	children := map[int][]int{}
	for pid, rc := range recs {
		children[rc.ppid] = append(children[rc.ppid], pid)
	}
	for queue := []int{r.cfg.selfPID}; len(queue) > 0; {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			if !excluded[c] {
				excluded[c] = true
				queue = append(queue, c)
			}
		}
	}

	var pages int64
	for pid, rc := range recs {
		if excluded[pid] {
			continue
		}
		cpuTicks += rc.ticks
		pages += rc.rssPages
	}
	return cpuTicks, pages * int64(os.Getpagesize()), true
}

// readProcStat parses ppid and utime+stime (jiffies) from /proc/<pid>/stat.
// The comm field (2nd) is wrapped in parens and may contain spaces and
// parens, so fields are taken relative to the final ')'.
func readProcStat(path string, procRoot string) (ppid int, ticks uint64, ok bool) {
	// Validate path is within procRoot to guard against directory traversal.
	pathClean := filepath.Clean(path)
	procRoot = filepath.Clean(procRoot)
	if !strings.HasPrefix(pathClean, procRoot+string(os.PathSeparator)) && pathClean != procRoot {
		return 0, 0, false
	}
	b, err := os.ReadFile(pathClean)
	if err != nil {
		return 0, 0, false
	}
	s := string(b)
	closeParen := strings.LastIndexByte(s, ')')
	if closeParen < 0 || closeParen+2 > len(s) {
		return 0, 0, false
	}
	// Fields after comm; field 3 (state) is index 0 here, so stat field N is
	// index N-3: ppid=4→[1], utime=14→[11], stime=15→[12].
	f := strings.Fields(s[closeParen+1:])
	if len(f) < 13 {
		return 0, 0, false
	}
	ppid, err = strconv.Atoi(f[1])
	if err != nil {
		return 0, 0, false
	}
	ut, err1 := strconv.ParseUint(f[11], 10, 64)
	st, err2 := strconv.ParseUint(f[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return ppid, ut + st, true
}

// readProcStatmRSS returns the resident set size in pages (field 2 of
// /proc/<pid>/statm), or 0 when unreadable.
func readProcStatmRSS(path string, procRoot string) int64 {
	// Validate path is within procRoot to guard against directory traversal.
	pathClean := filepath.Clean(path)
	procRoot = filepath.Clean(procRoot)
	if !strings.HasPrefix(pathClean, procRoot+string(os.PathSeparator)) && pathClean != procRoot {
		return 0
	}
	b, err := os.ReadFile(pathClean)
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	rss, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return 0
	}
	return rss
}

// readUintFile parses a cgroup file holding a single unsigned integer.
func readUintFile(path string) (uint64, bool) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
