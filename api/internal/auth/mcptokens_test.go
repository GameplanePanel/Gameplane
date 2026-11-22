package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// mcptSetup returns a migrated store with two users, alice and bob, and a session store on it.
func mcptSetup(t *testing.T) (*db.Store, *SessionStore) {
	t.Helper()
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "viewer")
	seedUser(t, s, "bob", "pw", "viewer")
	return s, NewSessionStore(s)
}

// mcptUserID returns the row ID of a user seeded by mcptSetup.
func mcptUserID(t *testing.T, s *db.Store, username string) int64 {
	t.Helper()
	var id int64
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT id FROM users WHERE username = ?`, username).Scan(&id); err != nil {
		t.Fatalf("user id for %s: %v", username, err)
	}
	return id
}

// mcptCreate creates a 30-day token and fails the test on error.
func mcptCreate(t *testing.T, ss *SessionStore, uid int64, name string) (MCPToken, string) {
	t.Helper()
	tok, raw, err := ss.CreateMCPToken(context.Background(), uid, name, 30)
	if err != nil {
		t.Fatalf("create token %q: %v", name, err)
	}
	return tok, raw
}

func TestMCPToken_FormatAndPrefix(t *testing.T) {
	s, ss := mcptSetup(t)
	alice := mcptUserID(t, s, "alice")
	tok, raw := mcptCreate(t, ss, alice, "laptop")

	if !strings.HasPrefix(raw, MCPTokenPrefix) {
		t.Fatalf("token %q lacks prefix %q", raw, MCPTokenPrefix)
	}
	if want := len(MCPTokenPrefix) + 43; len(raw) != want {
		t.Fatalf("token length = %d, want %d", len(raw), want)
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, MCPTokenPrefix))
	if err != nil || len(body) != 32 {
		t.Fatalf("token body decodes to %d bytes, err=%v; want 32 bytes", len(body), err)
	}
	if tok.Prefix != raw[:10] {
		t.Fatalf("prefix = %q, want %q", tok.Prefix, raw[:10])
	}
	_, other := mcptCreate(t, ss, alice, "phone")
	if other == raw {
		t.Fatal("two tokens are identical")
	}
}

func TestMCPToken_OnlyDigestStored(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	tok, raw := mcptCreate(t, ss, mcptUserID(t, s, "alice"), "laptop")

	var digest string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT digest FROM mcp_tokens WHERE id = ?`, tok.ID).Scan(&digest); err != nil {
		t.Fatalf("read digest: %v", err)
	}
	if digest != sessionDigest(raw) {
		t.Fatalf("stored digest = %q, want SHA-256 of the token", digest)
	}
	// The raw secret must not appear in any column of the table.
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_tokens WHERE digest = ? OR name = ? OR prefix = ?`,
		raw, raw, raw).Scan(&n); err != nil {
		t.Fatalf("scan raw token: %v", err)
	}
	if n != 0 {
		t.Fatalf("raw token is stored in mcp_tokens")
	}
}

func TestMCPToken_CreateListRevoke(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")
	bob := mcptUserID(t, s, "bob")
	laptop, _ := mcptCreate(t, ss, alice, "laptop")
	ci, _ := mcptCreate(t, ss, alice, "ci")

	items, err := ss.ListMCPTokens(ctx, alice)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]MCPToken{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if len(items) != 2 || byName["laptop"].ID != laptop.ID || byName["ci"].ID != ci.ID {
		t.Fatalf("list = %+v, want laptop and ci", items)
	}
	if byName["laptop"].RevokedAt != nil || byName["laptop"].LastUsedAt != nil {
		t.Fatalf("new token has revokedAt or lastUsedAt set: %+v", byName["laptop"])
	}

	if err := ss.RevokeMCPToken(ctx, bob, laptop.ID); !errors.Is(err, ErrMCPTokenNotFound) {
		t.Fatalf("bob revoking alice's token: err = %v, want ErrMCPTokenNotFound", err)
	}
	if err := ss.RevokeMCPToken(ctx, alice, laptop.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := ss.RevokeMCPToken(ctx, alice, laptop.ID); !errors.Is(err, ErrMCPTokenNotFound) {
		t.Fatalf("second revoke: err = %v, want ErrMCPTokenNotFound", err)
	}
	if err := ss.RevokeMCPToken(ctx, alice, 999999); !errors.Is(err, ErrMCPTokenNotFound) {
		t.Fatalf("revoke unknown id: err = %v, want ErrMCPTokenNotFound", err)
	}

	items, err = ss.ListMCPTokens(ctx, alice)
	if err != nil {
		t.Fatalf("list after revoke: %v", err)
	}
	for _, it := range items {
		switch it.Name {
		case "laptop":
			if it.RevokedAt == nil {
				t.Fatalf("revoked token has no revokedAt: %+v", it)
			}
		case "ci":
			if it.RevokedAt != nil {
				t.Fatalf("active token shows revokedAt: %+v", it)
			}
		}
	}

	bobItems, err := ss.ListMCPTokens(ctx, bob)
	if err != nil {
		t.Fatalf("bob list: %v", err)
	}
	if len(bobItems) != 0 {
		t.Fatalf("bob sees alice's tokens: %+v", bobItems)
	}
}

func TestMCPToken_DuplicateName(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")
	bob := mcptUserID(t, s, "bob")
	mcptCreate(t, ss, alice, "laptop")

	if _, _, err := ss.CreateMCPToken(ctx, alice, "laptop", 30); !errors.Is(err, ErrMCPTokenDuplicate) {
		t.Fatalf("same name: err = %v, want ErrMCPTokenDuplicate", err)
	}
	// Names are trimmed before the uniqueness check.
	if _, _, err := ss.CreateMCPToken(ctx, alice, "  laptop  ", 30); !errors.Is(err, ErrMCPTokenDuplicate) {
		t.Fatalf("padded name: err = %v, want ErrMCPTokenDuplicate", err)
	}
	// Another user may reuse the name.
	if _, _, err := ss.CreateMCPToken(ctx, bob, "laptop", 30); err != nil {
		t.Fatalf("bob same name: %v", err)
	}
}

func TestMCPToken_NameValidation(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")

	for _, name := range []string{"", "   ", strings.Repeat("a", 65)} {
		if _, _, err := ss.CreateMCPToken(ctx, alice, name, 30); !errors.Is(err, ErrMCPTokenName) {
			t.Fatalf("name %q: err = %v, want ErrMCPTokenName", name, err)
		}
	}
	// 64 runes is the longest allowed name.
	if _, _, err := ss.CreateMCPToken(ctx, alice, strings.Repeat("a", 64), 30); err != nil {
		t.Fatalf("64-character name: %v", err)
	}
}

func TestMCPToken_LifetimeValues(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")

	for _, days := range []int{7, 30, 90, 365} {
		tok, _, err := ss.CreateMCPToken(ctx, alice, fmt.Sprintf("life-%d", days), days)
		if err != nil {
			t.Fatalf("%d days: %v", days, err)
		}
		want := time.Now().Add(time.Duration(days) * 24 * time.Hour)
		if d := tok.ExpiresAt.Sub(want); d < -time.Minute || d > time.Minute {
			t.Fatalf("%d days: expiresAt off by %v", days, d)
		}
	}
	for _, days := range []int{0, 1, 14, 366, -7} {
		if _, _, err := ss.CreateMCPToken(ctx, alice, fmt.Sprintf("bad-%d", days), days); !errors.Is(err, ErrMCPTokenLifetime) {
			t.Fatalf("%d days: err = %v, want ErrMCPTokenLifetime", days, err)
		}
	}
}

func TestMCPToken_EleventhActiveTokenRefused(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")

	var first MCPToken
	for i := range maxActiveMCPTokens {
		tok, _ := mcptCreate(t, ss, alice, fmt.Sprintf("t%d", i))
		if i == 0 {
			first = tok
		}
	}
	if _, _, err := ss.CreateMCPToken(ctx, alice, "t10", 30); !errors.Is(err, ErrMCPTokenLimit) {
		t.Fatalf("11th active token: err = %v, want ErrMCPTokenLimit", err)
	}
	// Revoking one frees a slot.
	if err := ss.RevokeMCPToken(ctx, alice, first.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := ss.CreateMCPToken(ctx, alice, "t10", 30); err != nil {
		t.Fatalf("token after revoke: %v", err)
	}
}

func TestMCPToken_LookupRefusals(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")
	tok, raw := mcptCreate(t, ss, alice, "laptop")

	u, id, err := ss.lookupMCPToken(ctx, raw)
	if err != nil || id != tok.ID || u.Username != "alice" {
		t.Fatalf("valid lookup: user=%+v id=%d err=%v", u, id, err)
	}

	for _, bad := range []string{"", "gpmcp_short", "xxxxxx" + raw[len(MCPTokenPrefix):], raw + "A", MCPTokenPrefix + strings.Repeat("A", 43)} {
		if _, _, err := ss.lookupMCPToken(ctx, bad); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("lookup(%q): err = %v, want sql.ErrNoRows", bad, err)
		}
	}

	// Revoked.
	if err := ss.RevokeMCPToken(ctx, alice, tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := ss.lookupMCPToken(ctx, raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked token: err = %v, want sql.ErrNoRows", err)
	}

	// Expired: force the expiry into the past.
	_, expRaw := mcptCreate(t, ss, alice, "expiring")
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE mcp_tokens SET expires_at = ? WHERE digest = ?`,
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), sessionDigest(expRaw)); err != nil {
		t.Fatalf("expire row: %v", err)
	}
	if _, _, err := ss.lookupMCPToken(ctx, expRaw); !errors.Is(err, errSessionExpired) {
		t.Fatalf("expired token: err = %v, want errSessionExpired", err)
	}

	// Session epoch bumped (password reset or similar): the token must stop working.
	_, epochRaw := mcptCreate(t, ss, alice, "epoch")
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE users SET session_epoch = session_epoch + 1 WHERE id = ?`, alice); err != nil {
		t.Fatalf("bump epoch: %v", err)
	}
	if _, _, err := ss.lookupMCPToken(ctx, epochRaw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("epoch-bumped token: err = %v, want sql.ErrNoRows", err)
	}

	// Deleted owner.
	bob := mcptUserID(t, s, "bob")
	_, bobRaw := mcptCreate(t, ss, bob, "bob-laptop")
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, bob); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, _, err := ss.lookupMCPToken(ctx, bobRaw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted owner's token: err = %v, want sql.ErrNoRows", err)
	}
}

func TestMCPToken_AuthenticateBearer(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")
	tok, raw := mcptCreate(t, ss, alice, "laptop")

	var (
		called   bool
		gotUser  string
		gotTok   int64
		gotTokOK bool
	)
	h := ss.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		if u := UserFromContext(req.Context()); u != nil {
			gotUser = u.Username
		}
		gotTok, gotTokOK = MCPTokenFromContext(req.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/servers", nil)
	req.RemoteAddr = "198.51.100.1:4000"
	req.Header.Set("Authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || !called || gotUser != "alice" || !gotTokOK || gotTok != tok.ID {
		t.Fatalf("bearer: code=%d called=%v user=%q tokOK=%v tok=%d", rr.Code, called, gotUser, gotTokOK, gotTok)
	}

	// Bad credentials are refused without reaching the handler.
	if err := ss.RevokeMCPToken(ctx, alice, tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	for name, hdr := range map[string]string{
		"revoked":    "Bearer " + raw,
		"unknown":    "Bearer " + MCPTokenPrefix + strings.Repeat("A", 43),
		"not an mcp": "Bearer not-a-token",
	} {
		called, gotUser = false, ""
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/servers", nil)
		req.RemoteAddr = "198.51.100.2:4000"
		req.Header.Set("Authorization", hdr)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized || called {
			t.Fatalf("%s: code=%d called=%v, want 401 and no handler call", name, rr.Code, called)
		}
	}

	// A session cookie wins: the Authorization header is never read.
	bob := mcptUserID(t, s, "bob")
	_, bobRaw := mcptCreate(t, ss, bob, "bob-laptop")
	session, _, err := ss.Create(ctx, alice)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	called, gotUser, gotTokOK = false, "", false
	req = httptest.NewRequestWithContext(ctx, http.MethodGet, "/servers", nil)
	req.RemoteAddr = "198.51.100.3:4000"
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	req.Header.Set("Authorization", "Bearer "+bobRaw)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || gotUser != "alice" || gotTokOK {
		t.Fatalf("cookie with bearer: code=%d user=%q tokenAuthed=%v, want session user alice", rr.Code, gotUser, gotTokOK)
	}
}

// mcptAllowedExamples are concrete paths that each MCPTokenRoutes pattern matches.
var mcptAllowedExamples = []string{
	"/users/me",
	"/clusters",
	"/namespaces",
	"/servers",
	"/servers/alpha",
	"/servers/alpha/events",
	"/servers/alpha/pods",
	"/servers/alpha/logs/pod/tail",
	"/templates",
	"/templates/vanilla",
	"/backups",
	"/backups/b1",
	"/schedules",
	"/schedules/s1",
	"/restores",
	"/restores/r1",
	"/modules",
	"/modules/sources",
	"/modules/minecraft",
	"/users/me/servers",
	"/pods",
	"/pods/alpha-0",
	"/kube-events",
	"/admin/system-logs/api",
	"/admin/system-logs/operator",
}

// mcptGuardRequest builds a request that already carries a token ID, as Authenticate would leave it.
func mcptGuardRequest(t *testing.T, method, path string, tokenID int64) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	req.RemoteAddr = "198.51.100.9:4000"
	return req.WithContext(WithMCPToken(req.Context(), tokenID))
}

func TestMCPTokenGuard_AllowsReadRoutes(t *testing.T) {
	_, ss := mcptSetup(t)

	for _, pattern := range MCPTokenRoutes {
		re := regexp.MustCompile(pattern)
		covered := false
		for _, ex := range mcptAllowedExamples {
			if re.MatchString(ex) {
				covered = true
				break
			}
		}
		if !covered {
			t.Fatalf("no example path covers route pattern %s", pattern)
		}
	}

	for _, path := range mcptAllowedExamples {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			called := false
			h := ss.MCPTokenGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, mcptGuardRequest(t, method, path, 7001))
			if !called || rr.Code != http.StatusOK {
				t.Fatalf("%s %s: code=%d called=%v, want passthrough", method, path, rr.Code, called)
			}
		}
	}
}

func TestMCPTokenGuard_RefusesOtherRequests(t *testing.T) {
	_, ss := mcptSetup(t)
	cases := []struct {
		name    string
		method  string
		path    string
		upgrade string
	}{
		{name: "write method", method: http.MethodPost, path: "/servers/alpha"},
		{name: "put method", method: http.MethodPut, path: "/users/me"},
		{name: "websocket upgrade", method: http.MethodGet, path: "/servers/alpha/logs/pod/tail", upgrade: "websocket"},
		{name: "module catalog excluded", method: http.MethodGet, path: "/modules/catalog"},
		{name: "token management", method: http.MethodGet, path: "/users/me/mcp-tokens"},
		{name: "server files", method: http.MethodGet, path: "/servers/alpha/files"},
		{name: "console socket", method: http.MethodGet, path: "/ws/servers/alpha/console"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := ss.MCPTokenGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))
			req := mcptGuardRequest(t, tc.method, tc.path, 7002)
			if tc.upgrade != "" {
				req.Header.Set("Upgrade", tc.upgrade)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden || called {
				t.Fatalf("code=%d called=%v, want 403 without reaching the handler", rr.Code, called)
			}
		})
	}
}

type mcptAuditCall struct {
	method, path, target, reason string
	status                       int
}

func TestMCPTokenGuard_AuditEvents(t *testing.T) {
	_, ss := mcptSetup(t)
	var mu sync.Mutex
	var calls []mcptAuditCall
	ss.AttachMCPAudit(func(_ context.Context, method, path, target, reason string, status int) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, mcptAuditCall{method: method, path: path, target: target, reason: reason, status: status})
		return nil
	})
	snapshot := func() []mcptAuditCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]mcptAuditCall(nil), calls...)
	}
	countReason := func(reason string) int {
		n := 0
		for _, c := range snapshot() {
			if c.reason == reason {
				n++
			}
		}
		return n
	}
	// Token and session IDs are unique per run, because the session sighting map is process-wide.
	tokenID := time.Now().UnixNano()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

	// A refused request is audited with its method, path and 403.
	refused := ss.MCPTokenGuard(http.HandlerFunc(ok))
	rr := httptest.NewRecorder()
	refused.ServeHTTP(rr, mcptGuardRequest(t, http.MethodPost, "/servers/alpha", tokenID))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("refused POST: code=%d", rr.Code)
	}
	want := mcptAuditCall{method: http.MethodPost, path: "/servers/alpha", reason: "mcp-refused", status: http.StatusForbidden}
	if got := snapshot(); len(got) != 1 || got[0] != want {
		t.Fatalf("audit calls = %+v, want one %+v", got, want)
	}

	// A 401 or 403 produced by the handler is audited as refused.
	unauth := ss.MCPTokenGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	unauth.ServeHTTP(httptest.NewRecorder(), mcptGuardRequest(t, http.MethodGet, "/servers/alpha/events", tokenID))
	if n := countReason("mcp-refused"); n != 2 {
		t.Fatalf("mcp-refused calls = %d, want 2", n)
	}

	// Each new session ID is audited once, however many requests it sends.
	sessA := fmt.Sprintf("audit-a-%d", tokenID)
	sessB := fmt.Sprintf("audit-b-%d", tokenID)
	send := func(session string) {
		req := mcptGuardRequest(t, http.MethodGet, "/servers", tokenID)
		req.Header.Set(mcpSessionHeader, session)
		ss.MCPTokenGuard(http.HandlerFunc(ok)).ServeHTTP(httptest.NewRecorder(), req)
	}
	send(sessA)
	send(sessA)
	send(sessB)
	if n := countReason("mcp-session"); n != 2 {
		t.Fatalf("mcp-session calls = %d, want 2 (one per session ID)", n)
	}

	// Requests without a token are not audited by the guard.
	before := len(snapshot())
	plain := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/alpha", nil)
	ss.MCPTokenGuard(http.HandlerFunc(ok)).ServeHTTP(httptest.NewRecorder(), plain)
	if len(snapshot()) != before {
		t.Fatal("guard audited a request that carried no token")
	}
}
