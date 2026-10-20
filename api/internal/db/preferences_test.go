package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestUserThemePreferencesMigration proves migration 011 (data-model.md §3):
// the user_preferences table is created, every pre-existing user is
// backfilled with the legacy preset, and accounts created afterwards fall
// through to the pink defaults. It applies 001–010 by hand, seeds users
// under the pre-011 schema, then applies 011 — mirroring
// TestMigration010_ExpiresAtNullable.
func TestUserThemePreferencesMigration(t *testing.T) {
	s, err := Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	if _, err := s.DB.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	// Apply every migration up through 010 by hand, stopping before 011.
	names := []string{
		"001_init.sql", "002_config.sql", "003_roles.sql", "004_cluster_rbac.sql",
		"005_audit_chain.sql", "006_share_links.sql", "007_audit_reason.sql",
		"008_captures_rbac.sql", "009_share_links_cluster.sql",
		"010_share_links_expiry_nullable.sql",
	}
	for _, name := range names {
		content, err := migrations.ReadFile("migrations/sqlite/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := s.runMigration(ctx, name, string(content)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	// Pre-existing users, seeded before 011 runs.
	preA := insertTestUser(t, s, "pre-migration-a")
	preB := insertTestUser(t, s, "pre-migration-b")

	// Apply 011.
	content, err := migrations.ReadFile("migrations/sqlite/011_user_theme_preferences.sql")
	if err != nil {
		t.Fatalf("read 011: %v", err)
	}
	if err := s.runMigration(ctx, "011_user_theme_preferences.sql", string(content)); err != nil {
		t.Fatalf("apply 011: %v", err)
	}

	// Both pre-existing users must be backfilled with the legacy preset and
	// system appearance, overlay off, customs NULL.
	for _, id := range []int64{preA, preB} {
		var presetID, themeType, appearanceMode string
		var cssEnabled int
		var accent, surface, css sql.NullString
		err := s.DB.QueryRowContext(ctx,
			`SELECT theme_type, preset_id, appearance_mode, custom_accent, custom_surface, custom_css_enabled, custom_css
			   FROM user_preferences WHERE user_id = ?`, id).
			Scan(&themeType, &presetID, &appearanceMode, &accent, &surface, &cssEnabled, &css)
		if err != nil {
			t.Fatalf("query backfilled row %d: %v", id, err)
		}
		if presetID != "legacy" {
			t.Errorf("user %d: preset_id=%q, want legacy", id, presetID)
		}
		if themeType != "preset" || appearanceMode != "system" {
			t.Errorf("user %d: theme_type=%q appearance_mode=%q, want preset/system", id, themeType, appearanceMode)
		}
		if cssEnabled != 0 {
			t.Errorf("user %d: custom_css_enabled=%d, want 0", id, cssEnabled)
		}
		if accent.Valid || surface.Valid || css.Valid {
			t.Errorf("user %d: customs must be NULL, got accent=%v surface=%v css=%v", id, accent, surface, css)
		}
	}

	// The index must exist.
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, "idx_user_preferences_user").Scan(&n); err != nil {
		t.Fatalf("check index: %v", err)
	}
	if n != 1 {
		t.Error("index idx_user_preferences_user missing after migration 011")
	}

	// Apply the remaining migrations so GetPreferences sees the columns
	// later migrations add (016 adds language).
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("apply remaining migrations: %v", err)
	}

	// A user created after the migration has no row; GetPreferences must
	// return the pink defaults.
	post := insertTestUser(t, s, "post-migration")
	prefs, err := s.GetPreferences(ctx, post)
	if err != nil {
		t.Fatalf("get post-migration preferences: %v", err)
	}
	if prefs.PresetID != "pink" || prefs.ThemeType != "preset" || prefs.AppearanceMode != "system" {
		t.Errorf("post-migration defaults = %+v, want preset/pink/system", prefs)
	}
	if prefs.CustomCSSEnabled {
		t.Error("post-migration defaults: overlay must be off")
	}
	if prefs.CustomAccent != nil || prefs.CustomSurface != nil || prefs.CustomCSS != nil {
		t.Errorf("post-migration defaults: customs must be nil, got %+v", prefs)
	}

	// A row inserted with only user_id must take the column defaults.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_preferences (user_id) VALUES (?)`, post); err != nil {
		t.Fatalf("insert bare row: %v", err)
	}
	var presetID string
	var cssEnabled int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT preset_id, custom_css_enabled FROM user_preferences WHERE user_id = ?`, post).
		Scan(&presetID, &cssEnabled); err != nil {
		t.Fatalf("query bare row: %v", err)
	}
	if presetID != "pink" || cssEnabled != 0 {
		t.Errorf("column defaults = preset_id %q css_enabled %d, want pink/0", presetID, cssEnabled)
	}
}

func TestUserPreferences_GetDefaultsWithoutRow(t *testing.T) {
	s := newShareLinksStore(t)
	prefs, err := s.GetPreferences(context.Background(), 424242)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if prefs.PresetID != "pink" || prefs.ThemeType != "preset" || prefs.AppearanceMode != "system" {
		t.Errorf("defaults = %+v, want preset/pink/system", prefs)
	}
	if prefs.CustomCSSEnabled {
		t.Error("defaults: overlay must be off")
	}
	if prefs.CustomAccent != nil || prefs.CustomSurface != nil || prefs.CustomCSS != nil {
		t.Errorf("defaults: customs must be nil, got %+v", prefs)
	}
	if prefs.UpdatedAt == "" {
		t.Error("defaults: UpdatedAt must be populated")
	}
}

// TestUserPreferences_GetNormalizesLegacyBackfillTimestamp is the F-085
// regression test: migration 011's backfill stores updated_at via SQLite's
// datetime('now'), which is "YYYY-MM-DD HH:MM:SS" — not RFC 3339, contrary
// to api/specs.md's contract. GetPreferences must normalize it on read
// without editing the (append-only) migration itself.
func TestUserPreferences_GetNormalizesLegacyBackfillTimestamp(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "legacy-backfill")

	// Simulate what migration 011's backfill INSERT produces: a row whose
	// updated_at came from the column default datetime('now'), not Go's
	// RFC3339 writer.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_preferences (user_id, theme_type, preset_id, appearance_mode, updated_at)
		    VALUES (?, 'preset', 'legacy', 'system', '2026-09-24 08:00:00')`, userID); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, prefs.UpdatedAt); err != nil {
		t.Fatalf("UpdatedAt = %q, not RFC3339: %v", prefs.UpdatedAt, err)
	}
	if prefs.UpdatedAt != "2026-09-24T08:00:00Z" {
		t.Fatalf("UpdatedAt = %q, want 2026-09-24T08:00:00Z", prefs.UpdatedAt)
	}
}

// TestUserPreferences_GetPassesThroughRFC3339Timestamp confirms a row
// already written by Go (UpsertPreferences, RFC3339) is returned verbatim
// by normalizeTimestamp — no double-conversion or format drift.
func TestUserPreferences_GetPassesThroughRFC3339Timestamp(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "rfc3339-passthrough")

	if _, err := s.UpsertPreferences(ctx, userID, UserPreferences{
		ThemeType: "preset", PresetID: "pink", AppearanceMode: "system",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, prefs.UpdatedAt); err != nil {
		t.Fatalf("UpdatedAt = %q, not RFC3339: %v", prefs.UpdatedAt, err)
	}
}

func TestUserPreferences_UpsertRoundTrip(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "roundtrip")

	accent, surface, css := "#10B981", "#121114", ".card { border-radius: 12px; }"
	in := UserPreferences{
		ThemeType:        "custom_colors",
		PresetID:         "pink",
		AppearanceMode:   "dark",
		CustomAccent:     &accent,
		CustomSurface:    &surface,
		CustomCSSEnabled: true,
		CustomCSS:        &css,
	}
	saved, err := s.UpsertPreferences(ctx, userID, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if saved.UpdatedAt == "" {
		t.Error("upsert must stamp UpdatedAt")
	}

	got, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ThemeType != "custom_colors" || got.PresetID != "pink" || got.AppearanceMode != "dark" {
		t.Errorf("base fields = %+v", got)
	}
	if !got.CustomCSSEnabled {
		t.Error("overlay must be on")
	}
	if got.CustomAccent == nil || *got.CustomAccent != accent {
		t.Errorf("custom_accent = %v, want %q", got.CustomAccent, accent)
	}
	if got.CustomSurface == nil || *got.CustomSurface != surface {
		t.Errorf("custom_surface = %v, want %q", got.CustomSurface, surface)
	}
	if got.CustomCSS == nil || *got.CustomCSS != css {
		t.Errorf("custom_css = %v, want %q", got.CustomCSS, css)
	}

	// A second upsert for the same user must replace, not duplicate.
	if _, err := s.UpsertPreferences(ctx, userID, UserPreferences{
		ThemeType: "preset", PresetID: "legacy", AppearanceMode: "light",
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_preferences WHERE user_id = ?`, userID).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d rows, want 1", count)
	}
}

// TestUserPreferences_RetentionAcrossUpserts covers FR-012 at the DB layer:
// an ordinary update that carries the previously stored custom fields keeps
// them stored — switching the base preset and toggling the overlay never
// nulls custom_accent / custom_surface / custom_css. Only reset does.
func TestUserPreferences_RetentionAcrossUpserts(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "retention")

	accent, surface, css := "#3B82F6", "#18181B", ".topbar { border-bottom: 3px solid lime; }"
	if _, err := s.UpsertPreferences(ctx, userID, UserPreferences{
		ThemeType: "custom_colors", PresetID: "pink", AppearanceMode: "system",
		CustomAccent: &accent, CustomSurface: &surface, CustomCSSEnabled: true, CustomCSS: &css,
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	// Ordinary update: base switches to legacy, overlay toggled off. The
	// handler builds this struct by reading GetPreferences first, so the
	// custom fields ride along untouched.
	prev, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	prev.ThemeType = "preset"
	prev.PresetID = "legacy"
	prev.AppearanceMode = "dark"
	prev.CustomCSSEnabled = false
	if _, err := s.UpsertPreferences(ctx, userID, prev); err != nil {
		t.Fatalf("update upsert: %v", err)
	}

	got, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.PresetID != "legacy" || got.ThemeType != "preset" || got.AppearanceMode != "dark" {
		t.Errorf("base fields = %+v, want preset/legacy/dark", got)
	}
	if got.CustomCSSEnabled {
		t.Error("overlay must be off after toggle")
	}
	if got.CustomAccent == nil || *got.CustomAccent != accent {
		t.Errorf("custom_accent = %v, want retained %q", got.CustomAccent, accent)
	}
	if got.CustomSurface == nil || *got.CustomSurface != surface {
		t.Errorf("custom_surface = %v, want retained %q", got.CustomSurface, surface)
	}
	if got.CustomCSS == nil || *got.CustomCSS != css {
		t.Errorf("custom_css = %v, want retained %q", got.CustomCSS, css)
	}
}

func TestUserPreferences_ResetNullsCustoms(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "reset-user")

	accent, surface, css := "#10B981", "#121114", "body { opacity: 0.1 !important; }"
	if _, err := s.UpsertPreferences(ctx, userID, UserPreferences{
		ThemeType: "custom_colors", PresetID: "pink", AppearanceMode: "dark",
		CustomAccent: &accent, CustomSurface: &surface, CustomCSSEnabled: true, CustomCSS: &css,
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.ResetPreferences(ctx, userID, "legacy", "light")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got.ThemeType != "preset" || got.PresetID != "legacy" || got.AppearanceMode != "light" {
		t.Errorf("reset result = %+v, want preset/legacy/light", got)
	}
	if got.CustomCSSEnabled {
		t.Error("overlay must be off after reset")
	}
	if got.CustomAccent != nil || got.CustomSurface != nil || got.CustomCSS != nil {
		t.Errorf("reset result: customs must be nil, got %+v", got)
	}

	// The stored row must agree, customs NULL column-by-column.
	stored, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get after reset: %v", err)
	}
	if stored.CustomAccent != nil || stored.CustomSurface != nil || stored.CustomCSS != nil {
		t.Errorf("stored customs must be NULL, got %+v", stored)
	}
	if stored.PresetID != "legacy" || stored.ThemeType != "preset" || stored.CustomCSSEnabled {
		t.Errorf("stored = %+v, want preset/legacy/overlay-off", stored)
	}
}

// Reset on a user with no row must create one with the requested preset and
// no customs.
func TestUserPreferences_ResetWithoutRow(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "reset-blank")

	got, err := s.ResetPreferences(ctx, userID, "pink", "system")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got.ThemeType != "preset" || got.PresetID != "pink" || got.AppearanceMode != "system" {
		t.Errorf("reset result = %+v, want preset/pink/system", got)
	}
	if got.CustomAccent != nil || got.CustomSurface != nil || got.CustomCSS != nil || got.CustomCSSEnabled {
		t.Errorf("reset result: customs must be nil and overlay off, got %+v", got)
	}

	stored, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get after reset: %v", err)
	}
	if stored.PresetID != "pink" || stored.CustomAccent != nil {
		t.Errorf("stored = %+v, want pink with nil customs", stored)
	}
}

// strPtr returns a pointer to s, for the nullable language field in tests.
func strPtr(s string) *string { return &s }

// TestUserPreferences_SetLanguageRoundTrip checks that SetLanguage stores a
// tag that GetPreferences returns, and that a nil language clears it.
func TestUserPreferences_SetLanguageRoundTrip(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "lang-roundtrip")

	if err := s.SetLanguage(ctx, userID, strPtr("de")); err != nil {
		t.Fatalf("set language: %v", err)
	}
	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if prefs.Language == nil || *prefs.Language != "de" {
		t.Fatalf("Language = %v, want de", prefs.Language)
	}

	if err := s.SetLanguage(ctx, userID, nil); err != nil {
		t.Fatalf("clear language: %v", err)
	}
	prefs, err = s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if prefs.Language != nil {
		t.Fatalf("Language = %q after clear, want nil", *prefs.Language)
	}
}

// TestUserPreferences_UpsertKeepsLanguage checks that a theme save does not
// touch the saved language, and that the returned value echoes the caller's
// Language (the handler passes in the stored row it read first).
func TestUserPreferences_UpsertKeepsLanguage(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "lang-upsert")

	if err := s.SetLanguage(ctx, userID, strPtr("fr")); err != nil {
		t.Fatalf("set language: %v", err)
	}
	stored, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.AppearanceMode = "dark"
	saved, err := s.UpsertPreferences(ctx, userID, stored)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if saved.Language == nil || *saved.Language != "fr" {
		t.Fatalf("returned Language = %v, want fr", saved.Language)
	}

	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if prefs.AppearanceMode != "dark" {
		t.Fatalf("AppearanceMode = %q, want dark", prefs.AppearanceMode)
	}
	if prefs.Language == nil || *prefs.Language != "fr" {
		t.Fatalf("stored Language = %v after upsert, want fr", prefs.Language)
	}
}

// TestUserPreferences_UpsertNewRowLanguageNil checks that an upsert creating a
// new row leaves the language NULL.
func TestUserPreferences_UpsertNewRowLanguageNil(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "lang-upsert-new")

	if _, err := s.UpsertPreferences(ctx, userID, DefaultUserPreferences()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if prefs.Language != nil {
		t.Fatalf("new row Language = %q, want nil", *prefs.Language)
	}
}

// TestUserPreferences_ResetKeepsLanguage checks that a reset clears the custom
// styling but keeps the saved language.
func TestUserPreferences_ResetKeepsLanguage(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "lang-reset")

	if err := s.SetLanguage(ctx, userID, strPtr("ja")); err != nil {
		t.Fatalf("set language: %v", err)
	}
	if _, err := s.ResetPreferences(ctx, userID, "legacy", "light"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if prefs.PresetID != "legacy" || prefs.AppearanceMode != "light" {
		t.Fatalf("reset not applied: %+v", prefs)
	}
	if prefs.Language == nil || *prefs.Language != "ja" {
		t.Fatalf("Language = %v after reset, want ja", prefs.Language)
	}
}

// TestUserPreferences_SetLanguageNoRowGetsDefaults checks that SetLanguage
// for a user with no preferences row inserts one with the default values.
func TestUserPreferences_SetLanguageNoRowGetsDefaults(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "lang-no-row")

	if err := s.SetLanguage(ctx, userID, strPtr("en")); err != nil {
		t.Fatalf("set language: %v", err)
	}
	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if prefs.ThemeType != "preset" || prefs.PresetID != "pink" || prefs.AppearanceMode != "system" {
		t.Fatalf("defaults not applied: %+v", prefs)
	}
	if prefs.CustomCSSEnabled || prefs.CustomAccent != nil || prefs.CustomSurface != nil || prefs.CustomCSS != nil {
		t.Fatalf("customs must stay at defaults: %+v", prefs)
	}
	if prefs.Language == nil || *prefs.Language != "en" {
		t.Fatalf("Language = %v, want en", prefs.Language)
	}
}
