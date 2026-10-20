// Package status reads module-declared live metrics over RCON for the
// dashboard's Overview tab.
//
// A GameTemplate declares metrics in spec.capabilities.status.metrics[];
// the operator serializes them into GAMEPLANE_CAPABILITIES and the agent
// interprets them here. Each metric runs an RCON command and extracts a
// value via a named-group regex (group "value"), so modules surface
// game-specific readouts (TPS, world time, …) without an agent change.
package status

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
)

// Rcon is the slice of *rcon.Client we use. An interface so tests can
// swap in a recording fake.
type Rcon interface {
	Exec(cmd string) (string, error)
}

// metric is one declared readout whose regex compiled at mount time.
type metric struct {
	id, displayName, command, unit string
	re                             *regexp.Regexp
	valueIdx                       int
}

// Result is one metric's current value (empty when the command did not
// match this cycle). DisplayName/Unit are echoed so the dashboard needs
// no second lookup.
type Result struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Value       string `json:"value"`
	Unit        string `json:"unit,omitempty"`
}

type handler struct {
	rc      Rcon
	metrics []metric

	mu        sync.Mutex
	lastFetch time.Time
	cached    []Result
}

const cacheTTL = 5 * time.Second

// Mount registers GET /status. spec is the module's declared metrics
// (nil when the template declares none — the endpoint then returns []).
func Mount(r chi.Router, rc Rcon, spec *caps.Status) {
	h := &handler{rc: rc}
	if spec != nil {
		h.metrics = compile(spec.Metrics)
	}
	r.Get("/status", h.serve)
}

// compile builds the metric list, dropping any whose regex is invalid or
// lacks the required (?P<value>…) group (logged) so one bad declaration
// can't disable the whole panel.
func compile(specs []caps.StatusMetric) []metric {
	out := make([]metric, 0, len(specs))
	for _, s := range specs {
		if s.ID == "" || s.Command == "" || s.Regex == "" {
			continue
		}
		re, err := regexp.Compile(s.Regex)
		if err != nil {
			slog.Warn("invalid status metric regex; metric disabled", "metric", s.ID, "err", err)
			continue
		}
		idx := re.SubexpIndex("value")
		if idx < 0 {
			slog.Warn("status metric regex has no (?P<value>…) group; metric disabled", "metric", s.ID)
			continue
		}
		out = append(out, metric{
			id: s.ID, displayName: s.DisplayName, command: s.Command,
			unit: s.Unit, re: re, valueIdx: idx,
		})
	}
	return out
}

func (h *handler) serve(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	fresh := !h.lastFetch.IsZero() && time.Since(h.lastFetch) < cacheTTL
	cached := h.cached
	h.mu.Unlock()
	if fresh {
		httpjson.Write(w, http.StatusOK, cached)
		return
	}

	results := make([]Result, 0, len(h.metrics))
	for _, m := range h.metrics {
		raw, err := h.rc.Exec(m.command)
		if errors.Is(err, rcon.ErrDisabled) {
			// No RCON: no live metrics. Empty array so the dashboard can
			// render the panel uniformly for every game.
			httpjson.Write(w, http.StatusOK, []Result{})
			return
		}
		res := Result{ID: m.id, DisplayName: m.displayName, Unit: m.unit}
		if err != nil {
			slog.Warn("status metric rcon", "metric", m.id, "err", err)
		} else if sub := m.re.FindStringSubmatch(strings.ReplaceAll(raw, "\r", "")); sub != nil {
			res.Value = strings.TrimSpace(sub[m.valueIdx])
		}
		results = append(results, res)
	}

	h.mu.Lock()
	h.lastFetch = time.Now()
	h.cached = results
	h.mu.Unlock()
	httpjson.Write(w, http.StatusOK, results)
}
