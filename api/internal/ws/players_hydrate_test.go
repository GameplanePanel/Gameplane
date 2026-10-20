package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// fakePlayerNames is a PlayerNameResolver that returns a fixed map after an
// optional delay. The delay ignores ctx, like the real singleflight does.
type fakePlayerNames struct {
	names map[string]string
	delay time.Duration
}

func (f fakePlayerNames) Resolve(_ context.Context, _ []string) map[string]string {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.names
}

const (
	testSteamA = "76561198000000001"
	testSteamB = "76561198000000002"
)

const structuredPlayers = `{"online":2,"max":10,"players":["` + testSteamA + `","` + testSteamB + `"],"asOf":"2026-10-09T02:00:00Z","capabilities":["kick"],"entries":[{"steamId":"` + testSteamA + `","faction":"Blue"},{"steamId":"` + testSteamB + `"}]}`

// runPlayers serves one GET /servers/{name}/players request through the
// hydrating handler, with the agent faked to answer agentBody at status.
func runPlayers(t *testing.T, names PlayerNameResolver, status int, agentBody string) *httptest.ResponseRecorder {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(agentBody))
	}))
	t.Cleanup(srv.Close)
	// agentRoute needs a live Kubernetes lookup for the server UID; the
	// hydration handler under test does not, so it is mounted directly.
	p := &proxy{transport: testDirectTransport(srv), playerNames: names}
	r := chi.NewRouter()
	r.Get("/servers/{name}/players", p.hydratedPlayers("/players"))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha/players", nil))
	return rr
}

func TestPlayersHydrate_ResolvedNamesAndOtherFieldsKept(t *testing.T) {
	rr := runPlayers(t, fakePlayerNames{names: map[string]string{testSteamA: "Alice"}}, http.StatusOK, structuredPlayers)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entries, ok := got["entries"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("entries = %#v", got["entries"])
	}
	first := entries[0].(map[string]any)
	if first["displayName"] != "Alice" || first["faction"] != "Blue" || first["steamId"] != testSteamA {
		t.Fatalf("first entry = %#v", first)
	}
	second := entries[1].(map[string]any)
	if _, has := second["displayName"]; has {
		t.Fatalf("unresolved entry got displayName: %#v", second)
	}
	if got["online"] != float64(2) || got["max"] != float64(10) || got["asOf"] != "2026-10-09T02:00:00Z" {
		t.Fatalf("scalar fields changed: %#v", got)
	}
	players, _ := got["players"].([]any)
	if len(players) != 2 || players[0] != testSteamA || players[1] != testSteamB {
		t.Fatalf("players changed: %#v", got["players"])
	}
	if caps, _ := got["capabilities"].([]any); len(caps) != 1 || caps[0] != "kick" {
		t.Fatalf("capabilities changed: %#v", got["capabilities"])
	}
}

func TestPlayersHydrate_NoResolverPassesThroughVerbatim(t *testing.T) {
	rr := runPlayers(t, nil, http.StatusOK, structuredPlayers)
	if rr.Code != http.StatusOK || rr.Body.String() != structuredPlayers {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestPlayersHydrate_ResolverReturnsNothingKeepsRawIDs(t *testing.T) {
	rr := runPlayers(t, fakePlayerNames{names: map[string]string{}}, http.StatusOK, structuredPlayers)
	if rr.Body.String() != structuredPlayers {
		t.Fatalf("body changed without names: %s", rr.Body)
	}
	if strings.Contains(rr.Body.String(), "displayName") {
		t.Fatalf("displayName present: %s", rr.Body)
	}
}

func TestPlayersHydrate_SlowResolverReturnsUnhydratedPromptly(t *testing.T) {
	prev := playerNameBudget
	playerNameBudget = 20 * time.Millisecond
	t.Cleanup(func() { playerNameBudget = prev })

	start := time.Now()
	rr := runPlayers(t, fakePlayerNames{names: map[string]string{testSteamA: "Alice"}, delay: 2 * time.Second}, http.StatusOK, structuredPlayers)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("response took %s, want the budget to cut the wait", elapsed)
	}
	if rr.Code != http.StatusOK || rr.Body.String() != structuredPlayers {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestPlayersHydrate_FlatPlayersPassThroughVerbatim(t *testing.T) {
	flat := `{"online":1,"max":10,"players":["` + testSteamA + `"],"asOf":"2026-10-09T02:00:00Z"}`
	rr := runPlayers(t, fakePlayerNames{names: map[string]string{testSteamA: "Alice"}}, http.StatusOK, flat)
	if rr.Body.String() != flat {
		t.Fatalf("flat payload changed: %s", rr.Body)
	}
}

func TestPlayersHydrate_UndecodableBodyPassesThrough(t *testing.T) {
	const broken = `{"entries":[{"steamId":`
	rr := runPlayers(t, fakePlayerNames{names: map[string]string{testSteamA: "Alice"}}, http.StatusOK, broken)
	if rr.Code != http.StatusOK || rr.Body.String() != broken {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestPlayersHydrate_NonOKPassesThrough(t *testing.T) {
	const body = `{"error":"agent busy","entries":[{"steamId":"` + testSteamA + `"}]}`
	rr := runPlayers(t, fakePlayerNames{names: map[string]string{testSteamA: "Alice"}}, http.StatusServiceUnavailable, body)
	if rr.Code != http.StatusServiceUnavailable || rr.Body.String() != body {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}
