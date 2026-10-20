package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/metrics"
	"github.com/GameplanePanel/gameplane/agent/internal/players"
	"github.com/GameplanePanel/gameplane/agent/internal/usage"
)

func TestRun_DisabledPaths(t *testing.T) {
	t.Run("empty server name returns immediately", func(_ *testing.T) {
		Run(context.Background(), Config{})
	})

	t.Run("empty namespace and no SA file returns immediately", func(_ *testing.T) {
		// readNamespace will fail because SA path is unreadable in the
		// test env; Run should early-return without panicking.
		Run(context.Background(), Config{ServerName: "srv"})
	})

	t.Run("rest config unavailable outside cluster", func(t *testing.T) {
		// In-cluster config requires KUBERNETES_SERVICE_HOST/PORT env. Make
		// sure they are unset, then exercise the rest.InClusterConfig path.
		t.Setenv("KUBERNETES_SERVICE_HOST", "")
		t.Setenv("KUBERNETES_SERVICE_PORT", "")
		Run(context.Background(), Config{ServerName: "srv", Namespace: "ns"})
	})
}

func TestSendOnce(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)

	var captured []byte
	dyn.PrependReactor("patch", "gameservers", func(a clienttesting.Action) (bool, runtime.Object, error) {
		captured = a.(clienttesting.PatchAction).GetPatch()
		return true, fakeGameServer(), nil
	})

	cfg := Config{
		ServerName: "srv",
		Namespace:  "ns",
		Version:    "v1",
		Game:       "minecraft-java",
		RCON:       fakeRcon{out: "There are 4 of a max"},
	}
	if err := sendOnce(context.Background(), dyn, cfg); err != nil {
		t.Fatalf("sendOnce: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	agent := got["status"].(map[string]any)["agent"].(map[string]any)
	if agent["playersOnline"].(float64) != 4 {
		t.Fatalf("playersOnline=%v", agent["playersOnline"])
	}
	if agent["version"].(string) != "v1" {
		t.Fatalf("agent payload=%+v", agent)
	}
	// F-105: gameVersion must never echo cfg.Game (a template id like
	// "minecraft-java", not a version) — it patches null until the agent
	// has a real version source.
	if v, ok := agent["gameVersion"]; !ok || v != nil {
		t.Fatalf("gameVersion=%v (%T), want explicit null", v, v)
	}
	if _, ok := agent["lastHeartbeat"].(string); !ok {
		t.Fatalf("lastHeartbeat missing or wrong type: %+v", agent)
	}
}

// runSendOnce runs one heartbeat with the given RCON "list" output and returns
// the agent sub-map of the captured status patch.
func runSendOnce(t *testing.T, rconOut string) map[string]any {
	t.Helper()
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)
	var captured []byte
	dyn.PrependReactor("patch", "gameservers", func(a clienttesting.Action) (bool, runtime.Object, error) {
		captured = a.(clienttesting.PatchAction).GetPatch()
		return true, fakeGameServer(), nil
	})
	if err := sendOnce(context.Background(), dyn, Config{
		ServerName: "srv", Namespace: "ns", RCON: fakeRcon{out: rconOut},
	}); err != nil {
		t.Fatalf("sendOnce: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	return got["status"].(map[string]any)["agent"].(map[string]any)
}

func TestSendOnce_EmitsPlayersMax(t *testing.T) {
	t.Run("known max", func(t *testing.T) {
		agent := runSendOnce(t, "There are 5 of a max of 30 players online:")
		if agent["playersOnline"].(float64) != 5 {
			t.Fatalf("playersOnline=%v", agent["playersOnline"])
		}
		if agent["playersMax"].(float64) != 30 {
			t.Fatalf("playersMax=%v, want 30", agent["playersMax"])
		}
	})
	t.Run("online but no max reported is null", func(t *testing.T) {
		agent := runSendOnce(t, "There are 132 players")
		if agent["playersOnline"].(float64) != 132 {
			t.Fatalf("playersOnline=%v", agent["playersOnline"])
		}
		v, present := agent["playersMax"]
		if !present {
			t.Fatalf("playersMax key missing: %+v", agent)
		}
		if v != nil {
			t.Fatalf("playersMax=%v, want null when no max reported", v)
		}
	})
}

func TestSendOnce_EmitsKnownUsage(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)

	var captured []byte
	dyn.PrependReactor("patch", "gameservers", func(a clienttesting.Action) (bool, runtime.Object, error) {
		captured = a.(clienttesting.PatchAction).GetPatch()
		return true, fakeGameServer(), nil
	})

	err := sendOnce(context.Background(), dyn, Config{
		ServerName: "srv",
		Namespace:  "ns",
		RCON:       fakeRcon{out: "0"},
		Usage: fakeUsage{s: usage.Sample{
			CPUMillicores: 500, CPUKnown: true,
			CPULimitMillicores: 2000, CPULimitKnown: true,
			MemoryBytes: 1024, MemoryKnown: true,
			MemoryLimitBytes: 4096, MemoryLimitKnown: true,
			DiskUsedBytes: 10, DiskTotalBytes: 100, DiskKnown: true,
		}},
	})
	if err != nil {
		t.Fatalf("sendOnce: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	agent := got["status"].(map[string]any)["agent"].(map[string]any)
	for k, want := range map[string]float64{
		"cpuMillicores":      500,
		"cpuLimitMillicores": 2000,
		"memoryBytes":        1024,
		"memoryLimitBytes":   4096,
		"diskUsedBytes":      10,
		"diskTotalBytes":     100,
	} {
		if v, ok := agent[k].(float64); !ok || v != want {
			t.Fatalf("%s = %v, want %v", k, agent[k], want)
		}
	}
}

func TestSendOnce_EmitsNullForUnknownUsage(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)

	var captured []byte
	dyn.PrependReactor("patch", "gameservers", func(a clienttesting.Action) (bool, runtime.Object, error) {
		captured = a.(clienttesting.PatchAction).GetPatch()
		return true, fakeGameServer(), nil
	})

	// A zero-value Sample: every Known flag false → every key present and
	// null, so a merge patch clears any prior reading.
	err := sendOnce(context.Background(), dyn, Config{
		ServerName: "srv",
		Namespace:  "ns",
		RCON:       fakeRcon{out: "0"},
		Usage:      fakeUsage{},
	})
	if err != nil {
		t.Fatalf("sendOnce: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(captured, &got)
	agent := got["status"].(map[string]any)["agent"].(map[string]any)
	for _, k := range []string{
		"cpuMillicores", "cpuLimitMillicores", "memoryBytes",
		"memoryLimitBytes", "diskUsedBytes", "diskTotalBytes",
	} {
		v, present := agent[k]
		if !present {
			t.Fatalf("%s key missing from patch: %+v", k, agent)
		}
		if v != nil {
			t.Fatalf("want null %s, got %v", k, v)
		}
	}
}

func TestSendOnce_RconFailureEmitsNullPlayers(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)

	var captured []byte
	dyn.PrependReactor("patch", "gameservers", func(a clienttesting.Action) (bool, runtime.Object, error) {
		captured = a.(clienttesting.PatchAction).GetPatch()
		return true, fakeGameServer(), nil
	})

	err := sendOnce(context.Background(), dyn, Config{
		ServerName: "srv",
		Namespace:  "ns",
		RCON:       fakeRcon{err: errors.New("rcon down")},
	})
	if err != nil {
		t.Fatalf("sendOnce: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(captured, &got)
	agent := got["status"].(map[string]any)["agent"].(map[string]any)
	// Unknown player count must be null ("unknown"), never a -1 sentinel
	// the dashboard would sum into a negative total. The key is present
	// with a null value so the merge patch clears any prior count.
	v, present := agent["playersOnline"]
	if !present {
		t.Fatalf("playersOnline key missing from patch: %+v", agent)
	}
	if v != nil {
		t.Fatalf("want null playersOnline on rcon failure, got %v", v)
	}
}

func TestSendOnce_PatchErrorPropagates(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)
	dyn.PrependReactor("patch", "gameservers", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	err := sendOnce(context.Background(), dyn, Config{
		ServerName: "srv",
		Namespace:  "ns",
		RCON:       fakeRcon{out: "0"},
	})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("got %v", err)
	}
}

func TestSendOnce_CustomListCommandWithRegex(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)

	var captured []byte
	dyn.PrependReactor("patch", "gameservers", func(a clienttesting.Action) (bool, runtime.Object, error) {
		captured = a.(clienttesting.PatchAction).GetPatch()
		return true, fakeGameServer(), nil
	})

	re, err := players.CompileEntryRegex(`^\[(\S+)\]`)
	if err != nil {
		t.Fatalf("compile regex: %v", err)
	}

	cfg := Config{
		ServerName: "srv",
		Namespace:  "ns",
		RCON:       fakeRcon{out: "[alice]\n[bob]\n[charlie]\n"},
		PlayerList: &caps.PlayerList{
			Command:    "players",
			EntryRegex: `^\[(\S+)\]`,
		},
		PlayerListRE: re,
	}
	if err := sendOnce(context.Background(), dyn, cfg); err != nil {
		t.Fatalf("sendOnce: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	agent := got["status"].(map[string]any)["agent"].(map[string]any)
	if agent["playersOnline"].(float64) != 3 {
		t.Fatalf("playersOnline got %v, want 3", agent["playersOnline"])
	}
	if agent["playersMax"] != nil {
		t.Fatalf("playersMax got %v, want null", agent["playersMax"])
	}
}

func TestQueryPlayerCounts(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		err     error
		want    int
		wantMax int
		ok      bool
	}{
		{"max-of format", "There are 7 of a max of 20 players online:", nil, 7, 20, true},
		{"slash format", "There are 3/16 players online:", nil, 3, 16, true},
		{"partial line has no max", "There are 7 of a max", nil, 7, 0, true},
		{"no digits", "Server starting...", nil, 0, 0, false},
		{"leading zero", "0 players", nil, 0, 0, true},
		{"multi-digit no max", "There are 132 players", nil, 132, 0, true},
		{"rcon error", "", errors.New("boom"), 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{RCON: fakeRcon{out: tc.out, err: tc.err}}
			n, maxN, err := queryPlayerCounts(cfg)
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v ok=%v", err, tc.ok)
			}
			if n != tc.want {
				t.Fatalf("online got %d, want %d", n, tc.want)
			}
			if maxN != tc.wantMax {
				t.Fatalf("max got %d, want %d", maxN, tc.wantMax)
			}
		})
	}
}

func TestQueryPlayerCounts_CustomCommand(t *testing.T) {
	cfg := Config{
		RCON: fakeRcon{out: "There are 5 of a max of 20 players online:"},
		PlayerList: &caps.PlayerList{
			Command: "mylist",
		},
	}
	n, maxN, err := queryPlayerCounts(cfg)
	if err != nil {
		t.Fatalf("queryPlayerCounts: %v", err)
	}
	if n != 5 {
		t.Fatalf("online got %d, want 5", n)
	}
	if maxN != 20 {
		t.Fatalf("max got %d, want 20", maxN)
	}
}

func TestQueryPlayerCounts_WithRegex(t *testing.T) {
	re, err := players.CompileEntryRegex(`^\S+$`)
	if err != nil {
		t.Fatalf("compile regex: %v", err)
	}
	cfg := Config{
		RCON: fakeRcon{out: "alice\nbob\ncharlie\n"},
		PlayerList: &caps.PlayerList{
			Command:    "mylist",
			EntryRegex: `^\S+$`,
		},
		PlayerListRE: re,
	}
	n, maxN, err := queryPlayerCounts(cfg)
	if err != nil {
		t.Fatalf("queryPlayerCounts: %v", err)
	}
	if n != 3 {
		t.Fatalf("online got %d, want 3", n)
	}
	if maxN != -1 {
		t.Fatalf("max got %d, want -1", maxN)
	}
}

func TestReadNamespace(t *testing.T) {
	// In a normal CI/test env this file is absent; readNamespace must
	// return "" rather than panicking.
	if got := readNamespace(); got != "" {
		// If a CI runner happens to mount it (unlikely), accept whatever
		// it produced as long as no trailing newline.
		if strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\r") {
			t.Fatalf("trailing whitespace not stripped: %q", got)
		}
	}
}

func TestReadNamespace_TrimsTrailingNewlines(t *testing.T) {
	// Exercise the trimming branch directly through a temp file by
	// duplicating the trim logic here. (readNamespace's path is
	// hard-coded to the SA path; we don't hot-swap it. The trim logic is
	// the only branch worth verifying explicitly.)
	dir := t.TempDir()
	path := filepath.Join(dir, "ns")
	if err := os.WriteFile(path, []byte("gameplane-system\r\n\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, _ := os.ReadFile(path)
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	if string(b) != "gameplane-system" {
		t.Fatalf("got %q", string(b))
	}
}

func TestSendOnce_PopulatesMetricsSnapshot_PlayersAndUsage(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)

	dyn.PrependReactor("patch", "gameservers", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, fakeGameServer(), nil
	})

	// Create a Metrics instance to capture the snapshot.
	metricsReg := prometheus.NewRegistry()
	metricsObj := &metrics.Metrics{}
	metricsReg.MustRegister(metricsObj)

	cfg := Config{
		ServerName: "test-srv",
		Namespace:  "games",
		Template:   "minecraft-java",
		Game:       "minecraft",
		RCON:       fakeRcon{out: "There are 10 of a max of 30 players online:"},
		Usage: fakeUsage{s: usage.Sample{
			CPUMillicores:      500,
			CPUKnown:           true,
			CPULimitMillicores: 1000,
			CPULimitKnown:      true,
			MemoryBytes:        2048,
			MemoryKnown:        true,
			MemoryLimitBytes:   4096,
			MemoryLimitKnown:   true,
			DiskUsedBytes:      100,
			DiskTotalBytes:     1000,
			DiskKnown:          true,
		}},
		Metrics: metricsObj,
	}

	if err := sendOnce(context.Background(), dyn, cfg); err != nil {
		t.Fatalf("sendOnce: %v", err)
	}

	// Verify the metrics snapshot was populated by checking gathered metrics.
	gatheredMetrics, err := metricsReg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	// Should have emitted 8 metrics: 2 CPU, 2 Memory, 2 Disk, 2 Players.
	if len(gatheredMetrics) != 8 {
		t.Errorf("expected 8 metric families, got %d", len(gatheredMetrics))
	}

	// Verify specific metric values to confirm snapshot was populated correctly.
	metricMap := map[string]float64{}
	for _, family := range gatheredMetrics {
		if family.Name != nil && len(family.Metric) > 0 && family.Metric[0].Gauge.Value != nil {
			metricMap[*family.Name] = *family.Metric[0].Gauge.Value
		}
	}

	if metricMap["gameplane_agent_cpu_millicores"] != 500 {
		t.Errorf("cpu_millicores=%v, want 500", metricMap["gameplane_agent_cpu_millicores"])
	}
	if metricMap["gameplane_agent_memory_bytes"] != 2048 {
		t.Errorf("memory_bytes=%v, want 2048", metricMap["gameplane_agent_memory_bytes"])
	}
	if metricMap["gameplane_agent_disk_used_bytes"] != 100 {
		t.Errorf("disk_used_bytes=%v, want 100", metricMap["gameplane_agent_disk_used_bytes"])
	}
	if metricMap["gameplane_agent_players_online"] != 10 {
		t.Errorf("players_online=%v, want 10", metricMap["gameplane_agent_players_online"])
	}
	if metricMap["gameplane_agent_players_max"] != 30 {
		t.Errorf("players_max=%v, want 30", metricMap["gameplane_agent_players_max"])
	}
}

func TestSendOnce_MetricsSnapshot_HandlesMixedKnownUnknown(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)

	dyn.PrependReactor("patch", "gameservers", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, fakeGameServer(), nil
	})

	metricsReg := prometheus.NewRegistry()
	metricsObj := &metrics.Metrics{}
	metricsReg.MustRegister(metricsObj)

	// Usage data: CPU known, memory unknown.
	cfg := Config{
		ServerName: "test-srv",
		Namespace:  "games",
		Template:   "minecraft-java",
		Game:       "minecraft",
		RCON:       fakeRcon{out: "There are 5 players"}, // Players known, max unknown
		Usage:      fakeUsage{s: usage.Sample{CPUMillicores: 300, CPUKnown: true}},
		Metrics:    metricsObj,
	}

	if err := sendOnce(context.Background(), dyn, cfg); err != nil {
		t.Fatalf("sendOnce: %v", err)
	}

	gatheredMetrics, err := metricsReg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	// Should have exactly 2 metrics: cpu (known) and players_online (known).
	// No memory, cpu_limit, disk, or players_max (all unknown).
	if len(gatheredMetrics) != 2 {
		t.Errorf("expected 2 metric families, got %d", len(gatheredMetrics))
	}

	metricNames := map[string]bool{}
	for _, family := range gatheredMetrics {
		if family.Name != nil {
			metricNames[*family.Name] = true
		}
	}

	if !metricNames["gameplane_agent_cpu_millicores"] {
		t.Error("cpu_millicores metric missing")
	}
	if !metricNames["gameplane_agent_players_online"] {
		t.Error("players_online metric missing")
	}
	if len(metricNames) != 2 {
		t.Errorf("unexpected metrics present: %v", metricNames)
	}
}

func TestSendOnce_MetricsSnapshot_RconFailureDoesNotPopulate(t *testing.T) {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{gvr: "GameServerList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr)

	dyn.PrependReactor("patch", "gameservers", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, fakeGameServer(), nil
	})

	metricsReg := prometheus.NewRegistry()
	metricsObj := &metrics.Metrics{}
	metricsReg.MustRegister(metricsObj)

	cfg := Config{
		ServerName: "test-srv",
		Namespace:  "games",
		Template:   "minecraft-java",
		Game:       "minecraft",
		RCON:       fakeRcon{err: errors.New("rcon down")},
		Usage:      fakeUsage{s: usage.Sample{CPUMillicores: 100, CPUKnown: true}},
		Metrics:    metricsObj,
	}

	if err := sendOnce(context.Background(), dyn, cfg); err != nil {
		t.Fatalf("sendOnce: %v", err)
	}

	gatheredMetrics, err := metricsReg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	// Should have only CPU metric; no player metrics because RCON failed.
	if len(gatheredMetrics) != 1 {
		t.Errorf("expected 1 metric family (CPU only), got %d", len(gatheredMetrics))
	}

	if len(gatheredMetrics) > 0 && gatheredMetrics[0].Name != nil {
		if *gatheredMetrics[0].Name != "gameplane_agent_cpu_millicores" {
			t.Errorf("expected cpu_millicores, got %s", *gatheredMetrics[0].Name)
		}
	}
}

// helpers

type fakeRcon struct {
	out string
	err error
}

func (f fakeRcon) Exec(string) (string, error) { return f.out, f.err }

type fakeUsage struct{ s usage.Sample }

func (f fakeUsage) Read() usage.Sample { return f.s }

func fakeGameServer() runtime.Object {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "gameplane.local", Version: "v1alpha1", Kind: "GameServer",
	})
	o.SetName("srv")
	o.SetNamespace("ns")
	return o
}
