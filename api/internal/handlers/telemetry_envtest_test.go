//go:build envtest

package handlers

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/telemetry"
)

// Telemetry notice and config-hook tests (spec 022, US1).
//
// Each test builds its own router over its own in-memory store, with
// rbac.Middleware in front, because the notice and the config hook change
// the telemetry_state singleton: sharing captureAuditStore would let tests
// disturb each other. Callers are injected with auth.WithUser through
// doConfigAsUser (config_envtest_test.go).

var telemetryBundledDest = telemetry.Destination{
	Kind: telemetry.KindBundled, URL: "http://receiver.test:8080/ingest", Host: "receiver.test:8080",
}

// newTelemetryRouter returns a router with the config and telemetry routes
// mounted for dest, and the fresh-install store behind it (consent_source
// "default", basic and extended on, notice not shown).
func newTelemetryRouter(t *testing.T, dest telemetry.Destination) (*chi.Mux, *db.Store) {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite",
		"file:"+uniqueResourceName("telemetry")+"?mode=memory&cache=shared&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	settings := TelemetrySettings{Dest: dest, Interval: time.Hour}
	r := chi.NewRouter()
	r.Use(rbac.Middleware(kube.NewRegistry("default")))
	MountConfigWithTelemetry(r, store, nil, false, "", nil, settings)
	MountTelemetry(r, store, settings)
	return r, store
}

// telemetryAdmin is an admin ("*") with the given user ID.
func telemetryAdmin(id int64) *auth.User {
	u := configAdminUser()
	u.ID = id
	return u
}

// telemetryReader holds config:read but not config:manage.
func telemetryReader() *auth.User {
	return &auth.User{
		ID:       200,
		Username: "telemetry-reader",
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {"config:read": {}}},
		},
	}
}

func telemetryState(t *testing.T, store *db.Store) db.TelemetryState {
	t.Helper()
	st, err := store.GetTelemetryState(context.Background())
	if err != nil {
		t.Fatalf("get telemetry state: %v", err)
	}
	return st
}

func telemetryConfigValue(t *testing.T, store *db.Store) string {
	t.Helper()
	v, ok, err := store.ConfigValue(context.Background(), "telemetry")
	if err != nil || !ok {
		t.Fatalf("read telemetry config: %q, %v, %v", v, ok, err)
	}
	return v
}

// getNotice calls GET /admin/telemetry/notice as u and decodes the object.
func getNotice(t *testing.T, r http.Handler, u *auth.User) map[string]any {
	t.Helper()
	rr := doConfigAsUser(t, r, u, http.MethodGet, "/admin/telemetry/notice", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET notice = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode notice %q: %v", rr.Body.String(), err)
	}
	return out
}

func postNotice(t *testing.T, r http.Handler, u *auth.User, action string) *httptest.ResponseRecorder {
	t.Helper()
	return doConfigAsUser(t, r, u, http.MethodPost, "/admin/telemetry/notice", map[string]string{"action": action})
}

func TestTelemetryNotice_PendingOnlyForManagersOnADefaultInstallWithADestination(t *testing.T) {
	t.Run("a manager on a default install sees the notice", func(t *testing.T) {
		r, _ := newTelemetryRouter(t, telemetryBundledDest)
		got := getNotice(t, r, telemetryAdmin(1))
		if got["pending"] != true {
			t.Fatalf("notice = %v, want pending", got)
		}
		dest, _ := got["destination"].(map[string]any)
		if dest["kind"] != "bundled" || dest["host"] != "receiver.test:8080" {
			t.Fatalf("destination = %v, want kind bundled and the URL host only", dest)
		}
		fields, _ := got["fields"].(map[string]any)
		basic, _ := fields["basic"].([]any)
		extended, _ := fields["extended"].([]any)
		if len(basic) != 3 || len(extended) != 8 {
			t.Fatalf("fields = %v, want 3 basic and 8 extended", fields)
		}
	})

	t.Run("a reader without config:manage gets pending false only", func(t *testing.T) {
		r, _ := newTelemetryRouter(t, telemetryBundledDest)
		got := getNotice(t, r, telemetryReader())
		if got["pending"] != false || len(got) != 1 {
			t.Fatalf("notice = %v, want exactly {pending:false}", got)
		}
	})

	t.Run("a user without config:read is refused by RBAC", func(t *testing.T) {
		r, _ := newTelemetryRouter(t, telemetryBundledDest)
		rr := doConfigAsUser(t, r, configOperatorUser(), http.MethodGet, "/admin/telemetry/notice", nil)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("operator GET notice = %d, want 403", rr.Code)
		}
	})

	for _, dest := range []telemetry.Destination{{Kind: telemetry.KindNone}, {Kind: telemetry.KindDisabled}} {
		t.Run("not pending with destination "+dest.Kind, func(t *testing.T) {
			r, _ := newTelemetryRouter(t, dest)
			if got := getNotice(t, r, telemetryAdmin(1)); got["pending"] != false || len(got) != 1 {
				t.Fatalf("notice = %v, want exactly {pending:false}", got)
			}
		})
	}

	t.Run("not pending once an admin made a choice", func(t *testing.T) {
		r, store := newTelemetryRouter(t, telemetryBundledDest)
		rr := doConfigAsUser(t, r, telemetryAdmin(1), http.MethodPut, "/admin/config/telemetry",
			map[string]bool{"sendMetrics": true, "extended": true})
		if rr.Code != http.StatusOK {
			t.Fatalf("PUT = %d; body=%s", rr.Code, rr.Body.String())
		}
		if got := getNotice(t, r, telemetryAdmin(2)); got["pending"] != false {
			t.Fatalf("notice = %v, want not pending after an explicit choice", got)
		}
		if st := telemetryState(t, store); st.ConsentSource != db.TelemetryConsentAdmin {
			t.Fatalf("consent source = %q, want admin", st.ConsentSource)
		}
	})
}

func TestTelemetryNotice_SeenSetsNoticeShownOnce(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)

	if st := telemetryState(t, store); st.NoticeShownAt != "" || st.NextDueAt != "" {
		t.Fatalf("fresh state = %+v, want no notice and no slot", st)
	}
	if rr := postNotice(t, r, admin, "seen"); rr.Code != http.StatusNoContent {
		t.Fatalf("seen = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	first := telemetryState(t, store)
	if first.NoticeShownAt == "" || first.NextDueAt == "" || first.InstallID == "" {
		t.Fatalf("state after seen = %+v, want the notice stamped, the schedule open and an install id", first)
	}
	if first.ConsentSource != db.TelemetryConsentDefault {
		t.Fatalf("consent source = %q, seen must leave it as default", first.ConsentSource)
	}
	if acked, err := store.HasNoticeAck(context.Background(), admin.ID); err != nil || acked {
		t.Fatalf("HasNoticeAck = %v, %v; seen must record no ack", acked, err)
	}

	if rr := postNotice(t, r, admin, "seen"); rr.Code != http.StatusNoContent {
		t.Fatalf("second seen = %d, want 204", rr.Code)
	}
	if again := telemetryState(t, store); again != first {
		t.Fatalf("a second seen changed the state: %+v then %+v", first, again)
	}
	if got := getNotice(t, r, admin); got["pending"] != true {
		t.Fatal("seen alone must not dismiss the notice")
	}
}

func TestTelemetryNotice_SeenIsANoOpWhenNotPending(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetry.Destination{Kind: telemetry.KindNone})
	if rr := postNotice(t, r, telemetryAdmin(1), "seen"); rr.Code != http.StatusNoContent {
		t.Fatalf("seen = %d, want 204", rr.Code)
	}
	if st := telemetryState(t, store); st.NoticeShownAt != "" {
		t.Fatalf("notice_shown_at = %q, want it untouched when nothing is pending", st.NoticeShownAt)
	}
}

func TestTelemetryNotice_KeepOnlyAcksTheCaller(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)
	before := telemetryConfigValue(t, store)

	if rr := postNotice(t, r, admin, "keep"); rr.Code != http.StatusNoContent {
		t.Fatalf("keep = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if got := telemetryConfigValue(t, store); got != before {
		t.Fatalf("config = %s, keep must leave consent unchanged (was %s)", got, before)
	}
	if st := telemetryState(t, store); st.ConsentSource != db.TelemetryConsentDefault {
		t.Fatalf("consent source = %q, keep must leave it as default", st.ConsentSource)
	}
	if acked, err := store.HasNoticeAck(context.Background(), admin.ID); err != nil || !acked {
		t.Fatalf("HasNoticeAck = %v, %v; want the caller's ack recorded", acked, err)
	}
	if got := getNotice(t, r, admin); got["pending"] != false {
		t.Fatalf("notice = %v, want it dismissed for the caller", got)
	}
	if got := getNotice(t, r, telemetryAdmin(2)); got["pending"] != true {
		t.Fatalf("notice = %v, want it still pending for another admin", got)
	}
}

func TestTelemetryNotice_ExtendedOffKeepsBasic(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)
	if rr := postNotice(t, r, admin, "seen"); rr.Code != http.StatusNoContent {
		t.Fatalf("seen = %d", rr.Code)
	}
	if telemetryState(t, store).InstallID == "" {
		t.Fatal("setup: seen should have created the install id")
	}

	if rr := postNotice(t, r, admin, "extended-off"); rr.Code != http.StatusNoContent {
		t.Fatalf("extended-off = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if got := telemetryConfigValue(t, store); got != `{"sendMetrics":true,"extended":false}` {
		t.Fatalf("config = %s, want basic on and extended off", got)
	}
	st := telemetryState(t, store)
	if st.ConsentSource != db.TelemetryConsentAdmin || st.InstallID != "" || st.NextDueAt == "" {
		t.Fatalf("state = %+v, want source admin, the ID deleted and basic reporting still scheduled", st)
	}
	if acked, err := store.HasNoticeAck(context.Background(), admin.ID); err != nil || !acked {
		t.Fatalf("HasNoticeAck = %v, %v; want an ack", acked, err)
	}
}

func TestTelemetryNotice_AllOffStopsEverything(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)
	if rr := postNotice(t, r, admin, "seen"); rr.Code != http.StatusNoContent {
		t.Fatalf("seen = %d", rr.Code)
	}

	if rr := postNotice(t, r, admin, "all-off"); rr.Code != http.StatusNoContent {
		t.Fatalf("all-off = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if got := telemetryConfigValue(t, store); got != `{"sendMetrics":false,"extended":false}` {
		t.Fatalf("config = %s, want both tiers off", got)
	}
	st := telemetryState(t, store)
	if st.ConsentSource != db.TelemetryConsentAdmin || st.InstallID != "" || st.NextDueAt != "" {
		t.Fatalf("state = %+v, want source admin, no ID and the schedule closed", st)
	}
	if acked, err := store.HasNoticeAck(context.Background(), admin.ID); err != nil || !acked {
		t.Fatalf("HasNoticeAck = %v, %v; want an ack", acked, err)
	}
}

func TestTelemetryNotice_ActionsConflictWhenNotPending(t *testing.T) {
	r, _ := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)
	if rr := postNotice(t, r, admin, "keep"); rr.Code != http.StatusNoContent {
		t.Fatalf("keep = %d", rr.Code)
	}
	for _, action := range []string{"keep", "extended-off", "all-off"} {
		if rr := postNotice(t, r, admin, action); rr.Code != http.StatusConflict {
			t.Errorf("%s after dismissal = %d, want 409", action, rr.Code)
		}
	}

	none, _ := newTelemetryRouter(t, telemetry.Destination{Kind: telemetry.KindNone})
	if rr := postNotice(t, none, admin, "all-off"); rr.Code != http.StatusConflict {
		t.Errorf("all-off with no destination = %d, want 409", rr.Code)
	}
}

func TestTelemetryNotice_BadRequestsAndForbidden(t *testing.T) {
	r, _ := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)

	if rr := postNotice(t, r, admin, "explode"); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown action = %d, want 400", rr.Code)
	}
	req := httptest.NewRequestWithContext(auth.WithUser(context.Background(), admin),
		http.MethodPost, "/admin/telemetry/notice", strings.NewReader("{nope"))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed body = %d, want 400", rr.Code)
	}
	if rr := postNotice(t, r, telemetryReader(), "keep"); rr.Code != http.StatusForbidden {
		t.Errorf("POST without config:manage = %d, want 403", rr.Code)
	}
}

func TestTelemetryConfig_BasicOffStoresExtendedOffAndDeletesID(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)
	put := func(sendMetrics, extended bool) *httptest.ResponseRecorder {
		return doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/telemetry",
			map[string]bool{"sendMetrics": sendMetrics, "extended": extended})
	}

	if rr := put(true, true); rr.Code != http.StatusOK {
		t.Fatalf("PUT on = %d; body=%s", rr.Code, rr.Body.String())
	}
	if telemetryState(t, store).InstallID == "" {
		t.Fatal("setup: turning extended on should create the install id")
	}

	rr := put(false, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT {sendMetrics:false, extended:true} = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Section string          `json:"section"`
		Value   json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Section != "telemetry" || string(resp.Value) != `{"sendMetrics":false,"extended":false}` {
		t.Fatalf("response = %s, want the normalised value with extended false", rr.Body.String())
	}
	if got := telemetryConfigValue(t, store); got != `{"sendMetrics":false,"extended":false}` {
		t.Fatalf("stored config = %s, want extended stored off", got)
	}
	st := telemetryState(t, store)
	if st.InstallID != "" || st.NextDueAt != "" || st.ConsentSource != db.TelemetryConsentAdmin {
		t.Fatalf("state = %+v, want the ID deleted, the schedule closed and source admin", st)
	}
}

func TestTelemetryConfig_SaveCreatesAndDeletesInstallIDAndSetsAdminSource(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)
	put := func(sendMetrics, extended bool) {
		t.Helper()
		rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/telemetry",
			map[string]bool{"sendMetrics": sendMetrics, "extended": extended})
		if rr.Code != http.StatusOK {
			t.Fatalf("PUT {%v,%v} = %d; body=%s", sendMetrics, extended, rr.Code, rr.Body.String())
		}
	}

	if telemetryState(t, store).ConsentSource != db.TelemetryConsentDefault {
		t.Fatal("setup: a fresh install starts with source default")
	}
	put(true, true)
	st := telemetryState(t, store)
	if st.ConsentSource != db.TelemetryConsentAdmin || st.InstallID == "" || st.NextDueAt == "" {
		t.Fatalf("after extended on: %+v, want source admin, an ID and an open schedule", st)
	}
	put(true, false)
	if st := telemetryState(t, store); st.InstallID != "" || st.NextDueAt == "" {
		t.Fatalf("after extended off: %+v, want the ID deleted and basic still scheduled", st)
	}
	put(true, true)
	if again := telemetryState(t, store); again.InstallID == "" || again.InstallID == st.InstallID {
		t.Fatalf("after extended on again: id %q (was %q), want a new one", again.InstallID, st.InstallID)
	}
}

func TestTelemetryConfig_OperatorDisabledIsRefusedWith409(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetry.Destination{Kind: telemetry.KindDisabled})
	before := telemetryConfigValue(t, store)

	rr := doConfigAsUser(t, r, telemetryAdmin(1), http.MethodPut, "/admin/config/telemetry",
		map[string]bool{"sendMetrics": false, "extended": false})
	if rr.Code != http.StatusConflict {
		t.Fatalf("PUT = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "telemetry is disabled by the operator") {
		t.Fatalf("body = %q, want the operator-disabled message", rr.Body.String())
	}
	if got := telemetryConfigValue(t, store); got != before {
		t.Fatalf("config = %s, a refused save must change nothing (was %s)", got, before)
	}
	if st := telemetryState(t, store); st.ConsentSource != db.TelemetryConsentDefault {
		t.Fatalf("consent source = %q, a refused save must leave it alone", st.ConsentSource)
	}
}

func TestTelemetryConfig_PutNeedsManagePermission(t *testing.T) {
	r, _ := newTelemetryRouter(t, telemetryBundledDest)
	rr := doConfigAsUser(t, r, telemetryReader(), http.MethodPut, "/admin/config/telemetry",
		map[string]bool{"sendMetrics": false, "extended": false})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("PUT without config:manage = %d, want 403", rr.Code)
	}
}

// ---- GET /admin/telemetry and POST /admin/telemetry/install-id (US2, US3) ----

// newTelemetryRouterFor is newTelemetryRouter with explicit settings, for
// tests that also need the reporter's Deps (version) or its destination URL.
func newTelemetryRouterFor(t *testing.T, settings TelemetrySettings) (*chi.Mux, *db.Store) {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite",
		"file:"+uniqueResourceName("telemetry")+"?mode=memory&cache=shared&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := chi.NewRouter()
	r.Use(rbac.Middleware(kube.NewRegistry("default")))
	MountConfigWithTelemetry(r, store, nil, false, "", nil, settings)
	MountTelemetry(r, store, settings)
	return r, store
}

// getTelemetry calls GET /admin/telemetry as u and decodes the object. The
// raw body is returned too, for checks on what must not appear in it.
func getTelemetry(t *testing.T, r http.Handler, u *auth.User) (map[string]any, string) {
	t.Helper()
	rr := doConfigAsUser(t, r, u, http.MethodGet, "/admin/telemetry", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/telemetry = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return out, rr.Body.String()
}

func putTelemetryConsent(t *testing.T, r http.Handler, sendMetrics, extended bool) {
	t.Helper()
	rr := doConfigAsUser(t, r, telemetryAdmin(1), http.MethodPut, "/admin/config/telemetry",
		map[string]bool{"sendMetrics": sendMetrics, "extended": extended})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT telemetry {%v,%v} = %d; body=%s", sendMetrics, extended, rr.Code, rr.Body.String())
	}
}

// previewExt returns the preview's ext object, or nil when there is none.
func previewExt(t *testing.T, got map[string]any) map[string]any {
	t.Helper()
	preview, ok := got["preview"].(map[string]any)
	if !ok {
		t.Fatalf("preview = %v, want an object", got["preview"])
	}
	ext, _ := preview["ext"].(map[string]any)
	return ext
}

func TestTelemetryGet_EveryDestinationKind(t *testing.T) {
	cases := []struct {
		name         string
		dest         telemetry.Destination
		host         any // the expected destination.host, nil for JSON null
		disabled     bool
		wantsPreview bool
	}{
		{"default", telemetry.Destination{Kind: telemetry.KindDefault, URL: "https://telemetry.example.org/ingest?k=secret", Host: "telemetry.example.org"}, "telemetry.example.org", false, true},
		{"custom", telemetry.Destination{Kind: telemetry.KindCustom, URL: "http://custom.test:9000/ingest", Host: "custom.test:9000"}, "custom.test:9000", false, true},
		{"bundled", telemetryBundledDest, "receiver.test:8080", false, true},
		{"disabled", telemetry.Destination{Kind: telemetry.KindDisabled}, nil, true, false},
		{"none", telemetry.Destination{Kind: telemetry.KindNone}, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTelemetryRouter(t, tc.dest)
			got, raw := getTelemetry(t, r, telemetryAdmin(1))

			dest, _ := got["destination"].(map[string]any)
			if dest["kind"] != tc.dest.Kind || dest["host"] != tc.host {
				t.Fatalf("destination = %v, want kind %q and host %v (never the path or query)", dest, tc.dest.Kind, tc.host)
			}
			if strings.Contains(raw, "secret") || strings.Contains(raw, "/ingest") {
				t.Fatalf("body leaks the destination URL: %s", raw)
			}
			if got["operatorDisabled"] != tc.disabled {
				t.Fatalf("operatorDisabled = %v, want %v", got["operatorDisabled"], tc.disabled)
			}
			// The stored choice is reported even when the operator's setting
			// overrides it.
			consent, _ := got["consent"].(map[string]any)
			if consent["basic"] != true || consent["extended"] != true || consent["source"] != "default" {
				t.Fatalf("consent = %v, want the fresh-install default", consent)
			}
			if tc.wantsPreview {
				preview, ok := got["preview"].(map[string]any)
				if !ok || preview["servers"] != float64(0) || preview["templates"] != float64(0) {
					t.Fatalf("preview = %v, want a basic report with zero counts", got["preview"])
				}
				// A fresh install has no ID until the notice is seen, so the
				// preview carries no ext part yet.
				if _, hasExt := preview["ext"]; hasExt || got["installId"] != nil {
					t.Fatalf("preview ext / installId = %v / %v, want none before an ID exists", preview["ext"], got["installId"])
				}
			} else if got["preview"] != nil {
				t.Fatalf("preview = %v, want null for kind %s", got["preview"], tc.name)
			}
			status, _ := got["status"].(map[string]any)
			if status["lastOutcome"] != "never" || status["lastAttemptAt"] != nil ||
				status["lastSuccessAt"] != nil || status["lastIdRotationAt"] != nil {
				t.Fatalf("status = %v, want never with every timestamp null", status)
			}
		})
	}
}

func TestTelemetryGet_InstallIDAndPreviewFollowTheTiers(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)

	putTelemetryConsent(t, r, true, true)
	got, _ := getTelemetry(t, r, admin)
	id := telemetryState(t, store).InstallID
	if id == "" || got["installId"] != id {
		t.Fatalf("installId = %v, want the stored id %q while extended is on", got["installId"], id)
	}
	ext := previewExt(t, got)
	if ext == nil || ext["installId"] != id || ext["key"] == "" || ext["sentAt"] == "" {
		t.Fatalf("preview ext = %v, want the id, the public key and a send time", ext)
	}

	putTelemetryConsent(t, r, true, false)
	got, _ = getTelemetry(t, r, admin)
	if got["installId"] != nil || previewExt(t, got) != nil {
		t.Fatalf("installId / ext = %v / %v, want none while extended is off (FR-012)", got["installId"], previewExt(t, got))
	}

	putTelemetryConsent(t, r, false, false)
	got, _ = getTelemetry(t, r, admin)
	if got["preview"] != nil {
		t.Fatalf("preview = %v, want null while basic is off", got["preview"])
	}
}

func TestTelemetryGet_PreviewHonoursTheFallbackMarker(t *testing.T) {
	dest := telemetry.Destination{Kind: telemetry.KindCustom, URL: "http://old.test/ingest", Host: "old.test"}
	r, store := newTelemetryRouter(t, dest)
	putTelemetryConsent(t, r, true, true)
	if err := store.SetExtUnsupported(context.Background(),
		time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339), dest.URL); err != nil {
		t.Fatalf("set marker: %v", err)
	}
	got, _ := getTelemetry(t, r, telemetryAdmin(1))
	if previewExt(t, got) != nil {
		t.Fatal("the preview must be basic only while the endpoint is marked as not understanding ext")
	}
	if consent, _ := got["consent"].(map[string]any); consent["extended"] != true || got["installId"] == nil {
		t.Fatalf("consent / installId = %v / %v, the stored choice and the ID are unaffected", got["consent"], got["installId"])
	}
}

func TestTelemetryGet_StatusReportsTheStoredDelivery(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	putTelemetryConsent(t, r, true, true)
	st := telemetryState(t, store)
	st.LastAttemptAt = "2026-10-06T09:12:44Z"
	st.LastSuccessAt = "2026-10-05T09:12:44Z"
	st.LastOutcome = "failed"
	st.LastIDRotationAt = "2026-10-04T08:00:00Z"
	if err := store.UpdateTelemetryState(context.Background(), st); err != nil {
		t.Fatalf("update state: %v", err)
	}

	got, _ := getTelemetry(t, r, telemetryAdmin(1))
	status, _ := got["status"].(map[string]any)
	want := map[string]any{
		"lastAttemptAt":    "2026-10-06T09:12:44Z",
		"lastSuccessAt":    "2026-10-05T09:12:44Z",
		"lastOutcome":      "failed",
		"lastIdRotationAt": "2026-10-04T08:00:00Z",
	}
	if !reflect.DeepEqual(status, want) {
		t.Fatalf("status = %v, want %v", status, want)
	}
}

func TestTelemetryGet_NeverExposesTheSigningSecret(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	putTelemetryConsent(t, r, true, true)
	secret, err := store.EnsureSigningSecret(context.Background())
	if err != nil || len(secret) == 0 {
		t.Fatalf("signing secret = %x, %v, want one created by the save", secret, err)
	}
	_, raw := getTelemetry(t, r, telemetryAdmin(1))
	for _, enc := range []string{
		base64.StdEncoding.EncodeToString(secret), base64.RawURLEncoding.EncodeToString(secret),
		base64.RawStdEncoding.EncodeToString(secret), hex.EncodeToString(secret), "signing",
	} {
		if strings.Contains(raw, enc) {
			t.Fatalf("GET /admin/telemetry leaks the signing secret (%q): %s", enc, raw)
		}
	}
}

func TestTelemetryGet_NeedsConfigRead(t *testing.T) {
	r, _ := newTelemetryRouter(t, telemetryBundledDest)
	if got, _ := getTelemetry(t, r, telemetryReader()); got["destination"] == nil {
		t.Fatalf("a config:read holder must get the status, got %v", got)
	}
	if rr := doConfigAsUser(t, r, configOperatorUser(), http.MethodGet, "/admin/telemetry", nil); rr.Code != http.StatusForbidden {
		t.Fatalf("operator GET = %d, want 403", rr.Code)
	}
}

func TestTelemetryInstallID_ResetReplacesTheIDAndKeepsTheRest(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	admin := telemetryAdmin(1)
	putTelemetryConsent(t, r, true, true)
	before := telemetryState(t, store)
	secretBefore, err := store.EnsureSigningSecret(context.Background())
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPost, "/admin/telemetry/install-id", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("reset = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || len(resp) != 1 {
		t.Fatalf("reset body = %q (%v), want exactly {installId}", rr.Body.String(), err)
	}
	after := telemetryState(t, store)
	if resp["installId"] == "" || resp["installId"] == before.InstallID || after.InstallID != resp["installId"] {
		t.Fatalf("ids: response %q stored %q before %q, want a new id stored and returned", resp["installId"], after.InstallID, before.InstallID)
	}
	if after.ConsentSource != before.ConsentSource || after.NextDueAt != before.NextDueAt || after.LastIDRotationAt != "" {
		t.Fatalf("state = %+v, want the source, the schedule and the rotation stamp unchanged (was %+v)", after, before)
	}
	if secretAfter, err := store.EnsureSigningSecret(context.Background()); err != nil || string(secretAfter) != string(secretBefore) {
		t.Fatalf("a reset must keep the signing secret: %v", err)
	}
	got, _ := getTelemetry(t, r, admin)
	if got["installId"] != resp["installId"] || previewExt(t, got)["installId"] != resp["installId"] {
		t.Fatalf("GET after reset = %v, want the preview to show the new id at once", got)
	}
}

func TestTelemetryInstallID_CreatesTheFirstIDOnAFreshInstall(t *testing.T) {
	r, store := newTelemetryRouter(t, telemetryBundledDest)
	// Fresh install: extended is on in the stored choice but no ID exists yet.
	if rr := doConfigAsUser(t, r, telemetryAdmin(1), http.MethodPost, "/admin/telemetry/install-id", nil); rr.Code != http.StatusOK {
		t.Fatalf("reset = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if st := telemetryState(t, store); st.InstallID == "" || st.ConsentSource != db.TelemetryConsentDefault {
		t.Fatalf("state = %+v, want an ID and the source still default", st)
	}
}

func TestTelemetryInstallID_RefusedWhenExtendedOffOrOperatorDisabled(t *testing.T) {
	t.Run("extended off", func(t *testing.T) {
		r, store := newTelemetryRouter(t, telemetryBundledDest)
		putTelemetryConsent(t, r, true, false)
		rr := doConfigAsUser(t, r, telemetryAdmin(1), http.MethodPost, "/admin/telemetry/install-id", nil)
		if rr.Code != http.StatusConflict {
			t.Fatalf("reset = %d, want 409; body=%s", rr.Code, rr.Body.String())
		}
		if telemetryState(t, store).InstallID != "" {
			t.Fatal("a refused reset must not create an ID")
		}
	})
	t.Run("basic off", func(t *testing.T) {
		r, _ := newTelemetryRouter(t, telemetryBundledDest)
		putTelemetryConsent(t, r, false, true)
		if rr := doConfigAsUser(t, r, telemetryAdmin(1), http.MethodPost, "/admin/telemetry/install-id", nil); rr.Code != http.StatusConflict {
			t.Fatalf("reset = %d, want 409", rr.Code)
		}
	})
	t.Run("operator disabled", func(t *testing.T) {
		r, _ := newTelemetryRouter(t, telemetry.Destination{Kind: telemetry.KindDisabled})
		rr := doConfigAsUser(t, r, telemetryAdmin(1), http.MethodPost, "/admin/telemetry/install-id", nil)
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "disabled by the operator") {
			t.Fatalf("reset = %d %q, want 409 with the operator message", rr.Code, rr.Body.String())
		}
	})
	t.Run("needs config:manage", func(t *testing.T) {
		r, _ := newTelemetryRouter(t, telemetryBundledDest)
		if rr := doConfigAsUser(t, r, telemetryReader(), http.MethodPost, "/admin/telemetry/install-id", nil); rr.Code != http.StatusForbidden {
			t.Fatalf("reset by a reader = %d, want 403", rr.Code)
		}
	})
}

// SC-006: the preview is the body the reporter POSTs, apart from the send time.
func TestTelemetryPreview_EqualsTheBodyTheReporterPosts(t *testing.T) {
	for _, extended := range []bool{true, false} {
		name := "basic only"
		if extended {
			name = "with the extended part"
		}
		t.Run(name, func(t *testing.T) {
			bodies := make(chan []byte, 4)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet {
					// The reporter asks for a proof-of-work challenge first (spec 022
					// T105); this provider offers none.
					http.NotFound(w, req)
					return
				}
				b, _ := io.ReadAll(req.Body)
				bodies <- b
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)
			dest := telemetry.Destination{Kind: telemetry.KindCustom, URL: srv.URL, Host: "receiver.test"}
			r, store := newTelemetryRouterFor(t, TelemetrySettings{
				Dest: dest, Interval: time.Hour, Deps: telemetry.Deps{Version: "v9.9.9"},
			})
			putTelemetryConsent(t, r, true, extended)

			got, _ := getTelemetry(t, r, telemetryAdmin(1))
			// The reporter runs two hours ahead, so the saved slot is due.
			rep := telemetry.New(telemetry.Config{
				Dest: dest, Interval: time.Hour,
				Deps: telemetry.Deps{Store: store, Version: "v9.9.9"},
				Now:  func() time.Time { return time.Now().Add(2 * time.Hour) },
				Rand: func(int64) int64 { return 0 },
			})
			if err := rep.Tick(context.Background()); err != nil {
				t.Fatalf("tick: %v", err)
			}
			var posted map[string]any
			select {
			case b := <-bodies:
				if err := json.Unmarshal(b, &posted); err != nil {
					t.Fatalf("decode posted body %q: %v", b, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the reporter posted nothing")
			}

			preview, _ := got["preview"].(map[string]any)
			for _, m := range []map[string]any{preview, posted} {
				if ext, ok := m["ext"].(map[string]any); ok {
					delete(ext, "sentAt")
				}
			}
			if !reflect.DeepEqual(preview, posted) {
				t.Fatalf("preview != posted body (ignoring ext.sentAt):\npreview %v\nposted  %v", preview, posted)
			}
			if _, hasExt := posted["ext"]; hasExt != extended || posted["version"] != "v9.9.9" {
				t.Fatalf("posted = %v, want ext present = %v and the configured version", posted, extended)
			}
		})
	}
}
