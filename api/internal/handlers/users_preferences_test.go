package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// newPreferencesServer wires MountUsers behind a middleware that injects the
// auth.User named by the X-Test-User-ID header, so one server can serve
// requests for several users (cross-user isolation, FR-008). Mirrors what
// sessions.Authenticate does in production, without the cookie round-trip.
func newPreferencesServer(t *testing.T, store *db.Store) *httptest.Server {
	t.Helper()
	sessions := auth.NewSessionStore(store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if id, err := strconv.ParseInt(req.Header.Get("X-Test-User-ID"), 10, 64); err == nil {
				req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: id, Role: "viewer"}))
			}
			next.ServeHTTP(w, req)
		})
	})
	MountUsers(r, store, sessions, testClusterLister{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func doPrefsReq(t *testing.T, srv *httptest.Server, userID int64, method, path string, body any) (int, []byte) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != 0 {
		req.Header.Set("X-Test-User-ID", strconv.FormatInt(userID, 10))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func decodePrefs(t *testing.T, body []byte) userPreferencesDTO {
	t.Helper()
	var got userPreferencesDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode preferences: %v; body=%s", err, body)
	}
	return got
}

// seedLegacyPrefs simulates a pre-migration account: migration 011 backfilled
// every existing user with the legacy preset.
func seedLegacyPrefs(t *testing.T, store *db.Store, userID int64) {
	t.Helper()
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO user_preferences (user_id, theme_type, preset_id, appearance_mode)
		 VALUES (?, 'preset', 'legacy', 'system')`, userID); err != nil {
		t.Fatalf("seed legacy prefs: %v", err)
	}
}

func TestUserPreferences_GetDefaultsPinkWithoutRow(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "fresh-user", "viewer", "")

	status, body := doPrefsReq(t, srv, userID, "GET", "/users/me/preferences", nil)
	if status != 200 {
		t.Fatalf("want 200 got %d body=%s", status, body)
	}
	got := decodePrefs(t, body)
	if got.ThemeType != "preset" || got.PresetID != "pink" || got.AppearanceMode != "system" {
		t.Errorf("defaults = %+v, want preset/pink/system", got)
	}
	if got.CustomCSSEnabled {
		t.Error("overlay must default to off")
	}
	if got.CustomColors != nil || got.CustomCSS != nil {
		t.Errorf("customs must be null, got %+v", got)
	}
	if got.UpdatedAt == "" {
		t.Error("updatedAt must be populated")
	}
}

func TestUserPreferences_GetReturnsMigratedLegacy(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "migrated-user", "viewer", "")
	seedLegacyPrefs(t, store, userID)

	status, body := doPrefsReq(t, srv, userID, "GET", "/users/me/preferences", nil)
	if status != 200 {
		t.Fatalf("want 200 got %d body=%s", status, body)
	}
	if got := decodePrefs(t, body); got.PresetID != "legacy" || got.ThemeType != "preset" {
		t.Errorf("migrated prefs = %+v, want preset/legacy", got)
	}
}

func TestUserPreferences_PutRoundTrip(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "styler", "viewer", "")

	status, body := doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", map[string]any{
		"themeType":        "custom_colors",
		"presetId":         "pink",
		"appearanceMode":   "dark",
		"customColors":     map[string]any{"accent": "#10B981", "surface": "#121114"},
		"customCssEnabled": true,
		"customCss":        ".dashboard-card { border-radius: 12px; }",
	})
	if status != 200 {
		t.Fatalf("want 200 got %d body=%s", status, body)
	}
	got := decodePrefs(t, body)
	if got.ThemeType != "custom_colors" || got.PresetID != "pink" || got.AppearanceMode != "dark" {
		t.Errorf("base fields = %+v", got)
	}
	if got.CustomColors == nil || got.CustomColors.Accent != "#10B981" || got.CustomColors.Surface != "#121114" {
		t.Errorf("customColors = %+v", got.CustomColors)
	}
	if !got.CustomCSSEnabled {
		t.Error("overlay must be on")
	}
	if got.CustomCSS == nil || *got.CustomCSS != ".dashboard-card { border-radius: 12px; }" {
		t.Errorf("customCss = %v", got.CustomCSS)
	}
	if got.UpdatedAt == "" {
		t.Error("updatedAt must be populated")
	}

	// The values must be persisted, not just echoed.
	status, body = doPrefsReq(t, srv, userID, "GET", "/users/me/preferences", nil)
	if status != 200 {
		t.Fatalf("get want 200 got %d", status)
	}
	if again := decodePrefs(t, body); again.ThemeType != "custom_colors" || again.CustomColors == nil ||
		again.CustomColors.Accent != "#10B981" || again.CustomCSS == nil {
		t.Errorf("GET after PUT = %+v, want stored values", again)
	}
}

// FR-012: a PUT that switches the base preset and toggles the overlay off,
// omitting the optional fields, must keep stored customColors/customCss.
func TestUserPreferences_PutRetainsCustomsWhenOmitted(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "retainer", "viewer", "")

	css := ".topbar { border-bottom: 3px solid lime; }"
	status, body := doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", map[string]any{
		"themeType":        "custom_colors",
		"presetId":         "pink",
		"appearanceMode":   "system",
		"customColors":     map[string]any{"accent": "#3B82F6", "surface": "#18181B"},
		"customCssEnabled": true,
		"customCss":        css,
	})
	if status != 200 {
		t.Fatalf("seed put want 200 got %d body=%s", status, body)
	}

	// Ordinary update: switch base to legacy, overlay off, omit customs.
	status, body = doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", map[string]any{
		"themeType":        "preset",
		"presetId":         "legacy",
		"appearanceMode":   "dark",
		"customCssEnabled": false,
	})
	if status != 200 {
		t.Fatalf("update put want 200 got %d body=%s", status, body)
	}
	got := decodePrefs(t, body)
	if got.PresetID != "legacy" || got.ThemeType != "preset" || got.AppearanceMode != "dark" {
		t.Errorf("base fields = %+v, want preset/legacy/dark", got)
	}
	if got.CustomCSSEnabled {
		t.Error("overlay must be off")
	}
	if got.CustomColors == nil || got.CustomColors.Accent != "#3B82F6" || got.CustomColors.Surface != "#18181B" {
		t.Errorf("customColors = %+v, want retained", got.CustomColors)
	}
	if got.CustomCSS == nil || *got.CustomCSS != css {
		t.Errorf("customCss = %v, want retained %q", got.CustomCSS, css)
	}
}

func TestUserPreferences_PutReplacesProvidedCustoms(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "replacer", "viewer", "")

	put := func(colors map[string]any, css string) {
		t.Helper()
		body := map[string]any{
			"themeType": "custom_colors", "presetId": "pink", "appearanceMode": "system",
			"customCssEnabled": true,
		}
		if colors != nil {
			body["customColors"] = colors
		}
		if css != "" {
			body["customCss"] = css
		}
		status, resp := doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", body)
		if status != 200 {
			t.Fatalf("put want 200 got %d body=%s", status, resp)
		}
	}

	put(map[string]any{"accent": "#111111", "surface": "#222222"}, ".a { color: red; }")
	put(map[string]any{"accent": "#333333", "surface": "#444444"}, ".a { color: blue; }")

	_, body := doPrefsReq(t, srv, userID, "GET", "/users/me/preferences", nil)
	got := decodePrefs(t, body)
	if got.CustomColors == nil || got.CustomColors.Accent != "#333333" || got.CustomColors.Surface != "#444444" {
		t.Errorf("customColors = %+v, want replaced values", got.CustomColors)
	}
	if got.CustomCSS == nil || *got.CustomCSS != ".a { color: blue; }" {
		t.Errorf("customCss = %v, want replaced value", got.CustomCSS)
	}
}

func TestUserPreferences_PutValidationMatrix(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "validator", "viewer", "")

	validBase := map[string]any{
		"themeType":        "preset",
		"presetId":         "pink",
		"appearanceMode":   "system",
		"customCssEnabled": false,
	}
	cases := []struct {
		name    string
		patch   map[string]any // merged onto validBase
		drop    []string       // keys removed from validBase
		wantSub string
	}{
		{"missing themeType", nil, []string{"themeType"}, "themeType"},
		{"invalid themeType", map[string]any{"themeType": "gradient"}, nil, "themeType"},
		{"missing presetId", nil, []string{"presetId"}, "presetId"},
		{"invalid presetId", map[string]any{"presetId": "neon"}, nil, "presetId"},
		{"missing appearanceMode", nil, []string{"appearanceMode"}, "appearanceMode"},
		{"invalid appearanceMode", map[string]any{"appearanceMode": "auto"}, nil, "appearanceMode"},
		{"missing customCssEnabled", nil, []string{"customCssEnabled"}, "customCssEnabled"},
		{"bad hex accent", map[string]any{"customColors": map[string]any{"accent": "#XYZ", "surface": "#18181B"}}, nil, "customColors.accent"},
		{"short hex accent", map[string]any{"customColors": map[string]any{"accent": "#12345", "surface": "#18181B"}}, nil, "customColors.accent"},
		{"hex without hash", map[string]any{"customColors": map[string]any{"accent": "3B82F6", "surface": "#18181B"}}, nil, "customColors.accent"},
		{"bad hex surface", map[string]any{"customColors": map[string]any{"accent": "#3B82F6", "surface": "#18181"}}, nil, "customColors.surface"},
		{"css import", map[string]any{"customCss": `@import url("https://evil.example.com/track.css");`}, nil, "@import"},
		{"css import bare", map[string]any{"customCss": `@import "https://evil.example.com/x.css";`}, nil, "@import"},
		{"css external url", map[string]any{"customCss": `.a { background: url("https://fonts.example.com/x.woff2"); }`}, nil, "https://fonts.example.com/x.woff2"},
		{"css protocol relative url", map[string]any{"customCss": `.a { background: url(//cdn.example.com/x.png); }`}, nil, "//cdn.example.com/x.png"},
		{"css uppercase external url", map[string]any{"customCss": `.a { background: URL(HTTP://evil.example.com/x.png); }`}, nil, "HTTP://evil.example.com/x.png"},
		{"css oversized", map[string]any{"customCss": strings.Repeat(".a{b:c}", 6000)}, nil, "32768"},
		{"css unbalanced braces", map[string]any{"customCss": `.a { color: red;`}, nil, "brace"},
		{"css script delimiter", map[string]any{"customCss": `<script>alert(1)</script>`}, nil, "<script"},
		{"css style delimiter uppercase", map[string]any{"customCss": `<STYLE>.a{}</STYLE>`}, nil, "<style"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{}
			for k, v := range validBase {
				body[k] = v
			}
			for k, v := range tc.patch {
				body[k] = v
			}
			for _, k := range tc.drop {
				delete(body, k)
			}
			status, resp := doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", body)
			if status != 400 {
				t.Fatalf("want 400 got %d body=%s", status, resp)
			}
			if !strings.Contains(string(resp), tc.wantSub) {
				t.Errorf("body %q missing %q", resp, tc.wantSub)
			}
		})
	}
}

// The two url() cases must name the offending rule, not just fail.
func TestUserPreferences_PutCssRejectionNamesRule(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "rule-namer", "viewer", "")

	status, body := doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", map[string]any{
		"themeType": "preset", "presetId": "pink", "appearanceMode": "system",
		"customCssEnabled": true,
		"customCss":        `.a { background: url("https://fonts.example.com/x.css"); }`,
	})
	if status != 400 {
		t.Fatalf("want 400 got %d", status)
	}
	msg := string(body)
	for _, sub := range []string{"customCss rejected", "url()", "https://fonts.example.com/x.css", "not allowed"} {
		if !strings.Contains(msg, sub) {
			t.Errorf("message %q missing %q", msg, sub)
		}
	}
}

func TestUserPreferences_PutAllowsDataAndRelativeURLs(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "url-allow", "viewer", "")

	for name, css := range map[string]string{
		"data uri":     `.a { background: url(data:image/png;base64,iVBORw0KGgo=); }`,
		"relative url": `.a { background: url(/static/tile.png); }`,
		"bare url":     `.a { background: url(tile.png); }`,
	} {
		t.Run(name, func(t *testing.T) {
			status, body := doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", map[string]any{
				"themeType": "preset", "presetId": "pink", "appearanceMode": "system",
				"customCssEnabled": true,
				"customCss":        css,
			})
			if status != 200 {
				t.Fatalf("want 200 got %d body=%s", status, body)
			}
		})
	}
}

func TestUserPreferences_ResetClearsCustoms(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "resetter", "viewer", "")

	status, body := doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", map[string]any{
		"themeType": "custom_colors", "presetId": "pink", "appearanceMode": "dark",
		"customColors":     map[string]any{"accent": "#10B981", "surface": "#121114"},
		"customCssEnabled": true,
		"customCss":        "body { opacity: 0.1 !important; }",
	})
	if status != 200 {
		t.Fatalf("seed put want 200 got %d body=%s", status, body)
	}

	// Reset to an explicit preset/appearance.
	status, body = doPrefsReq(t, srv, userID, "POST", "/users/me/preferences/reset",
		map[string]any{"presetId": "legacy", "appearanceMode": "light"})
	if status != 200 {
		t.Fatalf("reset want 200 got %d body=%s", status, body)
	}
	got := decodePrefs(t, body)
	if got.ThemeType != "preset" || got.PresetID != "legacy" || got.AppearanceMode != "light" {
		t.Errorf("reset result = %+v, want preset/legacy/light", got)
	}
	if got.CustomCSSEnabled {
		t.Error("overlay must be off after reset")
	}
	if got.CustomColors != nil || got.CustomCSS != nil {
		t.Errorf("customs must be null after reset, got %+v", got)
	}

	// The stored row must agree.
	_, body = doPrefsReq(t, srv, userID, "GET", "/users/me/preferences", nil)
	stored := decodePrefs(t, body)
	if stored.CustomColors != nil || stored.CustomCSS != nil || stored.CustomCSSEnabled {
		t.Errorf("stored = %+v, want customs null and overlay off", stored)
	}
	if stored.PresetID != "legacy" {
		t.Errorf("stored presetId = %q, want legacy", stored.PresetID)
	}
}

// Reset with no body defaults to the user's current preset/appearance.
func TestUserPreferences_ResetDefaultsToCurrent(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "reset-default", "viewer", "")
	seedLegacyPrefs(t, store, userID)

	status, body := doPrefsReq(t, srv, userID, "POST", "/users/me/preferences/reset", nil)
	if status != 200 {
		t.Fatalf("reset want 200 got %d body=%s", status, body)
	}
	got := decodePrefs(t, body)
	if got.PresetID != "legacy" || got.AppearanceMode != "system" || got.ThemeType != "preset" {
		t.Errorf("reset result = %+v, want preset/legacy/system", got)
	}
	if got.CustomColors != nil || got.CustomCSS != nil || got.CustomCSSEnabled {
		t.Errorf("customs must be null after reset, got %+v", got)
	}
}

func TestUserPreferences_ResetValidatesEnums(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "reset-invalid", "viewer", "")

	cases := []struct {
		name    string
		body    map[string]any
		wantSub string
	}{
		{"bad presetId", map[string]any{"presetId": "neon"}, "presetId"},
		{"bad appearanceMode", map[string]any{"appearanceMode": "auto"}, "appearanceMode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doPrefsReq(t, srv, userID, "POST", "/users/me/preferences/reset", tc.body)
			if status != 400 {
				t.Fatalf("want 400 got %d body=%s", status, body)
			}
			if !strings.Contains(string(body), tc.wantSub) {
				t.Errorf("body %q missing %q", body, tc.wantSub)
			}
		})
	}
}

func TestUserPreferences_Unauthenticated(t *testing.T) {
	srv, _, _ := newUsersServer(t, nil) // no caller injected
	cases := []struct {
		method, path string
		body         any
	}{
		{"GET", "/users/me/preferences", nil},
		{"PUT", "/users/me/preferences", map[string]any{
			"themeType": "preset", "presetId": "pink", "appearanceMode": "system", "customCssEnabled": false,
		}},
		{"POST", "/users/me/preferences/reset", nil},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			status, _ := doReq(t, tc.method, srv.URL+tc.path, tc.body)
			if status != 401 {
				t.Fatalf("want 401 got %d", status)
			}
		})
	}
}

// FR-008: one user's stored preferences must never leak into another user's
// responses.
func TestUserPreferences_CrossUserIsolation(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userA := seedUser(t, store, "user-a", "viewer", "")
	userB := seedUser(t, store, "user-b", "viewer", "")

	status, body := doPrefsReq(t, srv, userA, "PUT", "/users/me/preferences", map[string]any{
		"themeType": "custom_colors", "presetId": "legacy", "appearanceMode": "dark",
		"customColors":     map[string]any{"accent": "#10B981", "surface": "#121114"},
		"customCssEnabled": true,
		"customCss":        ".topbar { border-bottom: 3px solid lime; }",
	})
	if status != 200 {
		t.Fatalf("user A put want 200 got %d body=%s", status, body)
	}

	// User B's GET returns the pink defaults with no trace of A's values.
	status, body = doPrefsReq(t, srv, userB, "GET", "/users/me/preferences", nil)
	if status != 200 {
		t.Fatalf("user B get want 200 got %d", status)
	}
	got := decodePrefs(t, body)
	if got.PresetID != "pink" || got.CustomColors != nil || got.CustomCSS != nil || got.CustomCSSEnabled {
		t.Errorf("user B prefs = %+v, want pink defaults", got)
	}
	for _, leak := range []string{"#10B981", "#121114", "lime"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("user B response leaks user A value %q: %s", leak, body)
		}
	}

	// User B's /users/me embeds defaults too.
	status, body = doPrefsReq(t, srv, userB, "GET", "/users/me", nil)
	if status != 200 {
		t.Fatalf("user B me want 200 got %d", status)
	}
	var me userDTO
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Preferences == nil || me.Preferences.PresetID != "pink" || me.Preferences.CustomColors != nil {
		t.Errorf("user B me preferences = %+v, want pink defaults", me.Preferences)
	}
}

func TestUserPreferences_MeEmbedsPreferences(t *testing.T) {
	store := newTestStore(t)
	srv := newPreferencesServer(t, store)
	userID := seedUser(t, store, "me-embed", "viewer", "")

	status, body := doPrefsReq(t, srv, userID, "GET", "/users/me", nil)
	if status != 200 {
		t.Fatalf("me want 200 got %d", status)
	}
	var me userDTO
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Preferences == nil {
		t.Fatal("me response must embed preferences")
	}
	if me.Preferences.PresetID != "pink" || me.Preferences.ThemeType != "preset" ||
		me.Preferences.AppearanceMode != "system" || me.Preferences.CustomCSSEnabled {
		t.Errorf("embedded defaults = %+v, want preset/pink/system/overlay-off", me.Preferences)
	}

	// After a PUT, /users/me reflects the stored values.
	doPrefsReq(t, srv, userID, "PUT", "/users/me/preferences", map[string]any{
		"themeType": "preset", "presetId": "legacy", "appearanceMode": "dark", "customCssEnabled": false,
	})
	_, body = doPrefsReq(t, srv, userID, "GET", "/users/me", nil)
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode me after put: %v", err)
	}
	if me.Preferences == nil || me.Preferences.PresetID != "legacy" || me.Preferences.AppearanceMode != "dark" {
		t.Errorf("embedded prefs = %+v, want legacy/dark", me.Preferences)
	}
}

// The list/create responses must not carry the per-user preferences object.
func TestUserPreferences_NotEmbeddedInListResponses(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	seedUser(t, store, "listed", "viewer", "")

	status, body := doReq(t, "GET", srv.URL+"/users", nil)
	if status != 200 {
		t.Fatalf("list want 200 got %d", status)
	}
	if strings.Contains(string(body), `"preferences"`) {
		t.Errorf("list response must not embed preferences: %s", body)
	}
}
