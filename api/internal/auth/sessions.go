package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

const (
	sessionCookie = "gameplane_session"
	csrfHeader    = "X-Gameplane-CSRF"
	csrfCookie    = "gameplane_csrf"
	sessionTTL    = 12 * time.Hour
)

type ctxKey int

const userCtxKey ctxKey = iota

// User is the authenticated-user view passed down the middleware
// chain. It's a snapshot, not a live row — mutations require a
// round-trip to the store.
type User struct {
	ID          int64
	Username    string
	DisplayName string
	Email       string
	Role        string

	// Perms is the caller's resolved permission set, three-level map:
	// cluster (outer key; "*" = all clusters) → namespace (middle key;
	// "*" = cluster-wide) → permission set (value; "*" = all permissions).
	// It is loaded from the user's role bindings at authentication time.
	// Nil for Users built outside the session path (treated as holding nothing).
	Perms map[string]map[string]map[string]struct{}
}

// SessionStore manages session lifecycle for authenticated users.
type SessionStore struct {
	db *db.Store
}

// NewSessionStore returns a new SessionStore.
func NewSessionStore(store *db.Store) *SessionStore { return &SessionStore{db: store} }

// sessionDigest returns the hex SHA-256 of a raw session cookie value. The
// sessions table stores only this digest (column "token"), never the cookie
// value, so a copy of the database cannot be replayed as a session cookie.
// The raw value is 256 bits of CSPRNG output, so a plain unsalted hash is
// sufficient and keeps lookup a single equality match.
func sessionDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Create writes a new session row for the given user and returns the
// values that need to go back on the response (raw session cookie value +
// CSRF token). Only the digest of the session value is persisted.
func (s *SessionStore) Create(ctx context.Context, userID int64) (session, csrf string, err error) {
	session = randomToken()
	csrf = randomToken()
	_, err = s.db.DB.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, csrf_token, expires_at) VALUES (?,?,?,?)`,
		sessionDigest(session), userID, csrf, time.Now().Add(sessionTTL).UTC().Format(time.RFC3339),
	)
	return
}

// StartGC launches a background goroutine that deletes expired session
// rows on the given interval until ctx is cancelled. Without this,
// expired sessions only get cleaned on the unlikely event that their
// exact token is re-looked-up — the table grows forever in practice.
func (s *SessionStore) StartGC(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.gcOnce(ctx)
			}
		}
	}()
}

// DeleteForUser drops every session row owned by the given user. Call
// after a privilege change (role demotion, password reset, etc.) so
// active sessions can't continue under the old trust boundary.
func (s *SessionStore) DeleteForUser(ctx context.Context, userID int64) error {
	return s.DeleteForUserWith(ctx, s.db.DB, userID)
}

// DeleteForUserWith deletes every session of userID through ex, so a caller
// can end sessions inside its own transaction.
func (s *SessionStore) DeleteForUserWith(ctx context.Context, ex db.Execer, userID int64) error {
	_, err := ex.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *SessionStore) gcOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.DB.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`, cutoff)
	if err != nil {
		slog.Warn("session gc", "err", err)
		return
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		slog.Info("session gc", "deleted", n)
	}
}

// Authenticate is the middleware wrapping all protected routes.
func (s *SessionStore) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cookie, err := req.Cookie(sessionCookie)
		if err != nil {
			httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
			return
		}
		u, csrf, err := s.lookup(req.Context(), cookie.Value)
		if err != nil {
			httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
			return
		}
		if isMutating(req.Method) {
			if req.Header.Get(csrfHeader) != csrf {
				httperr.WriteMessage(w, req, messages.New("server.auth.csrf_mismatch", nil))
				return
			}
		}
		ctx := context.WithValue(req.Context(), userCtxKey, u)
		// Record the actor on any audit holder the outer audit middleware
		// installed, so the audit log attributes this request to the user
		// (the user ctx below doesn't propagate back up to that middleware).
		SetActor(req.Context(), u.Username)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func (s *SessionStore) lookup(ctx context.Context, token string) (*User, string, error) {
	digest := sessionDigest(token)
	var (
		u       User
		csrf    string
		expires string
	)
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.display_name, u.email, u.role, s.csrf_token, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ?`, digest,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &csrf, &expires)
	if err != nil {
		return nil, "", err
	}
	perms, perr := s.LoadPerms(ctx, u.ID)
	if perr != nil {
		return nil, "", perr
	}
	u.Perms = perms
	exp, perr := time.Parse(time.RFC3339, expires)
	if perr != nil {
		// A corrupt expires_at row is indistinguishable from an expired
		// session from the client's perspective — delete it and make the
		// user log in again. Log so it's visible in ops.
		slog.Warn("session expires_at parse", "err", perr, "value", expires)
		_, _ = s.db.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, digest)
		return nil, "", errors.New("session invalid")
	}
	if time.Now().After(exp) {
		_, _ = s.db.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, digest)
		return nil, "", errors.New("session expired")
	}
	return &u, csrf, nil
}

// HandleLogout deletes the current session cookie server-side.
func (s *SessionStore) HandleLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if c, err := req.Cookie(sessionCookie); err == nil {
			_, _ = s.db.DB.ExecContext(req.Context(), `DELETE FROM sessions WHERE token = ?`, sessionDigest(c.Value))
		}
		clearCookie(w, sessionCookie)
		// The CSRF cookie is JS-readable while set (see setCSRFCookie), but
		// clearing it is server-driven and browsers match a delete on
		// Name/Domain/Path — the HttpOnly flag on the clearing Set-Cookie
		// doesn't matter, so clearCookie always sends HttpOnly.
		clearCookie(w, csrfCookie)
		w.WriteHeader(http.StatusNoContent)
	}
}

// UserFromContext returns the authenticated user from a context, or nil.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userCtxKey).(*User)
	return u
}

// WithUser returns a context carrying u as the authenticated caller.
// The handler middleware sets this in production; tests use it directly
// to bypass the cookie/session round-trip.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing means the OS CSPRNG is broken — there is
		// no safe continuation for an auth system. Panicking fails the
		// request loudly rather than handing out an all-zero token.
		panic("crypto/rand: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
	})
}

func setCSRFCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	// CSRF cookie is deliberately NOT HttpOnly so the SPA can read it
	// and echo it back as the X-Gameplane-CSRF header.
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	clearCookieAt(w, name, "/")
}

// clearCookieAt clears a cookie set on a specific path — browsers only
// drop a cookie when the clearing Set-Cookie carries the same Path.
//
// HttpOnly is always set here regardless of how the cookie was originally
// created: a deletion (Value: "", MaxAge: -1) is matched by the browser on
// Name/Domain/Path only, so the HttpOnly flag has no bearing on whether the
// clear lands, including for the JS-readable CSRF cookie.
func clearCookieAt(w http.ResponseWriter, name string, path string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}
