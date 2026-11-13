package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen_UnknownDriver(t *testing.T) {
	_, err := Open(t.Context(), "weird", "")
	if err == nil || !strings.Contains(err.Error(), "unknown db driver") {
		t.Fatalf("got %v", err)
	}
}

func TestOpen_SQLiteAndMigrate(t *testing.T) {
	s, err := Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.Driver != "sqlite" {
		t.Fatalf("driver=%q", s.Driver)
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Idempotent: second Migrate is a no-op.
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	// Schema is in place.
	var n int
	if err := s.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n < 3 {
		t.Fatalf("expected >=3 migrations applied, got %d", n)
	}
}

func TestMigrate_SeedsBuiltinRoles(t *testing.T) {
	s, err := Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The three built-in roles exist and are flagged builtin.
	for _, name := range []string{"admin", "operator", "viewer"} {
		var builtin int
		if err := s.DB.QueryRowContext(t.Context(), `SELECT builtin FROM roles WHERE name = ?`, name).Scan(&builtin); err != nil {
			t.Fatalf("role %q missing: %v", name, err)
		}
		if builtin != 1 {
			t.Errorf("role %q builtin=%d, want 1", name, builtin)
		}
	}

	// admin holds the wildcard; viewer is read-only; operator can write servers.
	for _, tc := range []struct {
		role, perm string
	}{
		{"admin", "*"},
		{"operator", "servers:write"},
		{"operator", "backups:restore"},
		{"viewer", "servers:read"},
	} {
		var got int
		if err := s.DB.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM role_permissions WHERE role_name = ? AND permission = ?`,
			tc.role, tc.perm).Scan(&got); err != nil {
			t.Fatalf("query %s/%s: %v", tc.role, tc.perm, err)
		}
		if got != 1 {
			t.Errorf("role %q missing permission %q", tc.role, tc.perm)
		}
	}

	// viewer must not hold any write/admin permission.
	var writes int
	if err := s.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM role_permissions WHERE role_name = 'viewer'
		   AND permission NOT LIKE '%:read'`).Scan(&writes); err != nil {
		t.Fatalf("viewer writes query: %v", err)
	}
	if writes != 0 {
		t.Errorf("viewer holds %d non-read permissions, want 0", writes)
	}
}

func TestOpen_SQLiteBadDSN(t *testing.T) {
	// modernc sqlite tolerates almost any DSN at Open; pinging is what
	// fails for an unwritable file. Use a path that can't be opened.
	_, err := Open(t.Context(), "sqlite", "file:/nonexistent/dir/x.db?_journal=BAD")
	if err == nil {
		t.Fatal("expected error for unwritable path")
	}
}

func TestRunMigration_RollbackOnFailure(t *testing.T) {
	s, err := Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Force a failure by running invalid SQL via runMigration.
	err = s.runMigration(t.Context(), "test_bad.sql", "NOT VALID SQL;\n")
	if err == nil {
		t.Fatal("expected migration to fail")
	}
}

func TestSplitStatements(t *testing.T) {
	in := "SELECT 1;\nSELECT 2;\n  \nSELECT 3"
	got := splitStatements(in)
	want := []string{"SELECT 1", "SELECT 2", "SELECT 3"}
	if len(got) != len(want) {
		t.Fatalf("got %d parts: %+v", len(got), got)
	}
	for i, s := range want {
		if got[i] != s {
			t.Errorf("[%d] %q != %q", i, got[i], s)
		}
	}
}

func TestMigrationApplied(t *testing.T) {
	s, _ := Open(t.Context(), "sqlite", ":memory:")
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got, err := s.migrationApplied(t.Context(), "001_init.sql")
	if err != nil || !got {
		t.Fatalf("applied=%v err=%v", got, err)
	}
	got, err = s.migrationApplied(t.Context(), "999_never.sql")
	if err != nil || got {
		t.Fatalf("expected false for missing, got %v err=%v", got, err)
	}
}

// TestSqlitePath tests DSN path extraction for various formats.
func TestSqlitePath(t *testing.T) {
	tests := []struct {
		dsn  string
		want string
	}{
		{`file:/data/gameplane.db?_pragma=journal_mode(WAL)`, `/data/gameplane.db`},
		{`/data/gameplane.db`, `/data/gameplane.db`},
		{`/data/gameplane.db?mode=rwc`, `/data/gameplane.db`},
		{`:memory:`, ""},
		{`file::memory:`, ""},
		{`file:/tmp/test.db`, `/tmp/test.db`},
		{`file:/tmp/test.db?journal_mode=WAL&_pragma=cache_size(5000)`, `/tmp/test.db`},
		{``, ""},
	}
	for _, tc := range tests {
		t.Run(tc.dsn, func(t *testing.T) {
			got := sqlitePath(tc.dsn)
			if got != tc.want {
				t.Errorf("sqlitePath(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}

// TestAdoptLegacySQLite_TargetAbsentLegacyPresent tests adoption of kestrel.db.
func TestAdoptLegacySQLite_TargetAbsentLegacyPresent(t *testing.T) {
	tmpdir := t.TempDir()

	// Create a legacy kestrel.db with a table and a row.
	legacyPath := filepath.Join(tmpdir, "kestrel.db")
	legacyDB, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatalf("create legacy db: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE test_data (id INTEGER, name TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `INSERT INTO test_data VALUES (1, 'alice')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	legacyDB.Close()

	// Now adopt the legacy file to gameplane.db.
	targetPath := filepath.Join(tmpdir, "gameplane.db")
	dsn := "file:" + targetPath + "?_pragma=journal_mode(WAL)"
	if err := adoptLegacySQLite(dsn); err != nil {
		t.Fatalf("adoptLegacySQLite: %v", err)
	}

	// Verify target now exists.
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("target file not found: %v", err)
	}

	// Verify legacy is gone.
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy file still exists or unexpected error: %v", err)
	}

	// Verify data survived the rename.
	targetDB, err := sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()
	var id int
	var name string
	if err := targetDB.QueryRowContext(t.Context(), `SELECT id, name FROM test_data WHERE id = 1`).Scan(&id, &name); err != nil {
		t.Fatalf("query target: %v", err)
	}
	if id != 1 || name != "alice" {
		t.Errorf("data mismatch: got (%d, %q), want (1, alice)", id, name)
	}
}

// TestAdoptLegacySQLite_Sidecars tests that the -wal sidecar moves but -shm is never moved.
func TestAdoptLegacySQLite_Sidecars(t *testing.T) {
	tmpdir := t.TempDir()

	legacyPath := filepath.Join(tmpdir, "kestrel.db")
	targetPath := filepath.Join(tmpdir, "gameplane.db")

	// Create legacy DB with WAL to generate sidecars.
	legacyDB, err := sql.Open("sqlite", legacyPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE t (id INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	legacyDB.Close()

	// Manually create both -wal and -shm sidecar files for testing.
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecarPath := legacyPath + suffix
		if f, err := os.Create(sidecarPath); err == nil {
			f.Close()
		}
	}

	dsn := "file:" + targetPath + "?_pragma=journal_mode(WAL)"
	if err := adoptLegacySQLite(dsn); err != nil {
		t.Fatalf("adoptLegacySQLite: %v", err)
	}

	// -wal must be moved to the target.
	legacyWAL := legacyPath + "-wal"
	targetWAL := targetPath + "-wal"
	if _, err := os.Stat(targetWAL); err != nil {
		t.Errorf("target -wal not found: %v", err)
	}
	if _, err := os.Stat(legacyWAL); !os.IsNotExist(err) {
		t.Errorf("legacy -wal still exists: %v", err)
	}

	// -shm must NOT be moved (it's rebuilt, and moving it adds failure modes for zero benefit).
	legacySHM := legacyPath + "-shm"
	targetSHM := targetPath + "-shm"
	if _, err := os.Stat(targetSHM); !os.IsNotExist(err) {
		t.Errorf("target -shm should not exist but does: %v", err)
	}
	if _, err := os.Stat(legacySHM); err != nil {
		t.Errorf("legacy -shm should still exist but doesn't: %v", err)
	}
}

// TestAdoptLegacySQLite_LegacyIsDirectory tests that if kestrel.db is a directory,
// adoption is skipped silently (no error, and the directory is left untouched).
func TestAdoptLegacySQLite_LegacyIsDirectory(t *testing.T) {
	tmpdir := t.TempDir()

	legacyPath := filepath.Join(tmpdir, "kestrel.db")
	targetPath := filepath.Join(tmpdir, "gameplane.db")

	// Create kestrel.db as a directory instead of a file.
	if err := os.Mkdir(legacyPath, 0755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}

	// Create a marker file inside to verify the directory is untouched.
	markerPath := filepath.Join(legacyPath, "marker")
	if f, err := os.Create(markerPath); err == nil {
		f.Close()
	}

	// Adoption should succeed silently (no error).
	dsn := "file:" + targetPath
	if err := adoptLegacySQLite(dsn); err != nil {
		t.Fatalf("adoptLegacySQLite: %v", err)
	}

	// Target should not have been created.
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("target should not exist after skipping adoption: %v", err)
	}

	// Legacy directory should still exist with its marker file.
	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("legacy directory should still exist: %v", err)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("legacy directory marker should still exist: %v", err)
	}
}

// TestAdoptLegacySQLite_TargetExists tests that we never overwrite an existing target.
func TestAdoptLegacySQLite_TargetExists(t *testing.T) {
	tmpdir := t.TempDir()

	legacyPath := filepath.Join(tmpdir, "kestrel.db")
	targetPath := filepath.Join(tmpdir, "gameplane.db")

	// Create both legacy and target files with distinct content.
	legacyDB, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE legacy_data (value TEXT)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `INSERT INTO legacy_data VALUES ('from_legacy')`); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	legacyDB.Close()

	targetDB, err := sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := targetDB.ExecContext(t.Context(), `CREATE TABLE target_data (value TEXT)`); err != nil {
		t.Fatalf("create target table: %v", err)
	}
	if _, err := targetDB.ExecContext(t.Context(), `INSERT INTO target_data VALUES ('from_target')`); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	targetDB.Close()

	// Attempt adoption.
	dsn := "file:" + targetPath
	if err := adoptLegacySQLite(dsn); err != nil {
		t.Fatalf("adoptLegacySQLite: %v", err)
	}

	// Verify target still has its original data.
	targetDB, err = sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer targetDB.Close()
	var val string
	if err := targetDB.QueryRowContext(t.Context(), `SELECT value FROM target_data WHERE value = 'from_target'`).Scan(&val); err != nil {
		t.Fatalf("target data lost: %v", err)
	}

	// Verify legacy still exists (not overwritten).
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy file missing: %v", err)
	}
}

// TestAdoptLegacySQLite_NeitherPresent tests fresh install behavior.
func TestAdoptLegacySQLite_NeitherPresent(t *testing.T) {
	tmpdir := t.TempDir()
	targetPath := filepath.Join(tmpdir, "gameplane.db")

	dsn := "file:" + targetPath + "?_pragma=journal_mode(WAL)"
	if err := adoptLegacySQLite(dsn); err != nil {
		t.Fatalf("adoptLegacySQLite: %v", err)
	}

	// Opening the database should create a fresh one without error.
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open after adopt: %v", err)
	}
	defer db.Close()

	// Verify we can create a table in the fresh database.
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE test (id INTEGER)`); err != nil {
		t.Fatalf("create table in fresh db: %v", err)
	}
}

// TestAdoptLegacySQLite_MemoryDatabase tests that memory databases are untouched.
func TestAdoptLegacySQLite_MemoryDatabase(t *testing.T) {
	// These should be no-ops and not error.
	for _, dsn := range []string{`:memory:`, `file::memory:`} {
		if err := adoptLegacySQLite(dsn); err != nil {
			t.Errorf("adoptLegacySQLite(%q): %v", dsn, err)
		}
	}
}

// TestAdoptLegacySQLite_PostgresNotAffected tests that adoptLegacySQLite
// is only called for sqlite (already tested in Open), but we verify sqlitePath
// returns "" for empty/invalid DSNs to confirm the guard.
func TestAdoptLegacySQLite_EmptyDSN(t *testing.T) {
	if err := adoptLegacySQLite(""); err != nil {
		t.Errorf("adoptLegacySQLite(empty): %v", err)
	}
}

// TestWithSQLiteBusyTimeout tests DSN transformation for busy_timeout pragma.
func TestWithSQLiteBusyTimeout(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "DSN with existing pragma, no query string",
			dsn:  "file:/data/g.db?_pragma=journal_mode(WAL)",
			want: "file:/data/g.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		},
		{
			name: "bare path, no query string",
			dsn:  "/data/g.db",
			want: "/data/g.db?_pragma=busy_timeout(5000)",
		},
		{
			name: "DSN already containing busy_timeout (lowercase)",
			dsn:  "file:/data/g.db?_pragma=busy_timeout(1000)",
			want: "file:/data/g.db?_pragma=busy_timeout(1000)",
		},
		{
			name: "DSN with BUSY_TIMEOUT (uppercase, case-insensitive check)",
			dsn:  "file:/data/g.db?_pragma=BUSY_TIMEOUT(1000)",
			want: "file:/data/g.db?_pragma=BUSY_TIMEOUT(1000)",
		},
		{
			name: "DSN with Busy_Timeout (mixed case)",
			dsn:  "file:/data/g.db?_pragma=Busy_Timeout(500)",
			want: "file:/data/g.db?_pragma=Busy_Timeout(500)",
		},
		{
			name: "memory database",
			dsn:  ":memory:",
			want: ":memory:?_pragma=busy_timeout(5000)",
		},
		{
			name: "file:memory: database",
			dsn:  "file::memory:",
			want: "file::memory:?_pragma=busy_timeout(5000)",
		},
		{
			name: "path containing busy_timeout without pragma query parameter",
			dsn:  "/data/busy_timeout.db?_pragma=journal_mode(WAL)",
			want: "/data/busy_timeout.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		},
		{
			name: "DSN with another pragma (foreign_keys) should add busy_timeout",
			dsn:  "file:/data/g.db?_pragma=foreign_keys(1)",
			want: "file:/data/g.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		},
		{
			name: "DSN with _pragma=busy_timeout using equals format",
			dsn:  "file:/data/g.db?_pragma=busy_timeout=5000",
			want: "file:/data/g.db?_pragma=busy_timeout=5000",
		},
		{
			name: "unrelated query parameter starting with busy_timeout should add pragma",
			dsn:  "file:/data/g.db?foo=busy_timeout(1)",
			want: "file:/data/g.db?foo=busy_timeout(1)&_pragma=busy_timeout(5000)",
		},
		{
			name: "bare pragma without assignment should add timeout",
			dsn:  "file:/data/g.db?_pragma=busy_timeout",
			want: "file:/data/g.db?_pragma=busy_timeout&_pragma=busy_timeout(5000)",
		},
		{
			name: "near-match pragma should add timeout",
			dsn:  "file:/data/g.db?_pragma=busy_timeout_extra(1)",
			want: "file:/data/g.db?_pragma=busy_timeout_extra(1)&_pragma=busy_timeout(5000)",
		},
		{
			name: "empty assignment should add timeout",
			dsn:  "file:/data/g.db?_pragma=busy_timeout()",
			want: "file:/data/g.db?_pragma=busy_timeout()&_pragma=busy_timeout(5000)",
		},
		{
			name: "spaced assignment is unchanged",
			dsn:  "file:/data/g.db?_pragma=busy_timeout%20=%2010000",
			want: "file:/data/g.db?_pragma=busy_timeout%20=%2010000",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := withSQLiteBusyTimeout(tc.dsn)
			if got != tc.want {
				t.Errorf("withSQLiteBusyTimeout(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}

// TestOpen_SQLiteAppliesBusyTimeout verifies that Open applies busy_timeout
// to the DSN and that PRAGMA busy_timeout returns the configured value.
func TestOpen_SQLiteAppliesBusyTimeout(t *testing.T) {
	tmpdir := t.TempDir()
	dbPath := filepath.Join(tmpdir, "test.db")
	dsn := "file:" + dbPath

	// Open the database; Open should add busy_timeout.
	store, err := Open(t.Context(), "sqlite", dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Query the PRAGMA to verify it was applied.
	var timeout int
	if err := store.DB.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("PRAGMA query: %v", err)
	}

	if timeout != 5000 {
		t.Errorf("PRAGMA busy_timeout = %d, want 5000", timeout)
	}
}

// TestMigrate_AddsAuditChainColumns verifies migration 005 adds prev_hash and
// hash columns to audit_events (used by the audit-log tamper-evidence hash
// chain in api/internal/audit), and applies cleanly on top of 001-004.
func TestMigrate_AddsAuditChainColumns(t *testing.T) {
	s, err := Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := s.DB.ExecContext(t.Context(),
		`INSERT INTO audit_events(ts, actor, method, path, status, prev_hash, hash)
		 VALUES (?, 'a', 'POST', '/x', 200, 'p', 'h')`,
		"2026-01-01T00:00:00Z",
	); err != nil {
		t.Fatalf("insert with chain columns: %v", err)
	}
	var prevHash, hash string
	if err := s.DB.QueryRowContext(t.Context(), `SELECT prev_hash, hash FROM audit_events LIMIT 1`).Scan(&prevHash, &hash); err != nil {
		t.Fatalf("query: %v", err)
	}
	if prevHash != "p" || hash != "h" {
		t.Fatalf("prev_hash=%q hash=%q, want p/h", prevHash, hash)
	}

	applied, err := s.migrationApplied(t.Context(), "005_audit_chain.sql")
	if err != nil || !applied {
		t.Fatalf("005_audit_chain.sql applied=%v err=%v", applied, err)
	}
}

// TestOpen_AdoptsLegacySQLite verifies that Open() triggers adoption
// and migrations run successfully on the adopted file.
func TestOpen_AdoptsLegacySQLite(t *testing.T) {
	tmpdir := t.TempDir()

	// Create legacy database with schema and data.
	legacyPath := filepath.Join(tmpdir, "kestrel.db")
	legacyDB, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	// Create the schema_migrations table so Open/Migrate recognizes it as initialized.
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `INSERT INTO schema_migrations VALUES ('001_init.sql', datetime('now'))`); err != nil {
		t.Fatalf("seed migration: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE, role TEXT NOT NULL DEFAULT 'viewer')`); err != nil {
		t.Fatalf("create users: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `INSERT INTO users(id, username, role) VALUES (1, 'Alice', 'admin')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE audit_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         TEXT NOT NULL DEFAULT (datetime('now')),
    actor      TEXT NOT NULL,
    method     TEXT NOT NULL,
    path       TEXT NOT NULL,
    target     TEXT,
    status     INTEGER NOT NULL,
    ip         TEXT
)`); err != nil {
		t.Fatalf("create audit_events: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE INDEX idx_audit_ts ON audit_events(ts DESC)`); err != nil {
		t.Fatalf("create audit_events index: %v", err)
	}
	// 001_init.sql also creates sessions, oidc_links and api_tokens; the
	// fixture records 001 as applied, so migration 012's cleanup statements
	// must find them.
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
)`); err != nil {
		t.Fatalf("create sessions: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE oidc_links (
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issuer   TEXT NOT NULL,
    subject  TEXT NOT NULL,
    email    TEXT,
    PRIMARY KEY (issuer, subject)
)`); err != nil {
		t.Fatalf("create oidc_links: %v", err)
	}
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE api_tokens (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    last_used   TEXT
)`); err != nil {
		t.Fatalf("create api_tokens: %v", err)
	}
	legacyDB.Close()

	// Open with a new target path — adoption should happen.
	targetPath := filepath.Join(tmpdir, "gameplane.db")
	dsn := "file:" + targetPath + "?_pragma=journal_mode(WAL)"
	store, err := Open(t.Context(), "sqlite", dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// Verify data from legacy survives.
	var username string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT username FROM users WHERE id = 1`).Scan(&username); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if username != "Alice" {
		t.Errorf("user data mismatch: got %q, want Alice", username)
	}

	// Verify migrations can run on the adopted database.
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Verify the migrations table has entries.
	var count int
	if err := store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if count == 0 {
		t.Fatal("no migrations applied")
	}
}

func TestNormalizeTimestamp_ConvertsNaive(t *testing.T) {
	tests := []struct {
		name  string
		input string
		after string
	}{
		{
			name:  "RFC3339 passes through",
			input: "2026-02-01T12:30:45Z",
			after: "2026-02-01T12:30:45Z",
		},
		{
			name:  "naive format converted",
			input: "2026-02-01 12:30:45",
			after: "2026-02-01T12:30:45Z",
		},
		{
			name:  "empty string",
			input: "",
			after: "",
		},
		{
			name:  "unparseable falls back",
			input: "not-a-date",
			after: "not-a-date",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeTimestamp(tt.input)
			if got != tt.after {
				t.Errorf("NormalizeTimestamp(%q) = %q, want %q", tt.input, got, tt.after)
			}
		})
	}
}

func (s *Store) migrationApplied(ctx context.Context, name string) (bool, error) {
	return migrationApplied(ctx, s.DB, name)
}

func (s *Store) runMigration(ctx context.Context, name, sqlText string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := applyMigration(ctx, tx, name, sqlText); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) seedTelemetryState(ctx context.Context, fresh bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("seed telemetry state: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := seedTelemetryState(ctx, tx, fresh); err != nil {
		return err
	}
	return tx.Commit()
}
