package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrThemeNotFound reports that no theme source, catalog entry, installed
// theme or preview matches the request (or the row belongs to another user).
var ErrThemeNotFound = errors.New("theme not found")

// ErrThemeSourceExists reports that a theme source with the name already exists.
var ErrThemeSourceExists = errors.New("theme source already exists")

// ErrThemeAlreadyInstalled reports that the user already installed this theme
// from this source (FR-013: one install per user, source and theme name).
var ErrThemeAlreadyInstalled = errors.New("theme already installed")

// ThemeSource is one admin-managed theme source (spec 043, data-model.md
// theme_sources). Spec holds the type-specific JSON settings, Verify the
// signature policy JSON. CredentialRef names the stored credential; the
// credential itself is never part of this row and is never returned by the API.
type ThemeSource struct {
	ID               int64
	Name             string
	Type             string  // "oci" | "git" | "http" | "local"
	Spec             string  // JSON, type-specific settings plus allow[] globs
	CredentialRef    *string // nullable reference to the stored credential
	Verify           *string // nullable JSON signature policy (oci only)
	RefreshIntervalS int     // seconds between automatic refreshes
	Enabled          bool
	LastSyncAt       *string // nullable RFC 3339 UTC
	LastError        *string // nullable, human-readable, no credentials
	CreatedAt        string  // RFC 3339 UTC
	UpdatedAt        string  // RFC 3339 UTC
	// ThemeCount is the number of catalog entries of this source. Filled by
	// ListThemeSources only; ignored on writes.
	ThemeCount int
}

// ThemeCatalogEntry is the cached latest version of one theme in one source
// (data-model.md theme_catalog_entries). CSS and previews are nil unless the
// read method documents otherwise; ListThemeCatalog never loads them.
type ThemeCatalogEntry struct {
	ID         int64
	SourceID   int64
	SourceName string // filled by reads that join theme_sources
	Name       string
	Version    string
	Versions   string // JSON array, semver descending
	Digest     string
	Metadata   string // JSON
	Tokens     string // JSON {light?, dark?}
	CSS        *string
	// PreviewLight and PreviewDark hold PNG or WebP bytes; nil when absent.
	PreviewLight []byte
	PreviewDark  []byte
	Status       string // "valid" | "invalid" | "unverified"
	Problems     string // JSON list of {rule, detail, line?}
	LowContrast  bool
	IndexedAt    string // RFC 3339 UTC
}

// ThemeCatalogFilter narrows ListThemeCatalog. Source matches a source name
// exactly when set; Query is a case-insensitive substring of the theme name.
type ThemeCatalogFilter struct {
	Source string
	Query  string
}

// InstalledTheme is a snapshot of a theme one user installed for their own
// account (data-model.md user_installed_themes). SourceID is nil once the
// source has been removed; SourceName is kept for display.
type InstalledTheme struct {
	ID          int64
	UserID      int64
	SourceID    *int64
	SourceName  string
	ThemeName   string
	Version     string
	Digest      string
	Metadata    string // JSON
	Tokens      string // JSON
	CSS         *string
	InstalledAt string // RFC 3339 UTC
	UpdatedAt   string // RFC 3339 UTC
}

// newThemeID returns a random positive 53-bit identifier, exact as a JSON
// number, for the theme tables. Ids are generated in Go so the same statements
// run on SQLite and PostgreSQL (migrations README rule 2).
func newThemeID() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("generate theme id: %w", err)
	}
	id := int64(binary.BigEndian.Uint64(b[:]) >> 11)
	if id == 0 {
		id = 1
	}
	return id, nil
}

// themeNow returns the current time as RFC 3339 UTC for the theme tables.
func themeNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// nullableBytes binds empty previews as SQL NULL.
func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// nullableInt64 binds a nil pointer as SQL NULL.
func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// themeSourceColumns lists the theme_sources columns in scan order.
const themeSourceColumns = `id, name, type, spec, credential_ref, verify, refresh_interval_s, enabled, last_sync_at, last_error, created_at, updated_at`

// rowScanner is the scan side shared by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanThemeSource scans one theme_sources row. extra pointers, when given,
// are scanned after the row's own columns.
func scanThemeSource(sc rowScanner, extra ...any) (ThemeSource, error) {
	var src ThemeSource
	var cred, verify, lastSync, lastErr sql.NullString
	var enabled int
	dest := []any{&src.ID, &src.Name, &src.Type, &src.Spec, &cred, &verify,
		&src.RefreshIntervalS, &enabled, &lastSync, &lastErr, &src.CreatedAt, &src.UpdatedAt}
	dest = append(dest, extra...)
	if err := sc.Scan(dest...); err != nil {
		return ThemeSource{}, err
	}
	if cred.Valid {
		src.CredentialRef = &cred.String
	}
	if verify.Valid {
		src.Verify = &verify.String
	}
	if lastSync.Valid {
		v := NormalizeTimestamp(lastSync.String)
		src.LastSyncAt = &v
	}
	if lastErr.Valid {
		src.LastError = &lastErr.String
	}
	src.Enabled = enabled != 0
	src.CreatedAt = NormalizeTimestamp(src.CreatedAt)
	src.UpdatedAt = NormalizeTimestamp(src.UpdatedAt)
	return src, nil
}

// themeSourceNameExists reports whether a source with the name is stored.
func (s *Store) themeSourceNameExists(ctx context.Context, name string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM theme_sources WHERE name = ?`, name).Scan(&n); err != nil {
		return false, fmt.Errorf("check theme source name: %w", err)
	}
	return n > 0, nil
}

// CreateThemeSource stores a new source and returns it with its generated id
// and timestamps. A zero ID is generated; CreatedAt and UpdatedAt are set to
// now. It returns ErrThemeSourceExists when the name is taken.
func (s *Store) CreateThemeSource(ctx context.Context, src ThemeSource) (ThemeSource, error) {
	exists, err := s.themeSourceNameExists(ctx, src.Name)
	if err != nil {
		return ThemeSource{}, err
	}
	if exists {
		return ThemeSource{}, ErrThemeSourceExists
	}
	if src.ID == 0 {
		if src.ID, err = newThemeID(); err != nil {
			return ThemeSource{}, err
		}
	}
	now := themeNow()
	src.CreatedAt, src.UpdatedAt = now, now
	enabled := 0
	if src.Enabled {
		enabled = 1
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO theme_sources (id, name, type, spec, credential_ref, verify, refresh_interval_s, enabled, last_sync_at, last_error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		src.ID, src.Name, src.Type, src.Spec, nullableString(src.CredentialRef), nullableString(src.Verify),
		src.RefreshIntervalS, enabled, nullableString(src.LastSyncAt), nullableString(src.LastError),
		src.CreatedAt, src.UpdatedAt); err != nil {
		return ThemeSource{}, fmt.Errorf("create theme source: %w", err)
	}
	return src, nil
}

// GetThemeSource loads a source by name, or ErrThemeNotFound.
func (s *Store) GetThemeSource(ctx context.Context, name string) (ThemeSource, error) {
	src, err := scanThemeSource(s.DB.QueryRowContext(ctx,
		`SELECT `+themeSourceColumns+` FROM theme_sources WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return ThemeSource{}, ErrThemeNotFound
	}
	if err != nil {
		return ThemeSource{}, fmt.Errorf("get theme source: %w", err)
	}
	return src, nil
}

// GetThemeSourceByID loads a source by id, or ErrThemeNotFound.
func (s *Store) GetThemeSourceByID(ctx context.Context, id int64) (ThemeSource, error) {
	src, err := scanThemeSource(s.DB.QueryRowContext(ctx,
		`SELECT `+themeSourceColumns+` FROM theme_sources WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ThemeSource{}, ErrThemeNotFound
	}
	if err != nil {
		return ThemeSource{}, fmt.Errorf("get theme source by id: %w", err)
	}
	return src, nil
}

// ListThemeSources returns every source ordered by name, each with its
// catalog entry count in ThemeCount.
func (s *Store) ListThemeSources(ctx context.Context) ([]ThemeSource, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+themeSourceColumns+`,
		        (SELECT COUNT(*) FROM theme_catalog_entries e WHERE e.source_id = theme_sources.id) AS theme_count
		   FROM theme_sources ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list theme sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ThemeSource
	for rows.Next() {
		var count int
		src, err := scanThemeSource(rows, &count)
		if err != nil {
			return nil, fmt.Errorf("scan theme source: %w", err)
		}
		src.ThemeCount = count
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list theme sources: %w", err)
	}
	return out, nil
}

// UpdateThemeSource writes the mutable settings of src (matched by ID):
// name, type, spec, credential reference, verify policy, refresh interval
// and enabled flag. It refreshes updated_at and returns ErrThemeNotFound
// when no row has the id.
func (s *Store) UpdateThemeSource(ctx context.Context, src ThemeSource) error {
	enabled := 0
	if src.Enabled {
		enabled = 1
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE theme_sources SET name = ?, type = ?, spec = ?, credential_ref = ?, verify = ?,
		        refresh_interval_s = ?, enabled = ?, updated_at = ?
		  WHERE id = ?`,
		src.Name, src.Type, src.Spec, nullableString(src.CredentialRef), nullableString(src.Verify),
		src.RefreshIntervalS, enabled, themeNow(), src.ID)
	if err != nil {
		return fmt.Errorf("update theme source: %w", err)
	}
	return requireThemeRow(res)
}

// SetThemeSourceSync records the outcome of the latest refresh: the time it
// ran and the error, if any. lastError must not contain credentials.
func (s *Store) SetThemeSourceSync(ctx context.Context, id int64, at time.Time, lastError *string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE theme_sources SET last_sync_at = ?, last_error = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339), nullableString(lastError), id)
	if err != nil {
		return fmt.Errorf("set theme source sync: %w", err)
	}
	return requireThemeRow(res)
}

// DeleteThemeSource removes a source, its catalog entries, and detaches its
// installed themes (source_id becomes NULL, so they show as source removed
// and never match a later source with the same name). Ids are compared, not
// names, so a recreated source never inherits the old installs.
func (s *Store) DeleteThemeSource(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete theme source: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM theme_catalog_entries WHERE source_id = ?`, id); err != nil {
		return fmt.Errorf("delete theme source: entries: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_installed_themes SET source_id = NULL WHERE source_id = ?`, id); err != nil {
		return fmt.Errorf("delete theme source: detach installs: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM theme_sources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete theme source: source row: %w", err)
	}
	if err := requireThemeRow(res); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete theme source: commit: %w", err)
	}
	return nil
}

// requireThemeRow maps a write that matched no row to ErrThemeNotFound.
func requireThemeRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("theme write: rows affected: %w", err)
	}
	if n == 0 {
		return ErrThemeNotFound
	}
	return nil
}

// UpsertThemeCatalogEntry inserts or replaces the entry for (SourceID, Name).
// The stored id survives re-indexing, so a theme keeps its catalog id (FR-013).
// Empty previews are stored as NULL.
func (s *Store) UpsertThemeCatalogEntry(ctx context.Context, e ThemeCatalogEntry) error {
	if e.ID == 0 {
		id, err := newThemeID()
		if err != nil {
			return err
		}
		e.ID = id
	}
	lowContrast := 0
	if e.LowContrast {
		lowContrast = 1
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO theme_catalog_entries
		    (id, source_id, name, version, versions, digest, metadata, tokens, css, preview_light, preview_dark, status, problems, low_contrast, indexed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (source_id, name) DO UPDATE SET
		    version = excluded.version,
		    versions = excluded.versions,
		    digest = excluded.digest,
		    metadata = excluded.metadata,
		    tokens = excluded.tokens,
		    css = excluded.css,
		    preview_light = excluded.preview_light,
		    preview_dark = excluded.preview_dark,
		    status = excluded.status,
		    problems = excluded.problems,
		    low_contrast = excluded.low_contrast,
		    indexed_at = excluded.indexed_at`,
		e.ID, e.SourceID, e.Name, e.Version, e.Versions, e.Digest, e.Metadata, e.Tokens, nullableString(e.CSS),
		nullableBytes(e.PreviewLight), nullableBytes(e.PreviewDark), e.Status, e.Problems, lowContrast, e.IndexedAt); err != nil {
		return fmt.Errorf("upsert theme catalog entry: %w", err)
	}
	return nil
}

// likeEscaper escapes LIKE wildcards in user input; the query uses ESCAPE '!'.
var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

// ListThemeCatalog returns catalog entries of enabled sources, ordered by
// source name then theme name. It reads the list columns and the small tokens
// JSON (the list shows the modes a theme offers); css and previews are never
// loaded here (SC-006); use GetThemeCatalogEntry and GetThemeCatalogPreview.
func (s *Store) ListThemeCatalog(ctx context.Context, filter ThemeCatalogFilter) ([]ThemeCatalogEntry, error) {
	q := `SELECT e.id, e.source_id, src.name, e.name, e.version, e.versions, e.digest, e.metadata, e.tokens,
	             e.status, e.problems, e.low_contrast, e.indexed_at
	        FROM theme_catalog_entries e
	        JOIN theme_sources src ON src.id = e.source_id
	       WHERE src.enabled = 1`
	var args []any
	if filter.Source != "" {
		q += ` AND src.name = ?`
		args = append(args, filter.Source)
	}
	if filter.Query != "" {
		q += ` AND LOWER(e.name) LIKE ? ESCAPE '!'`
		args = append(args, "%"+likeEscaper.Replace(strings.ToLower(filter.Query))+"%")
	}
	q += ` ORDER BY src.name, e.name`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list theme catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ThemeCatalogEntry
	for rows.Next() {
		var e ThemeCatalogEntry
		var lowContrast int
		if err := rows.Scan(&e.ID, &e.SourceID, &e.SourceName, &e.Name, &e.Version, &e.Versions, &e.Digest,
			&e.Metadata, &e.Tokens, &e.Status, &e.Problems, &lowContrast, &e.IndexedAt); err != nil {
			return nil, fmt.Errorf("scan theme catalog entry: %w", err)
		}
		e.LowContrast = lowContrast != 0
		e.IndexedAt = NormalizeTimestamp(e.IndexedAt)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list theme catalog: %w", err)
	}
	return out, nil
}

// GetThemeCatalogEntry loads one entry with its tokens and CSS, without the
// preview bytes (use GetThemeCatalogPreview), or ErrThemeNotFound. The entry
// is returned whatever its source's enabled state; callers decide visibility.
func (s *Store) GetThemeCatalogEntry(ctx context.Context, id int64) (ThemeCatalogEntry, error) {
	var e ThemeCatalogEntry
	var css sql.NullString
	var lowContrast int
	err := s.DB.QueryRowContext(ctx,
		`SELECT e.id, e.source_id, src.name, e.name, e.version, e.versions, e.digest, e.metadata, e.tokens, e.css,
		        e.status, e.problems, e.low_contrast, e.indexed_at
		   FROM theme_catalog_entries e
		   JOIN theme_sources src ON src.id = e.source_id
		  WHERE e.id = ?`, id).
		Scan(&e.ID, &e.SourceID, &e.SourceName, &e.Name, &e.Version, &e.Versions, &e.Digest, &e.Metadata,
			&e.Tokens, &css, &e.Status, &e.Problems, &lowContrast, &e.IndexedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ThemeCatalogEntry{}, ErrThemeNotFound
	}
	if err != nil {
		return ThemeCatalogEntry{}, fmt.Errorf("get theme catalog entry: %w", err)
	}
	if css.Valid {
		e.CSS = &css.String
	}
	e.LowContrast = lowContrast != 0
	e.IndexedAt = NormalizeTimestamp(e.IndexedAt)
	return e, nil
}

// GetThemeCatalogPreview returns the stored preview bytes for mode "light" or
// "dark". It returns ErrThemeNotFound for an unknown mode, an unknown entry,
// or an entry without that preview.
func (s *Store) GetThemeCatalogPreview(ctx context.Context, id int64, mode string) ([]byte, error) {
	var column string
	switch mode {
	case "light":
		column = "preview_light"
	case "dark":
		column = "preview_dark"
	default:
		return nil, ErrThemeNotFound
	}
	var data []byte
	err := s.DB.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT %s FROM theme_catalog_entries WHERE id = ?`, column), id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(data) == 0) {
		return nil, ErrThemeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get theme catalog preview: %w", err)
	}
	return data, nil
}

// DeleteThemeCatalogEntriesNotIn removes the source's entries whose names are
// not in names, after a re-index. An empty names list removes every entry of
// the source.
func (s *Store) DeleteThemeCatalogEntriesNotIn(ctx context.Context, sourceID int64, names []string) error {
	q := `DELETE FROM theme_catalog_entries WHERE source_id = ?`
	args := []any{sourceID}
	if len(names) > 0 {
		marks := make([]string, len(names))
		for i, name := range names {
			marks[i] = "?"
			args = append(args, name)
		}
		q += ` AND name NOT IN (` + strings.Join(marks, ", ") + `)`
	}
	if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("delete theme catalog entries: %w", err)
	}
	return nil
}

// installedThemeColumns lists the user_installed_themes columns in scan order.
const installedThemeColumns = `id, user_id, source_id, source_name, theme_name, version, digest, metadata, tokens, css, installed_at, updated_at`

// scanInstalledTheme scans one user_installed_themes row.
func scanInstalledTheme(sc rowScanner) (InstalledTheme, error) {
	var t InstalledTheme
	var sourceID sql.NullInt64
	var css sql.NullString
	if err := sc.Scan(&t.ID, &t.UserID, &sourceID, &t.SourceName, &t.ThemeName, &t.Version, &t.Digest,
		&t.Metadata, &t.Tokens, &css, &t.InstalledAt, &t.UpdatedAt); err != nil {
		return InstalledTheme{}, err
	}
	if sourceID.Valid {
		v := sourceID.Int64
		t.SourceID = &v
	}
	if css.Valid {
		t.CSS = &css.String
	}
	t.InstalledAt = NormalizeTimestamp(t.InstalledAt)
	t.UpdatedAt = NormalizeTimestamp(t.UpdatedAt)
	return t, nil
}

// InstallTheme stores a new installed theme for t.UserID. A zero ID is
// generated and the timestamps are set to now. It returns
// ErrThemeAlreadyInstalled when the user already has the same theme from
// the same source.
func (s *Store) InstallTheme(ctx context.Context, t InstalledTheme) (InstalledTheme, error) {
	if t.SourceID != nil {
		var n int
		if err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM user_installed_themes WHERE user_id = ? AND source_id = ? AND theme_name = ?`,
			t.UserID, *t.SourceID, t.ThemeName).Scan(&n); err != nil {
			return InstalledTheme{}, fmt.Errorf("check installed theme: %w", err)
		}
		if n > 0 {
			return InstalledTheme{}, ErrThemeAlreadyInstalled
		}
	}
	if t.ID == 0 {
		id, err := newThemeID()
		if err != nil {
			return InstalledTheme{}, err
		}
		t.ID = id
	}
	now := themeNow()
	t.InstalledAt, t.UpdatedAt = now, now
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_installed_themes
		    (id, user_id, source_id, source_name, theme_name, version, digest, metadata, tokens, css, installed_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, nullableInt64(t.SourceID), t.SourceName, t.ThemeName, t.Version, t.Digest,
		t.Metadata, t.Tokens, nullableString(t.CSS), t.InstalledAt, t.UpdatedAt); err != nil {
		return InstalledTheme{}, fmt.Errorf("install theme: %w", err)
	}
	return t, nil
}

// GetInstalledTheme loads one of the user's installed themes. Another user's
// id returns ErrThemeNotFound.
func (s *Store) GetInstalledTheme(ctx context.Context, userID, id int64) (InstalledTheme, error) {
	t, err := scanInstalledTheme(s.DB.QueryRowContext(ctx,
		`SELECT `+installedThemeColumns+` FROM user_installed_themes WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return InstalledTheme{}, ErrThemeNotFound
	}
	if err != nil {
		return InstalledTheme{}, fmt.Errorf("get installed theme: %w", err)
	}
	return t, nil
}

// ListInstalledThemes returns the user's installed themes ordered by source
// name then theme name.
func (s *Store) ListInstalledThemes(ctx context.Context, userID int64) ([]InstalledTheme, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+installedThemeColumns+` FROM user_installed_themes WHERE user_id = ? ORDER BY source_name, theme_name`, userID)
	if err != nil {
		return nil, fmt.Errorf("list installed themes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []InstalledTheme
	for rows.Next() {
		t, err := scanInstalledTheme(rows)
		if err != nil {
			return nil, fmt.Errorf("scan installed theme: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list installed themes: %w", err)
	}
	return out, nil
}

// UpdateInstalledTheme replaces the snapshot fields of t (matched by ID and
// UserID): version, digest, metadata, tokens, CSS and source name. The source
// link is not changed here. It returns ErrThemeNotFound when no row matches.
func (s *Store) UpdateInstalledTheme(ctx context.Context, t InstalledTheme) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE user_installed_themes SET source_name = ?, version = ?, digest = ?, metadata = ?, tokens = ?, css = ?, updated_at = ?
		  WHERE id = ? AND user_id = ?`,
		t.SourceName, t.Version, t.Digest, t.Metadata, t.Tokens, nullableString(t.CSS), themeNow(), t.ID, t.UserID)
	if err != nil {
		return fmt.Errorf("update installed theme: %w", err)
	}
	return requireThemeRow(res)
}

// DeleteInstalledTheme removes one of the user's installed themes. In the same
// transaction, any preference that selected it falls back to the Pink preset
// (FR-011); custom colors and CSS are kept. It returns ErrThemeNotFound when
// the theme is not the user's.
func (s *Store) DeleteInstalledTheme(ctx context.Context, userID, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete installed theme: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM user_installed_themes WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete installed theme: row: %w", err)
	}
	if err := requireThemeRow(res); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE user_preferences SET theme_type = 'preset', preset_id = 'pink', installed_theme_id = NULL, updated_at = ?
		  WHERE user_id = ? AND installed_theme_id = ?`,
		themeNow(), userID, id); err != nil {
		return fmt.Errorf("delete installed theme: preferences: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete installed theme: commit: %w", err)
	}
	return nil
}

// CountInstalledThemes returns how many themes the user has installed, for
// the per-user limit.
func (s *Store) CountInstalledThemes(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_installed_themes WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count installed themes: %w", err)
	}
	return n, nil
}
