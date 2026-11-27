package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
	"github.com/GameplanePanel/gameplane/agent/internal/status"
)

// newTestRoutes builds the protected routes the way main does, with no
// game attached: RCON is disabled and no game log, mods or caps are declared.
func newTestRoutes(t *testing.T, filesOnly bool) http.Handler {
	t.Helper()
	transport := rcon.Disabled{}
	r := chi.NewRouter()
	protectedRoutes{
		dataRoot:       t.TempDir(),
		maxUploadBytes: 1 << 20,
		rconClient:     transport,
		commands:       rcon.NewCommands(transport),
		gameName:       "test-game",
		statusSource:   status.New(transport, nil),
		filesOnly:      filesOnly,
	}.mount(r)
	return r
}

func serve(t *testing.T, h http.Handler, method, target string) int {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), method, target, nil)
	h.ServeHTTP(rr, req)
	return rr.Code
}

func TestFilesOnlyRouterServesOnlyFiles(t *testing.T) {
	h := newTestRoutes(t, true)

	for _, target := range []string{"/console", "/players", "/mods", "/logs/tail", "/status", "/lifecycle", "/actions"} {
		if code := serve(t, h, http.MethodGet, target); code != http.StatusNotFound {
			t.Errorf("files-only GET %s code=%d, want 404", target, code)
		}
	}
	if code := serve(t, h, http.MethodGet, "/files/stat?path=/"); code == http.StatusNotFound {
		t.Errorf("files-only GET /files/stat code=404, want the file routes mounted")
	}
}

func TestFullRouterKeepsGameRoutes(t *testing.T) {
	h := newTestRoutes(t, false)

	if code := serve(t, h, http.MethodGet, "/status"); code == http.StatusNotFound {
		t.Errorf("full GET /status code=404, want the status route mounted")
	}
	if code := serve(t, h, http.MethodGet, "/files/stat?path=/"); code == http.StatusNotFound {
		t.Errorf("full GET /files/stat code=404, want the file routes mounted")
	}
}
