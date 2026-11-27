package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// This file exercises the installed theme routes (spec 043, T036): install,
// select, isolation between users, refusal codes, the install limit, the
// preferences that select an installed theme, the rendered theme on the
// profile, and the audit targets. It reuses the catalog fixture of
// themes_catalog_envtest_test.go (newThemeCatEnv).

// themeInstSeq gives each test its own user names; the test store is shared.
var themeInstSeq atomic.Int64

// newThemeInstUsers creates two signed-in users and returns their ids.
func newThemeInstUsers(t *testing.T, store *db.Store) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	prefix := fmt.Sprintf("ti%d", themeInstSeq.Add(1))
	ids := make([]int64, 0, 2)
	for _, suffix := range []string{"alice", "bob"} {
		name := prefix + "-" + suffix
		if _, err := store.DB.ExecContext(ctx,
			`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?, ?, ?, ?, ?)`,
			name, name, name+"@example.com", "viewer", "x"); err != nil {
			t.Fatalf("insert user %s: %v", name, err)
		}
		var id int64
		if err := store.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, name).Scan(&id); err != nil {
			t.Fatalf("user id %s: %v", name, err)
		}
		ids = append(ids, id)
	}
	return ids[0], ids[1]
}

// themeInstRouter serves the catalog, the installed theme routes and the user
// routes (preferences and profile) as userID. Each request's audit target is
// appended to targets when targets is non-nil.
func themeInstRouter(env *themeCatEnv, userID int64, targets *[]string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx, holder := auth.WithActorHolder(req.Context())
			ctx = auth.WithUser(ctx, &auth.User{ID: userID, Username: fmt.Sprintf("user-%d", userID), Role: "viewer"})
			next.ServeHTTP(w, req.WithContext(ctx))
			if targets != nil {
				*targets = append(*targets, holder.Target())
			}
		})
	})
	// The production order: users first, then the theme routes.
	MountUsers(r, env.store, nil, nil)
	MountThemes(r, env.store, env.svc)
	MountInstalledThemes(r, env.store, env.svc)
	return r
}

// themeInstDo sends one request with an optional JSON body.
func themeInstDo(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// themeInstCatalogID returns the catalog id of the deep-ocean entry of source.
func themeInstCatalogID(t *testing.T, env *themeCatEnv, source string) int64 {
	t.Helper()
	entries, err := env.store.ListThemeCatalog(context.Background(), db.ThemeCatalogFilter{Source: source})
	if err != nil {
		t.Fatalf("list catalog %s: %v", source, err)
	}
	for _, e := range entries {
		if e.Name == "deep-ocean" {
			return e.ID
		}
	}
	t.Fatalf("deep-ocean missing from %s", source)
	return 0
}

// themeInstInstall installs the catalog entry as the router's user and
// returns the installed theme, failing the test unless the status is 201.
func themeInstInstall(t *testing.T, h http.Handler, catalogID int64, selectTheme bool) installedThemeDetail {
	t.Helper()
	body := fmt.Sprintf(`{"catalogId":%d,"select":%t}`, catalogID, selectTheme)
	rr := themeInstDo(t, h, http.MethodPost, "/users/me/themes", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("install: status %d body %s", rr.Code, rr.Body.String())
	}
	var detail installedThemeDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode install: %v", err)
	}
	return detail
}

// themeInstList decodes GET /users/me/themes.
func themeInstList(t *testing.T, h http.Handler) []installedThemeItem {
	t.Helper()
	rr := themeInstDo(t, h, http.MethodGet, "/users/me/themes", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list installed: status %d body %s", rr.Code, rr.Body.String())
	}
	var items []installedThemeItem
	if err := json.Unmarshal(rr.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode installed list: %v", err)
	}
	return items
}

// themeInstPrefsBody builds a PUT /users/me/preferences body. extra holds
// further JSON members, each starting with a comma.
func themeInstPrefsBody(themeType, extra string) string {
	return fmt.Sprintf(`{"themeType":%q,"presetId":"pink","appearanceMode":"system","customCssEnabled":false%s}`, themeType, extra)
}

func TestInstalledThemeInstallAndList(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	h := themeInstRouter(env, alice, nil)

	detail := themeInstInstall(t, h, themeInstCatalogID(t, env, env.good), false)
	if detail.Source != env.good || detail.Name != "deep-ocean" || detail.Version != "1.2.0" {
		t.Fatalf("installed theme identity: %+v", detail.installedThemeItem)
	}
	if detail.DisplayName != "Deep Ocean" || detail.Selected || detail.SourceRemoved || detail.UpdateAvailable {
		t.Fatalf("installed theme flags: %+v", detail.installedThemeItem)
	}
	if !bytes.Contains(detail.Tokens, []byte(`"dark"`)) {
		t.Fatalf("detail tokens missing the dark mode: %s", detail.Tokens)
	}

	items := themeInstList(t, h)
	if len(items) != 1 || items[0].ID != detail.ID {
		t.Fatalf("list after install: %+v", items)
	}

	rr := themeInstDo(t, h, http.MethodGet, fmt.Sprintf("/users/me/themes/%d", detail.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get installed: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestInstalledThemeSelectAndProfile(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	h := themeInstRouter(env, alice, nil)

	detail := themeInstInstall(t, h, themeInstCatalogID(t, env, env.good), true)
	if !detail.Selected {
		t.Fatalf("select=true did not select the theme: %+v", detail.installedThemeItem)
	}

	rr := themeInstDo(t, h, http.MethodGet, "/users/me/preferences", "")
	var prefs userPreferencesDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &prefs); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	if prefs.ThemeType != "installed" || prefs.InstalledThemeID == nil || *prefs.InstalledThemeID != detail.ID {
		t.Fatalf("preferences after select: %+v", prefs)
	}
	if prefs.InstalledTheme == nil || prefs.InstalledTheme.Version != "1.2.0" || !bytes.Contains(prefs.InstalledTheme.Tokens, []byte(`"dark"`)) {
		t.Fatalf("preferences carry no rendered theme: %+v", prefs.InstalledTheme)
	}

	rr = themeInstDo(t, h, http.MethodGet, "/users/me", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("profile: status %d body %s", rr.Code, rr.Body.String())
	}
	var profile userDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.Preferences == nil || profile.Preferences.InstalledThemeID == nil ||
		profile.Preferences.InstalledTheme == nil || profile.Preferences.InstalledTheme.Version != "1.2.0" {
		t.Fatalf("profile preferences do not carry the installed theme: %+v", profile.Preferences)
	}
}

func TestInstalledThemeIsolationBetweenUsers(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, bob := newThemeInstUsers(t, env.store)
	hAlice := themeInstRouter(env, alice, nil)
	hBob := themeInstRouter(env, bob, nil)

	detail := themeInstInstall(t, hAlice, themeInstCatalogID(t, env, env.good), false)
	path := fmt.Sprintf("/users/me/themes/%d", detail.ID)

	if rr := themeInstDo(t, hBob, http.MethodGet, path, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("bob read alice's theme: status %d", rr.Code)
	}
	if rr := themeInstDo(t, hBob, http.MethodPost, path+"/update", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("bob update alice's theme: status %d", rr.Code)
	}
	if rr := themeInstDo(t, hBob, http.MethodDelete, path, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("bob remove alice's theme: status %d", rr.Code)
	}
	if items := themeInstList(t, hBob); len(items) != 0 {
		t.Fatalf("bob sees alice's themes: %+v", items)
	}
	if items := themeInstList(t, hAlice); len(items) != 1 {
		t.Fatalf("alice lost her theme after bob's attempts: %+v", items)
	}

	// Bob cannot select alice's theme either (422, see the preferences test).
	rr := themeInstDo(t, hBob, http.MethodPut, "/users/me/preferences",
		themeInstPrefsBody("installed", fmt.Sprintf(`,"installedThemeId":%d`, detail.ID)))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bob selected alice's theme: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestInstalledThemeRefusalCodes(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, bob := newThemeInstUsers(t, env.store)
	hAlice := themeInstRouter(env, alice, nil)
	hBob := themeInstRouter(env, bob, nil)

	install := func(h http.Handler, catalogID int64) *httptest.ResponseRecorder {
		return themeInstDo(t, h, http.MethodPost, "/users/me/themes", fmt.Sprintf(`{"catalogId":%d}`, catalogID))
	}
	expect := func(name string, rr *httptest.ResponseRecorder, status int, text string) {
		t.Helper()
		if rr.Code != status {
			t.Fatalf("%s: status %d want %d body %s", name, rr.Code, status, rr.Body.String())
		}
		if text != "" && !strings.Contains(rr.Body.String(), text) {
			t.Fatalf("%s: body %q lacks %q", name, rr.Body.String(), text)
		}
	}

	expect("invalid entry", install(hAlice, themeInstCatalogID(t, env, env.bad)),
		http.StatusUnprocessableEntity, "theme is not valid and cannot be installed")
	expect("unverified entry", install(hAlice, themeInstCatalogID(t, env, env.unsigned)),
		http.StatusUnprocessableEntity, "theme is not valid and cannot be installed")
	expect("unknown entry", install(hAlice, 999999999), http.StatusNotFound, "theme not found")

	goodID := themeInstCatalogID(t, env, env.good)
	expect("first install", install(hAlice, goodID), http.StatusCreated, "")
	expect("already installed", install(hAlice, goodID),
		http.StatusConflict, "theme is already installed")

	// A disabled source refuses installs, while its cached entries stay.
	env.setEnabled(env.good, false)
	expect("source disabled", install(hBob, goodID),
		http.StatusConflict, "the theme source is disabled")
}

func TestInstalledThemeLimitRefusesInstall(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	h := themeInstRouter(env, alice, nil)

	ctx := context.Background()
	for i := range 50 {
		if _, err := env.store.InstallTheme(ctx, db.InstalledTheme{
			UserID:     alice,
			SourceName: "limit-fill",
			ThemeName:  fmt.Sprintf("filler-%d", i),
			Version:    "1.0.0",
			Digest:     "sha256:filler",
			Metadata:   "{}",
			Tokens:     "{}",
		}); err != nil {
			t.Fatalf("fill installed themes: %v", err)
		}
	}
	rr := themeInstDo(t, h, http.MethodPost, "/users/me/themes",
		fmt.Sprintf(`{"catalogId":%d}`, themeInstCatalogID(t, env, env.good)))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "you have reached the limit of installed themes") {
		t.Fatalf("install past the limit: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestInstalledThemePreferencesSelection(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, bob := newThemeInstUsers(t, env.store)
	hAlice := themeInstRouter(env, alice, nil)
	hBob := themeInstRouter(env, bob, nil)

	detail := themeInstInstall(t, hAlice, themeInstCatalogID(t, env, env.good), false)
	put := func(h http.Handler, body string) *httptest.ResponseRecorder {
		return themeInstDo(t, h, http.MethodPut, "/users/me/preferences", body)
	}

	// Selecting without an id, or with an id that is not the caller's, is 422.
	if rr := put(hAlice, themeInstPrefsBody("installed", "")); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("installed without id: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := put(hBob, themeInstPrefsBody("installed", fmt.Sprintf(`,"installedThemeId":%d`, detail.ID))); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("installed with another user's id: status %d body %s", rr.Code, rr.Body.String())
	}
	// A nonexistent id is the same refusal.
	if rr := put(hAlice, themeInstPrefsBody("installed", `,"installedThemeId":999999999`)); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("installed with unknown id: status %d body %s", rr.Code, rr.Body.String())
	}

	// The caller's own id selects it.
	rr := put(hAlice, themeInstPrefsBody("installed", fmt.Sprintf(`,"installedThemeId":%d`, detail.ID)))
	if rr.Code != http.StatusOK {
		t.Fatalf("select own theme: status %d body %s", rr.Code, rr.Body.String())
	}
	var saved userPreferencesDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode saved preferences: %v", err)
	}
	if saved.ThemeType != "installed" || saved.InstalledThemeID == nil || *saved.InstalledThemeID != detail.ID || saved.InstalledTheme == nil {
		t.Fatalf("saved preferences: %+v", saved)
	}

	// Switching back to a preset clears the selection and the rendered theme.
	rr = put(hAlice, themeInstPrefsBody("preset", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("switch to preset: status %d body %s", rr.Code, rr.Body.String())
	}
	var back userPreferencesDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &back); err != nil {
		t.Fatalf("decode preset preferences: %v", err)
	}
	if back.ThemeType != "preset" || back.InstalledThemeID != nil || back.InstalledTheme != nil {
		t.Fatalf("preset did not clear the installed selection: %+v", back)
	}

	// The existing theme type message is unchanged.
	rr = put(hAlice, themeInstPrefsBody("bogus", ""))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "themeType must be") {
		t.Fatalf("invalid theme type: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestInstalledThemeAuditTargets(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	var targets []string
	h := themeInstRouter(env, alice, &targets)

	catalogID := themeInstCatalogID(t, env, env.good)
	entry, err := env.store.GetThemeCatalogEntry(context.Background(), catalogID)
	if err != nil {
		t.Fatalf("load entry: %v", err)
	}
	detail := themeInstInstall(t, h, catalogID, false)

	want := audit.ThemeTarget(audit.EventThemeInstall,
		"source", env.good, "theme", "deep-ocean", "to", "1.2.0", "digest", entry.Digest)
	if got := targets[len(targets)-1]; got != want {
		t.Fatalf("install audit target %q, want %q", got, want)
	}

	// Reads are not audited: a GET records no target.
	themeInstDo(t, h, http.MethodGet, fmt.Sprintf("/users/me/themes/%d", detail.ID), "")
	if got := targets[len(targets)-1]; got != "" {
		t.Fatalf("read recorded an audit target %q", got)
	}
}
