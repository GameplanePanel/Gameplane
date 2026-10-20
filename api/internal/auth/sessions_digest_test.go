package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// storedSessionToken returns the sessions.token value stored for userID's
// only session.
func storedSessionToken(t *testing.T, s *db.Store, userID int64) string {
	t.Helper()
	var stored string
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT token FROM sessions WHERE user_id = ?`, userID).Scan(&stored); err != nil {
		t.Fatalf("read stored session token: %v", err)
	}
	return stored
}

// countSessions runs a SELECT COUNT(*) query against the sessions table.
func countSessions(t *testing.T, s *db.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestSessionDigest_Deterministic(t *testing.T) {
	a := sessionDigest("a")
	if a != sessionDigest("a") {
		t.Fatal("sessionDigest is not deterministic")
	}
	if a == sessionDigest("b") {
		t.Fatal("sessionDigest(a) == sessionDigest(b)")
	}
	sum := sha256.Sum256([]byte("a"))
	if want := hex.EncodeToString(sum[:]); a != want {
		t.Fatalf("sessionDigest(a) = %q, want %q", a, want)
	}
	if len(a) != 64 {
		t.Fatalf("digest length = %d, want 64", len(a))
	}
}

func TestSessions_StoresDigestNotCookieValue(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "admin")
	store := NewSessionStore(s)
	tok, _, err := store.Create(context.Background(), 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stored := storedSessionToken(t, s, 1)
	if stored == tok {
		t.Fatal("sessions.token holds the raw cookie value")
	}
	if stored != sessionDigest(tok) {
		t.Fatalf("sessions.token = %q, want sessionDigest(cookie) = %q", stored, sessionDigest(tok))
	}
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE token = ?`, tok); n != 0 {
		t.Fatalf("%d rows match the raw cookie value, want 0", n)
	}
}

func TestSessions_StoredValueCannotBeUsedAsCookie(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "admin")
	store := NewSessionStore(s)
	ctx := context.Background()
	tok, csrf, err := store.Create(ctx, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stored := storedSessionToken(t, s, 1)

	called := false
	h := store.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	serve := func(method, cookie, csrfValue string) int {
		called = false
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, method, "/protected", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		if csrfValue != "" {
			req.Header.Set(csrfHeader, csrfValue)
		}
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	// The database copy of the session, presented as the cookie, is rejected
	// on reads and on writes that carry the session's real CSRF token.
	if code := serve(http.MethodGet, stored, ""); code != http.StatusUnauthorized || called {
		t.Fatalf("GET with the stored value as cookie: code=%d called=%v, want 401 and not called", code, called)
	}
	if code := serve(http.MethodPost, stored, csrf); code != http.StatusUnauthorized || called {
		t.Fatalf("POST with the stored value as cookie and the valid CSRF token: code=%d called=%v, want 401 and not called", code, called)
	}
	if _, _, err := store.lookup(ctx, stored); err == nil {
		t.Fatal("lookup(stored value) succeeded, want an error")
	}

	// The raw cookie value handed out by Create still authenticates.
	if code := serve(http.MethodGet, tok, ""); code != http.StatusNoContent || !called {
		t.Fatalf("GET with the issued cookie: code=%d called=%v, want 204 and called", code, called)
	}
	// The rejected attempts left the real session in place.
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE user_id = 1`); n != 1 {
		t.Fatalf("%d session rows after the rejected attempts, want 1", n)
	}
}

func TestSessions_LogoutDeletesRowByDigest(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "admin")
	store := NewSessionStore(s)
	ctx := context.Background()
	tokA, _, err := store.Create(ctx, 1)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	tokB, _, err := store.Create(ctx, 1)
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	logout := func(cookie string) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/logout", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		store.HandleLogout().ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("logout code=%d, want 204", rr.Code)
		}
	}

	logout(tokA)
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE token = ?`, sessionDigest(tokA)); n != 0 {
		t.Fatalf("logged-out session: %d rows left, want 0", n)
	}
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE token = ?`, sessionDigest(tokB)); n != 1 {
		t.Fatalf("other session: %d rows, want 1", n)
	}
	if _, _, err := store.lookup(ctx, tokB); err != nil {
		t.Fatalf("other session no longer authenticates: %v", err)
	}

	// Logging out with the stored value as the cookie deletes nothing.
	logout(storedSessionToken(t, s, 1))
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE user_id = 1`); n != 1 {
		t.Fatalf("logout with the stored value removed a row: %d rows left, want 1", n)
	}
	if _, _, err := store.lookup(ctx, tokB); err != nil {
		t.Fatalf("session B after a logout with the stored value: %v", err)
	}
}

func TestSessions_ExpiredDigestRowRemovedOnLookupAndGC(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "admin")
	store := NewSessionStore(s)
	ctx := context.Background()
	tokA, _, err := store.Create(ctx, 1)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	tokB, _, err := store.Create(ctx, 1)
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, tok := range []string{tokA, tokB} {
		res, err := s.DB.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token = ?`, past, sessionDigest(tok))
		if err != nil {
			t.Fatalf("expire: %v", err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			t.Fatalf("expire matched %d rows (err %v), want 1", n, err)
		}
	}

	// lookup removes the expired row it was asked about.
	if _, _, err := store.lookup(ctx, tokA); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("lookup of an expired session: err=%v, want expired", err)
	}
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE token = ?`, sessionDigest(tokA)); n != 0 {
		t.Fatalf("expired session A: %d rows left after lookup, want 0", n)
	}
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE token = ?`, sessionDigest(tokB)); n != 1 {
		t.Fatalf("expired session B before gc: %d rows, want 1", n)
	}

	// The interval GC removes the other one.
	store.gcOnce(ctx)
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Fatalf("%d session rows after gc, want 0", n)
	}
}

func TestSessions_DeleteForUserRevokesEveryDigestRow(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "admin")
	seedUser(t, s, "bob", "pw", "admin")
	store := NewSessionStore(s)
	ctx := context.Background()
	aliceA, _, err := store.Create(ctx, 1)
	if err != nil {
		t.Fatalf("create alice A: %v", err)
	}
	aliceB, _, err := store.Create(ctx, 1)
	if err != nil {
		t.Fatalf("create alice B: %v", err)
	}
	bob, _, err := store.Create(ctx, 2)
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	if err := store.DeleteForUser(ctx, 1); err != nil {
		t.Fatalf("DeleteForUser: %v", err)
	}
	for name, tok := range map[string]string{"alice A": aliceA, "alice B": aliceB} {
		if _, _, err := store.lookup(ctx, tok); err == nil {
			t.Errorf("%s still authenticates after DeleteForUser", name)
		}
	}
	if _, _, err := store.lookup(ctx, bob); err != nil {
		t.Fatalf("bob's session after revoking alice's: %v", err)
	}
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE user_id = 1`); n != 0 {
		t.Fatalf("alice: %d session rows left, want 0", n)
	}
	if n := countSessions(t, s, `SELECT COUNT(*) FROM sessions WHERE user_id = 2`); n != 1 {
		t.Fatalf("bob: %d session rows, want 1", n)
	}
}

func TestSessions_CSRFStillEnforcedWithDigestStorage(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "admin")
	store := NewSessionStore(s)
	ctx := context.Background()
	tok, csrf, err := store.Create(ctx, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stored := storedSessionToken(t, s, 1)

	// The CSRF token is still stored as issued.
	var storedCSRF string
	if err := s.DB.QueryRowContext(ctx, `SELECT csrf_token FROM sessions WHERE user_id = 1`).Scan(&storedCSRF); err != nil {
		t.Fatalf("read csrf_token: %v", err)
	}
	if storedCSRF != csrf {
		t.Fatalf("csrf_token = %q, want the issued token %q", storedCSRF, csrf)
	}

	h := store.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusForbidden},
		{"wrong header", "wrong", http.StatusForbidden},
		{"stored session value as header", stored, http.StatusForbidden},
		{"session cookie value as header", tok, http.StatusForbidden},
		{"issued csrf token", csrf, http.StatusNoContent},
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/protected", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		if tc.header != "" {
			req.Header.Set(csrfHeader, tc.header)
		}
		h.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Errorf("%s: code=%d, want %d", tc.name, rr.Code, tc.want)
		}
	}
}

func TestSessions_LoginCookieIsRawValue(t *testing.T) {
	s := newAuthDB(t)
	// LoginUserLimiter and LoginLimiter are package singletons keyed by
	// username and client IP; TestLogin_PerUserRateLimit drains "alice", so
	// this test logs in with its own username and address.
	seedUser(t, s, "digest-alice", "hunter2", "admin")
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (1, 'viewer', 'local', '*')`,
	); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	store := NewSessionStore(s)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/login",
		strings.NewReader(`{"username":"digest-alice","password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.71:1234"
	NewLocal(s).HandleLogin(store, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login code=%d body=%s", rr.Code, rr.Body)
	}
	res := rr.Result()
	defer func() { _ = res.Body.Close() }()
	var body loginResp
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	var cookie string
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("login set no session cookie")
	}

	stored := storedSessionToken(t, s, 1)
	if stored == cookie {
		t.Fatal("sessions.token holds the login cookie value")
	}
	if stored != sessionDigest(cookie) {
		t.Fatalf("sessions.token = %q, want sessionDigest(login cookie) = %q", stored, sessionDigest(cookie))
	}
	u, csrf, err := store.lookup(context.Background(), cookie)
	if err != nil || u.Username != "digest-alice" || csrf != body.CSRF {
		t.Fatalf("lookup(login cookie) = %+v, %q, %v; want digest-alice with the login CSRF token", u, csrf, err)
	}
}
