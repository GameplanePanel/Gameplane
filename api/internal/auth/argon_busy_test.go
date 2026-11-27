package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLimiterSourceKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":               "203.0.113.7",
		"2001:db8:1:2:aaaa::1":      "2001:db8:1:2::/64",
		"2001:db8:1:2:bbbb:cccc::9": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":           "2001:db8:1:3::/64",
		"not-an-ip":                 "not-an-ip",
		"::ffff:203.0.113.7":        "::ffff:203.0.113.7",
	}
	for in, want := range cases {
		if got := LimiterSourceKey(in); got != want {
			t.Errorf("LimiterSourceKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAcquireArgon_FailsFastWhenQueueFull(t *testing.T) {
	if err := argonSem.Acquire(context.Background(), argonConcurrency); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer argonSem.Release(argonConcurrency)
	argonWaiters.Add(maxArgonWaiters)
	defer argonWaiters.Add(-maxArgonWaiters)

	if err := acquireArgon(context.Background()); !errors.Is(err, ErrPasswordBusy) {
		t.Fatalf("err = %v, want ErrPasswordBusy", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acquireArgon(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func fillArgon(t *testing.T) func() {
	t.Helper()
	if err := argonSem.Acquire(context.Background(), argonConcurrency); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	argonWaiters.Add(maxArgonWaiters)
	return func() {
		argonWaiters.Add(-maxArgonWaiters)
		argonSem.Release(argonConcurrency)
	}
}

func TestLogin_Busy_IdenticalForKnownAndUnknownUser(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "rightpw-123456", "admin")
	// Compute the dummy hash before the queue is saturated.
	dummyHash()
	release := fillArgon(t)
	defer release()

	post := func(user string) *httptest.ResponseRecorder {
		body := strings.NewReader(`{"username":"` + user + `","password":"whatever"}`)
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), "POST", "/login", body)
		req.Header.Set("Content-Type", "application/json")
		NewLocal(s).HandleLogin(NewSessionStore(s), nil).ServeHTTP(rr, req)
		return rr
	}
	known, unknown := post("alice"), post("ghost")
	for name, rr := range map[string]*httptest.ResponseRecorder{"known": known, "unknown": unknown} {
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: code=%d", name, rr.Code)
		}
		if got := rr.Header().Get("Retry-After"); got != "5" {
			t.Fatalf("%s: Retry-After=%q", name, got)
		}
	}
	if known.Body.String() != unknown.Body.String() {
		t.Fatalf("bodies differ: %q vs %q", known.Body.String(), unknown.Body.String())
	}
}

func TestVerifyDummy_FailsFastWhileQueueFullThenRecovers(t *testing.T) {
	SetFastHashParams(t)
	// Hold the slot and fill the waiter queue, as during a flood. The deny
	// path must fail fast (not block on the lazy dummy hash), and work again
	// once the slot frees.
	if err := argonSem.Acquire(context.Background(), argonConcurrency); err != nil {
		t.Fatal(err)
	}
	argonWaiters.Add(maxArgonWaiters)
	done := make(chan error, 1)
	go func() { done <- VerifyDummy(context.Background(), "x") }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrPasswordBusy) {
			t.Fatalf("err = %v, want ErrPasswordBusy", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("VerifyDummy blocked while the queue was full")
	}
	argonWaiters.Add(-maxArgonWaiters)
	argonSem.Release(argonConcurrency)
	if err := VerifyDummy(context.Background(), "x"); err != nil {
		t.Fatalf("VerifyDummy after release: %v", err)
	}
	if h := computeDummyHash(); !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("dummy hash = %q", h)
	}
}

func TestMiddleware_SharesBucketAcrossIPv6Prefix(t *testing.T) {
	tb := newTokenBucket(0.0001, 1)
	h := tb.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	send := func(remote string) int {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/x", nil)
		req.RemoteAddr = remote
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if c := send("[2001:db8:1:2:aaaa::1]:1000"); c != http.StatusOK {
		t.Fatalf("first=%d", c)
	}
	if c := send("[2001:db8:1:2:bbbb::9]:2000"); c != http.StatusTooManyRequests {
		t.Fatalf("second=%d, want 429", c)
	}
}
