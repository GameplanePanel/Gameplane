package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UserPreferences is a user's stored dashboard styling choices: the active
// base theme (a named preset or custom colors), the appearance mode, and an
// optional sanitized custom CSS overlay. Custom colors and CSS are retained
// across preset switches and overlay toggles; only an explicit reset clears
// them (FR-012, contracts/user-preferences-api.md §1.3).
type UserPreferences struct {
	ThemeType        string  // base mode: "preset" | "custom_colors"
	PresetID         string  // "pink" | "legacy"; fallback when ThemeType is custom_colors
	AppearanceMode   string  // "light" | "dark" | "system"
	CustomAccent     *string // nullable #RRGGBB hex
	CustomSurface    *string // nullable #RRGGBB hex
	CustomCSSEnabled bool    // whether the custom CSS overlay is injected
	CustomCSS        *string // nullable sanitized CSS text (max 32 KiB)
	Language         *string // nullable BCP 47 tag; nil = no saved choice (follow the instance default)
	UpdatedAt        string  // RFC3339 UTC, application-generated in Go
}

// DefaultUserPreferences returns the configuration for a user with no stored
// row: the pink preset, system appearance, no custom colors, overlay off.
// This is what a newly created account sees — migration 011's backfill only
// touches users that already existed when it ran.
func DefaultUserPreferences() UserPreferences {
	return UserPreferences{
		ThemeType:      "preset",
		PresetID:       "pink",
		AppearanceMode: "system",
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
}

// GetPreferences loads the user's preferences, returning
// DefaultUserPreferences when the user has no stored row (e.g. a newly
// created account).
func (s *Store) GetPreferences(ctx context.Context, userID int64) (UserPreferences, error) {
	var p UserPreferences
	var accent, surface, css, language sql.NullString
	var cssEnabled int
	err := s.DB.QueryRowContext(ctx,
		`SELECT theme_type, preset_id, appearance_mode, custom_accent, custom_surface, custom_css_enabled, custom_css, language, updated_at
		   FROM user_preferences WHERE user_id = ?`, userID).
		Scan(&p.ThemeType, &p.PresetID, &p.AppearanceMode, &accent, &surface, &cssEnabled, &css, &language, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultUserPreferences(), nil
	}
	if err != nil {
		return UserPreferences{}, fmt.Errorf("get preferences: %w", err)
	}
	p.CustomCSSEnabled = cssEnabled != 0
	if accent.Valid {
		p.CustomAccent = &accent.String
	}
	if surface.Valid {
		p.CustomSurface = &surface.String
	}
	if css.Valid {
		p.CustomCSS = &css.String
	}
	if language.Valid {
		p.Language = &language.String
	}
	p.UpdatedAt = NormalizeTimestamp(p.UpdatedAt)
	return p, nil
}

// UpsertPreferences inserts the user's preference row or replaces it in
// full. The caller owns merge/retention semantics: every field of p is
// written verbatim, so a caller that wants FR-012 retention must carry the
// previously stored custom fields into p (the handler does this by reading
// GetPreferences first and applying only the fields present in the request).
// Enum validation and CSS sanitization live in the handlers. The language
// column is not written here (SetLanguage owns it), so the stored language
// survives an upsert; the returned value echoes the caller's p.Language.
func (s *Store) UpsertPreferences(ctx context.Context, userID int64, p UserPreferences) (UserPreferences, error) {
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	cssEnabled := 0
	if p.CustomCSSEnabled {
		cssEnabled = 1
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_preferences
		    (user_id, theme_type, preset_id, appearance_mode, custom_accent, custom_surface, custom_css_enabled, custom_css, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET
		    theme_type = excluded.theme_type,
		    preset_id = excluded.preset_id,
		    appearance_mode = excluded.appearance_mode,
		    custom_accent = excluded.custom_accent,
		    custom_surface = excluded.custom_surface,
		    custom_css_enabled = excluded.custom_css_enabled,
		    custom_css = excluded.custom_css,
		    updated_at = excluded.updated_at`,
		userID, p.ThemeType, p.PresetID, p.AppearanceMode,
		nullableString(p.CustomAccent), nullableString(p.CustomSurface), cssEnabled, nullableString(p.CustomCSS),
		p.UpdatedAt)
	if err != nil {
		return UserPreferences{}, fmt.Errorf("upsert preferences: %w", err)
	}
	return p, nil
}

// ResetPreferences is the single operation that deletes stored custom
// settings (FR-012): custom_accent, custom_surface and custom_css are nulled,
// the overlay is disabled and the base mode returns to "preset". presetID
// and appearanceMode are applied as given — the handler validates the enums
// and resolves "omit" to the user's current values before calling. The
// language choice is not a custom setting and is kept (SetLanguage owns it).
func (s *Store) ResetPreferences(ctx context.Context, userID int64, presetID, appearanceMode string) (UserPreferences, error) {
	p := UserPreferences{
		ThemeType:      "preset",
		PresetID:       presetID,
		AppearanceMode: appearanceMode,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_preferences
		    (user_id, theme_type, preset_id, appearance_mode, custom_accent, custom_surface, custom_css_enabled, custom_css, updated_at)
		 VALUES (?, 'preset', ?, ?, NULL, NULL, 0, NULL, ?)
		 ON CONFLICT (user_id) DO UPDATE SET
		    theme_type = 'preset',
		    preset_id = excluded.preset_id,
		    appearance_mode = excluded.appearance_mode,
		    custom_accent = NULL,
		    custom_surface = NULL,
		    custom_css_enabled = 0,
		    custom_css = NULL,
		    updated_at = excluded.updated_at`,
		userID, presetID, appearanceMode, p.UpdatedAt)
	if err != nil {
		return UserPreferences{}, fmt.Errorf("reset preferences: %w", err)
	}
	return p, nil
}

// SetLanguage stores the user's language choice. A nil language clears it
// back to "no saved choice". A user with no preferences row gets one with the
// default preferences; an existing row changes only its language and
// updated_at, so the theme and custom styling are untouched.
func (s *Store) SetLanguage(ctx context.Context, userID int64, language *string) error {
	d := DefaultUserPreferences()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_preferences
		    (user_id, theme_type, preset_id, appearance_mode, custom_css_enabled, language, updated_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET
		    language = excluded.language,
		    updated_at = excluded.updated_at`,
		userID, d.ThemeType, d.PresetID, d.AppearanceMode, nullableString(language), d.UpdatedAt)
	if err != nil {
		return fmt.Errorf("set language: %w", err)
	}
	return nil
}

func nullableString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
