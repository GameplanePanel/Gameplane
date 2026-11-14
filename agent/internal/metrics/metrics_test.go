package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsEmitsWhenKnown(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		CPUMillicores:      500,
		CPUKnown:           true,
		CPULimitMillicores: 1000,
		CPULimitKnown:      true,
		MemoryBytes:        1024 * 1024 * 512, // 512 MiB
		MemoryKnown:        true,
		MemoryLimitBytes:   1024 * 1024 * 1024, // 1 GiB
		MemoryLimitKnown:   true,
		DiskUsedBytes:      1024 * 1024 * 100,  // 100 MiB
		DiskTotalBytes:     1024 * 1024 * 1024, // 1 GiB
		DiskTotalKnown:     true,
		DiskKnown:          true,
		PlayersOnline:      5,
		PlayersOnlineKnown: true,
		PlayersMax:         20,
		PlayersMaxKnown:    true,
		ServerName:         "test-server",
		Namespace:          "games",
		TemplateName:       "minecraft-java",
		GameName:           "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Expect 8 metric families (2 CPU, 2 Memory, 2 Disk, 2 Players).
	if len(metrics) != 8 {
		t.Errorf("expected 8 metric families, got %d", len(metrics))
	}

	// Check for expected metric names.
	expectedNames := map[string]bool{
		"gameplane_agent_cpu_millicores":       false,
		"gameplane_agent_cpu_limit_millicores": false,
		"gameplane_agent_memory_bytes":         false,
		"gameplane_agent_memory_limit_bytes":   false,
		"gameplane_agent_disk_used_bytes":      false,
		"gameplane_agent_disk_total_bytes":     false,
		"gameplane_agent_players_online":       false,
		"gameplane_agent_players_max":          false,
	}
	for _, family := range metrics {
		if family.Name != nil {
			expectedNames[*family.Name] = true
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("metric %q not found", name)
		}
	}

	// Check one metric's value and labels.
	for _, family := range metrics {
		if family.Name != nil && *family.Name == "gameplane_agent_cpu_millicores" {
			if len(family.Metric) != 1 {
				t.Fatalf("expected 1 sample for cpu_millicores, got %d", len(family.Metric))
			}
			if family.Metric[0].Gauge.Value == nil {
				t.Fatal("cpu_millicores value is nil")
			}
			if *family.Metric[0].Gauge.Value != 500 {
				t.Errorf("expected cpu_millicores=500, got %v", *family.Metric[0].Gauge.Value)
			}
			// Check labels.
			labelMap := map[string]string{}
			for _, lp := range family.Metric[0].Label {
				labelMap[*lp.Name] = *lp.Value
			}
			if labelMap["server"] != "test-server" {
				t.Errorf("expected server=test-server, got %s", labelMap["server"])
			}
			if labelMap["namespace"] != "games" {
				t.Errorf("expected namespace=games, got %s", labelMap["namespace"])
			}
			if labelMap["template"] != "minecraft-java" {
				t.Errorf("expected template=minecraft-java, got %s", labelMap["template"])
			}
			if labelMap["game"] != "minecraft" {
				t.Errorf("expected game=minecraft, got %s", labelMap["game"])
			}
			break
		}
	}
}

func TestMetricsOmitsWhenUnknown(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		// All unknown: no *Known flags are true.
		ServerName:   "test-server",
		Namespace:    "games",
		TemplateName: "minecraft-java",
		GameName:     "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Expect 0 metric families when everything is unknown.
	if len(metrics) != 0 {
		t.Errorf("expected 0 metric families, got %d", len(metrics))
	}
}

func TestMetricsPartiallyUnknown(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		// Only CPU and players known.
		CPUMillicores:      300,
		CPUKnown:           true,
		CPULimitMillicores: 2000,
		CPULimitKnown:      true,
		PlayersOnline:      10,
		PlayersOnlineKnown: true,
		PlayersMax:         50,
		PlayersMaxKnown:    true,
		// Memory and Disk: unknown
		MemoryKnown:      false,
		MemoryLimitKnown: false,
		DiskKnown:        false,
		ServerName:       "test-server",
		Namespace:        "games",
		TemplateName:     "minecraft-java",
		GameName:         "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Expect 4 metric families: 2 CPU + 2 Players, no Memory or Disk.
	if len(metrics) != 4 {
		t.Errorf("expected 4 metric families, got %d", len(metrics))
	}

	names := map[string]bool{}
	for _, family := range metrics {
		if family.Name != nil {
			names[*family.Name] = true
		}
	}

	expectedPresent := []string{
		"gameplane_agent_cpu_millicores",
		"gameplane_agent_cpu_limit_millicores",
		"gameplane_agent_players_online",
		"gameplane_agent_players_max",
	}
	for _, name := range expectedPresent {
		if !names[name] {
			t.Errorf("expected metric %q to be present", name)
		}
	}

	shouldNotBePresent := []string{
		"gameplane_agent_memory_bytes",
		"gameplane_agent_memory_limit_bytes",
		"gameplane_agent_disk_used_bytes",
		"gameplane_agent_disk_total_bytes",
	}
	for _, name := range shouldNotBePresent {
		if names[name] {
			t.Errorf("expected metric %q to not be present", name)
		}
	}
}

func TestMetricsConcurrentUpdate(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	// Start with a known value.
	m.Update(Snapshot{
		CPUMillicores: 100,
		CPUKnown:      true,
		ServerName:    "server",
		Namespace:     "ns",
		TemplateName:  "t",
		GameName:      "g",
	})

	// Update it concurrently with a gather.
	done := make(chan bool)
	go func() {
		for i := 0; i < 10; i++ {
			m.Update(Snapshot{
				CPUMillicores: int64(100 + i),
				CPUKnown:      true,
				ServerName:    "server",
				Namespace:     "ns",
				TemplateName:  "t",
				GameName:      "g",
			})
		}
		done <- true
	}()

	for i := 0; i < 10; i++ {
		_, err := reg.Gather()
		if err != nil {
			t.Fatalf("gather: %v", err)
		}
	}

	<-done
}

func TestMetricsZeroValuesEmitted(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		// Zero is a valid measurement (e.g., no players online, 0 disk used).
		CPUMillicores:      0,
		CPUKnown:           true,
		MemoryBytes:        0,
		MemoryKnown:        true,
		DiskUsedBytes:      0,
		DiskTotalKnown:     true,
		DiskKnown:          true,
		PlayersOnline:      0,
		PlayersOnlineKnown: true,
		ServerName:         "test-server",
		Namespace:          "games",
		TemplateName:       "minecraft-java",
		GameName:           "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// All metrics should be present with their zero values.
	if len(metrics) == 0 {
		t.Fatal("expected metrics to be emitted for zero values")
	}

	for _, family := range metrics {
		if family.Name == nil {
			continue
		}
		if len(family.Metric) > 0 && family.Metric[0].Gauge.Value != nil {
			if *family.Metric[0].Gauge.Value != 0 {
				t.Errorf("%q expected value 0, got %v", *family.Name, *family.Metric[0].Gauge.Value)
			}
		}
	}
}

func TestMetricsPlayerMaxUnlimited(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		PlayersOnline:      5,
		PlayersMax:         -1, // Unlimited
		PlayersOnlineKnown: true,
		PlayersMaxKnown:    true,
		ServerName:         "test-server",
		Namespace:          "games",
		TemplateName:       "minecraft-java",
		GameName:           "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Find the players_max metric and verify it's -1.
	for _, family := range metrics {
		if family.Name != nil && *family.Name == "gameplane_agent_players_max" {
			if len(family.Metric) > 0 && family.Metric[0].Gauge.Value != nil {
				if *family.Metric[0].Gauge.Value != -1 {
					t.Errorf("expected players_max=-1, got %v", *family.Metric[0].Gauge.Value)
				}
			}
			return
		}
	}
	t.Error("players_max metric not found")
}

func TestMetricsUpdateSwapsSnapshot(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	// First snapshot.
	m.Update(Snapshot{
		CPUMillicores: 100,
		CPUKnown:      true,
		ServerName:    "server1",
		Namespace:     "ns1",
		TemplateName:  "t1",
		GameName:      "g1",
	})

	metrics1, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather 1: %v", err)
	}

	// Find the label value for server.
	var server1 string
	for _, family := range metrics1 {
		if family.Name != nil && *family.Name == "gameplane_agent_cpu_millicores" {
			if len(family.Metric) > 0 {
				for _, lp := range family.Metric[0].Label {
					if lp.Name != nil && *lp.Name == "server" {
						server1 = *lp.Value
					}
				}
			}
		}
	}
	if server1 != "server1" {
		t.Errorf("expected server=server1, got %s", server1)
	}

	// Update with a different snapshot.
	m.Update(Snapshot{
		CPUMillicores: 200,
		CPUKnown:      true,
		ServerName:    "server2",
		Namespace:     "ns2",
		TemplateName:  "t2",
		GameName:      "g2",
	})

	metrics2, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather 2: %v", err)
	}

	// Find the new label value.
	var server2 string
	for _, family := range metrics2 {
		if family.Name != nil && *family.Name == "gameplane_agent_cpu_millicores" {
			if len(family.Metric) > 0 {
				for _, lp := range family.Metric[0].Label {
					if lp.Name != nil && *lp.Name == "server" {
						server2 = *lp.Value
					}
				}
			}
		}
	}
	if server2 != "server2" {
		t.Errorf("expected server=server2, got %s", server2)
	}
}

func TestMetricsEmptyLabels(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		CPUMillicores: 100,
		CPUKnown:      true,
		// Labels are empty strings.
		ServerName:   "",
		Namespace:    "",
		TemplateName: "",
		GameName:     "",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Should still emit, just with empty label values.
	if len(metrics) == 0 {
		t.Fatal("expected metrics to be emitted even with empty labels")
	}
}

func TestMetricsDiskZeroTotal(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		DiskUsedBytes:  0,
		DiskTotalBytes: 0, // Edge case: zero total (e.g., mount failed)
		DiskTotalKnown: true,
		DiskKnown:      true,
		ServerName:     "test-server",
		Namespace:      "games",
		TemplateName:   "minecraft-java",
		GameName:       "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Should emit both disk metrics with 0 values.
	diskMetrics := 0
	for _, family := range metrics {
		if family.Name != nil && (*family.Name == "gameplane_agent_disk_used_bytes" || *family.Name == "gameplane_agent_disk_total_bytes") {
			diskMetrics++
		}
	}
	if diskMetrics != 2 {
		t.Errorf("expected 2 disk metrics, got %d", diskMetrics)
	}
}

func TestMetricsLargeValues(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		// Simulate a large memory-heavy server (e.g., 256 GiB).
		MemoryBytes:      256 * 1024 * 1024 * 1024,
		MemoryKnown:      true,
		MemoryLimitBytes: 512 * 1024 * 1024 * 1024,
		MemoryLimitKnown: true,
		ServerName:       "test-server",
		Namespace:        "games",
		TemplateName:     "minecraft-java",
		GameName:         "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Verify large values are preserved.
	for _, family := range metrics {
		if family.Name != nil && *family.Name == "gameplane_agent_memory_bytes" {
			if len(family.Metric) > 0 && family.Metric[0].Gauge.Value != nil {
				if *family.Metric[0].Gauge.Value != float64(256*1024*1024*1024) {
					t.Errorf("expected memory_bytes=%d, got %v", 256*1024*1024*1024, *family.Metric[0].Gauge.Value)
				}
			}
		}
	}
}

func TestMetricsPlayersMaxUnknown(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		// Players online is known, but max is not (e.g., no max reported by game).
		// This mirrors the heartbeat scenario where queryPlayerCounts succeeds
		// but returns maxN=0 or maxN=-1 (custom regex), so only online is emitted.
		PlayersOnline:      15,
		PlayersOnlineKnown: true,
		PlayersMax:         0,
		PlayersMaxKnown:    false,
		ServerName:         "test-server",
		Namespace:          "games",
		TemplateName:       "minecraft-java",
		GameName:           "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Should have 1 metric: playersOnline, but NOT playersMax.
	if len(metrics) != 1 {
		t.Errorf("expected 1 metric family, got %d", len(metrics))
	}

	for _, family := range metrics {
		if family.Name != nil && *family.Name != "gameplane_agent_players_online" {
			t.Errorf("expected only players_online, got %q", *family.Name)
		}
	}
}

func TestMetricsPlayersOnlineUnknown(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		// Everything about players is unknown (e.g., RCON failed).
		PlayersOnline:      0,
		PlayersOnlineKnown: false,
		PlayersMax:         0,
		PlayersMaxKnown:    false,
		ServerName:         "test-server",
		Namespace:          "games",
		TemplateName:       "minecraft-java",
		GameName:           "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Should have 0 metrics when player counts are unknown.
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(metrics))
	}
}

func TestMetricsAllMetricsIndependent(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	// Create a snapshot with mixed known/unknown across all metric groups.
	snap := Snapshot{
		// CPU: known
		CPUMillicores:      250,
		CPUKnown:           true,
		CPULimitMillicores: 1000,
		CPULimitKnown:      true,
		// Memory: unknown
		MemoryBytes:      0,
		MemoryKnown:      false,
		MemoryLimitBytes: 0,
		MemoryLimitKnown: false,
		// Disk: known
		DiskUsedBytes:  500 * 1024 * 1024,
		DiskTotalBytes: 1024 * 1024 * 1024,
		DiskTotalKnown: true,
		DiskKnown:      true,
		// Players: online known, max unknown
		PlayersOnline:      8,
		PlayersOnlineKnown: true,
		PlayersMax:         0,
		PlayersMaxKnown:    false,
		ServerName:         "test-server",
		Namespace:          "games",
		TemplateName:       "minecraft-java",
		GameName:           "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Should have 5 families: 2 CPU + 2 Disk + 1 Players-Online.
	if len(metrics) != 5 {
		t.Errorf("expected 5 metric families, got %d", len(metrics))
	}

	expected := map[string]bool{
		"gameplane_agent_cpu_millicores":       false,
		"gameplane_agent_cpu_limit_millicores": false,
		"gameplane_agent_disk_used_bytes":      false,
		"gameplane_agent_disk_total_bytes":     false,
		"gameplane_agent_players_online":       false,
	}

	notExpected := []string{
		"gameplane_agent_memory_bytes",
		"gameplane_agent_memory_limit_bytes",
		"gameplane_agent_players_max",
	}

	for _, family := range metrics {
		if family.Name != nil {
			if _, found := expected[*family.Name]; found {
				expected[*family.Name] = true
			}
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("expected metric %q to be present", name)
		}
	}

	for _, name := range notExpected {
		for _, family := range metrics {
			if family.Name != nil && *family.Name == name {
				t.Errorf("expected metric %q to not be present", name)
			}
		}
	}
}

func TestMetricsDiskValuesIndependent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot Snapshot
		want     string
	}{
		{"usage only", Snapshot{DiskKnown: true}, "gameplane_agent_disk_used_bytes"},
		{"capacity only", Snapshot{DiskTotalKnown: true, DiskTotalBytes: 4096}, "gameplane_agent_disk_total_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			m := &Metrics{}
			reg.MustRegister(m)
			m.Update(tc.snapshot)
			families, err := reg.Gather()
			if err != nil {
				t.Fatal(err)
			}
			if len(families) != 1 || families[0].GetName() != tc.want {
				t.Fatalf("expected only %s, got %v", tc.want, families)
			}
		})
	}
}

func TestMetricsPlayerMaxZero(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := &Metrics{}
	reg.MustRegister(m)

	snap := Snapshot{
		// This mimics the heartbeat's behavior when queryPlayerCounts returns
		// maxN=0 (unknown max) but online count is valid.
		PlayersOnline:      5,
		PlayersOnlineKnown: true,
		PlayersMax:         0, // Unknown
		PlayersMaxKnown:    false,
		ServerName:         "test-server",
		Namespace:          "games",
		TemplateName:       "minecraft-java",
		GameName:           "minecraft",
	}
	m.Update(snap)

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Should only have players_online, not players_max (0 is not emitted).
	for _, family := range metrics {
		if family.Name != nil && *family.Name == "gameplane_agent_players_max" {
			t.Error("players_max should not be emitted when PlayersMaxKnown=false")
		}
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	m := New()
	if m == nil {
		t.Fatal("New() returned nil")
	}

	// Verify the returned Metrics can be used with Gather.
	// We can't call Gather directly on the global registry (due to pollution),
	// but we can verify the object is properly initialized by Update/Collect.
	m.Update(Snapshot{
		CPUMillicores: 100,
		CPUKnown:      true,
		ServerName:    "test",
		Namespace:     "ns",
		TemplateName:  "t",
		GameName:      "g",
	})

	// Verify Describe emits metric descriptors without error.
	ch := make(chan *prometheus.Desc, 10)
	m.Describe(ch)
	close(ch)
	count := 0
	for range ch {
		count++
	}
	if count != 8 {
		t.Errorf("Describe emitted %d descriptors, want 8", count)
	}
}
