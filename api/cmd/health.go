package main

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// mountHealthRoutes separates process liveness from database availability.
// Only one readiness ping can wait for a connection at a time, particularly
// important for SQLite's single-connection pool. Concurrent probes fail closed
// immediately instead of queueing more database work. No background work or
// cached success can outlive the two-second request budget.
func mountHealthRoutes(r chi.Router, store *db.Store) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	pingSlot := make(chan struct{}, 1)
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		select {
		case pingSlot <- struct{}{}:
			defer func() { <-pingSlot }()
		default:
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := store.DB.PingContext(ctx); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
}
