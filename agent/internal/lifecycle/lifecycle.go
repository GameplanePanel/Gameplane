// Package lifecycle exposes a /lifecycle/stop endpoint the operator calls to
// run the module-declared in-game stop sequence over RCON before it scales the
// server to zero, so the world saves and shuts down cleanly instead of relying
// on a container SIGTERM.
//
// The sequence is per-game and declared by the module's template
// (spec.capabilities.lifecycle.stop), e.g. Minecraft's ["stop"]. A stop
// command terminates the server, so the RCON connection naturally drops as it
// runs — a connection-level error after the command is sent is the expected
// outcome, not a failure. The call is therefore best-effort: the operator owns
// the wait-and-scale, with the server's readiness (and a grace deadline) as the
// authority on whether it actually went down.
package lifecycle

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
)

// Rcon is the subset of *rcon.Client we use, as an interface so tests can
// substitute a recording fake.
type Rcon interface {
	Exec(cmd string) (string, error)
}

// Stopper runs the game's stop sequence. Supported()=false means the module
// declared none.
type Stopper interface {
	Supported() bool
	Stop(rc Rcon)
}

// Pick builds the stopper from the module's declared sequence
// (spec.capabilities.lifecycle.stop). A template that declares none gets a
// no-op so callers can invoke /lifecycle/stop unconditionally.
func Pick(spec *caps.Lifecycle) Stopper {
	if spec != nil && len(spec.Stop) > 0 {
		return declaredStopper{stop: spec.Stop}
	}
	return unsupportedStopper{}
}

type response struct {
	Stopped bool   `json:"stopped"`
	Reason  string `json:"reason,omitempty"`
}

// Mount registers POST /lifecycle/stop on r.
func Mount(r chi.Router, rc Rcon, _ string, spec *caps.Lifecycle) {
	s := Pick(spec)
	r.Post("/lifecycle/stop", func(w http.ResponseWriter, _ *http.Request) {
		if !s.Supported() {
			httpjson.Write(w, http.StatusOK, response{Stopped: false, Reason: "game declares no stop sequence"})
			return
		}
		s.Stop(rc)
		httpjson.Write(w, http.StatusOK, response{Stopped: true})
	})
}

// --- Declared (module-driven) ------------------------------------------

type declaredStopper struct{ stop []string }

func (declaredStopper) Supported() bool { return true }

// Stop issues each declared command in order, best-effort. Errors are logged,
// not surfaced: a stop command brings the server (and thus RCON) down, so the
// connection dropping is success — and a genuinely unreachable RCON is handled
// by the operator falling back to a timed scale-to-zero.
func (s declaredStopper) Stop(rc Rcon) {
	for _, cmd := range s.stop {
		if _, err := rc.Exec(cmd); err != nil {
			slog.Info("stop command errored (expected as the server goes down)", "cmd", cmd, "err", err)
		}
	}
}

// --- Default ----------------------------------------------------------

type unsupportedStopper struct{}

func (unsupportedStopper) Supported() bool { return false }
func (unsupportedStopper) Stop(Rcon)       {}
