package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrThemeInstallLimit reports that the user already has the maximum number of
// installed themes (spec 043, T033: 50 per user).
var ErrThemeInstallLimit = errors.New("installed theme limit reached")

// InstallThemeAndSelect stores a new installed theme for t.UserID in one
// transaction. When limit is positive, the install is refused once the user
// holds that many themes. When selectTheme is true, the user's preferences
// select the new theme in the same transaction; the base preset, appearance
// and custom colors and CSS keep their stored values (FR-012), or take the
// column defaults for a user without a preferences row.
//
// It returns ErrThemeAlreadyInstalled when the user already has the same theme
// from the same source, and ErrThemeInstallLimit when the limit is reached.
func (s *Store) InstallThemeAndSelect(ctx context.Context, t InstalledTheme, limit int, selectTheme bool) (InstalledTheme, error) {
	if t.ID == 0 {
		id, err := newThemeID()
		if err != nil {
			return InstalledTheme{}, err
		}
		t.ID = id
	}
	now := themeNow()
	t.InstalledAt, t.UpdatedAt = now, now

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return InstalledTheme{}, fmt.Errorf("install theme: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if t.SourceID != nil {
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM user_installed_themes WHERE user_id = ? AND source_id = ? AND theme_name = ?`,
			t.UserID, *t.SourceID, t.ThemeName).Scan(&n); err != nil {
			return InstalledTheme{}, fmt.Errorf("install theme: check duplicate: %w", err)
		}
		if n > 0 {
			return InstalledTheme{}, ErrThemeAlreadyInstalled
		}
	}
	if limit > 0 {
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM user_installed_themes WHERE user_id = ?`, t.UserID).Scan(&n); err != nil {
			return InstalledTheme{}, fmt.Errorf("install theme: count: %w", err)
		}
		if n >= limit {
			return InstalledTheme{}, ErrThemeInstallLimit
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_installed_themes
		    (id, user_id, source_id, source_name, theme_name, version, digest, metadata, tokens, css, installed_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, nullableInt64(t.SourceID), t.SourceName, t.ThemeName, t.Version, t.Digest,
		t.Metadata, t.Tokens, nullableString(t.CSS), t.InstalledAt, t.UpdatedAt); err != nil {
		return InstalledTheme{}, fmt.Errorf("install theme: insert: %w", err)
	}
	if selectTheme {
		d := DefaultUserPreferences()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_preferences
			    (user_id, theme_type, preset_id, appearance_mode, custom_css_enabled, installed_theme_id, updated_at)
			 VALUES (?, 'installed', ?, ?, 0, ?, ?)
			 ON CONFLICT (user_id) DO UPDATE SET
			    theme_type = 'installed',
			    installed_theme_id = excluded.installed_theme_id,
			    updated_at = excluded.updated_at`,
			t.UserID, d.PresetID, d.AppearanceMode, t.ID, now); err != nil {
			return InstalledTheme{}, fmt.Errorf("install theme: select: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return InstalledTheme{}, fmt.Errorf("install theme: commit: %w", err)
	}
	return t, nil
}

// GetThemeCatalogEntryByName loads the cached entry of one theme in one source,
// matched by source id and theme name, or ErrThemeNotFound. Like
// GetThemeCatalogEntry, it returns the entry whatever its source's enabled
// state; callers decide visibility.
func (s *Store) GetThemeCatalogEntryByName(ctx context.Context, sourceID int64, name string) (ThemeCatalogEntry, error) {
	var e ThemeCatalogEntry
	var css sql.NullString
	var lowContrast int
	err := s.DB.QueryRowContext(ctx,
		`SELECT e.id, e.source_id, src.name, e.name, e.version, e.versions, e.digest, e.metadata, e.tokens, e.css,
		        e.status, e.problems, e.low_contrast, e.indexed_at
		   FROM theme_catalog_entries e
		   JOIN theme_sources src ON src.id = e.source_id
		  WHERE e.source_id = ? AND e.name = ?`, sourceID, name).
		Scan(&e.ID, &e.SourceID, &e.SourceName, &e.Name, &e.Version, &e.Versions, &e.Digest, &e.Metadata,
			&e.Tokens, &css, &e.Status, &e.Problems, &lowContrast, &e.IndexedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ThemeCatalogEntry{}, ErrThemeNotFound
	}
	if err != nil {
		return ThemeCatalogEntry{}, fmt.Errorf("get theme catalog entry by name: %w", err)
	}
	if css.Valid {
		e.CSS = &css.String
	}
	e.LowContrast = lowContrast != 0
	e.IndexedAt = NormalizeTimestamp(e.IndexedAt)
	return e, nil
}
