package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const (
	// sftpLoginFailedReason is the only reason a failed login writes. The cause stays out of the audit trail.
	sftpLoginFailedReason = "sftp-login-failed"
	// sftpActorMaxRunes caps the username recorded as the audit actor of a failed login.
	sftpActorMaxRunes = 64
	// sftpCheckMaxTokens bounds one sessions/check request.
	sftpCheckMaxTokens = 500
)

// MountSFTPLogin registers the endpoint-facing routes under /sftp/v1. The caller mounts it OUTSIDE the protected
// group: these routes are authenticated by sessions.SFTPServiceAuth (service token), never by a user session.
func MountSFTPLogin(r chi.Router, sessions *auth.SessionStore, store *db.Store, files *rbac.FileChecker, aud *audit.Auditor) {
	h := &sftpLoginHandler{sessions: sessions, store: store, files: files, aud: aud}
	r.Group(func(r chi.Router) {
		r.Use(sessions.SFTPServiceAuth)
		r.Post("/sftp/v1/login", h.login)
		r.Post("/sftp/v1/sessions/check", h.check)
		r.Post("/sftp/v1/sessions/end", h.end)
	})
}

// MountSFTPServers registers GET /sftp/v1/servers on the protected group. It is a session-token route:
// the SFTP endpoint lists the servers the signed-in user may reach on the session's cluster.
func MountSFTPServers(p chi.Router, files *rbac.FileChecker) {
	h := &sftpServersHandler{files: files}
	p.Get("/sftp/v1/servers", h.list)
}

type sftpLoginHandler struct {
	sessions *auth.SessionStore
	store    *db.Store
	files    *rbac.FileChecker
	aud      *audit.Auditor
}

type sftpLoginReq struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	PublicKey  string `json:"publicKey"`
	RemoteAddr string `json:"remoteAddr"`
}

type sftpLoginTarget struct {
	Namespace string `json:"namespace"`
	Server    string `json:"server"`
}

type sftpLoginResp struct {
	SessionToken string           `json:"sessionToken"`
	ExpiresAt    time.Time        `json:"expiresAt"`
	User         string           `json:"user"`
	Target       *sftpLoginTarget `json:"target"`
}

// login exchanges a password or a public key for a gpsftp_ session token. Every refusal writes the same
// body and the same audit reason, whatever the cause.
func (h *sftpLoginHandler) login(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	caller, ok := auth.SFTPCallerFromContext(ctx)
	if !ok {
		httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
		return
	}
	var body sftpLoginReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}

	base, rawTarget, hasTarget := splitSFTPUsername(body.Username)
	actor := truncateRunes(base, sftpActorMaxRunes)
	refuse := func(cause string) {
		// The client and the audit trail get one generic reason; the operator's log names the cause so a
		// refused login can be diagnosed (key not registered, wrong account, no access to the target, …).
		slog.Info("sftp login refused", "user", actor, "cluster", caller.Cluster, "cause", cause)
		h.auditLoginFailure(ctx, req, actor)
		httperr.WriteMessage(w, req, messages.New("server.sftp.login_failed", nil))
	}

	// Rate limits run before any validation or credential work (so refusals cannot flood the audit
	// trail either), keyed like /auth/login: the source address the endpoint reports and the
	// full username. The request IP is always the SFTP pod, so it is not used. actor is the username
	// capped to 64 runes for the audit trail only; the username budget keys on the full base, so two
	// long SSO email names that share a 64-rune prefix do not share a budget. The key is capped one rune
	// past the longest valid name, so an oversized name cannot grow the limiter's keys.
	auth.SetActor(ctx, actor)
	if !auth.LoginLimiter.RequestBudget(w, req, sftpSourceKey(body.RemoteAddr, req)) {
		return
	}
	if !auth.LoginUserLimiter.RequestBudget(w, req, truncateRunes(base, sftpUsernameMaxRunes+1)) {
		return
	}

	if (body.Password == "") == (body.PublicKey == "") {
		refuse("credential-shape")
		return
	}
	if !validSFTPUsername(base) {
		refuse("username")
		return
	}
	var target *sftpLoginTarget
	if hasTarget {
		t, valid := parseSFTPTarget(rawTarget)
		if !valid {
			refuse("target")
			return
		}
		target = &t
	}

	var (
		userID     int64
		credential string
		epoch      int64
	)
	if body.Password != "" {
		uid, ep, good, err := h.sessions.VerifySFTPPassword(ctx, base, body.Password)
		if err != nil || !good {
			refuse("credential")
			return
		}
		userID, epoch, credential = uid, ep, "password"
	} else {
		fp, err := auth.SFTPFingerprintForLogin(body.PublicKey)
		if err != nil {
			refuse("key")
			return
		}
		keyUser, keyID, err := h.sessions.FindSFTPKey(ctx, fp)
		if err != nil {
			refuse("key")
			return
		}
		userID, credential = keyUser, fmt.Sprintf("key:%d", keyID)
	}

	// The user row is read again for the permissions, and its session_epoch must match the one the
	// credential check read, so a password reset during login invalidates the new session. A key must
	// belong to the user named in the username.
	user, dbEpoch, err := h.sessions.SFTPUserByName(ctx, base)
	if err != nil || user.ID != userID || (credential == "password" && dbEpoch != epoch) {
		refuse("account")
		return
	}
	epoch = dbEpoch

	if target != nil {
		ref := rbac.FileTarget{Cluster: caller.Cluster, Namespace: target.Namespace, Server: target.Server}
		if err := h.files.Allow(ctx, user, rbac.FileList, ref); err != nil {
			refuse("no-access")
			return
		}
		// A namespace-level servers:read passes Allow for any name, so the server must exist too.
		exists, err := h.targetServerExists(ctx, user, caller.Cluster, target.Namespace, target.Server)
		if err != nil || !exists {
			refuse("no-access")
			return
		}
	}

	token, expires, err := h.sessions.IssueSFTPSessionForToken(ctx, user.ID, caller.Cluster, credential, epoch, caller.ServiceTokenID)
	if err != nil {
		refuse("issue")
		return
	}
	h.sessions.TouchSFTPCredential(ctx, user.ID, credential)

	auditTarget := caller.Cluster
	if target != nil {
		auditTarget = fmt.Sprintf("%s/%s/%s", caller.Cluster, target.Namespace, target.Server)
	}
	if err := h.aud.WriteSyncAs(ctx, user.Username, http.MethodPost, req.URL.Path, auditTarget, "", http.StatusOK); err != nil {
		// Without a success row the session must not work: revoke it before refusing.
		if rerr := h.sessions.RevokeSFTPSessionToken(ctx, token); rerr != nil {
			slog.Error("sftp login: revoke unaudited session", "err", rerr)
		}
		httperr.WriteMessage(w, req, messages.New("server.generic.503", nil))
		return
	}
	audit.MarkRecorded(ctx)

	writeJSON(w, sftpLoginResp{SessionToken: token, ExpiresAt: expires, User: user.Username, Target: target})
}

// auditLoginFailure writes the failed-login row. The middleware row is skipped only after this one lands.
func (h *sftpLoginHandler) auditLoginFailure(ctx context.Context, req *http.Request, actor string) {
	if err := h.aud.WriteSyncAs(ctx, actor, http.MethodPost, req.URL.Path, "", sftpLoginFailedReason, http.StatusUnauthorized); err != nil {
		slog.Warn("sftp login audit", "err", err)
		return
	}
	audit.MarkRecorded(ctx)
}

// targetServerExists reports whether the named server exists on cluster and is visible to user. The file
// checker has no direct server getter, so it uses the visible-server listing, which covers every server
// the user may reach through a namespace grant or through ownership.
func (h *sftpLoginHandler) targetServerExists(ctx context.Context, user *auth.User, cluster, ns, name string) (bool, error) {
	refs, err := h.files.VisibleServers(ctx, user, cluster)
	if err != nil {
		return false, fmt.Errorf("list sftp target servers: %w", err)
	}
	for _, ref := range refs {
		if ref.Namespace == ns && ref.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// check reports which of the endpoint's session tokens are no longer valid. Never audited.
func (h *sftpLoginHandler) check(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	caller, ok := auth.SFTPCallerFromContext(ctx)
	if !ok {
		httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
		return
	}
	var body struct {
		SessionTokens []string `json:"sessionTokens"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	if len(body.SessionTokens) > sftpCheckMaxTokens {
		httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
		return
	}

	// A token without the session prefix can never be valid: it is reported, not rejected.
	revoked := []string{}
	byDigest := make(map[string]string, len(body.SessionTokens))
	digests := make([]string, 0, len(body.SessionTokens))
	for _, tok := range body.SessionTokens {
		if !strings.HasPrefix(tok, auth.SFTPSessionTokenPrefix) {
			revoked = append(revoked, tok)
			continue
		}
		d := auth.SFTPTokenDigest(tok)
		if _, dup := byDigest[d]; dup {
			continue
		}
		byDigest[d] = tok
		digests = append(digests, d)
	}

	missing, err := h.sessions.CheckSFTPSessions(ctx, caller.Cluster, digests)
	if err != nil {
		httperr.WriteMessage(w, req, messages.New("server.generic.503", nil))
		return
	}
	for _, d := range missing {
		if tok, ok := byDigest[d]; ok {
			revoked = append(revoked, tok)
		}
	}
	writeJSON(w, map[string]any{"revoked": revoked})
}

// end revokes sessions the endpoint turned away. Unknown or foreign tokens are ignored.
func (h *sftpLoginHandler) end(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	caller, ok := auth.SFTPCallerFromContext(ctx)
	if !ok {
		httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
		return
	}
	var body struct {
		SessionTokens []string `json:"sessionTokens"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.common.invalid_json", nil))
		return
	}
	if len(body.SessionTokens) > sftpCheckMaxTokens {
		httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
		return
	}
	digests := make([]string, 0, len(body.SessionTokens))
	for _, tok := range body.SessionTokens {
		if strings.HasPrefix(tok, auth.SFTPSessionTokenPrefix) {
			digests = append(digests, auth.SFTPTokenDigest(tok))
		}
	}
	if err := h.sessions.EndSFTPSessions(ctx, caller.Cluster, digests); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.generic.503", nil))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sftpServersHandler struct {
	files *rbac.FileChecker
}

type sftpServerItem struct {
	Namespace string `json:"namespace"`
	Server    string `json:"server"`
	// Write reports whether the file-access check lets the session change files on this server, so the
	// SFTP endpoint refuses an upload when the file is opened instead of when it is closed.
	Write bool `json:"write"`
}

// list serves the session's servers. Only session-token callers may use it.
func (h *sftpServersHandler) list(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	caller, ok := auth.SFTPCallerFromContext(ctx)
	if !ok || caller.SessionID == 0 {
		httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
		return
	}
	user := auth.UserFromContext(ctx)
	if user == nil {
		httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
		return
	}
	refs, err := h.files.VisibleServers(ctx, user, caller.Cluster)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	items := make([]sftpServerItem, 0, len(refs))
	for _, ref := range refs {
		target := rbac.FileTarget{Cluster: ref.Cluster, Namespace: ref.Namespace, Server: ref.Name}
		write := h.files.Allow(ctx, user, rbac.FileCreate, target) == nil
		items = append(items, sftpServerItem{Namespace: ref.Namespace, Server: ref.Name, Write: write})
	}
	writeJSON(w, map[string]any{"items": items})
}

// sftpUsernameMaxRunes bounds an SFTP login name: long enough for an SSO account named by its email
// address (RFC 5321 caps a path at 254), far below anything that costs real work.
const sftpUsernameMaxRunes = 254

// validSFTPUsername reports whether name can be a panel username. Local accounts follow identifierRE,
// but SSO accounts are named by the identity provider (usually an email address), so the check only
// refuses what no account can hold: an empty or overlong name, whitespace, control characters and "/".
// The user lookup decides the rest.
func validSFTPUsername(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > sftpUsernameMaxRunes {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' {
			return false
		}
	}
	return true
}

// splitSFTPUsername splits "name+target" at the last "+". The base is what authenticates.
func splitSFTPUsername(raw string) (base, target string, hasTarget bool) {
	i := strings.LastIndex(raw, "+")
	if i < 0 {
		return raw, "", false
	}
	return raw[:i], raw[i+1:], true
}

// parseSFTPTarget accepts "server" (default namespace) or "namespace/server".
func parseSFTPTarget(s string) (sftpLoginTarget, bool) {
	ns, server := scope.DefaultNamespace, s
	if before, after, found := strings.Cut(s, "/"); found {
		ns, server = before, after
	}
	if !dnsLabelRE.MatchString(ns) || !dnsLabelRE.MatchString(server) || !scope.Allowed(ns) {
		return sftpLoginTarget{}, false
	}
	return sftpLoginTarget{Namespace: ns, Server: server}, true
}

// sftpSourceKey is the address the endpoint reports for the client, falling back to the request's own
// client IP when the body carries no valid address.
func sftpSourceKey(remote string, req *http.Request) string {
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return ap.Addr().String()
	}
	if a, err := netip.ParseAddr(remote); err == nil {
		return a.String()
	}
	if ip := middleware.GetClientIP(req.Context()); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// truncateRunes returns at most n runes of s.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
