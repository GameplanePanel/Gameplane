package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// MCPTokenPrefix starts every personal MCP token, so the bearer path can tell
// a token from any other credential without a lookup.
const MCPTokenPrefix = "gpmcp_"

const (
	mcpTokenRandomBytes = 32
	mcpTokenBodyLen     = 43 // base64 raw URL encoding of 32 bytes
	maxMCPTokenNameLen  = 64
	maxActiveMCPTokens  = 10
	mcpRevokedKeep      = 30 * 24 * time.Hour
	mcpSessionTTL       = 24 * time.Hour
	mcpSessionHeader    = "X-Gameplane-MCP-Session"
	maxMCPSessionIDLen  = 64
	lastUsedInterval    = time.Minute
)

// mcpTokenLifetimes are the allowed expiry windows in days.
var mcpTokenLifetimes = []int{7, 30, 90, 365}

// MCPToken is the listed view of a token. The secret is never part of it.
type MCPToken struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

// Errors returned by the MCP token store. Handlers map them to responses.
var (
	ErrMCPTokenName      = errors.New("mcp token: name must be 1 to 64 characters")
	ErrMCPTokenLifetime  = errors.New("mcp token: lifetime must be 7, 30, 90 or 365 days")
	ErrMCPTokenDuplicate = errors.New("mcp token: name already in use")
	ErrMCPTokenLimit     = errors.New("mcp token: too many active tokens")
	ErrMCPTokenNotFound  = errors.New("mcp token: not found")
)

// mcpTokenKey is the context key for the token ID of a token-authenticated request.
type mcpTokenKey struct{}

// WithMCPToken returns a context carrying the ID of the MCP token that authenticated the request.
func WithMCPToken(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, mcpTokenKey{}, id)
}

// MCPTokenFromContext returns the MCP token ID that authenticated the request, if any.
func MCPTokenFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(mcpTokenKey{}).(int64)
	return id, ok
}

// mcpLastTouch records the last last_used_at write per token ID.
var mcpLastTouch sync.Map

// mcpSessions records first sightings of X-Gameplane-MCP-Session values, keyed tokenID+"/"+session.
var mcpSessions = struct {
	mu   sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

// AttachMCPAudit installs the audit writer for MCP events. It is called once
// at startup, after the auditor exists (auth cannot import audit).
func (s *SessionStore) AttachMCPAudit(fn func(ctx context.Context, method, path, target, reason string, status int) error) {
	s.mcpAuditFn = fn
}

// mcpAudit records an MCP event through the attached writer. It is a no-op when none is attached.
func (s *SessionStore) mcpAudit(ctx context.Context, req *http.Request, reason string, status int) {
	if s.mcpAuditFn == nil {
		return
	}
	if err := s.mcpAuditFn(context.WithoutCancel(ctx), req.Method, req.URL.Path, "", reason, status); err != nil {
		slog.Warn("mcp audit", "reason", reason, "err", err)
	}
}

// newMCPTokenID returns a random positive 53-bit integer for mcp_tokens.id.
// maxSafeJSONInt is 2^53-1, the largest integer a JSON number carries exactly.
const maxSafeJSONInt = 1<<53 - 1

func newMCPTokenID() int64 {
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic("crypto/rand: " + err.Error())
		}
		// 53 bits: the id travels as a JSON number, and JavaScript (the
		// dashboard) loses precision above Number.MAX_SAFE_INTEGER.
		if id := int64(binary.BigEndian.Uint64(b[:]) & maxSafeJSONInt); id != 0 {
			return id
		}
	}
}

func validMCPTokenName(name string) (string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	if n < 1 || n > maxMCPTokenNameLen {
		return "", ErrMCPTokenName
	}
	return name, nil
}

func validMCPTokenLifetime(days int) error {
	for _, d := range mcpTokenLifetimes {
		if d == days {
			return nil
		}
	}
	return ErrMCPTokenLifetime
}

// CreateMCPToken creates a named token for userID that expires after days.
// It returns the token string, which is shown once and never stored.
func (s *SessionStore) CreateMCPToken(ctx context.Context, userID int64, name string, days int) (MCPToken, string, error) {
	name, err := validMCPTokenName(name)
	if err != nil {
		return MCPToken{}, "", err
	}
	if err := validMCPTokenLifetime(days); err != nil {
		return MCPToken{}, "", err
	}

	raw := make([]byte, mcpTokenRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	token := MCPTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)

	now := time.Now().UTC()
	expires := now.Add(time.Duration(days) * 24 * time.Hour)
	tok := MCPToken{
		ID:        newMCPTokenID(),
		Name:      name,
		Prefix:    token[:10],
		CreatedAt: now.Truncate(time.Second),
		ExpiresAt: expires.Truncate(time.Second),
	}

	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return MCPToken{}, "", fmt.Errorf("begin mcp token tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_tokens WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?`,
		userID, now.Format(time.RFC3339),
	).Scan(&active); err != nil {
		return MCPToken{}, "", fmt.Errorf("count mcp tokens: %w", err)
	}
	if active >= maxActiveMCPTokens {
		return MCPToken{}, "", ErrMCPTokenLimit
	}

	var one int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM mcp_tokens WHERE user_id = ? AND name = ?`, userID, name,
	).Scan(&one)
	switch {
	case err == nil:
		return MCPToken{}, "", ErrMCPTokenDuplicate
	case !errors.Is(err, sql.ErrNoRows):
		return MCPToken{}, "", fmt.Errorf("check mcp token name: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO mcp_tokens(id, user_id, name, digest, prefix, session_epoch, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, (SELECT session_epoch FROM users WHERE id = ?), ?, ?)`,
		tok.ID, userID, name, sessionDigest(token), tok.Prefix, userID,
		now.Format(time.RFC3339), expires.Format(time.RFC3339),
	); err != nil {
		// A concurrent create with the same name passes the pre-check and
		// then hits UNIQUE (user_id, name); report it as a duplicate.
		var dup int
		if qerr := s.db.DB.QueryRowContext(ctx,
			`SELECT 1 FROM mcp_tokens WHERE user_id = ? AND name = ?`, userID, name,
		).Scan(&dup); qerr == nil {
			return MCPToken{}, "", ErrMCPTokenDuplicate
		}
		return MCPToken{}, "", fmt.Errorf("insert mcp token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MCPToken{}, "", fmt.Errorf("commit mcp token: %w", err)
	}
	return tok, token, nil
}

// ListMCPTokens returns userID's tokens, newest first. Tokens revoked or
// expired more than 30 days ago are hidden.
func (s *SessionStore) ListMCPTokens(ctx context.Context, userID int64) ([]MCPToken, error) {
	cutoff := time.Now().UTC().Add(-mcpRevokedKeep).Format(time.RFC3339)
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, name, prefix, created_at, expires_at, last_used_at, revoked_at
		FROM mcp_tokens
		WHERE user_id = ? AND expires_at >= ? AND (revoked_at IS NULL OR revoked_at >= ?)
		ORDER BY created_at DESC, id DESC`, userID, cutoff, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list mcp tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []MCPToken{}
	for rows.Next() {
		var (
			t                   MCPToken
			created, expires    string
			lastUsed, revokedAt sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &created, &expires, &lastUsed, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan mcp token: %w", err)
		}
		if t.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
			return nil, fmt.Errorf("parse mcp token created_at: %w", err)
		}
		if t.ExpiresAt, err = time.Parse(time.RFC3339, expires); err != nil {
			return nil, fmt.Errorf("parse mcp token expires_at: %w", err)
		}
		if t.LastUsedAt, err = parseOptionalTime(lastUsed); err != nil {
			return nil, err
		}
		if t.RevokedAt, err = parseOptionalTime(revokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp tokens: %w", err)
	}
	return out, nil
}

func parseOptionalTime(v sql.NullString) (*time.Time, error) {
	if !v.Valid {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v.String)
	if err != nil {
		return nil, fmt.Errorf("parse mcp token time: %w", err)
	}
	return &t, nil
}

// RevokeMCPToken revokes an active token owned by userID.
func (s *SessionStore) RevokeMCPToken(ctx context.Context, userID, tokenID int64) error {
	res, err := s.db.DB.ExecContext(ctx,
		`UPDATE mcp_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339), tokenID, userID)
	if err != nil {
		return fmt.Errorf("revoke mcp token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke mcp token rows: %w", err)
	}
	if n == 0 {
		return ErrMCPTokenNotFound
	}
	return nil
}

// lookupMCPToken resolves a raw bearer token to its owner and token ID. It
// returns sql.ErrNoRows for an unknown, revoked or epoch-mismatched token.
func (s *SessionStore) lookupMCPToken(ctx context.Context, raw string) (*User, int64, error) {
	if !strings.HasPrefix(raw, MCPTokenPrefix) || len(raw) != len(MCPTokenPrefix)+mcpTokenBodyLen {
		return nil, 0, sql.ErrNoRows
	}
	var (
		u       User
		tokenID int64
		expires string
	)
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT t.id, u.id, u.username, u.display_name, u.email, u.role, t.expires_at
		FROM mcp_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.digest = ? AND t.revoked_at IS NULL AND t.session_epoch = u.session_epoch`,
		sessionDigest(raw),
	).Scan(&tokenID, &u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &expires)
	if err != nil {
		return nil, 0, err
	}
	exp, perr := time.Parse(time.RFC3339, expires)
	if perr != nil {
		slog.Warn("mcp token expires_at parse", "err", perr)
		return nil, 0, errSessionInvalid
	}
	if time.Now().After(exp) {
		return nil, 0, errSessionExpired
	}
	perms, err := s.LoadPerms(ctx, u.ID)
	if err != nil {
		return nil, 0, err
	}
	u.Perms = perms
	return &u, tokenID, nil
}

// touchMCPToken writes last_used_at for tokenID at most once per lastUsedInterval.
// Errors are logged and ignored: a failed touch must not fail the request.
func (s *SessionStore) touchMCPToken(ctx context.Context, tokenID int64) {
	now := time.Now()
	if v, ok := mcpLastTouch.Load(tokenID); ok {
		if last, isTime := v.(time.Time); isTime && now.Sub(last) < lastUsedInterval {
			return
		}
	}
	mcpLastTouch.Store(tokenID, now)
	if _, err := s.db.DB.ExecContext(ctx,
		`UPDATE mcp_tokens SET last_used_at = ? WHERE id = ?`, now.UTC().Format(time.RFC3339), tokenID,
	); err != nil {
		slog.Debug("mcp token last_used_at", "err", err)
	}
}

// bearerMCPToken returns the MCP token in an "Authorization: Bearer" header, if it has the MCP prefix.
func bearerMCPToken(req *http.Request) (string, bool) {
	h := req.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	tok := strings.TrimPrefix(h, "Bearer ")
	if !strings.HasPrefix(tok, MCPTokenPrefix) {
		return "", false
	}
	return tok, true
}

// mcpClientIP returns the client IP the same way the rate limiter does.
func mcpClientIP(req *http.Request) string {
	if ip := middleware.GetClientIP(req.Context()); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// serveMCPToken authenticates a bearer MCP token and serves the request
// with the token owner as the user. Failures never reveal which check failed.
func (s *SessionStore) serveMCPToken(w http.ResponseWriter, req *http.Request, raw string, next http.Handler) {
	u, id, err := s.lookupMCPToken(req.Context(), raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, errSessionInvalid) || errors.Is(err, errSessionExpired) {
			// Limit first so a flood of bad tokens cannot write unbounded audit rows.
			if !MCPTokenFailLimiter.Allow(mcpClientIP(req)) {
				httperr.WriteMessage(w, req, messages.New("server.common.too_many_requests", nil))
				return
			}
			s.mcpAudit(req.Context(), req, "mcp-refused", http.StatusUnauthorized)
			httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
			return
		}
		httperr.WriteMessage(w, req, messages.New("server.generic.503", nil))
		return
	}
	if !MCPTokenReadLimiter.Allow(fmt.Sprint(id)) {
		httperr.WriteMessage(w, req, messages.New("server.common.too_many_requests", nil))
		return
	}
	ctx := context.WithValue(req.Context(), userCtxKey, u)
	ctx = WithMCPToken(ctx, id)
	SetActor(req.Context(), u.Username+" (mcp)")
	s.touchMCPToken(ctx, id)
	next.ServeHTTP(w, req.WithContext(ctx))
}

// MCPTokenRoutes are the anchored path patterns a token may call. Every
// route is GET or HEAD only. Names are matched as [^/:]+.
var MCPTokenRoutes = []string{
	`^/users/me$`,
	`^/clusters$`,
	`^/namespaces$`,
	`^/servers$`,
	`^/servers/[^/:]+$`,
	`^/servers/[^/:]+/(events|pods)$`,
	`^/servers/[^/:]+/logs/pod/tail$`,
	`^/(templates|backups|schedules|restores)$`,
	`^/(templates|backups|schedules|restores)/[^/:]+$`,
	`^/modules$`,
	`^/modules/sources$`,
	`^/modules/[^/:]+$`,
	`^/users/me/servers$`,
	`^/pods$`,
	`^/pods/[^/:]+$`,
	`^/kube-events$`,
	`^/admin/system-logs/(api|operator)$`,
}

// mcpTokenExcluded lists paths that match a pattern above but are not
// readable by tokens. /modules/catalog matches ^/modules/[^/:]+$ and is refused here.
var mcpTokenExcluded = regexp.MustCompile(`^/modules/catalog$`)

var mcpTokenRouteRes = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(MCPTokenRoutes))
	for _, p := range MCPTokenRoutes {
		out = append(out, regexp.MustCompile(p))
	}
	return out
}()

func mcpTokenRouteAllowed(path string) bool {
	if mcpTokenExcluded.MatchString(path) {
		return false
	}
	for _, re := range mcpTokenRouteRes {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// statusRecorder captures the status code written by the wrapped handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// firstMCPSession reports whether this token and session ID have not been
// seen in the last 24 hours, and records the sighting. Expired entries are pruned on insert.
func firstMCPSession(tokenID int64, session string, now time.Time) bool {
	key := fmt.Sprintf("%d/%s", tokenID, session)
	mcpSessions.mu.Lock()
	defer mcpSessions.mu.Unlock()
	if seen, ok := mcpSessions.seen[key]; ok && now.Sub(seen) < mcpSessionTTL {
		return false
	}
	for k, t := range mcpSessions.seen {
		if now.Sub(t) >= mcpSessionTTL {
			delete(mcpSessions.seen, k)
		}
	}
	mcpSessions.seen[key] = now
	return true
}

// MCPTokenGuard limits token-authenticated requests to read-only routes in
// MCPTokenRoutes. Requests without a token pass through unchanged.
func (s *SessionStore) MCPTokenGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id, ok := MCPTokenFromContext(req.Context())
		if !ok {
			next.ServeHTTP(w, req)
			return
		}
		ctx := req.Context()
		if (req.Method != http.MethodGet && req.Method != http.MethodHead) ||
			req.Header.Get("Upgrade") != "" || !mcpTokenRouteAllowed(req.URL.Path) {
			s.mcpAudit(ctx, req, "mcp-refused", http.StatusForbidden)
			httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
			return
		}

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, req)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			s.mcpAudit(ctx, req, "mcp-refused", status)
		}
		if sess := req.Header.Get(mcpSessionHeader); sess != "" && len(sess) <= maxMCPSessionIDLen {
			if firstMCPSession(id, sess, time.Now()) {
				s.mcpAudit(ctx, req, "mcp-session", status)
			}
		}
	})
}
