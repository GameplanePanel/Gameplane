package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// newTestStore opens an in-memory SQLite store and runs migrations.
// Each test gets its own store so PUT/GET cases don't bleed.
func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite", "file::memory:?_pragma=journal_mode(WAL)&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func newConfigServer(t *testing.T) (*httptest.Server, *db.Store) {
	t.Helper()
	store := newTestStore(t)
	r := chi.NewRouter()
	MountConfig(r, store, nil, false, "", nil)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, store
}

func doReq(t *testing.T, method, url string, body any) (int, []byte) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestConfig_GetFreshInstallDefaults(t *testing.T) {
	srv, _ := newConfigServer(t)
	status, body := doReq(t, "GET", srv.URL+"/admin/config", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	// Migrate seeds the telemetry default on a fresh install (spec 022 FR-003).
	if len(got) != 1 {
		t.Fatalf("want one key, got %d: %v", len(got), got)
	}
	raw, ok := got["telemetry"]
	if !ok {
		t.Fatalf("want telemetry key, got %v", got)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal telemetry: %v", err)
	}
	if sendMetrics, ok := cfg["sendMetrics"].(bool); !ok || !sendMetrics {
		t.Fatalf("want sendMetrics=true, got %v", cfg)
	}
	if extended, ok := cfg["extended"].(bool); !ok || !extended {
		t.Fatalf("want extended=true, got %v", cfg)
	}
}

// TestConfig_ResetRoleMapping_NoAuthRowYet_Returns200 is the F-076
// regression test: on a fresh install (no "auth" row has ever been
// written) DELETE /admin/config/auth/role-mappings/{role} must return 200
// (idempotent reset), not 500. The store here is freshly migrated per
// newTestStore/newConfigServer, so no prior PUT has created the row —
// resetRoleMapping's QueryRowContext hits sql.ErrNoRows on a store the
// string comparison `err.Error() != "sql: no rows"` (actual message: "sql:
// no rows in result set") never matched, which used to fall through to
// httperr.Write as a 500.
func TestConfig_ResetRoleMapping_NoAuthRowYet_Returns200(t *testing.T) {
	srv, _ := newConfigServer(t)

	status, body := doReq(t, "DELETE", srv.URL+"/admin/config/auth/role-mappings/admin", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	if got["section"] != "auth" {
		t.Fatalf("body = %s, want section=auth", body)
	}
}

func TestConfig_PutThenGet(t *testing.T) {
	srv, _ := newConfigServer(t)

	in := generalCfg{
		InstanceName:     "homelab-01",
		ExternalURL:      "https://gameplane.example.dev",
		DefaultNamespace: "gameplane-games",
	}
	status, body := doReq(t, "PUT", srv.URL+"/admin/config/general", in)
	if status != 200 {
		t.Fatalf("PUT status = %d, want 200; body=%s", status, body)
	}

	status, body = doReq(t, "GET", srv.URL+"/admin/config", nil)
	if status != 200 {
		t.Fatalf("GET status = %d", status)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw, ok := all["general"]
	if !ok {
		t.Fatalf("missing general key; got %v", all)
	}
	var got generalCfg
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal general: %v", err)
	}
	if got != in {
		t.Fatalf("got %+v want %+v", got, in)
	}
}

func TestConfig_PutUpsert(t *testing.T) {
	srv, _ := newConfigServer(t)
	first := telemetryCfg{SendMetrics: true}
	if s, b := doReq(t, "PUT", srv.URL+"/admin/config/telemetry", first); s != 200 {
		t.Fatalf("first PUT status %d body=%s", s, b)
	}
	second := telemetryCfg{SendMetrics: false}
	if s, b := doReq(t, "PUT", srv.URL+"/admin/config/telemetry", second); s != 200 {
		t.Fatalf("second PUT status %d body=%s", s, b)
	}
	_, body := doReq(t, "GET", srv.URL+"/admin/config", nil)
	var all map[string]telemetryCfg
	_ = json.Unmarshal(body, &all)
	if all["telemetry"].SendMetrics {
		t.Fatalf("expected upsert to flip SendMetrics to false; got %+v", all["telemetry"])
	}
}

func TestConfig_PutUnknownSection(t *testing.T) {
	srv, _ := newConfigServer(t)
	status, _ := doReq(t, "PUT", srv.URL+"/admin/config/wat", map[string]any{"x": 1})
	if status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
	// "updates" was removed as a writable section — the channel is the
	// chart's informational value on /cluster/info now.
	status, _ = doReq(t, "PUT", srv.URL+"/admin/config/updates", map[string]any{"channel": "stable"})
	if status != 400 {
		t.Fatalf("updates PUT status = %d, want 400 (section removed)", status)
	}
}

func TestConfig_PutBadJSON(t *testing.T) {
	srv, _ := newConfigServer(t)
	req, _ := http.NewRequestWithContext(t.Context(), "PUT", srv.URL+"/admin/config/general", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestConfig_PutValidationFailure(t *testing.T) {
	srv, _ := newConfigServer(t)
	cases := []struct {
		name    string
		section string
		body    any
	}{
		{"general missing defaultNamespace", "general", generalCfg{InstanceName: "x"}},
		{"general bad URL", "general", generalCfg{InstanceName: "x", DefaultNamespace: "gameplane-games", ExternalURL: "not a url"}},
		{"general bad namespace", "general", generalCfg{InstanceName: "x", DefaultNamespace: "Bad_Name"}},
		{"auth bad kind", "auth", authCfg{Providers: []authProvider{{Name: "x", Kind: "wat"}}}},
		{"auth duplicate names", "auth", authCfg{Providers: []authProvider{{Name: "x", Kind: "local"}, {Name: "x", Kind: "oidc"}}}},
		{"notif bad kind", "notifications", notifCfg{Sinks: []notifSink{{Name: "x", Kind: "telegram"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doReq(t, "PUT", srv.URL+"/admin/config/"+tc.section, tc.body)
			if status != 422 {
				t.Fatalf("status = %d, want 422; body=%s", status, body)
			}
		})
	}
}

func TestConfig_PutEmptyBody(t *testing.T) {
	srv, _ := newConfigServer(t)
	req, _ := http.NewRequestWithContext(t.Context(), "PUT", srv.URL+"/admin/config/general", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestConfig_GetSkipsUnknownSections(t *testing.T) {
	srv, store := newConfigServer(t)
	// Insert a row with a key the API doesn't expose anymore (e.g. a
	// removed section). GET should skip it rather than return it as
	// json.RawMessage.
	_, err := store.DB.ExecContext(t.Context(), `INSERT INTO config(key, value) VALUES('legacy', '{"foo":1}')`)
	if err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	_, body := doReq(t, "GET", srv.URL+"/admin/config", nil)
	var all map[string]json.RawMessage
	_ = json.Unmarshal(body, &all)
	if _, ok := all["legacy"]; ok {
		t.Fatalf("legacy section should be filtered out; got %v", all)
	}
}
