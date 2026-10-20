//go:build postgres

package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// These tests run the Store against a real PostgreSQL server. They need a
// binary built with -tags postgres and GAMEPLANE_TEST_POSTGRES_DSN pointing
// at a database the test user can create schemas in (the `api (postgres)`
// CI job provides one); otherwise they skip. Each test migrates into its
// own throwaway schema, so tests don't see each other's rows.

const postgresDSNEnv = "GAMEPLANE_TEST_POSTGRES_DSN"

// newPostgresStore opens a migrated Store on a fresh schema of the test
// database and drops the schema when the test ends.
func newPostgresStore(t *testing.T) *Store {
	t.Helper()
	s := openPostgresSchema(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// openPostgresSchema opens a Store on a fresh, unmigrated schema of the test
// database and drops the schema when the test ends.
func openPostgresSchema(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		t.Skipf("%s not set; skipping PostgreSQL test", postgresDSNEnv)
	}
	ctx := t.Context()

	admin, err := Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random schema name: %v", err)
	}
	schema := "gp_test_" + hex.EncodeToString(suffix[:])
	if _, err := admin.DB.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		if _, err := admin.DB.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
		_ = admin.Close()
	})

	s, err := Open(ctx, "postgres", dsnWithSearchPath(dsn, schema))
	if err != nil {
		t.Fatalf("open postgres schema %s: %v", schema, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.Driver != "postgres" {
		t.Fatalf("driver = %q, want postgres", s.Driver)
	}
	return s
}

// dsnWithSearchPath points every pooled connection at schema. pgx passes
// unknown DSN settings through as session parameters.
func dsnWithSearchPath(dsn, schema string) string {
	if strings.Contains(dsn, "://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "search_path=" + schema
	}
	return dsn + " search_path=" + schema
}

func newSQLiteReferenceStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	return s
}

func queryStrings(t *testing.T, s *Store, q string, args ...any) []string {
	t.Helper()
	rows, err := s.DB.QueryContext(t.Context(), q, args...)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan %q: %v", q, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows %q: %v", q, err)
	}
	return out
}

func pgInsertUser(t *testing.T, s *Store, username, role string) int64 {
	t.Helper()
	var id int64
	if err := s.DB.QueryRowContext(t.Context(),
		`INSERT INTO users(username, role) VALUES (?, ?) RETURNING id`, username, role).Scan(&id); err != nil {
		t.Fatalf("insert user %q: %v", username, err)
	}
	return id
}

func TestPostgres_MigrateRecordsSameVersionsAsSQLite(t *testing.T) {
	s := newPostgresStore(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	q := `SELECT version FROM schema_migrations ORDER BY version`
	got := queryStrings(t, s, q)
	want := queryStrings(t, newSQLiteReferenceStore(t), q)
	if !slices.Equal(got, want) {
		t.Fatalf("postgres versions = %v, sqlite versions = %v", got, want)
	}
	for _, at := range queryStrings(t, s, `SELECT applied_at FROM schema_migrations`) {
		if _, err := time.Parse(sqliteTimestampLayout, at); err != nil {
			t.Errorf("applied_at %q: %v", at, err)
		}
	}
}

// TestPostgres_SchemaMatchesSQLite checks the hand-written Postgres
// migrations produce the same tables, columns, nullability and named
// indexes as the SQLite set.
func TestPostgres_SchemaMatchesSQLite(t *testing.T) {
	pg := newPostgresStore(t)
	lite := newSQLiteReferenceStore(t)

	liteTables := queryStrings(t, lite,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	pgTables := queryStrings(t, pg,
		`SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() ORDER BY table_name`)
	if !slices.Equal(liteTables, pgTables) {
		t.Fatalf("tables differ:\n sqlite:   %v\n postgres: %v", liteTables, pgTables)
	}

	for _, table := range liteTables {
		liteCols := queryStrings(t, lite,
			`SELECT name || CASE WHEN "notnull" = 1 OR pk > 0 THEN ' NOT NULL' ELSE '' END FROM pragma_table_info(?)`, table)
		pgCols := queryStrings(t, pg,
			`SELECT column_name || CASE WHEN is_nullable = 'NO' THEN ' NOT NULL' ELSE '' END
			   FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ?`, table)
		sort.Strings(liteCols)
		sort.Strings(pgCols)
		if !slices.Equal(liteCols, pgCols) {
			t.Errorf("table %s columns differ:\n sqlite:   %v\n postgres: %v", table, liteCols, pgCols)
		}
	}

	liteIdx := queryStrings(t, lite,
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_%' ORDER BY name`)
	pgIdx := queryStrings(t, pg,
		`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema() AND indexname LIKE 'idx_%' ORDER BY indexname`)
	if !slices.Equal(liteIdx, pgIdx) {
		t.Errorf("indexes differ:\n sqlite:   %v\n postgres: %v", liteIdx, pgIdx)
	}
}

func TestPostgres_TimestampDefaultsMatchSQLiteFormat(t *testing.T) {
	s := newPostgresStore(t)
	id := pgInsertUser(t, s, "ts-user", "viewer")
	var created, updated string
	if err := s.DB.QueryRowContext(t.Context(),
		`SELECT created_at, updated_at FROM users WHERE id = ?`, id).Scan(&created, &updated); err != nil {
		t.Fatalf("select: %v", err)
	}
	for _, v := range []string{created, updated} {
		ts, err := time.Parse(sqliteTimestampLayout, v)
		if err != nil {
			t.Fatalf("timestamp %q is not %q: %v", v, sqliteTimestampLayout, err)
		}
		if d := time.Since(ts); d < -time.Minute || d > time.Minute {
			t.Fatalf("timestamp %q is not the current UTC time", v)
		}
	}
}

func TestPostgres_RBACAndConfig(t *testing.T) {
	s := newPostgresStore(t)
	ctx := t.Context()
	admin := pgInsertUser(t, s, "pg-admin", "admin")
	viewer := pgInsertUser(t, s, "pg-viewer", "viewer")
	if admin == 0 || viewer == 0 || admin == viewer {
		t.Fatalf("RETURNING id gave admin=%d viewer=%d", admin, viewer)
	}

	if err := s.SetClusterRoleBinding(ctx, nil, admin, "local", "admin"); err != nil {
		t.Fatalf("bind admin: %v", err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.SetClusterRoleBinding(ctx, tx, viewer, "local", "viewer"); err != nil {
		t.Fatalf("bind viewer in tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if ok, err := s.RoleExists(ctx, "operator"); err != nil || !ok {
		t.Fatalf("RoleExists(operator) = %v, %v", ok, err)
	}
	if ok, err := s.RoleGrantsUserManagement(ctx, "admin"); err != nil || !ok {
		t.Fatalf("RoleGrantsUserManagement(admin) = %v, %v", ok, err)
	}
	if ok, err := s.UserManagesUsers(ctx, admin); err != nil || !ok {
		t.Fatalf("UserManagesUsers(admin) = %v, %v", ok, err)
	}
	if ok, err := s.UserManagesUsers(ctx, viewer); err != nil || ok {
		t.Fatalf("UserManagesUsers(viewer) = %v, %v", ok, err)
	}
	if n, err := s.UserManagerCount(ctx); err != nil || n != 1 {
		t.Fatalf("UserManagerCount = %d, %v", n, err)
	}
	if n, err := s.UserManagerCountExcludingRole(ctx, "admin"); err != nil || n != 0 {
		t.Fatalf("UserManagerCountExcludingRole(admin) = %d, %v", n, err)
	}
	if err := s.DeleteUserBindings(ctx, nil, viewer); err != nil {
		t.Fatalf("DeleteUserBindings: %v", err)
	}

	const want = `{"instanceName":"gameplane"}`
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO config(key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		"general", want, NowTimestamp()); err != nil {
		t.Fatalf("upsert config: %v", err)
	}
	if v, ok, err := s.ConfigValue(ctx, "general"); err != nil || !ok || v != want {
		t.Fatalf("ConfigValue = %q, %v, %v", v, ok, err)
	}
	tx, err = s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if v, ok, err := ConfigValueTx(ctx, tx, "absent"); err != nil || ok || v != "" {
		t.Fatalf("ConfigValueTx(absent) = %q, %v, %v", v, ok, err)
	}
}

func TestPostgres_Preferences(t *testing.T) {
	s := newPostgresStore(t)
	ctx := t.Context()
	uid := pgInsertUser(t, s, "pg-prefs", "viewer")

	accent := "#112233"
	if _, err := s.UpsertPreferences(ctx, uid, UserPreferences{
		ThemeType: "custom_colors", PresetID: "pink", AppearanceMode: "dark",
		CustomAccent: &accent, CustomCSSEnabled: true,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.GetPreferences(ctx, uid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ThemeType != "custom_colors" || got.AppearanceMode != "dark" || !got.CustomCSSEnabled ||
		got.CustomAccent == nil || *got.CustomAccent != accent {
		t.Fatalf("round trip = %+v", got)
	}
	if _, err := s.ResetPreferences(ctx, uid, "legacy", "light"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, err = s.GetPreferences(ctx, uid)
	if err != nil {
		t.Fatalf("get after reset: %v", err)
	}
	if got.ThemeType != "preset" || got.PresetID != "legacy" || got.CustomAccent != nil || got.CustomCSSEnabled {
		t.Fatalf("after reset = %+v", got)
	}
}

func TestPostgres_ShareLinks(t *testing.T) {
	s := newPostgresStore(t)
	ctx := t.Context()
	uid := pgInsertUser(t, s, "pg-sharer", "operator")

	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	raw, link, err := s.CreateShareLink(ctx, "local", "default", "mc", uid, true, &exp)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.CreateShareLink(ctx, "local", "default", "mc", uid, false, nil); err != nil {
		t.Fatalf("create without expiry: %v", err)
	}
	got, err := s.LookupShareLink(ctx, raw)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != link.ID || !got.CanStart || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("lookup = %+v, want id %s expiring %v", got, link.ID, exp)
	}
	if err := s.TouchShareLink(ctx, link.ID); err != nil {
		t.Fatalf("touch: %v", err)
	}
	list, err := s.ListShareLinks(ctx, "local", "default", "mc")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d links, %v", len(list), err)
	}
	if err := s.RevokeShareLink(ctx, "local", "default", "mc", link.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.LookupShareLink(ctx, raw); !errors.Is(err, ErrShareLinkInvalid) {
		t.Fatalf("lookup after revoke = %v, want ErrShareLinkInvalid", err)
	}
	if err := s.RevokeShareLink(ctx, "other", "default", "mc", link.ID); !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("revoke in another cluster = %v, want ErrShareLinkNotFound", err)
	}
	if err := s.RevokeShareLink(ctx, "local", "default", "other-server", link.ID); !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("revoke on another server = %v, want ErrShareLinkNotFound", err)
	}
}

// TestPostgres_DeleteUser checks DeleteUser's transaction on Postgres: the
// account's rows go, and its share links stay behind, revoked, exactly as on
// SQLite (share_links.created_by has no foreign key, so nothing cascades).
// Another account's rows and links are untouched.
func TestPostgres_DeleteUser(t *testing.T) {
	s := newPostgresStore(t)
	ctx := t.Context()
	gone := pgInsertUser(t, s, "pg-leaving", "viewer")
	kept := pgInsertUser(t, s, "pg-staying", "viewer")
	goneToken := seedAccountRows(t, s, gone, "pg-leaving")
	keptToken := seedAccountRows(t, s, kept, "pg-staying")

	if err := s.DeleteUser(ctx, gone); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if n := accountRowCount(t, s, gone); n != 0 {
		t.Errorf("deleted user still has %d account rows", n)
	}
	if _, err := s.LookupShareLink(ctx, goneToken); !errors.Is(err, ErrShareLinkInvalid) {
		t.Errorf("deleted user's share link: got %v, want ErrShareLinkInvalid", err)
	}
	links, err := s.ListShareLinks(ctx, "local", "default", "server-pg-leaving")
	if err != nil {
		t.Fatalf("list share links: %v", err)
	}
	if len(links) != 1 || links[0].CreatedBy != gone || links[0].RevokedAt == nil {
		t.Errorf("deleted user's share link should be kept and revoked, got %+v", links)
	}

	if n := accountRowCount(t, s, kept); n != 6 {
		t.Errorf("other user's account rows = %d, want 6", n)
	}
	if _, err := s.LookupShareLink(ctx, keptToken); err != nil {
		t.Errorf("other user's share link no longer resolves: %v", err)
	}
	if err := s.DeleteUser(ctx, 424242); err != nil {
		t.Fatalf("DeleteUser(unknown): %v", err)
	}
}

// TestPostgres_TextOrderingIsByteWise checks the COLLATE "C" columns sort
// like SQLite's BINARY collation, whatever the database's locale.
func TestPostgres_TextOrderingIsByteWise(t *testing.T) {
	s := newPostgresStore(t)
	for _, name := range []string{"Zeta", "alpha", "_under", "Beta"} {
		if _, err := s.DB.ExecContext(t.Context(),
			`INSERT INTO roles(name, description, builtin) VALUES (?, '', 0)`, name); err != nil {
			t.Fatalf("insert role %q: %v", name, err)
		}
	}
	got := queryStrings(t, s, `SELECT name FROM roles ORDER BY name`)
	want := slices.Clone(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("ORDER BY name = %v, want byte order %v", got, want)
	}
}

func TestPostgres_RebindLeavesLiteralsAlone(t *testing.T) {
	s := newPostgresStore(t)
	var lit, param string
	if err := s.DB.QueryRowContext(t.Context(), `SELECT '?' /* ? */, ?::text -- ?`, "bound").Scan(&lit, &param); err != nil {
		t.Fatalf("query: %v", err)
	}
	if lit != "?" || param != "bound" {
		t.Fatalf("got %q, %q", lit, param)
	}
	stmt, err := s.DB.PrepareContext(t.Context(), `SELECT ?::int + ?::int`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer func() { _ = stmt.Close() }()
	var sum int
	if err := stmt.QueryRowContext(t.Context(), 2, 3).Scan(&sum); err != nil || sum != 5 {
		t.Fatalf("prepared sum = %d, %v", sum, err)
	}
}

// TestPostgres_MigrateFromFrozenBaseline upgrades a database that stopped at
// the frozen per-dialect set (migrations/postgres 001–012, the last layout
// before migrations/common) to head. Rows written with the 012-era columns
// must read back unchanged, and schema_migrations must list every version.
func TestPostgres_MigrateFromFrozenBaseline(t *testing.T) {
	s := openPostgresSchema(t)
	ctx := t.Context()

	if _, err := s.DB.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`,
	); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	frozen, err := listSQL(migrations, legacyMigrationDirs["postgres"])
	if err != nil {
		t.Fatalf("list frozen migrations: %v", err)
	}
	if len(frozen) != 12 {
		t.Fatalf("frozen postgres set has %d files, want 12", len(frozen))
	}
	for _, f := range frozen {
		content, err := fs.ReadFile(migrations, f.path)
		if err != nil {
			t.Fatalf("read %s: %v", f.path, err)
		}
		if err := s.runMigration(ctx, f.name, string(content)); err != nil {
			t.Fatalf("apply %s: %v", f.name, err)
		}
	}

	// Rows as a 012-era API wrote them.
	const (
		stamp     = "2026-01-02 03:04:05"
		username  = "baseline-admin"
		email     = "admin@example.test"
		display   = "Baseline Admin"
		pwHash    = "$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$aGFzaA"
		linkID    = "baseline-link"
		rawToken  = "baseline-share-token"
		auditPath = "/roles"
	)
	var uid int64
	if err := s.DB.QueryRowContext(ctx,
		`INSERT INTO users(username, email, display_name, pw_hash, role, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		username, email, display, pwHash, "admin", stamp, stamp).Scan(&uid); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_role_bindings(user_id, role_name, namespace, cluster) VALUES (?, ?, ?, ?)`,
		uid, "admin", "*", "local"); err != nil {
		t.Fatalf("insert role binding: %v", err)
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	expires := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO share_links(id, cluster, namespace, server_name, created_by, can_start, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		linkID, "local", "default", "mc", uid, 1, hashShareLinkToken(rawToken),
		expires.Format(time.RFC3339), created.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert share link: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO audit_events(ts, actor, method, path, target, status, ip, prev_hash, hash, reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stamp, username, "POST", auditPath, "roles/ops", 201, "10.0.0.1", "prev-hash", "row-hash", "created"); err != nil {
		t.Fatalf("insert audit event: %v", err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}

	all, err := migrationFiles(migrations, "postgres")
	if err != nil {
		t.Fatalf("list head migrations: %v", err)
	}
	want := make([]string, 0, len(all))
	for _, f := range all {
		want = append(want, f.name)
	}
	got := queryStrings(t, s, `SELECT version FROM schema_migrations`)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("schema_migrations = %v, want %v", got, want)
	}

	var gotUser, gotEmail, gotDisplay, gotHash, gotRole, gotCreated, gotUpdated string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT username, email, display_name, pw_hash, role, created_at, updated_at FROM users WHERE id = ?`, uid,
	).Scan(&gotUser, &gotEmail, &gotDisplay, &gotHash, &gotRole, &gotCreated, &gotUpdated); err != nil {
		t.Fatalf("read user: %v", err)
	}
	if gotUser != username || gotEmail != email || gotDisplay != display || gotHash != pwHash ||
		gotRole != "admin" || gotCreated != stamp || gotUpdated != stamp {
		t.Fatalf("user after migrate = %q %q %q %q %q %q %q", gotUser, gotEmail, gotDisplay, gotHash, gotRole, gotCreated, gotUpdated)
	}

	bindings := queryStrings(t, s,
		`SELECT role_name || '|' || namespace || '|' || cluster FROM user_role_bindings WHERE user_id = ?`, uid)
	if !slices.Equal(bindings, []string{"admin|*|local"}) {
		t.Fatalf("role bindings after migrate = %v, want [admin|*|local]", bindings)
	}
	if ok, err := s.UserManagesUsers(ctx, uid); err != nil || !ok {
		t.Fatalf("UserManagesUsers after migrate = %v, %v", ok, err)
	}

	link, err := s.LookupShareLink(ctx, rawToken)
	if err != nil {
		t.Fatalf("lookup share link after migrate: %v", err)
	}
	if link.ID != linkID || link.Cluster != "local" || link.Namespace != "default" || link.ServerName != "mc" ||
		link.ServerUID != "" || link.CreatedBy != uid || !link.CanStart ||
		link.ExpiresAt == nil || !link.ExpiresAt.Equal(expires) || !link.CreatedAt.Equal(created) ||
		link.RevokedAt != nil || link.LastUsed != nil {
		t.Fatalf("share link after migrate = %+v", link)
	}

	var ts, actor, method, path, target, ip, prevHash, rowHash, reason string
	var status int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT ts, actor, method, path, target, status, ip, prev_hash, hash, reason FROM audit_events WHERE path = ?`, auditPath,
	).Scan(&ts, &actor, &method, &path, &target, &status, &ip, &prevHash, &rowHash, &reason); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if ts != stamp || actor != username || method != "POST" || path != auditPath || target != "roles/ops" ||
		status != 201 || ip != "10.0.0.1" || prevHash != "prev-hash" || rowHash != "row-hash" || reason != "created" {
		t.Fatalf("audit event after migrate = %q %q %q %q %q %d %q %q %q %q",
			ts, actor, method, path, target, status, ip, prevHash, rowHash, reason)
	}
}
