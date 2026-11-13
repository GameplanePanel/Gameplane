package db

import (
	"fmt"
	"io/fs"
	"testing"
)

// migrateBefore applies, in order, every migration whose version sorts
// before stop (e.g. "020") and leaves the rest pending, so a test can seed
// rows the way an older release stored them and then run Migrate.
func migrateBefore(t *testing.T, s *Store, stop string) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`,
	); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	files, err := migrationFiles(migrations, s.Driver)
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	for _, f := range files {
		if migrationVersion(f.name) >= stop {
			break
		}
		content, err := fs.ReadFile(migrations, f.path)
		if err != nil {
			t.Fatalf("read migration %s: %v", f.path, err)
		}
		if err := s.runMigration(ctx, f.name, string(content)); err != nil {
			t.Fatalf("migration %s: %v", f.name, err)
		}
	}
}

// checkRoleSourceMigration seeds users as a pre-020 database stores them,
// runs migration 020 and checks OD-4: users with an oidc_links row become
// sso, every other user manual, and no role changes. Users created after
// the migration default to manual.
func checkRoleSourceMigration(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	migrateBefore(t, s, "020")

	insert := func(username, role string) int64 {
		t.Helper()
		var id int64
		if err := s.DB.QueryRowContext(ctx,
			`INSERT INTO users(username, role) VALUES (?, ?) RETURNING id`, username, role).Scan(&id); err != nil {
			t.Fatalf("insert user %s: %v", username, err)
		}
		return id
	}
	ssoAdmin := insert("rs-mig-sso-admin", "admin")
	ssoCustom := insert("rs-mig-sso-custom", "login-only")
	local := insert("rs-mig-local", "operator")
	pending := insert("rs-mig-pending", "viewer")
	for i, id := range []int64{ssoAdmin, ssoCustom} {
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO oidc_links(user_id, issuer, subject, email) VALUES (?, 'https://idp.example', ?, '')`,
			id, fmt.Sprintf("rs-mig-sub-%d", i)); err != nil {
			t.Fatalf("link user %d: %v", id, err)
		}
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := []struct {
		id           int64
		role, source string
	}{
		{ssoAdmin, "admin", RoleSourceSSO},
		{ssoCustom, "login-only", RoleSourceSSO},
		{local, "operator", RoleSourceManual},
		{pending, "viewer", RoleSourceManual},
	}
	for _, w := range want {
		var role, source string
		if err := s.DB.QueryRowContext(ctx,
			`SELECT role, role_source FROM users WHERE id = ?`, w.id).Scan(&role, &source); err != nil {
			t.Fatalf("read user %d: %v", w.id, err)
		}
		if role != w.role || source != w.source {
			t.Errorf("user %d: role/role_source = %q/%q, want %q/%q", w.id, role, source, w.role, w.source)
		}
	}

	after := insert("rs-mig-after", "viewer")
	if src, err := s.RoleSource(ctx, after); err != nil || src != RoleSourceManual {
		t.Fatalf("new user's role source = %q (err %v), want manual", src, err)
	}
}

func TestMigration020_RoleSource_SQLite(t *testing.T) {
	s, err := Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	checkRoleSourceMigration(t, s)
}
