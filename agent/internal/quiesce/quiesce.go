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
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
)

// Rcon is the slice of *rcon.Client we actually use. Defined here as
// an interface so tests can swap in a recording fake.
type Rcon = rcon.Executor

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
	Outcome  string `json:"outcome,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Mount preserves the legacy declaration-driven endpoints for older clients.
func Mount(r chi.Router, rc *rcon.Commands, game string, spec *caps.Quiesce) {
	mount(r, rc, game, Pick(spec), false)
}

// MountVerified accepts only a sequence for which every game reply can be verified.
// Unsupported pause is safe to degrade because no command has been issued.
func MountVerified(r chi.Router, rc *rcon.Commands, game string, spec *caps.Quiesce) {
	var q Quiescer = unsupportedQuiescer{}
	if game == "minecraft-java" && spec != nil &&
		slices.Equal(spec.Quiesce, []string{"save-off", "save-all flush"}) &&
		slices.Equal(spec.Unquiesce, []string{"save-on"}) {
		declared := newDeclaredQuiescer(spec)
		declared.rollbackFirst = true
		q = declared
	}
	mount(r, rc, game, q, true)
}

func mount(r chi.Router, rc *rcon.Commands, game string, q Quiescer, verified bool) {
	handle := func(path, outcome string, paused bool, run func(Rcon) error) {
		r.Post(path, func(w http.ResponseWriter, req *http.Request) {
			if !q.Supported() {
				httpjson.Write(w, http.StatusOK, response{Outcome: "unsupported", Reason: "game does not support quiesce"})
				return
			}
			err := rc.Sequence(req.Context(), func(transport rcon.Executor) error {
				if verified {
					transport = minecraftReplies{transport}
				}
				return run(transport)
			})
			if err != nil {
				slog.Warn("backup command failed", "path", path, "game", game, "err", err)
				httpjson.Write(w, http.StatusBadGateway, response{Quiesced: !paused, Reason: "backup command failed"})
				return
			}
			ack := response{Quiesced: paused}
			if verified {
				ack.Outcome = outcome
			}
			httpjson.Write(w, http.StatusOK, ack)
		})
	}
	handle("/quiesce", "paused", true, q.Quiesce)
	handle("/unquiesce", "resumed", false, q.Unquiesce)
}

// MountMinecraftFlush cannot pause or resume saving. Even a lost response
// leaves the game free to write; the caller can repeat the same checkpoint.
func MountMinecraftFlush(r chi.Router, rc *rcon.Commands) {
	r.Post("/flush", func(w http.ResponseWriter, req *http.Request) {
		err := rc.Sequence(req.Context(), func(transport rcon.Executor) error {
			_, err := (minecraftReplies{transport}).Exec("save-all flush")
			return err
		})
		if err != nil {
			httpjson.Write(w, http.StatusBadGateway, response{Reason: "checkpoint was not acknowledged"})
			return
		}
		httpjson.Write(w, http.StatusOK, response{Outcome: "flushed"})
	})
}

type minecraftReplies struct{ Rcon }

// Vanilla Java 1.21.4 en_us commands.save.* messages. RCON can concatenate
// the two flush messages with no separator. Never accept a success substring
// embedded in an error, or a reply to the opposite command.
func (m minecraftReplies) Exec(cmd string) (string, error) {
	reply, err := m.Rcon.Exec(cmd)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(reply)
	var ok bool
	switch cmd {
	case "save-off":
		ok = out == "Automatic saving is now disabled" || out == "Saving is already turned off"
	case "save-on":
		ok = out == "Automatic saving is now enabled" || out == "Saving is already turned on"
	case "save-all flush":
		out = strings.TrimSpace(strings.TrimPrefix(out, "Saving the game (this may take a moment!)"))
		ok = out == "Saved the game"
	}
	if !ok {
		return "", fmt.Errorf("game did not acknowledge %q", cmd)
	}
	return reply, nil
}

// --- Declared (module-driven) ------------------------------------------

type declaredQuiescer struct {
	quiesce       []string
	unquiesce     []string
	failRE        *regexp.Regexp
	rollbackFirst bool
}

// newDeclaredQuiescer compiles the declared sequences. An invalid
// FailurePattern is logged and ignored — the commands still run, the
// output check just can't.
func newDeclaredQuiescer(spec *caps.Quiesce) declaredQuiescer {
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
			// A timed-out save-off may already have reached the game.
			// The verified save-on contract accepts "already on" on retry.
			if i > 0 || q.rollbackFirst {
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
		out, err := rc.Exec(cmd)
		if err == nil && q.failRE != nil && q.failRE.MatchString(out) {
			err = fmt.Errorf("unquiesce command %q reported failure", cmd)
		}
		if err != nil && firstErr == nil {
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
