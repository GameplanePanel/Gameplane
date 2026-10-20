// Package db wraps the API's user/session/audit store. SQLite is the
// default so homelab installs have zero external dependencies; Postgres
// is opt-in via --db-driver=postgres --db-dsn=...
//
// Schema migrations live alongside this package as plain .sql files,
// applied in version order at startup: migrations 001-012 exist once per
// dialect (migrations/sqlite, migrations/postgres); every later migration
// is a single portable file in migrations/common that both drivers run.
// Runtime queries are written with `?` placeholders; the Postgres
// connection rewrites them to $n (see Rebind).
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	// Pure-Go SQLite driver; registered as "sqlite" via database/sql.
	_ "modernc.org/sqlite"
)

// migrations holds the per-driver legacy sets and the shared portable set;
// see migrations/README.md for which directory a new migration goes in.
//
//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql migrations/common
var migrations embed.FS

// Store wraps the database connection and driver name, providing methods
// for migrations and access to user/session/audit data.
type Store struct {
	DB         *sql.DB
	Driver     string
	userMgmtMu sync.Mutex // Serializes changes that can remove a user's user-management access.
}

// Open connects to a database using the specified driver and DSN.
// Supported drivers are "sqlite" and "postgres".
func Open(ctx context.Context, driver, dsn string) (*Store, error) {
	switch driver {
	case "sqlite":
		// Before opening the database, attempt to adopt any legacy kestrel.db file.
		// This is safe because we only rename if the target does not exist.
		if err := adoptLegacySQLite(dsn); err != nil {
			return nil, err
		}

		// Add a busy timeout to the DSN if not already present, so concurrent
		// processes (e.g., API and bootstrap-admin) waiting for locks don't fail immediately.
		dsn = withSQLiteBusyTimeout(dsn)

		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1) // modernc sqlite + WAL is safe with multiple readers; cap for simplicity
		if err := db.PingContext(ctx); err != nil {
			return nil, err
		}
		return &Store{DB: db, Driver: "sqlite"}, nil
	case "postgres":
		// pgx driver lives in a separate build tag to keep the default
		// binary small; see db_postgres.go for the registration.
		return openPostgres(ctx, dsn)
	default:
		return nil, fmt.Errorf("unknown db driver %q", driver)
	}
}

// Close closes the database connection.
func (s *Store) Close() error { return s.DB.Close() }

// LockUserManagement serializes changes that can remove a user's
// user-management access (role permission edits, primary-role changes,
// user deletion). Hold it from the last-user-manager check through the
// commit of the change so two requests can't both pass the check. It
// covers one API process; the SQLite install runs a single API replica.
func (s *Store) LockUserManagement() (unlock func()) {
	s.userMgmtMu.Lock()
	return s.userMgmtMu.Unlock
}

// Migrate applies every pending migration for s.Driver, in version order:
// first the driver's legacy set (migrations/<driver>/, versions 001-012,
// written per dialect), then the shared set (migrations/common/, 013
// onward, portable SQL that both drivers run unchanged). See
// migrations/README.md. Each file runs in a single transaction; failures
// are fatal. Afterwards it seeds the telemetry_state row once (see
// seedTelemetryState), which is why it first notes whether the database was
// fresh.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`,
	); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// A database whose schema_migrations is still empty is a fresh install;
	// the telemetry consent seeding below depends on telling it apart from an
	// upgrade. Counting here, before anything is applied, keeps the answer
	// right whichever entrypoint (bootstrap-admin or serve) migrates first.
	var recorded int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&recorded); err != nil {
		return fmt.Errorf("count schema_migrations: %w", err)
	}
	fresh := recorded == 0

	files, err := migrationFiles(migrations, s.Driver)
	if err != nil {
		return err
	}

	for _, f := range files {
		applied, err := s.migrationApplied(ctx, f.name)
		if err != nil {
			return fmt.Errorf("migration %s: %w", f.name, err)
		}
		if applied {
			continue
		}
		content, err := fs.ReadFile(migrations, f.path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f.path, err)
		}
		if err := s.runMigration(ctx, f.name, string(content)); err != nil {
			return fmt.Errorf("migration %s: %w", f.name, err)
		}
	}
	return s.seedTelemetryState(ctx, fresh)
}

// Migration directories inside the embedded FS. legacyMigrationDirs maps a
// driver to its per-dialect set; sharedMigrationDir holds the portable set.
const sharedMigrationDir = "migrations/common"

var legacyMigrationDirs = map[string]string{
	"sqlite":   "migrations/sqlite",
	"postgres": "migrations/postgres",
}

// migrationFile is one migration to apply. name is the bare filename, which
// is also the version recorded in schema_migrations; path is where it lives
// in the embedded FS.
type migrationFile struct {
	name string
	path string
}

// migrationFiles lists the migrations for driver: its legacy set followed
// by the shared set, sorted by filename (the numeric version prefix). It
// rejects a version number that appears in both sets.
func migrationFiles(fsys fs.FS, driver string) ([]migrationFile, error) {
	legacyDir, ok := legacyMigrationDirs[driver]
	if !ok {
		return nil, fmt.Errorf("no migrations for db driver %q", driver)
	}
	legacy, err := listSQL(fsys, legacyDir)
	if err != nil {
		return nil, err
	}
	shared, err := listSQL(fsys, sharedMigrationDir)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]string, len(legacy)+len(shared))
	all := make([]migrationFile, 0, len(legacy)+len(shared))
	for _, f := range append(legacy, shared...) {
		v := migrationVersion(f.name)
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migration version %s is used by both %s and %s", v, prev, f.path)
		}
		seen[v] = f.path
		all = append(all, f)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].name < all[j].name })
	return all, nil
}

// listSQL returns the .sql files directly under dir. A missing directory
// is treated as empty.
func listSQL(fsys fs.FS, dir string) ([]migrationFile, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list migrations in %s: %w", dir, err)
	}
	out := make([]migrationFile, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			out = append(out, migrationFile{name: e.Name(), path: dir + "/" + e.Name()})
		}
	}
	return out, nil
}

// migrationVersion is the leading run of digits in a migration filename
// ("013" for "013_add_foo.sql"); the whole name when it has none.
func migrationVersion(name string) string {
	i := 0
	for i < len(name) && name[i] >= '0' && name[i] <= '9' {
		i++
	}
	if i == 0 {
		return name
	}
	return name[:i]
}

func (s *Store) migrationApplied(ctx context.Context, name string) (bool, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE version=?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// sqliteTimestampLayout is the text format SQLite's datetime('now')
// produces ("YYYY-MM-DD HH:MM:SS", UTC). Timestamps that columns used to
// get from datetime('now') are now generated in Go with this layout and
// bound as parameters, so the stored values are unchanged on SQLite and
// identical on Postgres.
const sqliteTimestampLayout = "2006-01-02 15:04:05"

// NowTimestamp returns the current UTC time in sqliteTimestampLayout, for
// the columns (users.updated_at, roles.updated_at, config.updated_at,
// schema_migrations.applied_at) that have always stored that format.
func NowTimestamp() string {
	return time.Now().UTC().Format(sqliteTimestampLayout)
}

// NormalizeTimestamp converts a stored timestamp to RFC3339 UTC. Rows written
// by Go that use NowTimestamp are in sqliteTimestampLayout; migrations that
// used datetime('now')/to_char stored legacy naive "YYYY-MM-DD HH:MM:SS" values.
// This function converts both to RFC3339 UTC for consistent API responses,
// so web clients (which parse with new Date()) receive unambiguous UTC strings.
// Falls back to the raw value if it matches neither format, rather than losing data.
func NormalizeTimestamp(raw string) string {
	if raw == "" {
		return ""
	}
	// Try RFC3339 first (already normalized, or from share_links/audit).
	if _, err := time.Parse(time.RFC3339, raw); err == nil {
		return raw
	}
	// Try naive format and convert.
	if t, err := time.Parse(sqliteTimestampLayout, raw); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	// Fall back to raw (data preserved, but still unparseable).
	return raw
}

func (s *Store) runMigration(ctx context.Context, name, sqlText string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range splitStatements(sqlText) {
		if isCommentOnly(stmt) {
			continue
		}
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, name, NowTimestamp(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// isCommentOnly reports whether stmt holds nothing but blank lines and
// `--` comments (e.g. 008_captures_rbac.sql), so there is nothing to run.
func isCommentOnly(stmt string) bool {
	for line := range strings.SplitSeq(stmt, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			return false
		}
	}
	return true
}

// splitStatements is a quick-and-dirty splitter that breaks on ";\n".
// Good enough for the short migrations we'll ship; replace with a real
// parser if we start needing dollar-quoted PL/pgSQL blocks.
func splitStatements(s string) []string {
	parts := strings.Split(s, ";\n")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// sqlitePath extracts the filesystem path from a SQLite DSN.
// Returns "" for non-file DSNs (e.g., :memory:, file::memory:).
// Handles DSN formats like "file:/path/db.db?_pragma=..." and bare "/path/db.db".
func sqlitePath(dsn string) string {
	// Strip the "file:" prefix if present.
	dsn = strings.TrimPrefix(dsn, "file:")

	// If it's a memory database, return empty.
	if dsn == ":memory:" || strings.HasPrefix(dsn, ":memory:") {
		return ""
	}

	// Strip query parameters (everything from the first ?).
	if idx := strings.IndexByte(dsn, '?'); idx != -1 {
		dsn = dsn[:idx]
	}

	// If nothing remains, it's not a valid file path.
	if dsn == "" {
		return ""
	}

	return dsn
}

// withSQLiteBusyTimeout appends a busy_timeout pragma to the DSN if one is not
// already present. This allows multiple processes (e.g., the API server and
// bootstrap-admin both accessing the same SQLite file) to wait for locks instead
// of failing immediately with SQLITE_BUSY. The timeout is set to 5000 milliseconds.
// Only DSNs that already contain a _pragma parameter value assigning busy_timeout
// (busy_timeout(N) or busy_timeout=N, case-insensitive) are returned unchanged; bare
// reads, near-match names and file paths containing "busy_timeout" do not prevent
// pragma addition.
func withSQLiteBusyTimeout(dsn string) string {
	// Find the query string separator.
	idx := strings.IndexByte(dsn, '?')
	var queryStr string

	if idx == -1 {
		// No query string; add one.
		return dsn + "?_pragma=busy_timeout(5000)"
	}

	// Extract the query part (everything after the ?).
	queryStr = dsn[idx+1:]

	// Parse the query parameters.
	params, err := url.ParseQuery(queryStr)
	if err != nil {
		// If parsing fails, append the pragma (safer than skipping).
		return dsn + "&_pragma=busy_timeout(5000)"
	}

	// Check if a _pragma parameter value contains a busy_timeout assignment (case-insensitive).
	// Only match assignments with values: busy_timeout(...) or busy_timeout=...
	if pragmaValues, ok := params["_pragma"]; ok {
		for _, val := range pragmaValues {
			lower := strings.ToLower(strings.TrimSpace(val))
			rest, found := strings.CutPrefix(lower, "busy_timeout")
			rest = strings.TrimSpace(rest)
			if found && (strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, "(")) &&
				strings.Trim(rest, "=() ") != "" {
				// Already present; return unchanged.
				return dsn
			}
		}
	}

	// busy_timeout not found in pragmas; append it.
	return dsn + "&_pragma=busy_timeout(5000)"
}

// adoptLegacySQLite renames kestrel.db to the target DSN path if the target
// doesn't exist but the legacy file does. This handles Kestrel → Gameplane
// upgrades where the DSN changed from kestrel.db to gameplane.db.
// Also moves the -wal WAL sidecar if present (but not -shm, which is rebuilt).
// Returns a fatal error if the rename fails; silently succeeds if either
// the target exists (do not overwrite) or the legacy file is absent.
func adoptLegacySQLite(dsn string) error {
	targetPath := sqlitePath(dsn)

	// Not a file-based SQLite database; nothing to adopt.
	if targetPath == "" {
		return nil
	}

	// Check if target already exists. If it does, never overwrite.
	if _, err := os.Stat(targetPath); err == nil {
		// Target exists; nothing to do.
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// Some other error (permission, etc.); propagate it.
		return fmt.Errorf("checking target file %s: %w", targetPath, err)
	}

	// Target does not exist. Check for legacy kestrel.db in the same directory.
	dir := filepath.Dir(targetPath)
	legacyPath := filepath.Join(dir, "kestrel.db")

	legacyInfo, err := os.Stat(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		// Legacy file does not exist either; this is a fresh install.
		return nil
	} else if err != nil {
		// Some error other than "not exist"; propagate it.
		return fmt.Errorf("checking legacy file %s: %w", legacyPath, err)
	}

	// Guard: legacy must be a regular file, not a directory or other type.
	// A directory would be renamed into place, then sql.Open would fail confusingly.
	if !legacyInfo.Mode().IsRegular() {
		// Not a regular file; skip adoption silently (leave it alone).
		return nil
	}

	// Legacy file exists and target does not. Move the -wal sidecar FIRST (if present),
	// then the main database file. This ordering ensures that a partial failure always
	// leaves the original kestrel.db intact, so the next startup re-adopts cleanly
	// instead of silently skipping adoption with an orphaned WAL.
	legacyWAL := legacyPath + "-wal"
	targetWAL := targetPath + "-wal"
	if _, err := os.Stat(legacyWAL); err == nil {
		// WAL sidecar exists; move it first.
		if err := os.Rename(legacyWAL, targetWAL); err != nil {
			return fmt.Errorf("rename %s to %s: %w", legacyWAL, targetWAL, err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Some error other than "not exist"; propagate it.
		return fmt.Errorf("checking legacy WAL %s: %w", legacyWAL, err)
	}

	// Now rename the main database file (this is the point of no return for the rename pair).
	if err := os.Rename(legacyPath, targetPath); err != nil {
		return fmt.Errorf("rename %s to %s: %w", legacyPath, targetPath, err)
	}

	// Note: -shm (shared memory index) is not moved because SQLite rebuilds it automatically.
	// Moving it adds a failure mode for zero benefit.

	slog.Warn("adopted legacy SQLite database", "old", legacyPath, "new", targetPath)
	return nil
}
