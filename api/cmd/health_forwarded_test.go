package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

func TestReadinessRefusesProxiedRequests(t *testing.T) {
	store, err := db.Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	r := chi.NewRouter()
	mountHealthRoutes(r, store)

	for _, header := range []string{"X-Forwarded-For", "Forwarded"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
		req.Header.Set(header, "203.0.113.7")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404", header, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("direct probe: status=%d, want 200", rr.Code)
	}
}
