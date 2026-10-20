package players

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
)

// With RCON disabled (game declares no console protocol), the players
// endpoints must degrade — a valid unknown-count snapshot, an empty ban
// list, and 501s for moderation — never a 502.
func TestPlayers_DisabledRcon(t *testing.T) {
	r := chi.NewRouter()
	Mount(r, rcon.Disabled{}, "busybox", nil)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	get := func(path string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		return srv.Client().Do(req)
	}

	resp, err := get("/players")
	if err != nil {
		t.Fatalf("GET /players: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/players status=%d, want 200", resp.StatusCode)
	}
	var snap Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.Online != -1 || snap.Players == nil {
		t.Fatalf("snapshot=%+v, want online=-1 with non-nil players", snap)
	}

	resp, err = get("/players/banned")
	if err != nil {
		t.Fatalf("GET /players/banned: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/players/banned status=%d, want 200", resp.StatusCode)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		srv.URL+"/players/kick", strings.NewReader(`{"name":"alice"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	kickResp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /players/kick: %v", err)
	}
	defer kickResp.Body.Close()
	// busybox's generic commander may already 501 before reaching RCON;
	// either way it must not be a 502.
	if kickResp.StatusCode == http.StatusBadGateway {
		t.Fatalf("/players/kick returned 502 for a game without RCON")
	}
}
