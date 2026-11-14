// Package metrics exposes per-server Prometheus metrics collected by the heartbeat loop.
//
// Metrics are emitted only when values are known, following the same null/unknown
// contract as status.agent patching: an unreadable source means the metric series
// is not emitted, so a scrape will show a gap rather than a stale or sentinel value.
package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Snapshot holds the latest known resource and player state.
// Each field may be unknown; the corresponding *Known flag indicates validity.
type Snapshot struct {
	// CPU and memory are only emitted when known=true.
	CPUMillicores      int64
	CPUKnown           bool
	CPULimitMillicores int64
	CPULimitKnown      bool

	MemoryBytes      int64
	MemoryKnown      bool
	MemoryLimitBytes int64
	MemoryLimitKnown bool

	// Disk bytes: used and total. Only emitted when known=true.
	DiskUsedBytes  int64
	DiskTotalBytes int64
	DiskTotalKnown bool
	DiskKnown      bool

	// Player counts. PlayersOnline is emitted when known=true.
	// PlayersMax is emitted when known=true: -1 for unlimited (custom regex),
	// or positive for a finite limit. Not emitted (known=false) when the game
	// reported an online count but no maximum (maxN=0).
	PlayersOnline      int
	PlayersOnlineKnown bool
	PlayersMax         int
	PlayersMaxKnown    bool

	// Identifying labels for the server.
	ServerName   string
	Namespace    string
	TemplateName string
	GameName     string
}

// Metrics holds the latest state for a per-pod agent and registers itself
// with the default Prometheus registry as a custom Collector.
type Metrics struct {
	mu       sync.Mutex
	snapshot Snapshot
}

// New creates and registers a new Metrics collector with prometheus.DefaultRegisterer.
// It panics if registration fails.
func New() *Metrics {
	m := &Metrics{}
	prometheus.MustRegister(m)
	return m
}

// Update stores the latest resource usage and player count snapshot from the heartbeat loop.
// This is called synchronously from the heartbeat's sendOnce, so updates are never stale
// relative to what status.agent reports.
func (m *Metrics) Update(snap Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot = snap
}

// Describe implements prometheus.Collector, declaring the metric shapes.
func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- cpuUsageDesc
	ch <- cpuLimitDesc
	ch <- memoryUsageDesc
	ch <- memoryLimitDesc
	ch <- diskUsedDesc
	ch <- diskTotalDesc
	ch <- playersOnlineDesc
	ch <- playersMaxDesc
}

// Collect implements prometheus.Collector, emitting current metric values.
// Unknown values are not emitted (the series is missing), matching the heartbeat's
// null-on-unknown contract for status.agent patching.
func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	snap := m.snapshot
	m.mu.Unlock()

	labels := []string{snap.ServerName, snap.Namespace, snap.TemplateName, snap.GameName}

	// CPU: emit only when known.
	if snap.CPUKnown {
		ch <- prometheus.MustNewConstMetric(cpuUsageDesc, prometheus.GaugeValue, float64(snap.CPUMillicores), labels...)
	}
	if snap.CPULimitKnown {
		ch <- prometheus.MustNewConstMetric(cpuLimitDesc, prometheus.GaugeValue, float64(snap.CPULimitMillicores), labels...)
	}

	// Memory: emit only when known.
	if snap.MemoryKnown {
		ch <- prometheus.MustNewConstMetric(memoryUsageDesc, prometheus.GaugeValue, float64(snap.MemoryBytes), labels...)
	}
	if snap.MemoryLimitKnown {
		ch <- prometheus.MustNewConstMetric(memoryLimitDesc, prometheus.GaugeValue, float64(snap.MemoryLimitBytes), labels...)
	}

	// Disk: emit only when known.
	if snap.DiskKnown {
		ch <- prometheus.MustNewConstMetric(diskUsedDesc, prometheus.GaugeValue, float64(snap.DiskUsedBytes), labels...)
	}
	if snap.DiskTotalKnown {
		ch <- prometheus.MustNewConstMetric(diskTotalDesc, prometheus.GaugeValue, float64(snap.DiskTotalBytes), labels...)
	}

	// Players: emit online when known, emit max only if known.
	// (max can be unknown even when online is known; see snapshot doc.)
	if snap.PlayersOnlineKnown {
		ch <- prometheus.MustNewConstMetric(playersOnlineDesc, prometheus.GaugeValue, float64(snap.PlayersOnline), labels...)
	}
	if snap.PlayersMaxKnown {
		ch <- prometheus.MustNewConstMetric(playersMaxDesc, prometheus.GaugeValue, float64(snap.PlayersMax), labels...)
	}
}

// Metric descriptors with gameplane_ prefix and stable label names.
// Labels: server, namespace, template, game. These identify the GameServer pod
// and allow scrapes to be attributed without additional joins.
var (
	cpuUsageDesc = prometheus.NewDesc(
		"gameplane_agent_cpu_millicores",
		"CPU used by the game process, in millicores. Emitted only when readable.",
		[]string{"server", "namespace", "template", "game"}, nil,
	)
	cpuLimitDesc = prometheus.NewDesc(
		"gameplane_agent_cpu_limit_millicores",
		"CPU limit for the game container, in millicores. Emitted only when readable.",
		[]string{"server", "namespace", "template", "game"}, nil,
	)
	memoryUsageDesc = prometheus.NewDesc(
		"gameplane_agent_memory_bytes",
		"Memory used by the game process, in bytes. Emitted only when readable.",
		[]string{"server", "namespace", "template", "game"}, nil,
	)
	memoryLimitDesc = prometheus.NewDesc(
		"gameplane_agent_memory_limit_bytes",
		"Memory limit for the game container, in bytes. Emitted only when readable.",
		[]string{"server", "namespace", "template", "game"}, nil,
	)
	diskUsedDesc = prometheus.NewDesc(
		"gameplane_agent_disk_used_bytes",
		"Disk used in the game's data directory, in bytes. Emitted only when readable.",
		[]string{"server", "namespace", "template", "game"}, nil,
	)
	diskTotalDesc = prometheus.NewDesc(
		"gameplane_agent_disk_total_bytes",
		"Observed capacity scoped to the game's data directory, in bytes. Emitted only when available.",
		[]string{"server", "namespace", "template", "game"}, nil,
	)
	playersOnlineDesc = prometheus.NewDesc(
		"gameplane_agent_players_online",
		"Number of players currently online. Emitted only when the game RCON succeeded.",
		[]string{"server", "namespace", "template", "game"}, nil,
	)
	playersMaxDesc = prometheus.NewDesc(
		"gameplane_agent_players_max",
		"Maximum player capacity: -1 for unlimited, positive value for a known limit. Emitted only when the capacity is known; absent when unknown (no max reported) or when the RCON query failed.",
		[]string{"server", "namespace", "template", "game"}, nil,
	)
)
