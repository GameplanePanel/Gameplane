package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// mountMCPTokenRoutes registers the personal MCP token routes on the
// /users router. It is called from MountUsers, because chi panics when
// /users is routed twice.
//
// Token management is a session-only surface. A request authenticated by an
// MCP token is refused here as well as by the guard in auth.MCPTokenGuard.
func mountMCPTokenRoutes(r chi.Router, sessions *auth.SessionStore) {
	h := &mcpTokenHandler{sessions: sessions}
	r.Get("/me/mcp-tokens", h.listSelf)
	r.Post("/me/mcp-tokens", h.createSelf)
	r.Delete("/me/mcp-tokens/{id}", h.revokeSelf)
	r.Get("/{id}/mcp-tokens", h.listUser)
	r.Delete("/{id}/mcp-tokens/{tokenId}", h.revokeUser)
}

type mcpTokenHandler struct {
	sessions *auth.SessionStore
}

type createMCPTokenReq struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expiresInDays"`
}

type createMCPTokenResp struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Token     string    `json:"token"`
}

// refuseTokenCaller writes 403 and reports true when the request was
// authenticated by an MCP token or by an SFTP session (spec 040) rather than a
// session.
func refuseTokenCaller(w http.ResponseWriter, req *http.Request) bool {
	_, isMCP := auth.MCPTokenFromContext(req.Context())
	_, isSFTP := auth.SFTPCallerFromContext(req.Context())
	if isMCP || isSFTP {
		httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
		return true
	}
	return false
}

// callerID returns the signed-in user's ID, or writes 401 and reports false.
func callerID(w http.ResponseWriter, req *http.Request) (int64, bool) {
	u := auth.UserFromContext(req.Context())
	if u == nil {
		httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
		return 0, false
	}
	return u.ID, true
}

// writeMCPTokenErr maps the MCP token store errors to their responses.
func writeMCPTokenErr(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrMCPTokenName):
		httperr.WriteMessage(w, req, messages.New("server.mcp_tokens.invalid_name", nil))
	case errors.Is(err, auth.ErrMCPTokenLifetime):
		httperr.WriteMessage(w, req, messages.New("server.mcp_tokens.invalid_lifetime", nil))
	case errors.Is(err, auth.ErrMCPTokenDuplicate):
		httperr.WriteMessage(w, req, messages.New("server.mcp_tokens.duplicate_name", nil))
	case errors.Is(err, auth.ErrMCPTokenLimit):
		httperr.WriteMessage(w, req, messages.New("server.mcp_tokens.limit_reached", nil))
	case errors.Is(err, auth.ErrMCPTokenNotFound):
		httperr.WriteMessage(w, req, messages.New("server.mcp_tokens.not_found", nil))
	default:
		httperr.Write(w, req, err)
	}
}

func (h *mcpTokenHandler) listSelf(w http.ResponseWriter, req *http.Request) {
	if refuseTokenCaller(w, req) {
		return
	}
	id, ok := callerID(w, req)
	if !ok {
		return
	}
	h.list(w, req, id)
}

func (h *mcpTokenHandler) listUser(w http.ResponseWriter, req *http.Request) {
	if refuseTokenCaller(w, req) {
		return
	}
	userID, ok := parseIDParam(w, req, "id")
	if !ok {
		return
	}
	h.list(w, req, userID)
}

func (h *mcpTokenHandler) list(w http.ResponseWriter, req *http.Request, userID int64) {
	items, err := h.sessions.ListMCPTokens(req.Context(), userID)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

func (h *mcpTokenHandler) createSelf(w http.ResponseWriter, req *http.Request) {
	if refuseTokenCaller(w, req) {
		return
	}
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	var body createMCPTokenReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	t, raw, err := h.sessions.CreateMCPToken(req.Context(), userID, body.Name, body.ExpiresInDays)
	if err != nil {
		writeMCPTokenErr(w, req, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createMCPTokenResp{
		ID:        t.ID,
		Name:      t.Name,
		Prefix:    t.Prefix,
		CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt,
		Token:     raw,
	})
}

func (h *mcpTokenHandler) revokeSelf(w http.ResponseWriter, req *http.Request) {
	if refuseTokenCaller(w, req) {
		return
	}
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	tokenID, ok := parseIDParam(w, req, "id")
	if !ok {
		return
	}
	h.revoke(w, req, userID, tokenID)
}

func (h *mcpTokenHandler) revokeUser(w http.ResponseWriter, req *http.Request) {
	if refuseTokenCaller(w, req) {
		return
	}
	userID, ok := parseIDParam(w, req, "id")
	if !ok {
		return
	}
	tokenID, ok := parseIDParam(w, req, "tokenId")
	if !ok {
		return
	}
	h.revoke(w, req, userID, tokenID)
}

func (h *mcpTokenHandler) revoke(w http.ResponseWriter, req *http.Request, userID, tokenID int64) {
	if err := h.sessions.RevokeMCPToken(req.Context(), userID, tokenID); err != nil {
		writeMCPTokenErr(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseIDParam reads a positive integer path parameter. A bad value writes
// 400 and reports false.
func parseIDParam(w http.ResponseWriter, req *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(req, name), 10, 64)
	if err != nil || id <= 0 {
		httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
		return 0, false
	}
	return id, true
}
