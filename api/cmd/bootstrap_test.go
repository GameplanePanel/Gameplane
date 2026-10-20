package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// dsnIn returns a sqlite DSN pointing at a fresh file under t.TempDir().
// :memory: is unusable here because db.Open caps MaxOpenConns to 1 but we
// still re-open the same DB inside bootstrapAdmin (it owns its own *Store);
// a temp file lets the second open see the first call's writes.
func dsnIn(t *testing.T) string {
	t.Helper()
	return "file:" + filepath.Join(t.TempDir(), "k.db") + "?_pragma=journal_mode(WAL)"
}

func runBootstrap(t *testing.T, dsn, stdin string, args ...string) (string, error) {
	t.Helper()
	auth.SetFastHashParams(t)
	full := append([]string{"--db-driver=sqlite", "--db-dsn=" + dsn}, args...)
	var stderr bytes.Buffer
	err := bootstrapAdmin(context.Background(), full, strings.NewReader(stdin), &stderr)
	return stderr.String(), err
}

func mustOpen(t *testing.T, dsn string) *db.Store {
	t.Helper()
	s, err := db.Open(context.Background(), "sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestBootstrap_Insert(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	out, err := runBootstrap(t, dsn, "",
		"--username=admin", "--password=correct-horse-battery", "--email=a@b.test",
	)
	if err != nil {
		t.Fatalf("bootstrap: %v (stderr=%q)", err, out)
	}
	if !strings.Contains(out, "created user \"admin\"") {
		t.Fatalf("expected created log, got %q", out)
	}

	s := mustOpen(t, dsn)
	var role, hash, display, email string
	err = s.DB.QueryRowContext(context.Background(), `SELECT role, pw_hash, display_name, email FROM users WHERE username='admin'`).
		Scan(&role, &hash, &display, &email)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if role != "admin" {
		t.Fatalf("role=%q want admin", role)
	}
	if display != "admin" {
		t.Fatalf("display_name=%q want default to username", display)
	}
	if email != "a@b.test" {
		t.Fatalf("email=%q", email)
	}
	ok, err := auth.VerifyPassword(context.Background(), "correct-horse-battery", hash)
	if err != nil || !ok {
		t.Fatalf("password did not verify (ok=%v err=%v)", ok, err)
	}
}

func TestBootstrap_PasswordStdin(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	_, err := runBootstrap(t, dsn, "fromstdin-password\n",
		"--username=admin", "--password-stdin",
	)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	s := mustOpen(t, dsn)
	var hash string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT pw_hash FROM users WHERE username='admin'`).Scan(&hash); err != nil {
		t.Fatalf("select: %v", err)
	}
	ok, _ := auth.VerifyPassword(context.Background(), "fromstdin-password", hash)
	if !ok {
		t.Fatal("stdin password did not verify")
	}
}

func TestBootstrap_PasswordEnv(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "env-supplied-pass")
	_, err := runBootstrap(t, dsn, "", "--username=admin")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	s := mustOpen(t, dsn)
	var hash string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT pw_hash FROM users WHERE username='admin'`).Scan(&hash); err != nil {
		t.Fatalf("select: %v", err)
	}
	ok, _ := auth.VerifyPassword(context.Background(), "env-supplied-pass", hash)
	if !ok {
		t.Fatal("env password did not verify")
	}
}

func TestBootstrap_RejectsShortPassword(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	_, err := runBootstrap(t, dsn, "", "--username=admin", "--password=short")
	if err == nil || !strings.Contains(err.Error(), "too short") {
		t.Fatalf("expected too-short error, got %v", err)
	}
}

func TestBootstrap_RejectsMissingPassword(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	_, err := runBootstrap(t, dsn, "", "--username=admin")
	if err == nil || !strings.Contains(err.Error(), "password required") {
		t.Fatalf("expected password-required error, got %v", err)
	}
}

func TestBootstrap_RejectsConflictingPasswordSources(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	_, err := runBootstrap(t, dsn, "ignored\n",
		"--username=admin", "--password=in-flag", "--password-stdin",
	)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually-exclusive error, got %v", err)
	}
}

func TestBootstrap_RejectsMissingUsername(t *testing.T) {
	_, err := runBootstrap(t, dsnIn(t), "", "--password=correct-horse-battery")
	if err == nil || !strings.Contains(err.Error(), "username") {
		t.Fatalf("expected username-required error, got %v", err)
	}
}

func TestBootstrap_RefusesOverwriteWithoutForce(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	if _, err := runBootstrap(t, dsn, "",
		"--username=admin", "--password=correct-horse-battery"); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	_, err := runBootstrap(t, dsn, "",
		"--username=admin", "--password=different-correct-horse")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected --force advisory, got %v", err)
	}

	// Original hash must be untouched.
	s := mustOpen(t, dsn)
	var hash string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT pw_hash FROM users WHERE username='admin'`).Scan(&hash); err != nil {
		t.Fatalf("select: %v", err)
	}
	ok, _ := auth.VerifyPassword(context.Background(), "correct-horse-battery", hash)
	if !ok {
		t.Fatal("original password no longer verifies after rejected overwrite")
	}
}

func TestBootstrap_ForceUpdatesPassword(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	if _, err := runBootstrap(t, dsn, "",
		"--username=admin", "--password=original-correct-horse"); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	out, err := runBootstrap(t, dsn, "",
		"--username=admin", "--password=fresh-rotation-secret", "--force",
	)
	if err != nil {
		t.Fatalf("force bootstrap: %v", err)
	}
	if !strings.Contains(out, "updated user") {
		t.Fatalf("expected updated log, got %q", out)
	}

	s := mustOpen(t, dsn)
	var hash string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT pw_hash FROM users WHERE username='admin'`).Scan(&hash); err != nil {
		t.Fatalf("select: %v", err)
	}
	if ok, _ := auth.VerifyPassword(context.Background(), "original-correct-horse", hash); ok {
		t.Fatal("old password still verifies after --force")
	}
	if ok, _ := auth.VerifyPassword(context.Background(), "fresh-rotation-secret", hash); !ok {
		t.Fatal("new password does not verify after --force")
	}
}

func TestBootstrap_ForcePromotesToAdmin(t *testing.T) {
	dsn := dsnIn(t)
	// Pre-seed a non-admin user directly so we can confirm --force
	// also rewrites role to admin (the install path may have created
	// a viewer-role row before someone realised admin was missing).
	s := mustOpen(t, dsn)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?,?,?,?,?)`,
		"alice", "Alice", "alice@example.com", "viewer", "placeholder",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	if _, err := runBootstrap(t, dsn, "",
		"--username=alice", "--password=promotion-correct-horse", "--force",
	); err != nil {
		t.Fatalf("force bootstrap: %v", err)
	}
	var role string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT role FROM users WHERE username='alice'`).Scan(&role); err != nil {
		t.Fatalf("select: %v", err)
	}
	if role != "admin" {
		t.Fatalf("role=%q want admin after --force", role)
	}
}

// --enable-local-login alone (no username) is the break-glass for "local
// disabled in Admin Settings, OIDC broken": it flips the local provider
// back on in the auth config row and touches nothing else.
func TestBootstrap_EnableLocalLoginAlone(t *testing.T) {
	dsn := dsnIn(t)
	s := mustOpen(t, dsn)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seed := `{"providers":[{"name":"local","kind":"local","enabled":false},{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g"}]}`
	if _, err := s.DB.ExecContext(context.Background(), `INSERT INTO config(key, value) VALUES ('auth', ?)`, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runBootstrap(t, dsn, "", "--enable-local-login")
	if err != nil {
		t.Fatalf("bootstrap: %v (stderr=%q)", err, out)
	}
	if !strings.Contains(out, "local login re-enabled") {
		t.Fatalf("stderr = %q", out)
	}

	var raw string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT value FROM config WHERE key='auth'`).Scan(&raw); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(raw, `"enabled":true`) || !strings.Contains(raw, `"kind":"local"`) {
		t.Fatalf("auth row = %s", raw)
	}
	// The OIDC provider must be preserved untouched.
	if !strings.Contains(raw, `"corp"`) || !strings.Contains(raw, "idp.example") {
		t.Fatalf("other providers were dropped: %s", raw)
	}
}

// Without a row there is nothing to flip — local is already enabled by
// default and the command says so instead of inventing a row.
func TestBootstrap_EnableLocalLoginNoRow(t *testing.T) {
	dsn := dsnIn(t)
	out, err := runBootstrap(t, dsn, "", "--enable-local-login")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !strings.Contains(out, "already enabled by default") {
		t.Fatalf("stderr = %q", out)
	}
}

// The break-glass local-login switch rewrites only the local provider's
// enabled flag: other providers and the helmOverride role-mapping overlay
// (including an explicit empty list) are kept as they were.
func TestBootstrap_EnableLocalLoginKeepsRestOfAuthConfig(t *testing.T) {
	dsn := dsnIn(t)
	s := mustOpen(t, dsn)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seed := `{"providers":[{"name":"local","kind":"local","enabled":false},` +
		`{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"g"}],` +
		`"helmOverride":{"roleMappings":{"admin":["ops-admins"],"viewer":[]}}}`
	if _, err := s.DB.ExecContext(context.Background(), `INSERT INTO config(key, value) VALUES ('auth', ?)`, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runBootstrap(t, dsn, "", "--enable-local-login")
	if err != nil {
		t.Fatalf("bootstrap: %v (stderr=%q)", err, out)
	}

	var raw string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT value FROM config WHERE key='auth'`).Scan(&raw); err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got struct {
		Providers    []map[string]any `json:"providers"`
		HelmOverride *struct {
			RoleMappings *struct {
				Admin  []string `json:"admin"`
				Viewer []string `json:"viewer"`
			} `json:"roleMappings"`
		} `json:"helmOverride"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode auth row: %v (%s)", err, raw)
	}
	if got.HelmOverride == nil || got.HelmOverride.RoleMappings == nil {
		t.Fatalf("helmOverride.roleMappings was dropped: %s", raw)
	}
	if !reflect.DeepEqual(got.HelmOverride.RoleMappings.Admin, []string{"ops-admins"}) {
		t.Fatalf("helmOverride admin = %v, want [ops-admins]", got.HelmOverride.RoleMappings.Admin)
	}
	if got.HelmOverride.RoleMappings.Viewer == nil || len(got.HelmOverride.RoleMappings.Viewer) != 0 {
		t.Fatalf("helmOverride viewer = %#v, want an explicit empty list", got.HelmOverride.RoleMappings.Viewer)
	}
	var localEnabled, corpKept bool
	for _, p := range got.Providers {
		if p["kind"] == "local" && p["enabled"] == true {
			localEnabled = true
		}
		if p["name"] == "corp" && p["issuer"] == "https://idp.example" {
			corpKept = true
		}
	}
	if !localEnabled || !corpKept {
		t.Fatalf("providers = %v, want local enabled and corp kept", got.Providers)
	}
}

// A forced reset through bootstrap-admin ends every existing session of
// the reset account, as the dashboard password reset does; other
// accounts' sessions are left alone.
func TestBootstrap_ForceEndsExistingSessions(t *testing.T) {
	dsn := dsnIn(t)
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	if _, err := runBootstrap(t, dsn, "",
		"--username=admin", "--password=original-correct-horse"); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}

	s := mustOpen(t, dsn)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?,?,?,?,?)`,
		"bob", "Bob", "bob@example.com", "viewer", "placeholder",
	); err != nil {
		t.Fatalf("seed bob: %v", err)
	}
	var adminID, bobID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username='bob'`).Scan(&bobID); err != nil {
		t.Fatalf("bob id: %v", err)
	}
	for _, row := range []struct {
		token string
		user  int64
	}{
		{"tok-admin-1", adminID},
		{"tok-admin-2", adminID},
		{"tok-bob-1", bobID},
	} {
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO sessions(token, user_id, csrf_token, expires_at) VALUES (?, ?, ?, ?)`,
			row.token, row.user, "csrf-"+row.token, "2999-01-01T00:00:00Z",
		); err != nil {
			t.Fatalf("seed session %s: %v", row.token, err)
		}
	}

	if _, err := runBootstrap(t, dsn, "",
		"--username=admin", "--password=fresh-rotation-secret", "--force",
	); err != nil {
		t.Fatalf("force bootstrap: %v", err)
	}

	var adminSessions, bobSessions int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, adminID).Scan(&adminSessions); err != nil {
		t.Fatalf("count admin sessions: %v", err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, bobID).Scan(&bobSessions); err != nil {
		t.Fatalf("count bob sessions: %v", err)
	}
	if adminSessions != 0 {
		t.Fatalf("reset account still has %d sessions after --force, want 0", adminSessions)
	}
	if bobSessions != 1 {
		t.Fatalf("other account has %d sessions after --force, want 1 (left alone)", bobSessions)
	}
}

// When a forced reset's session deletion fails inside the transaction, the
// entire reset (password, role, and sessions) rolls back: the user's pw_hash
// and role remain unchanged, and the session row still exists.
func TestBootstrap_ForceResetIsAllOrNothing(t *testing.T) {
	dsn := dsnIn(t)
	auth.SetFastHashParams(t)
	s := mustOpen(t, dsn)
	ctx := context.Background()

	// Seed a user with a known password hash and a session.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	oldHash, _ := auth.HashPassword(context.Background(), "original-password")
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?,?,?,?,?)`,
		"admin", "Admin", "admin@example.com", "viewer", oldHash,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var userID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username='admin'`).Scan(&userID); err != nil {
		t.Fatalf("select user id: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, csrf_token, expires_at) VALUES (?, ?, ?, ?)`,
		"tok-admin", userID, "csrf-token", "2999-01-01T00:00:00Z",
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// Create a trigger that blocks session deletion without mocks.
	if _, err := s.DB.ExecContext(ctx,
		`CREATE TRIGGER fail_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'blocked'); END`,
	); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	// Run bootstrap --force; it should fail because the transaction cannot
	// delete the session.
	t.Setenv("GAMEPLANE_ADMIN_PASSWORD", "")
	_, err := runBootstrap(t, dsn, "",
		"--username=admin", "--password=new-password", "--force",
	)
	if err == nil {
		t.Fatal("expected bootstrap --force to fail due to trigger block, but it succeeded")
	}

	// Verify that the transaction rolled back: pw_hash, role, and session
	// must be unchanged.
	var hash, role string
	if err := s.DB.QueryRowContext(ctx, `SELECT pw_hash, role FROM users WHERE id=?`, userID).Scan(&hash, &role); err != nil {
		t.Fatalf("select user state: %v", err)
	}
	if ok, _ := auth.VerifyPassword(context.Background(), "original-password", hash); !ok {
		t.Fatal("pw_hash was modified even though transaction should have rolled back")
	}
	if role != "viewer" {
		t.Fatalf("role=%q want viewer (unchanged)", role)
	}

	var sessionCount int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id=?`, userID).Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionCount != 1 {
		t.Fatalf("session count=%d want 1 (unchanged)", sessionCount)
	}
}
