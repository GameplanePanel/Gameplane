package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// A revoked token's name can be used again at once (names are unique among
// active tokens only).
func TestMCPToken_NameReusableAfterRevoke(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")
	tok, _ := mcptCreate(t, ss, alice, "laptop")
	if err := ss.RevokeMCPToken(ctx, alice, tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := ss.CreateMCPToken(ctx, alice, "laptop", 30); err != nil {
		t.Fatalf("create after revoke: %v", err)
	}
}

// An expired token's name can be used again at once.
func TestMCPToken_NameReusableAfterExpiry(t *testing.T) {
	s, ss := mcptSetup(t)
	ctx := context.Background()
	alice := mcptUserID(t, s, "alice")
	tok, _ := mcptCreate(t, ss, alice, "laptop")
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := s.DB.ExecContext(ctx, `UPDATE mcp_tokens SET expires_at = ? WHERE id = ?`, past, tok.ID); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if _, _, err := ss.CreateMCPToken(ctx, alice, "laptop", 30); err != nil {
		t.Fatalf("create after expiry: %v", err)
	}
}

// Streaming handlers behind the guard (system logs) need http.Flusher.
func TestMCPTokenGuard_ForwardsFlush(t *testing.T) {
	_, ss := mcptSetup(t)
	h := ss.MCPTokenGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("line\n"))
		f.Flush()
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, mcptGuardRequest(t, http.MethodGet, "/admin/system-logs/api", time.Now().UnixNano()))
	if rr.Code != http.StatusOK || !rr.Flushed {
		t.Fatalf("code=%d flushed=%v body=%q", rr.Code, rr.Flushed, rr.Body.String())
	}
}

// Session IDs outside the expected shape are not audited, and one token
// records at most maxMCPSessionsPerToken sessions per day.
func TestMCPTokenGuard_SessionIDsBounded(t *testing.T) {
	_, ss := mcptSetup(t)
	var mu sync.Mutex
	sessions := 0
	ss.AttachMCPAudit(func(_ context.Context, _, _, _, reason string, _ int) error {
		mu.Lock()
		defer mu.Unlock()
		if reason == "mcp-session" {
			sessions++
		}
		return nil
	})
	tokenID := time.Now().UnixNano()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	send := func(session string) {
		req := mcptGuardRequest(t, http.MethodGet, "/servers", tokenID)
		req.Header.Set(mcpSessionHeader, session)
		ss.MCPTokenGuard(ok).ServeHTTP(httptest.NewRecorder(), req)
	}

	send("short")
	send("has space in it")
	send(fmt.Sprintf("%065d", 1))
	mu.Lock()
	got := sessions
	mu.Unlock()
	if got != 0 {
		t.Fatalf("malformed session IDs audited %d times, want 0", got)
	}

	for i := range maxMCPSessionsPerToken + 5 {
		send(fmt.Sprintf("bounded-%d-%02d", tokenID, i))
	}
	mu.Lock()
	got = sessions
	mu.Unlock()
	if got != maxMCPSessionsPerToken {
		t.Fatalf("mcp-session audits = %d, want the cap %d", got, maxMCPSessionsPerToken)
	}
}
