package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

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
	r.Put("/me/sftp/login-name", h.setLoginName)
	r.Post("/me/sftp/password", h.generatePassword)
	r.Delete("/me/sftp/password", h.revokePassword)
	// Admin view and revoke of another user's SFTP credentials (users:manage in rbac.go).
	admin := &sftpAdminHandler{sessions: sessions}
	r.Get("/{id}/sftp", admin.get)
	r.Delete("/{id}/sftp/keys/{keyId}", admin.deleteKey)
	r.Delete("/{id}/sftp/password", admin.revokePassword)
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
	case errors.Is(err, auth.ErrSFTPLoginNameInvalid):
		httperr.WriteMessage(w, req, messages.New("server.sftp.login_name_invalid", nil))
	case errors.Is(err, auth.ErrSFTPLoginNameTaken):
		httperr.WriteMessage(w, req, messages.New("server.sftp.login_name_taken", nil))
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
	loginName, custom, err := h.sessions.SFTPLoginName(ctx, uid)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, map[string]any{
		"username":        user.Username,
		"loginName":       loginName,
		"customLoginName": custom,
		"endpoint":        endpoint,
		"keys":            keys,
		"password":        password,
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

// setLoginName sets the SFTP username the caller chose; an empty username goes back to the default
// (the display name when no other account holds it, otherwise the panel username).
func (h *sftpSelfHandler) setLoginName(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	uid, ok := callerID(w, req)
	if !ok {
		return
	}
	var body struct {
		LoginName string `json:"loginName"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	ctx := req.Context()
	if err := h.sessions.SetSFTPLoginName(ctx, uid, strings.TrimSpace(body.LoginName)); err != nil {
		writeSFTPErr(w, req, err)
		return
	}
	name, custom, err := h.sessions.SFTPLoginName(ctx, uid)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, map[string]any{"loginName": name, "customLoginName": custom})
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

// sftpAdminHandler serves the admin routes on another user's SFTP credentials. Every route needs users:manage.
type sftpAdminHandler struct {
	sessions *auth.SessionStore
}

// sftpAdminKey is the admin view of one key: no public key body, and the algorithm name in place of it.
type sftpAdminKey struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
}

// sftpKeyAlgorithm returns the algorithm name, the first field of a normalized "<type> <base64>" key.
func sftpKeyAlgorithm(normalized string) string {
	if fields := strings.Fields(normalized); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// adminUser returns the username of userID and true. For an unknown user it answers 404 user_not_found and
// reports false.
func (h *sftpAdminHandler) adminUser(w http.ResponseWriter, req *http.Request, userID int64) (string, bool) {
	name, err := h.sessions.SFTPUsername(req.Context(), userID)
	if errors.Is(err, sql.ErrNoRows) {
		httperr.WriteMessage(w, req, messages.New("server.common.user_not_found", nil))
		return "", false
	}
	if err != nil {
		httperr.Write(w, req, err)
		return "", false
	}
	return name, true
}

// get returns one user's keys and password metadata. The password hash and key bodies are never included.
func (h *sftpAdminHandler) get(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	userID, ok := parseIDParam(w, req, "id")
	if !ok {
		return
	}
	username, ok := h.adminUser(w, req, userID)
	if !ok {
		return
	}
	ctx := req.Context()
	keys, err := h.sessions.ListSFTPKeys(ctx, userID)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	out := make([]sftpAdminKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, sftpAdminKey{
			ID:          k.ID,
			Name:        k.Name,
			Type:        sftpKeyAlgorithm(k.PublicKey),
			Fingerprint: k.Fingerprint,
			CreatedAt:   k.CreatedAt,
			LastUsedAt:  k.LastUsedAt,
		})
	}
	info, found, err := h.sessions.GetSFTPPasswordInfo(ctx, userID)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	var password any
	if found {
		password = info
	}
	writeJSON(w, map[string]any{
		"userId":   userID,
		"username": username,
		"keys":     out,
		"password": password,
	})
}

// deleteKey removes one of the user's keys and revokes the sessions that used it.
func (h *sftpAdminHandler) deleteKey(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	userID, ok := parseIDParam(w, req, "id")
	if !ok {
		return
	}
	keyID, ok := parseIDParam(w, req, "keyId")
	if !ok {
		return
	}
	if _, ok := h.adminUser(w, req, userID); !ok {
		return
	}
	if err := h.sessions.DeleteSFTPKey(req.Context(), userID, keyID); err != nil {
		writeSFTPErr(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokePassword deletes the user's SFTP password and revokes its sessions.
func (h *sftpAdminHandler) revokePassword(w http.ResponseWriter, req *http.Request) {
	if refuseNonSession(w, req) {
		return
	}
	userID, ok := parseIDParam(w, req, "id")
	if !ok {
		return
	}
	if _, ok := h.adminUser(w, req, userID); !ok {
		return
	}
	if err := h.sessions.DeleteSFTPPassword(req.Context(), userID); err != nil {
		if errors.Is(err, auth.ErrSFTPNotFound) {
			httperr.WriteMessage(w, req, messages.New("server.sftp.password_not_found", nil))
			return
		}
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
