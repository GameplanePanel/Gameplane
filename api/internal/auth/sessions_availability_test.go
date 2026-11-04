package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthenticateStoreFailureDoesNotInvalidateSession(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "admin")
	store := NewSessionStore(s)
	token, _, err := store.Create(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	h := store.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func(cookie string, want int) {
		t.Helper()
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/protected", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		}
		h.ServeHTTP(rr, req)
		if rr.Code != want {
			t.Fatalf("status=%d body=%q, want %d", rr.Code, rr.Body.String(), want)
		}
		if len(rr.Result().Cookies()) != 0 {
			t.Fatal("authentication failure changed the client's session cookies")
		}
		if want == http.StatusServiceUnavailable && rr.Body.String() != "Service Unavailable\n" {
			t.Fatalf("store error was not generic: %q", rr.Body.String())
		}
	}

	request(token, http.StatusNoContent)
	// A permission-query failure after a successful session lookup must deny
	// access without being mistaken for an expired/unknown session.
	if _, err := s.DB.ExecContext(t.Context(), "ALTER TABLE role_permissions RENAME TO unavailable_permissions"); err != nil {
		t.Fatal(err)
	}
	request(token, http.StatusServiceUnavailable)
	request("", http.StatusUnauthorized)
	request("unknown-token", http.StatusUnauthorized)
	if _, err := s.DB.ExecContext(t.Context(), "ALTER TABLE unavailable_permissions RENAME TO role_permissions"); err != nil {
		t.Fatal(err)
	}
	request(token, http.StatusNoContent)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	request(token, http.StatusServiceUnavailable)
	request("unknown-token", http.StatusServiceUnavailable)
	request("", http.StatusUnauthorized)
}

func TestAuthenticateInvalidSessionRemainsUnauthorized(t *testing.T) {
	for _, expires := range []string{"invalid-timestamp", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)} {
		t.Run(expires, func(t *testing.T) {
			s := newAuthDB(t)
			seedUser(t, s, "alice", "pw", "admin")
			store := NewSessionStore(s)
			token, _, err := store.Create(t.Context(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.ExecContext(t.Context(), "UPDATE sessions SET expires_at = ?", expires); err != nil {
				t.Fatal(err)
			}
			h := store.Authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("invalid session reached protected handler")
			}))
			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/protected", nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized || strings.TrimSpace(rr.Body.String()) != "unauthenticated" {
				t.Fatalf("invalid session returned %d %q", rr.Code, rr.Body.String())
			}
		})
	}
}
