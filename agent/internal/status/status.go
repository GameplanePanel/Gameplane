// Package status shares module-declared RCON readouts between the dashboard
// and explicitly opted-in numeric performance monitoring.
package status

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
	"github.com/go-chi/chi/v5"
)

// Rcon executes module-declared commands through the configured game transport.
type Rcon interface {
	Exec(cmd string) (string, error)
}

const (
	cacheTTL             = 5 * time.Second
	performanceInterval  = 30 * time.Second
	performanceFreshness = 75 * time.Second
	cycleTimeout         = 10 * time.Second
	maxCommandBytes      = 1024
	maxRegexBytes        = 4096
	maxResponseBytes     = 64 * 1024
)

type metric struct {
	id, displayName, command, unit string
	re                             *regexp.Regexp
	valueIdx                       int
	performance                    *caps.PerformanceMetric
}

// Result is one module-declared readout returned by the dashboard status route.
type Result struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Value       string `json:"value"`
	Unit        string `json:"unit,omitempty"`
}

// Source is shared by both protected route mounts and the metrics collector.
// At most one legacy context-free Exec can remain in flight after a timeout.
type Source struct {
	rc             Rcon
	metrics        []metric
	automatic      bool
	mu             sync.Mutex
	refreshMu      sync.Mutex
	lastFetch      time.Time
	cached         []Result
	samples        map[string]performanceSample
	execSlot       chan struct{}
	commandTimeout time.Duration
}

// New validates the declared readouts and creates their shared cache and collector.
// Call Run to collect opted-in performance readings independently of requests.
func New(rc Rcon, spec *caps.Status) *Source {
	s := &Source{rc: rc, execSlot: make(chan struct{}, 1), commandTimeout: 3 * time.Second, cached: []Result{}}
	if spec != nil {
		s.metrics = compile(spec.Metrics)
	}
	for _, m := range s.metrics {
		s.automatic = s.automatic || m.performance != nil
	}
	return s
}

// Mount retains the standalone handler API. Production constructs one Source.
func Mount(r chi.Router, rc Rcon, spec *caps.Status) { New(rc, spec).Mount(r) }

// Mount registers the status route using this source's shared readout cache.
func (s *Source) Mount(r chi.Router) { r.Get("/status", s.serve) }

var metricID = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func compile(specs []caps.StatusMetric) []metric {
	out := make([]metric, 0, min(len(specs), 16))
	seen := map[string]bool{}
	for _, s := range specs[:min(len(specs), 16)] {
		if seen[s.ID] || len(s.ID) > 63 || !metricID.MatchString(s.ID) || s.Command == "" || len(s.Command) > maxCommandBytes || s.Regex == "" || len(s.Regex) > maxRegexBytes {
			continue
		}
		re, err := regexp.Compile(s.Regex)
		if err != nil || re.SubexpIndex("value") < 0 {
			continue
		}
		m := metric{id: s.ID, displayName: s.DisplayName, command: s.Command, unit: s.Unit, re: re, valueIdx: re.SubexpIndex("value")}
		if validPerformance(s.Performance) {
			p := *s.Performance
			if p.DegradedThreshold != nil {
				value := *p.DegradedThreshold
				p.DegradedThreshold = &value
			}
			m.performance = &p
		}
		seen[s.ID] = true
		out = append(out, m)
	}
	return out
}

// Run refreshes opted-in performance readouts until ctx is canceled.
// Sources without performance declarations retain on-demand collection.
func (s *Source) Run(ctx context.Context) {
	if !s.automatic {
		return
	}
	for ctx.Err() == nil {
		s.refresh(ctx)
		timer := time.NewTimer(performanceInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type commandResult struct {
	raw       string
	err       error
	completed time.Time
}

var errUnavailable = errors.New("status source unavailable")

func (s *Source) execute(ctx context.Context, command string) commandResult {
	if ctx.Err() != nil {
		return commandResult{err: ctx.Err()}
	}
	select {
	case s.execSlot <- struct{}{}:
	default:
		return commandResult{err: errUnavailable}
	}
	result := make(chan commandResult, 1)
	go func() {
		raw, err := s.rc.Exec(command)
		if len(raw) > maxResponseBytes {
			raw, err = "", errUnavailable
		}
		<-s.execSlot
		result <- commandResult{raw: raw, err: err, completed: time.Now()}
	}()
	queryCtx, cancel := context.WithTimeout(ctx, s.commandTimeout)
	defer cancel()
	select {
	case <-queryCtx.Done():
		return commandResult{err: queryCtx.Err()}
	case reply := <-result:
		if queryCtx.Err() != nil {
			return commandResult{err: queryCtx.Err()}
		}
		return reply
	}
}

func (s *Source) refresh(ctx context.Context) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.mu.Lock()
	fresh := !s.automatic && !s.lastFetch.IsZero() && time.Since(s.lastFetch) < cacheTTL
	s.mu.Unlock()
	if fresh {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, cycleTimeout)
	defer cancel()
	results := make([]Result, 0, len(s.metrics))
	samples := map[string]performanceSample{}
	commands := map[string]commandResult{}
	for _, m := range s.metrics {
		reply, ok := commands[m.command]
		if !ok {
			reply = s.execute(ctx, m.command)
			commands[m.command] = reply
		}
		if errors.Is(reply.err, rcon.ErrDisabled) {
			results = []Result{}
			samples = map[string]performanceSample{}
			break
		}
		res := Result{ID: m.id, DisplayName: m.displayName, Unit: m.unit}
		if reply.err == nil {
			if sub := m.re.FindStringSubmatch(strings.ReplaceAll(reply.raw, "\r", "")); sub != nil {
				res.Value = strings.TrimSpace(sub[m.valueIdx])
			}
		}
		if m.performance != nil {
			if sample, valid := parsePerformance(res.Value, m.performance); valid {
				sample.observed = reply.completed
				samples[m.id] = sample
			}
		}
		results = append(results, res)
	}
	s.mu.Lock()
	s.lastFetch, s.cached, s.samples = time.Now(), results, samples
	s.mu.Unlock()
}

func (s *Source) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	fresh := !s.lastFetch.IsZero() && time.Since(s.lastFetch) < cacheTTL
	s.mu.Unlock()
	if !s.automatic && !fresh {
		s.refresh(r.Context())
	}
	s.mu.Lock()
	cached := append([]Result{}, s.cached...)
	if s.automatic && time.Since(s.lastFetch) >= performanceFreshness {
		for i := range cached {
			cached[i].Value = ""
		}
	}
	s.mu.Unlock()
	httpjson.Write(w, http.StatusOK, cached)
}
