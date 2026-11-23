package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// SFTPEndpoint is the address a user connects to for SFTP and the server's host key fingerprint.
type SFTPEndpoint struct {
	Address            string `json:"address"`
	Port               int    `json:"port"`
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
}

// SFTPEndpointSource reports the SFTP endpoint of cluster. It returns false when none is configured.
type SFTPEndpointSource func(ctx context.Context, cluster string) (SFTPEndpoint, bool)

// sftpEndpointSource holds the source the chart wiring installs. Nil means the endpoint is unknown.
var sftpEndpointSource atomic.Pointer[SFTPEndpointSource]

// SetSFTPEndpointSource installs the endpoint source used by GET /users/me/sftp. Nil clears it.
func SetSFTPEndpointSource(fn SFTPEndpointSource) {
	if fn == nil {
		sftpEndpointSource.Store(nil)
		return
	}
	sftpEndpointSource.Store(&fn)
}

// mountSFTPRoutes registers the personal SFTP credential routes on the /users router (called from MountUsers:
// chi panics when /users is routed twice). Session-only surface: SFTP-session and MCP-token callers are refused.
func mountSFTPRoutes(r chi.Router, sessions *auth.SessionStore, store *db.Store) {
	h := &sftpSelfHandler{sessions: sessions, store: store}
	r.Get("/me/sftp", h.getSelf)
	r.Post("/me/sftp/keys", h.addKey)
	r.Delete("/me/sftp/keys/{id}", h.deleteKey)
	r.Post("/me/sftp/password", h.generatePassword)
	r.Delete("/me/sftp/password", h.revokePassword)
}

type sftpSelfHandler struct {
	sessions *auth.SessionStore
	store    *db.Store
}

type sftpKeyReq struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

type sftpPasswordReq struct {
	Length int `json:"length"`
}

// refuseNonSession writes 403 and reports true when the request is not a plain user session. The MCP
// token and SFTP guards already keep those callers out; this is the handler-level second check.
func refuseNonSession(w http.ResponseWriter, req *http.Request) bool {
	return refuseTokenCaller(w, req)
}

// writeSFTPErr maps the SFTP store and validation errors to their registry messages.
func writeSFTPErr(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrSFTPKeyInvalid):
		httperr.WriteMessage(w, req, messages.New("server.sftp.key_invalid", nil))
	case errors.Is(err, auth.ErrSFTPKeyType):
		httperr.WriteMessage(w, req, messages.New("server.sftp.key_type_unsupported", nil))
	case errors.Is(err, auth.ErrSFTPKeyWeak):
		httperr.WriteMessage(w, req, messages.New("server.sftp.key_weak", nil))
	case errors.Is(err, auth.ErrSFTPKeyName):
		httperr.WriteMessage(w, req, messages.New("server.sftp.key_name_invalid", nil))
	case errors.Is(err, auth.ErrSFTPKeyNameInUse):
		httperr.WriteMessage(w, req, messages.New("server.sftp.key_name_in_use", nil))
	case errors.Is(err, auth.ErrSFTPKeyDuplicate):
		httperr.WriteMessage(w, req, messages.New("server.sftp.key_duplicate", nil))
	case errors.Is(err, auth.ErrSFTPKeyLimit):
		httperr.WriteMessage(w, req, messages.New("server.sftp.key_limit_reached", nil))
	case errors.Is(err, auth.ErrSFTPNotFound):
		httperr.WriteMessage(w, req, messages.New("server.sftp.key_not_found", nil))
	case errors.Is(err, auth.ErrSFTPPasswordLen):
		httperr.WriteMessage(w, req, messages.New("server.sftp.password_length_invalid", nil))
	default:
		httperr.Write(w, req, err)
	}
}

// getSelf returns the caller's SFTP username, endpoint, keys and password metadata. The password itself is never
// included.
func (h *sftpSelfHandler) getSelf(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	uid, ok := callerID(w, req)
	if !ok {
		return
	}
	ctx := req.Context()
	user := auth.UserFromContext(ctx)
	if user == nil {
		httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
		return
	}
	keys, err := h.sessions.ListSFTPKeys(ctx, uid)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if keys == nil {
		keys = []auth.SFTPKey{}
	}
	info, found, err := h.sessions.GetSFTPPasswordInfo(ctx, uid)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	var password any
	if found {
		password = info
	}
	var endpoint any
	if fn := sftpEndpointSource.Load(); fn != nil {
		if ep, ok := (*fn)(ctx, scope.DefaultCluster); ok {
			endpoint = ep
		}
	}
	writeJSON(w, map[string]any{
		"username": user.Username,
		"endpoint": endpoint,
		"keys":     keys,
		"password": password,
	})
}

// addKey stores one public key for the caller.
func (h *sftpSelfHandler) addKey(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	uid, ok := callerID(w, req)
	if !ok {
		return
	}
	var body sftpKeyReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	key, err := h.sessions.AddSFTPKey(req.Context(), uid, body.Name, body.PublicKey)
	if err != nil {
		writeSFTPErr(w, req, err)
		return
	}
	writeJSONCreated(w, key)
}

// deleteKey removes one of the caller's keys and revokes the sessions that used it.
func (h *sftpSelfHandler) deleteKey(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	uid, ok := callerID(w, req)
	if !ok {
		return
	}
	keyID, ok := parseIDParam(w, req, "id")
	if !ok {
		return
	}
	if err := h.sessions.DeleteSFTPKey(req.Context(), uid, keyID); err != nil {
		writeSFTPErr(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// generatePassword replaces the caller's SFTP password and returns the clear text once.
func (h *sftpSelfHandler) generatePassword(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	uid, ok := callerID(w, req)
	if !ok {
		return
	}
	var body sftpPasswordReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	pw, err := h.sessions.GenerateAndStoreSFTPPassword(req.Context(), uid, body.Length)
	if err != nil {
		writeSFTPErr(w, req, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"password": pw, "length": body.Length})
}

// revokePassword deletes the caller's SFTP password and revokes its sessions.
func (h *sftpSelfHandler) revokePassword(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	uid, ok := callerID(w, req)
	if !ok {
		return
	}
	if err := h.sessions.DeleteSFTPPassword(req.Context(), uid); err != nil {
		if errors.Is(err, auth.ErrSFTPNotFound) {
			httperr.WriteMessage(w, req, messages.New("server.sftp.password_not_found", nil))
			return
		}
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
