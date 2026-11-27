package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

// newThemeTestStore opens a migrated in-memory SQLite store for theme tests.
func newThemeTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// TestThemeStore_SQLite runs the shared theme store checks on SQLite.
func TestThemeStore_SQLite(t *testing.T) {
	s := newThemeTestStore(t)
	checkThemeStore(t, s, func(t *testing.T, name string) int64 {
		return insertTestUser(t, s, name)
	})
}

// checkThemeStore exercises the theme store on any driver. newUser inserts a
// user and returns its id, since the insert statement differs per driver.
func checkThemeStore(t *testing.T, s *Store, newUser func(t *testing.T, name string) int64) {
	t.Helper()
	ctx := context.Background()
	alice := newUser(t, "theme-alice")
	bob := newUser(t, "theme-bob")

	// Sources: create, duplicate name, lookup, list with counts.
	src, err := s.CreateThemeSource(ctx, ThemeSource{
		Name: "community", Type: "http", Spec: `{"http":{"url":"https://example.com/t.tgz"}}`,
		RefreshIntervalS: 3600, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if src.ID <= 0 || src.CreatedAt == "" {
		t.Fatalf("create source: id or timestamps not set: %+v", src)
	}
	if _, err := s.CreateThemeSource(ctx, ThemeSource{Name: "community", Type: "http", Spec: "{}", RefreshIntervalS: 3600}); !errors.Is(err, ErrThemeSourceExists) {
		t.Fatalf("duplicate source: got %v, want ErrThemeSourceExists", err)
	}
	got, err := s.GetThemeSource(ctx, "community")
	if err != nil || got.ID != src.ID || !got.Enabled || got.Type != "http" {
		t.Fatalf("get source by name: %+v, %v", got, err)
	}
	if _, err := s.GetThemeSource(ctx, "missing"); !errors.Is(err, ErrThemeNotFound) {
		t.Fatalf("missing source: got %v, want ErrThemeNotFound", err)
	}
	if got, err := s.GetThemeSourceByID(ctx, src.ID); err != nil || got.Name != "community" {
		t.Fatalf("get source by id: %+v, %v", got, err)
	}

	// Update and sync bookkeeping.
	credRef := "gameplane-theme-source-" + "community"
	got.CredentialRef = &credRef
	got.Enabled = true
	if err := s.UpdateThemeSource(ctx, got); err != nil {
		t.Fatalf("update source: %v", err)
	}
	lastErr := "fetch failed"
	if err := s.SetThemeSourceSync(ctx, src.ID, time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), &lastErr); err != nil {
		t.Fatalf("set sync: %v", err)
	}
	got, err = s.GetThemeSourceByID(ctx, src.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if got.CredentialRef == nil || *got.CredentialRef != credRef {
		t.Fatalf("credential ref not stored: %+v", got)
	}
	if got.LastSyncAt == nil || *got.LastSyncAt != "2026-11-27T10:00:00Z" || got.LastError == nil || *got.LastError != lastErr {
		t.Fatalf("sync state not stored: %+v", got)
	}
	if err := s.SetThemeSourceSync(ctx, 999999, time.Now(), nil); !errors.Is(err, ErrThemeNotFound) {
		t.Fatalf("sync on missing source: got %v, want ErrThemeNotFound", err)
	}

	// Catalog: upsert keeps the id on re-index.
	entry := ThemeCatalogEntry{
		SourceID: src.ID, Name: "deep-ocean", Version: "1.0.0", Versions: `["1.0.0"]`,
		Digest: "sha256:aaa", Metadata: `{"displayName":"Deep Ocean"}`, Tokens: `{"light":{}}`,
		PreviewLight: []byte("\x89PNG-light"), Status: "valid", Problems: "[]", IndexedAt: "2026-11-27T10:00:00Z",
	}
	if err := s.UpsertThemeCatalogEntry(ctx, entry); err != nil {
		t.Fatalf("upsert entry: %v", err)
	}
	list, err := s.ListThemeCatalog(ctx, ThemeCatalogFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("list catalog: %d entries, %v", len(list), err)
	}
	firstID := list[0].ID
	if list[0].SourceName != "community" || list[0].Name != "deep-ocean" {
		t.Fatalf("list catalog row: %+v", list[0])
	}
	entry.Version = "1.1.0"
	entry.Versions = `["1.1.0","1.0.0"]`
	entry.Digest = "sha256:bbb"
	entry.PreviewLight = nil
	entry.LowContrast = true
	if err := s.UpsertThemeCatalogEntry(ctx, entry); err != nil {
		t.Fatalf("re-upsert entry: %v", err)
	}
	list, err = s.ListThemeCatalog(ctx, ThemeCatalogFilter{Query: "OCEAN"})
	if err != nil || len(list) != 1 || list[0].ID != firstID || list[0].Version != "1.1.0" || !list[0].LowContrast {
		t.Fatalf("re-index must keep id and update version: %+v, %v", list, err)
	}
	if none, err := s.ListThemeCatalog(ctx, ThemeCatalogFilter{Query: "%"}); err != nil || len(none) != 0 {
		t.Fatalf("query wildcard must match literally: %+v, %v", none, err)
	}
	if none, err := s.ListThemeCatalog(ctx, ThemeCatalogFilter{Source: "other"}); err != nil || len(none) != 0 {
		t.Fatalf("source filter: %+v, %v", none, err)
	}
	full, err := s.GetThemeCatalogEntry(ctx, firstID)
	if err != nil || full.Tokens != `{"light":{}}` || full.CSS != nil {
		t.Fatalf("get catalog entry: %+v, %v", full, err)
	}
	if _, err := s.GetThemeCatalogPreview(ctx, firstID, "light"); !errors.Is(err, ErrThemeNotFound) {
		t.Fatalf("cleared light preview: got %v, want ErrThemeNotFound", err)
	}
	if _, err := s.GetThemeCatalogPreview(ctx, firstID, "sepia"); !errors.Is(err, ErrThemeNotFound) {
		t.Fatalf("unknown mode: got %v, want ErrThemeNotFound", err)
	}
	if err := s.UpsertThemeCatalogEntry(ctx, ThemeCatalogEntry{
		SourceID: src.ID, Name: "sunset", Version: "1.0.0", Versions: "[]", Digest: "sha256:c",
		Metadata: "{}", Tokens: "{}", PreviewDark: []byte("\x89PNG-dark"), Status: "valid", Problems: "[]", IndexedAt: "2026-11-27T10:00:00Z",
	}); err != nil {
		t.Fatalf("upsert second entry: %v", err)
	}
	if _, err := s.GetThemeCatalogPreview(ctx, firstID, "dark"); !errors.Is(err, ErrThemeNotFound) {
		t.Fatalf("deep-ocean has no dark preview: got %v, want ErrThemeNotFound", err)
	}
	if err := s.DeleteThemeCatalogEntriesNotIn(ctx, src.ID, []string{"sunset"}); err != nil {
		t.Fatalf("delete not in: %v", err)
	}
	list, err = s.ListThemeCatalog(ctx, ThemeCatalogFilter{})
	if err != nil || len(list) != 1 || list[0].Name != "sunset" {
		t.Fatalf("after delete not in: %+v, %v", list, err)
	}

	// Installs: one per user, source and name; ownership; preferences link.
	sid := src.ID
	inst, err := s.InstallTheme(ctx, InstalledTheme{
		UserID: alice, SourceID: &sid, SourceName: "community", ThemeName: "sunset",
		Version: "1.0.0", Digest: "sha256:c", Metadata: "{}", Tokens: "{}",
	})
	if err != nil || inst.ID <= 0 {
		t.Fatalf("install: %+v, %v", inst, err)
	}
	if _, err := s.InstallTheme(ctx, InstalledTheme{
		UserID: alice, SourceID: &sid, SourceName: "community", ThemeName: "sunset",
		Version: "1.0.0", Digest: "sha256:c", Metadata: "{}", Tokens: "{}",
	}); !errors.Is(err, ErrThemeAlreadyInstalled) {
		t.Fatalf("second install: got %v, want ErrThemeAlreadyInstalled", err)
	}
	if n, err := s.CountInstalledThemes(ctx, alice); err != nil || n != 1 {
		t.Fatalf("count: %d, %v", n, err)
	}
	if _, err := s.GetInstalledTheme(ctx, bob, inst.ID); !errors.Is(err, ErrThemeNotFound) {
		t.Fatalf("foreign installed theme: got %v, want ErrThemeNotFound", err)
	}
	mine, err := s.GetInstalledTheme(ctx, alice, inst.ID)
	if err != nil || mine.SourceID == nil || *mine.SourceID != sid {
		t.Fatalf("get installed: %+v, %v", mine, err)
	}
	if list, err := s.ListInstalledThemes(ctx, alice); err != nil || len(list) != 1 {
		t.Fatalf("list installed: %+v, %v", list, err)
	}
	mine.Version = "1.1.0"
	css := "body { color: red; }"
	mine.CSS = &css
	if err := s.UpdateInstalledTheme(ctx, mine); err != nil {
		t.Fatalf("update installed: %v", err)
	}
	mine, err = s.GetInstalledTheme(ctx, alice, inst.ID)
	if err != nil || mine.Version != "1.1.0" || mine.CSS == nil || *mine.CSS != css {
		t.Fatalf("update installed not stored: %+v, %v", mine, err)
	}

	selected := inst.ID
	if _, err := s.UpsertPreferences(ctx, alice, UserPreferences{
		ThemeType: "installed", PresetID: "pink", AppearanceMode: "system", InstalledThemeID: &selected,
	}); err != nil {
		t.Fatalf("select installed theme: %v", err)
	}
	prefs, err := s.GetPreferences(ctx, alice)
	if err != nil || prefs.ThemeType != "installed" || prefs.InstalledThemeID == nil || *prefs.InstalledThemeID != selected {
		t.Fatalf("preferences round trip: %+v, %v", prefs, err)
	}

	// Removing the selected theme falls back to Pink and keeps the custom colors.
	accent := "#ff00aa"
	if _, err := s.UpsertPreferences(ctx, alice, UserPreferences{
		ThemeType: "installed", PresetID: "pink", AppearanceMode: "dark", CustomAccent: &accent, InstalledThemeID: &selected,
	}); err != nil {
		t.Fatalf("keep custom accent: %v", err)
	}
	if err := s.DeleteInstalledTheme(ctx, bob, inst.ID); !errors.Is(err, ErrThemeNotFound) {
		t.Fatalf("foreign delete: got %v, want ErrThemeNotFound", err)
	}
	if err := s.DeleteInstalledTheme(ctx, alice, inst.ID); err != nil {
		t.Fatalf("delete installed: %v", err)
	}
	prefs, err = s.GetPreferences(ctx, alice)
	if err != nil || prefs.ThemeType != "preset" || prefs.PresetID != "pink" || prefs.InstalledThemeID != nil {
		t.Fatalf("selection must fall back to pink: %+v, %v", prefs, err)
	}
	if prefs.CustomAccent == nil || *prefs.CustomAccent != accent {
		t.Fatalf("custom accent must survive removal: %+v", prefs)
	}

	// Removing a source detaches its installs (source_id NULL) and drops its entries.
	again, err := s.InstallTheme(ctx, InstalledTheme{
		UserID: alice, SourceID: &sid, SourceName: "community", ThemeName: "sunset",
		Version: "1.0.0", Digest: "sha256:c", Metadata: "{}", Tokens: "{}",
	})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if err := s.DeleteThemeSource(ctx, sid); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	detached, err := s.GetInstalledTheme(ctx, alice, again.ID)
	if err != nil || detached.SourceID != nil || detached.SourceName != "community" {
		t.Fatalf("install must survive source removal detached: %+v, %v", detached, err)
	}
	if list, err := s.ListThemeCatalog(ctx, ThemeCatalogFilter{}); err != nil || len(list) != 0 {
		t.Fatalf("entries must be gone with the source: %+v, %v", list, err)
	}
	if err := s.DeleteThemeSource(ctx, sid); !errors.Is(err, ErrThemeNotFound) {
		t.Fatalf("delete twice: got %v, want ErrThemeNotFound", err)
	}

	// Disabled sources are hidden from the catalog.
	hidden, err := s.CreateThemeSource(ctx, ThemeSource{Name: "hidden", Type: "local", Spec: "{}", RefreshIntervalS: 300})
	if err != nil {
		t.Fatalf("create hidden source: %v", err)
	}
	if err := s.UpsertThemeCatalogEntry(ctx, ThemeCatalogEntry{
		SourceID: hidden.ID, Name: "ghost", Version: "1.0.0", Versions: "[]", Digest: "d",
		Metadata: "{}", Tokens: "{}", Status: "valid", Problems: "[]", IndexedAt: "2026-11-27T10:00:00Z",
	}); err != nil {
		t.Fatalf("upsert hidden entry: %v", err)
	}
	if list, err := s.ListThemeCatalog(ctx, ThemeCatalogFilter{}); err != nil || len(list) != 0 {
		t.Fatalf("disabled source must be hidden (enabled false by default): %+v, %v", list, err)
	}
	if summaries, err := s.ListThemeSources(ctx); err != nil || len(summaries) != 1 || summaries[0].ThemeCount != 1 {
		t.Fatalf("list sources with counts: %+v, %v", summaries, err)
	}

	// Deleting a user removes their installed themes.
	tx, err := s.BeginUserManagement(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.DeleteUserTx(ctx, tx, alice); err != nil {
		_ = tx.Rollback()
		t.Fatalf("delete user: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n, err := s.CountInstalledThemes(ctx, alice); err != nil || n != 0 {
		t.Fatalf("installs must be removed with the user: %d, %v", n, err)
	}
}
