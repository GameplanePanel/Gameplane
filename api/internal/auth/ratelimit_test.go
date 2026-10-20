package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenBucket_BurstThenDeny(t *testing.T) {
	b := newTokenBucket(0, 3) // no refill; pure burst
	for i := 0; i < 3; i++ {
		if !b.Allow("k") {
			t.Fatalf("call %d denied", i)
		}
	}
	if b.Allow("k") {
		t.Fatal("expected deny after burst exhausted")
	}
}

func TestTokenBucket_PerKey(t *testing.T) {
	b := newTokenBucket(0, 1)
	if !b.Allow("a") {
		t.Fatal("a denied")
	}
	if !b.Allow("b") {
		t.Fatal("b denied (should have own bucket)")
	}
	if b.Allow("a") {
		t.Fatal("a not denied")
	}
}

func TestTokenBucket_RefillCaps(t *testing.T) {
	b := newTokenBucket(1000, 2) // huge refill
	_ = b.Allow("k")
	_ = b.Allow("k") // exhaust
	time.Sleep(20 * time.Millisecond)
	if !b.Allow("k") {
		t.Fatal("expected refill to allow")
	}
	// Even with 1000 tokens/s we should still cap at burst=2.
	time.Sleep(20 * time.Millisecond)
	if !b.Allow("k") {
		t.Fatal("denied (cap)")
	}
	if !b.Allow("k") {
		t.Fatal("denied (cap2)")
	}
	if b.Allow("k") {
		t.Fatal("burst cap exceeded")
	}
}

func TestTokenBucket_Middleware_AllowsUnderLimit(t *testing.T) {
	b := newTokenBucket(10, 5)
	called := 0
	h := b.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(204)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || called != 1 {
		t.Fatalf("code=%d called=%d", rr.Code, called)
	}
}

func TestTokenBucket_UserMiddlewareRequiresAuthentication(t *testing.T) {
	b := newTokenBucket(0, 1)
	called := 0
	h := b.UserMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/fleet/servers", nil))
	if rr.Code != http.StatusUnauthorized || called != 0 {
		t.Fatalf("unauthenticated status=%d calls=%d", rr.Code, called)
	}
	ctx := WithUser(t.Context(), &User{ID: 42})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/fleet/servers", nil))
	if rr.Code != http.StatusNoContent || called != 1 {
		t.Fatalf("authenticated status=%d calls=%d", rr.Code, called)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/fleet/backups", nil))
	if rr.Code != http.StatusTooManyRequests || called != 1 {
		t.Fatalf("exhausted budget status=%d calls=%d", rr.Code, called)
	}
}

func TestTokenBucket_Middleware_DeniesAtZero(t *testing.T) {
	b := newTokenBucket(0, 0)
	h := b.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("code=%d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestTokenBucket_Middleware_HandlesBareHost(t *testing.T) {
	// Some upstreams set RemoteAddr without :port; SplitHostPort errors,
	// the middleware must still derive a key.
	b := newTokenBucket(10, 5)
	h := b.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", nil)
	req.RemoteAddr = "10.0.0.1"
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestAllowUser_AliasesAllow(t *testing.T) {
	b := newTokenBucket(0, 1)
	if !b.AllowUser("user") {
		t.Fatal("first call denied")
	}
	if b.AllowUser("user") {
		t.Fatal("second call should deny")
	}
}

func TestTokenBucket_GC(t *testing.T) {
	b := newTokenBucket(1, 1)
	// Manually backdate lastGC and an old bucket so the GC branch fires.
	b.Allow("stale")
	b.mu.Lock()
	b.lastGC = time.Now().Add(-10 * time.Minute)
	b.buckets["stale"].lastUpdate = time.Now().Add(-10 * time.Minute)
	b.mu.Unlock()
	_ = b.Allow("fresh") // triggers GC sweep
	b.mu.Lock()
	_, exists := b.buckets["stale"]
	b.mu.Unlock()
	if exists {
		t.Fatal("stale bucket should have been GC'd")
	}
}

func TestTokenBucketMiddleware_429IsGenericProblem(t *testing.T) {
	b := newTokenBucket(0, 0)
	h := b.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("Accept", "application/problem+json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("code=%d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type=%q", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if doc["messageId"] != "server.generic.429" {
		t.Fatalf("messageId=%v, want server.generic.429", doc["messageId"])
	}
}
