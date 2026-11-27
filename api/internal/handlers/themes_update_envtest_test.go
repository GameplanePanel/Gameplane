package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
)

// This file exercises updating and removing installed themes (spec 043, T043):
// two users on different versions, the removal fallback to Pink, a recreated
// source that never feeds old installs, and a disabled source that refuses
// updates. It reuses the catalog fixture and the helpers of
// themes_installed_envtest_test.go.

// themeUpdPublishGood publishes version 1.3.0 of the good theme to the registry
// fake without indexing it.
func themeUpdPublishGood(t *testing.T, env *themeCatEnv) {
	t.Helper()
	theme := bytes.Replace(themeCatFixture(t, "good", "theme.yaml"),
		[]byte("version: 1.2.0"), []byte("version: 1.3.0"), 1)
	files := map[string][]byte{
		"theme.yaml":        theme,
		"previews/dark.png": themeCatFixture(t, "good", "previews", "dark.png"),
	}
	env.oci.publish("registry.test/"+env.good+"/deep-ocean", "1.3.0", "sha256:good-1.3.0", files)
}

// themeUpdBumpGood publishes version 1.3.0 and refreshes the good source, so its
// catalog entry moves to the newer version.
func themeUpdBumpGood(t *testing.T, env *themeCatEnv) {
	t.Helper()
	themeUpdPublishGood(t, env)
	if err := env.svc.Refresh(context.Background(), env.good); err != nil {
		t.Fatalf("refresh %s after bump: %v", env.good, err)
	}
}

// themeUpdFind returns the list row with the given id.
func themeUpdFind(t *testing.T, items []installedThemeItem, id int64) installedThemeItem {
	t.Helper()
	for _, it := range items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("installed theme %d missing from %+v", id, items)
	return installedThemeItem{}
}

func TestThemeUpdateAffectsOnlyTheCallingUser(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, bob := newThemeInstUsers(t, env.store)
	hAlice := themeInstRouter(env, alice, nil)
	hBob := themeInstRouter(env, bob, nil)
	catalogID := themeInstCatalogID(t, env, env.good)

	aliceTheme := themeInstInstall(t, hAlice, catalogID, false)
	if aliceTheme.Version != "1.2.0" {
		t.Fatalf("alice installed %s, want 1.2.0", aliceTheme.Version)
	}

	themeUpdBumpGood(t, env)

	bobTheme := themeInstInstall(t, hBob, catalogID, false)
	if bobTheme.Version != "1.3.0" {
		t.Fatalf("bob installed %s, want 1.3.0", bobTheme.Version)
	}

	if row := themeUpdFind(t, themeInstList(t, hAlice), aliceTheme.ID); !row.UpdateAvailable || row.Version != "1.2.0" {
		t.Fatalf("alice's list does not offer the update: %+v", row)
	}
	if row := themeUpdFind(t, themeInstList(t, hBob), bobTheme.ID); row.UpdateAvailable || row.Version != "1.3.0" {
		t.Fatalf("bob's list offers an update he does not need: %+v", row)
	}

	rr := themeInstDo(t, hAlice, http.MethodPost, fmt.Sprintf("/users/me/themes/%d/update", aliceTheme.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("alice update: status %d body %s", rr.Code, rr.Body.String())
	}
	var updated installedThemeDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.ID != aliceTheme.ID || updated.Version != "1.3.0" || updated.UpdateAvailable {
		t.Fatalf("alice's update result: %+v", updated.installedThemeItem)
	}

	// Bob's copy did not move with alice's update.
	if row := themeUpdFind(t, themeInstList(t, hBob), bobTheme.ID); row.Version != "1.3.0" {
		t.Fatalf("bob's copy changed: %+v", row)
	}
	if row := themeUpdFind(t, themeInstList(t, hAlice), aliceTheme.ID); row.Version != "1.3.0" || row.UpdateAvailable {
		t.Fatalf("alice's list after update: %+v", row)
	}

	// Nothing newer is left: a second update is refused.
	rr = themeInstDo(t, hAlice, http.MethodPost, fmt.Sprintf("/users/me/themes/%d/update", aliceTheme.ID), "")
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "no newer version of this theme is available") {
		t.Fatalf("second update: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestThemeUpdateRefusedWhileSourceDisabled(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	h := themeInstRouter(env, alice, nil)

	detail := themeInstInstall(t, h, themeInstCatalogID(t, env, env.good), false)
	themeUpdBumpGood(t, env)
	env.setEnabled(env.good, false)

	rr := themeInstDo(t, h, http.MethodPost, fmt.Sprintf("/users/me/themes/%d/update", detail.ID), "")
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "the theme source is disabled") {
		t.Fatalf("update from a disabled source: status %d body %s", rr.Code, rr.Body.String())
	}

	// The install keeps working and shows as source removed, with no update.
	row := themeUpdFind(t, themeInstList(t, h), detail.ID)
	if !row.SourceRemoved || row.UpdateAvailable || row.Version != "1.2.0" {
		t.Fatalf("disabled source flags: %+v", row)
	}
}

func TestThemeUpdateNeverFollowsARecreatedSource(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	h := themeInstRouter(env, alice, nil)
	ctx := context.Background()

	detail := themeInstInstall(t, h, themeInstCatalogID(t, env, env.good), false)

	// Delete the source: its installs are detached, not removed.
	src, err := env.store.GetThemeSource(ctx, env.good)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if err := env.store.DeleteThemeSource(ctx, src.ID); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	if row := themeUpdFind(t, themeInstList(t, h), detail.ID); !row.SourceRemoved || row.UpdateAvailable {
		t.Fatalf("deleted source flags: %+v", row)
	}

	// Recreate the source under the same name, with a newer version published.
	themeUpdPublishGood(t, env)
	env.addSource(env.good, `{"oci":{"url":"registry.test/%s","themes":["deep-ocean"]}}`, nil)
	if err := env.svc.Refresh(ctx, env.good); err != nil {
		t.Fatalf("refresh recreated source: %v", err)
	}

	// The old install is matched by source id, so the new source offers nothing.
	row := themeUpdFind(t, themeInstList(t, h), detail.ID)
	if !row.SourceRemoved || row.UpdateAvailable {
		t.Fatalf("recreated source fed an old install: %+v", row)
	}
	rr := themeInstDo(t, h, http.MethodPost, fmt.Sprintf("/users/me/themes/%d/update", detail.ID), "")
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "theme source was removed") {
		t.Fatalf("update of a removed source: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestThemeRemoveSelectedFallsBackToPink(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	var targets []string
	h := themeInstRouter(env, alice, &targets)

	// Custom colors are set first; they must survive the install, the removal
	// and the fallback (FR-012).
	rr := themeInstDo(t, h, http.MethodPut, "/users/me/preferences",
		themeInstPrefsBody("custom_colors", `,"customColors":{"accent":"#112233"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("set custom colors: status %d body %s", rr.Code, rr.Body.String())
	}

	catalogID := themeInstCatalogID(t, env, env.good)
	entry, err := env.store.GetThemeCatalogEntry(context.Background(), catalogID)
	if err != nil {
		t.Fatalf("load entry: %v", err)
	}
	detail := themeInstInstall(t, h, catalogID, true)
	if !detail.Selected {
		t.Fatalf("install with select did not select: %+v", detail.installedThemeItem)
	}

	rr = themeInstDo(t, h, http.MethodDelete, fmt.Sprintf("/users/me/themes/%d", detail.ID), "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("remove selected: status %d body %s", rr.Code, rr.Body.String())
	}
	want := audit.ThemeTarget(audit.EventThemeRemove,
		"source", env.good, "theme", "deep-ocean", "from", "1.2.0", "digest", entry.Digest)
	if got := targets[len(targets)-1]; got != want {
		t.Fatalf("remove audit target %q, want %q", got, want)
	}

	rr = themeInstDo(t, h, http.MethodGet, "/users/me/preferences", "")
	var prefs userPreferencesDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &prefs); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	if prefs.ThemeType != "preset" || prefs.PresetID != "pink" || prefs.InstalledThemeID != nil || prefs.InstalledTheme != nil {
		t.Fatalf("removal did not fall back to Pink: %+v", prefs)
	}
	if prefs.CustomColors == nil || prefs.CustomColors.Accent != "#112233" {
		t.Fatalf("custom colors lost on fallback: %+v", prefs.CustomColors)
	}
	if items := themeInstList(t, h); len(items) != 0 {
		t.Fatalf("removed theme still listed: %+v", items)
	}
}
