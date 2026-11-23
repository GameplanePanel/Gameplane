package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// SFTPCaller identifies a request made on behalf of the SFTP endpoint. SessionID is 0 for service-token requests.
type SFTPCaller struct {
	Cluster        string
	SessionID      int64
	ServiceTokenID int64
}

type sftpCallerKey struct{}

// WithSFTPCaller returns a context carrying the SFTP caller that made the request.
func WithSFTPCaller(ctx context.Context, c SFTPCaller) context.Context {
	return context.WithValue(ctx, sftpCallerKey{}, c)
}

// SFTPCallerFromContext returns the SFTP caller of the request, if the request came from the SFTP endpoint.
func SFTPCallerFromContext(ctx context.Context) (SFTPCaller, bool) {
	c, ok := ctx.Value(sftpCallerKey{}).(SFTPCaller)
	return c, ok
}

// SFTPTokenFailLimiter caps failed SFTP bearer lookups per client IP. Valid tokens are never throttled by it.
var SFTPTokenFailLimiter = namedBucket("sftp_token_fail_ip", 5.0/60.0, 10)

// SFTPSessionLimiter caps requests per SFTP session id. SFTP traffic arrives from the SFTP pod's
// address, so the per-IP mutation limiter cannot tell its users apart.
var SFTPSessionLimiter = namedBucket("sftp_session", 100.0, 400)

// RequestBudget exports the per-key budget check so handlers can key buckets on values other than
// the request IP (the SFTP login keys on the body's remoteAddr and username). It writes 429 or 503 itself.
func (t *TokenBucket) RequestBudget(w http.ResponseWriter, req *http.Request, key string) bool {
	return t.requestBudget(w, req, key)
}

// sftpSessionRoutes are the only routes a session token may call (contracts/api.md). Names are [^/:]+.
var sftpSessionRoutes = []struct {
	method string
	re     *regexp.Regexp
}{
	{http.MethodGet, regexp.MustCompile(`^/sftp/v1/servers$`)},
	{http.MethodGet, regexp.MustCompile(`^/servers/[^/:]+/files/(list|stat|download)$`)},
	{http.MethodPost, regexp.MustCompile(`^/servers/[^/:]+/files/(mkdir|rename)$`)},
	{http.MethodDelete, regexp.MustCompile(`^/servers/[^/:]+/files/delete$`)},
	{http.MethodPut, regexp.MustCompile(`^/servers/[^/:]+/files/stream$`)},
}

// sftpRouteAllowed reports whether a session token may call method on path.
func sftpRouteAllowed(method, path string) bool {
	for _, r := range sftpSessionRoutes {
		if r.method == method && r.re.MatchString(path) {
			return true
		}
	}
	return false
}

// bearerSFTPSession returns the SFTP session token in an "Authorization: Bearer" header, if it has that prefix.
func bearerSFTPSession(req *http.Request) (string, bool) {
	return bearerWithPrefix(req, SFTPSessionTokenPrefix)
}

// bearerSFTPService returns the SFTP service token in an "Authorization: Bearer" header, if it has that prefix.
func bearerSFTPService(req *http.Request) (string, bool) {
	return bearerWithPrefix(req, SFTPServiceTokenPrefix)
}

func bearerWithPrefix(req *http.Request, prefix string) (string, bool) {
	h := req.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	tok := strings.TrimPrefix(h, "Bearer ")
	if !strings.HasPrefix(tok, prefix) {
		return "", false
	}
	return tok, true
}

// AttachSFTPAudit installs the audit writer for SFTP refusals. It is called once at startup, after the
// auditor exists (auth cannot import audit).
func (s *SessionStore) AttachSFTPAudit(fn func(ctx context.Context, method, path, target, reason string, status int) error) {
	s.sftpAuditFn = fn
}

// sftpAudit records an SFTP event through the attached writer. It is a no-op when none is attached.
func (s *SessionStore) sftpAudit(ctx context.Context, req *http.Request, reason string, status int) {
	if s.sftpAuditFn == nil {
		return
	}
	if err := s.sftpAuditFn(context.WithoutCancel(ctx), req.Method, req.URL.Path, "", reason, status); err != nil {
		slog.Warn("sftp audit", "reason", reason, "err", err)
	}
}

// serveSFTPSession authenticates a bearer session token and serves the request with the session's user
// as the actor, scoped to the session's cluster. Failures never reveal which check failed.
func (s *SessionStore) serveSFTPSession(w http.ResponseWriter, req *http.Request, raw string, next http.Handler) {
	u, sess, err := s.LookupSFTPSessionToken(req.Context(), raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, errSessionInvalid) || errors.Is(err, errSessionExpired) {
			// Limit first so a flood of bad tokens cannot write unbounded audit rows.
			if !SFTPTokenFailLimiter.Allow(mcpClientIP(req)) {
				httperr.WriteMessage(w, req, messages.New("server.common.too_many_requests", nil))
				return
			}
			s.sftpAudit(req.Context(), req, "sftp-refused", http.StatusUnauthorized)
			httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
			return
		}
		httperr.WriteMessage(w, req, messages.New("server.generic.503", nil))
		return
	}
	if !SFTPSessionLimiter.Allow(fmt.Sprint(sess.ID)) {
		httperr.WriteMessage(w, req, messages.New("server.common.too_many_requests", nil))
		return
	}
	ctx := WithUser(req.Context(), u)
	ctx = WithSFTPCaller(ctx, SFTPCaller{Cluster: sess.Cluster, SessionID: sess.ID})
	SetActor(req.Context(), u.Username)
	SetSource(req.Context(), "sftp")
	s.TouchSFTPCredential(ctx, sess.UserID, sess.Credential)
	s.slideSFTPSession(ctx, sess.ID)
	next.ServeHTTP(w, req.WithContext(ctx))
}

// SFTPSessionGuard limits SFTP session-token requests to sftpSessionRoutes and pins them to the
// session's cluster. Requests without an SFTP session caller pass through unchanged.
func (s *SessionStore) SFTPSessionGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		caller, ok := SFTPCallerFromContext(req.Context())
		if !ok || caller.SessionID == 0 {
			next.ServeHTTP(w, req)
			return
		}
		if req.Header.Get("Upgrade") != "" || !sftpRouteAllowed(req.Method, req.URL.Path) {
			s.sftpAudit(req.Context(), req, "sftp-refused", http.StatusForbidden)
			httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
			return
		}
		// The cluster is never taken from the caller: whatever the request carries is overwritten.
		q := req.URL.Query()
		q.Set("cluster", caller.Cluster)
		req.URL.RawQuery = q.Encode()
		next.ServeHTTP(w, req)
	})
}

// SFTPServiceAuth authenticates the SFTP endpoint's service token on /sftp/v1/login and
// /sftp/v1/sessions/check. Anything else is refused with the generic 401.
func (s *SessionStore) SFTPServiceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var (
			id      int64
			cluster string
			err     error
		)
		if raw, ok := bearerSFTPService(req); ok {
			id, cluster, err = s.AuthenticateSFTPService(req.Context(), raw)
		} else {
			err = sql.ErrNoRows
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Limit first so a flood of bad tokens cannot write unbounded audit rows.
				if !SFTPTokenFailLimiter.Allow(mcpClientIP(req)) {
					httperr.WriteMessage(w, req, messages.New("server.common.too_many_requests", nil))
					return
				}
				s.sftpAudit(req.Context(), req, "sftp-refused", http.StatusUnauthorized)
				httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
				return
			}
			httperr.WriteMessage(w, req, messages.New("server.generic.503", nil))
			return
		}
		ctx := WithSFTPCaller(req.Context(), SFTPCaller{Cluster: cluster, ServiceTokenID: id})
		SetSource(req.Context(), "sftp")
		s.TouchSFTPServiceToken(ctx, id)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}
