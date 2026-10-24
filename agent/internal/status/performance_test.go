package status

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
)

func performanceSpec() *caps.Status {
	threshold := "18"
	return &caps.Status{Metrics: []caps.StatusMetric{{ID: "tps", Command: "stats", Regex: `TPS: (?P<value>\S+)`, Performance: &caps.PerformanceMetric{Signal: "tick_rate", Unit: "per_second", DegradedThreshold: &threshold}}, {ID: "tick", Command: "stats", Regex: `Tick: (?P<value>\S+)`, Performance: &caps.PerformanceMetric{Signal: "tick_duration", Unit: "milliseconds"}}}}
}

func TestPerformanceCacheZeroFreshnessAndSharedCommand(t *testing.T) {
	t.Parallel()
	rc := &fakeRcon{respond: func(string) (string, error) { return "TPS: 0 Tick: 50", nil }}
	s := New(rc, performanceSpec())
	s.refresh(t.Context())
	if rc.count() != 1 {
		t.Fatalf("shared command executed %d times", rc.count())
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(s)
	router := chi.NewRouter()
	router.Route("/legacy", s.Mount)
	router.Route("/versioned", s.Mount)
	for _, route := range []string{"/legacy/status", "/versioned/status"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", route, nil))
		if response.Code != 200 {
			t.Fatal("shared status cache unavailable")
		}
	}
	for range 20 {
		if _, err := reg.Gather(); err != nil {
			t.Fatal(err)
		}
	}
	if rc.count() != 1 {
		t.Fatal("scrape executed RCON")
	}
	s.mu.Lock()
	if s.samples["tps"].value != 0 || s.samples["tick"].value != .05 {
		t.Fatal("zero or unit conversion lost")
	}
	s.lastFetch = time.Now().Add(-performanceFreshness - time.Second)
	for id, sample := range s.samples {
		sample.observed = s.lastFetch
		s.samples[id] = sample
	}
	s.mu.Unlock()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "gameplane_agent_performance_value" {
			t.Fatal("stale value emitted")
		}
	}
}

func TestPerformanceRejectsNonFiniteAndInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"NaN", "+Inf", "-1", "no-value"} {
		s := New(&fakeRcon{respond: func(string) (string, error) { return "TPS: " + value, nil }}, performanceSpec())
		s.refresh(t.Context())
		if len(s.samples) != 0 {
			t.Fatalf("invalid %q emitted", value)
		}
	}
	inf := "+Inf"
	for _, p := range []*caps.PerformanceMetric{{Signal: "seed", Unit: "per_second"}, {Signal: "tick_rate", Unit: "milliseconds"}, {Signal: "tick_duration", Unit: "seconds", DegradedThreshold: &inf}} {
		if validPerformance(p) {
			t.Fatalf("accepted %+v", p)
		}
	}
}

func TestPerformanceTimeoutBoundsOutstandingWorkAndDiscardsLateResult(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var calls atomic.Int32
	rc := &fakeRcon{respond: func(string) (string, error) { calls.Add(1); <-release; return "TPS: 20", nil }}
	s := New(rc, performanceSpec())
	s.commandTimeout = 10 * time.Millisecond
	s.refresh(t.Context())
	for range 10 {
		s.refresh(t.Context())
	}
	if calls.Load() != 1 {
		t.Fatalf("unbounded workers: %d", calls.Load())
	}
	close(release)
	deadline := time.After(time.Second)
	for len(s.execSlot) != 0 {
		select {
		case <-deadline:
			t.Fatal("worker failed to finish")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if len(s.samples) != 0 {
		t.Fatal("late result became a fresh sample")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s.Run(ctx)
	if calls.Load() != 1 {
		t.Fatal("cancelled collector executed a command")
	}
}

func TestPerformanceBoundsDirectCapabilitiesAndDeduplicatesIDs(t *testing.T) {
	t.Parallel()
	spec := performanceSpec()
	for range 30 {
		spec.Metrics = append(spec.Metrics, spec.Metrics[0])
	}
	if compiled := compile(spec.Metrics); len(compiled) != 2 {
		t.Fatalf("duplicate IDs expanded cardinality: %d", len(compiled))
	}
	spec.Metrics[0].Command = string(make([]byte, maxCommandBytes+1))
	if len(compile(spec.Metrics[:1])) != 0 {
		t.Fatal("oversized command accepted")
	}
}
