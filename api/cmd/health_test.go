package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

func TestHealthRoutesDatabaseUnavailable(t *testing.T) {
	store, err := db.Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	r := chi.NewRouter()
	mountHealthRoutes(r, store)

	check := func(path string, want int, body string) {
		t.Helper()
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if rr.Code != want || rr.Body.String() != body {
			t.Fatalf("%s: status=%d body=%q, want %d %q", path, rr.Code, rr.Body.String(), want, body)
		}
	}
	check("/readyz", http.StatusOK, "ok")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	check("/readyz", http.StatusServiceUnavailable, "not ready\n")
	check("/healthz", http.StatusOK, "ok")
}

// Holding SQLite's only connection reproduces pool starvation without a fake
// driver. Readiness must time out, cap concurrent pings, and recover when the
// connection is released; liveness must never wait for that connection.
func TestHealthRoutesBoundPingAndRecover(t *testing.T) {
	store, err := db.Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	conn, err := store.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	r := chi.NewRouter()
	mountHealthRoutes(r, store)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	started := time.Now()
	go func() {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))
		done <- rr
	}()
	for store.DB.Stats().WaitCount == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("readiness did not attempt the database ping")
		case <-time.After(time.Millisecond):
		}
	}
	for _, path := range []string{"/readyz", "/healthz"} {
		rr := httptest.NewRecorder()
		before := time.Now()
		r.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
		want := http.StatusOK
		if path == "/readyz" {
			want = http.StatusServiceUnavailable
		}
		if rr.Code != want || time.Since(before) > 500*time.Millisecond {
			t.Fatalf("%s blocked behind the ping or returned %d", path, rr.Code)
		}
	}
	select {
	case rr := <-done:
		if rr.Code != http.StatusServiceUnavailable || time.Since(started) > 2500*time.Millisecond {
			t.Fatalf("ping did not fail within its two-second budget: status=%d elapsed=%s", rr.Code, time.Since(started))
		}
	case <-ctx.Done():
		t.Fatal("readiness exceeded its own deadline")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("readiness did not recover: %d %s", rr.Code, rr.Body.String())
	}
}
