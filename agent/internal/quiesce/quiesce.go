// Package quiesce exposes /quiesce and /unquiesce endpoints used by the
// Backup controller to flush in-flight game state to disk before a
// snapshot is taken (and resume it after).
//
// The command sequences are per-game and declared by the module's
// template (spec.capabilities.quiesce), e.g. Minecraft toggles
// auto-save with "save-off" + "save-all flush" / "save-on" over RCON.
// Games that declare nothing get a best-effort no-op so the backup
// pipeline can proceed unconditionally — backups should never fail
// because a game can't be paused.
package quiesce

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
)

// Rcon is the slice of *rcon.Client we actually use. Defined here as
// an interface so tests can swap in a recording fake.
type Rcon interface {
	Exec(cmd string) (string, error)
}

// Quiescer encapsulates the game-specific RCON sequence for pausing
// writes (Quiesce) and resuming them (Unquiesce). Implementations
// return Supported()=false when the game cannot quiesce; the HTTP
// handler turns that into a 200 with quiesced=false rather than a
// hard error.
type Quiescer interface {
	Supported() bool
	Quiesce(rc Rcon) error
	Unquiesce(rc Rcon) error
}

// Pick builds the quiescer from the module's declared sequences
// (spec.capabilities.quiesce). A template that declares no sequence (or
// only half of one) gets a no-op quiescer: backups proceed
// unconditionally and we never pause a game we can't resume. Quiesce is
// module-driven, with no per-game special-casing in the agent.
func Pick(spec *caps.Quiesce) Quiescer {
	if spec != nil && len(spec.Quiesce) > 0 && len(spec.Unquiesce) > 0 {
		return newDeclaredQuiescer(spec)
	}
	return unsupportedQuiescer{}
}

type response struct {
	Quiesced bool   `json:"quiesced"`
	Reason   string `json:"reason,omitempty"`
}

// Mount registers POST /quiesce and POST /unquiesce on r.
func Mount(r chi.Router, rc Rcon, game string, spec *caps.Quiesce) {
	q := Pick(spec)
	r.Post("/quiesce", func(w http.ResponseWriter, _ *http.Request) {
		if !q.Supported() {
			httpjson.Write(w, http.StatusOK, response{Quiesced: false, Reason: "game does not support quiesce"})
			return
		}
		if err := q.Quiesce(rc); err != nil {
			slog.Warn("quiesce failed", "game", game, "err", err)
			httpjson.Write(w, http.StatusBadGateway, response{Quiesced: false, Reason: "rcon error"})
			return
		}
		httpjson.Write(w, http.StatusOK, response{Quiesced: true})
	})
	r.Post("/unquiesce", func(w http.ResponseWriter, _ *http.Request) {
		if !q.Supported() {
			httpjson.Write(w, http.StatusOK, response{Quiesced: false, Reason: "game does not support quiesce"})
			return
		}
		if err := q.Unquiesce(rc); err != nil {
			// Unquiesce failure is more dangerous than quiesce failure
			// (the game is now stuck with auto-save off), but we still
			// must not block the controller — surface 502 so the
			// operator can record an event.
			slog.Warn("unquiesce failed", "game", game, "err", err)
			httpjson.Write(w, http.StatusBadGateway, response{Quiesced: true, Reason: "unquiesce rcon error"})
			return
		}
		httpjson.Write(w, http.StatusOK, response{Quiesced: false})
	})
}

// --- Declared (module-driven) ------------------------------------------

type declaredQuiescer struct {
	quiesce   []string
	unquiesce []string
	failRE    *regexp.Regexp
}

// newDeclaredQuiescer compiles the declared sequences. An invalid
// FailurePattern is logged and ignored — the commands still run, the
// output check just can't.
func newDeclaredQuiescer(spec *caps.Quiesce) Quiescer {
	q := declaredQuiescer{quiesce: spec.Quiesce, unquiesce: spec.Unquiesce}
	if spec.FailurePattern != "" {
		re, err := regexp.Compile("(?i)" + spec.FailurePattern)
		if err != nil {
			slog.Warn("invalid quiesce failurePattern; output check disabled", "err", err)
		} else {
			q.failRE = re
		}
	}
	return q
}

func (declaredQuiescer) Supported() bool { return true }

func (q declaredQuiescer) Quiesce(rc Rcon) error {
	for i, cmd := range q.quiesce {
		out, err := rc.Exec(cmd)
		if err == nil && q.failRE != nil && q.failRE.MatchString(out) {
			err = fmt.Errorf("quiesce command %q reported failure", cmd)
		}
		if err != nil {
			// Roll auto-save (or whatever the game paused) back on so we
			// don't leave the world frozen — but only if any pausing
			// command already ran.
			if i > 0 {
				_ = q.Unquiesce(rc)
			}
			return err
		}
	}
	return nil
}

func (q declaredQuiescer) Unquiesce(rc Rcon) error {
	var firstErr error
	for _, cmd := range q.unquiesce {
		if _, err := rc.Exec(cmd); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// --- Default ----------------------------------------------------------

type unsupportedQuiescer struct{}

func (unsupportedQuiescer) Supported() bool      { return false }
func (unsupportedQuiescer) Quiesce(Rcon) error   { return nil }
func (unsupportedQuiescer) Unquiesce(Rcon) error { return nil }
