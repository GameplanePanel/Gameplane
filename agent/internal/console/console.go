// Package console provides a duplex WebSocket that forwards user input
// to the game via RCON and echoes the response back.
//
// For Minecraft this gives the UI a usable "console" even though there
// is no real PTY — each submitted line is executed as an RCON command
// and its textual response streams back as a single WS frame.
//
// This package is RCON-only by design: games without RCON declare
// GameTemplate.spec.consoleMode "pty", which the Gameplane API serves by
// bridging the browser WebSocket to the Kubernetes pod-attach API
// (api/internal/ws/attach.go) — no agent involvement.
package console

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
)

// Rcon is the interface to the game's remote console.
type Rcon interface {
	Exec(cmd string) (string, error)
}

type handler struct {
	rcon Rcon
}

// Mount registers the console WebSocket endpoint on the supplied router.
func Mount(r chi.Router, rc Rcon) {
	h := &handler{rcon: rc}
	r.Get("/console", h.serve)
}

// Envelope is the wire format on both directions. Kind=="cmd" on write,
// kind=="out" on read. Keeping a tagged union leaves room for other
// kinds (e.g. "err", "status") without a breaking change.
type Envelope struct {
	Kind string `json:"kind"`
	Body string `json:"body"`
}

func (h *handler) serve(w http.ResponseWriter, req *http.Request) {
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	ctx := req.Context()
	for {
		var in Envelope
		if err := wsjson.Read(ctx, conn, &in); err != nil {
			if !errors.Is(err, context.Canceled) {
				_ = conn.Close(websocket.StatusInternalError, err.Error())
			}
			return
		}
		if in.Kind != "cmd" || in.Body == "" {
			continue
		}
		out, err := h.rcon.Exec(in.Body)
		env := Envelope{Kind: "out", Body: out}
		if err != nil {
			errBody := "upstream unavailable"
			if errors.Is(err, rcon.ErrDisabled) {
				errBody = "rcon disabled"
			} else if errors.Is(err, rcon.ErrAuth) {
				errBody = "rcon authentication failed"
			} else {
				slog.Warn("console rcon", "err", "unclassified error (redacted)")
			}
			env = Envelope{Kind: "err", Body: errBody}
		}
		if err := wsjson.Write(ctx, conn, env); err != nil {
			return
		}
	}
}
