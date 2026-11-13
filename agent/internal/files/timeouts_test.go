package files

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestFileRouteTimeouts(t *testing.T) {
	router := chi.NewRouter()
	Mount(router, t.TempDir())
	// Exercise the middleware registered on each real route, substituting only
	// the final filesystem operation to observe its context before any I/O.
	err := chi.Walk(router, func(method, route string, _ http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		t.Run(method+route, func(t *testing.T) {
			handler := chi.Chain(middlewares...).Handler(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				deadline, bounded := req.Context().Deadline()
				transfer := route == "/files/download" || route == "/files/upload" || route == "/files/archive"
				if bounded == transfer {
					t.Fatalf("bounded=%t, transfer=%t", bounded, transfer)
				}
				if bounded && (time.Until(deadline) <= 29*time.Second || time.Until(deadline) > 30*time.Second) {
					t.Fatalf("ordinary operation deadline=%s", time.Until(deadline))
				}
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), method, route, nil))
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
