package auth

import (
	"context"
	"testing"
	"time"
)

func TestOIDCProfileFailureStillCommitsRoleSync(t *testing.T) {
	store := newAuthDB(t)
	ctx := t.Context()
	o := &OIDC{}
	o.AttachStore(store)
	user, _, err := o.resolveOrLinkUser(ctx, "test", "subject", "old@example.com", "Old", "viewer", "none", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `CREATE TRIGGER reject_link_email BEFORE UPDATE OF email ON oidc_links
		BEGIN SELECT RAISE(ABORT, 'profile update failed'); END`); err != nil {
		t.Fatal(err)
	}
	got, outcome, err := o.resolveOrLinkUser(ctx, "test", "subject", "new@example.com", "New", "admin", "admins", true)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Applied || got.Role != "admin" || got.Email != user.Email || got.DisplayName != user.DisplayName {
		t.Fatalf("user=%+v outcome=%+v", got, outcome)
	}
	var email, displayName, role string
	if err := store.DB.QueryRowContext(ctx, `SELECT email, display_name, role FROM users WHERE id = ?`, user.ID).
		Scan(&email, &displayName, &role); err != nil {
		t.Fatal(err)
	}
	if email != user.Email || displayName != user.DisplayName || role != "admin" {
		t.Fatalf("stored email=%q display name=%q role=%q", email, displayName, role)
	}
}

func TestOIDCResyncWaitsForConcurrentUserManagement(t *testing.T) {
	store := newAuthDB(t)
	ctx := context.Background()
	for _, name := range []string{"oidc-admin", "other-admin"} {
		if _, err := store.DB.ExecContext(ctx,
			`INSERT INTO users(username, display_name, email, role) VALUES (?, ?, ?, 'admin')`, name, name, name+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	var userID int64
	if err := store.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'oidc-admin'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO oidc_links(user_id, issuer, subject, email) VALUES (?, 'test', 'subject', 'admin@example.com')`, userID); err != nil {
		t.Fatal(err)
	}
	o := &OIDC{}
	o.AttachStore(store)

	// A manual management operation owns the guard while it removes the
	// other administrator. OIDC must wait before reading the manager count.
	tx, err := store.BeginUserManagement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	type result struct {
		outcome *RoleAssignmentOutcome
		err     error
	}
	started := make(chan struct{})
	done := make(chan result, 1)
	go func() {
		close(started)
		_, outcome, err := o.resolveOrLinkUser(ctx, "test", "subject", "admin@example.com", "Admin", "viewer", "viewers", true)
		done <- result{outcome, err}
	}()
	<-started
	select {
	case got := <-done:
		t.Fatalf("resync completed while user management was locked: outcome=%+v err=%v", got.outcome, got.err)
	case <-time.After(100 * time.Millisecond):
		// Bound this negative assertion; do not release the guard early.
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE username = 'other-admin'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.outcome.Applied {
			t.Fatal("OIDC demoted the last remaining manager")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resync did not finish after releasing the management guard")
	}
	count, err := store.UserManagerCount(ctx)
	if err != nil || count != 1 {
		t.Fatalf("manager count=%d err=%v, want one manager", count, err)
	}
}
