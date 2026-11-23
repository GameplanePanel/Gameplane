package handlers

import (
	"net/http"
	"testing"
)

// insertSFTPUser adds a user with an SFTP password and returns that password.
func insertSFTPUser(t *testing.T, env sftpLoginEnv, username, displayName string) string {
	t.Helper()
	if _, err := env.store.DB.ExecContext(t.Context(),
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?, ?, ?, ?, ?)`,
		username, displayName, username, "viewer", "x"); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), userIDOf(t, env, username), 24)
	if err != nil {
		t.Fatalf("set sftp password for %s: %v", username, err)
	}
	return pw
}

// An SSO user can log in with their display name instead of the email address the account is named by.
func TestSFTPLogin_UniqueDisplayName(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	pw := insertSFTPUser(t, env, "dn.unique@example.com", "dnunique")
	if rr := env.login(t, h, map[string]any{"username": "dnunique", "password": pw}); rr.Code != http.StatusOK {
		t.Fatalf("login by display name: status %d body %s", rr.Code, rr.Body.String())
	}
}

// A display name two accounts share names neither of them.
func TestSFTPLogin_SharedDisplayNameRefused(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	pw := insertSFTPUser(t, env, "dn.one@example.com", "dnshared")
	insertSFTPUser(t, env, "dn.two@example.com", "dnshared")
	if rr := env.login(t, h, map[string]any{"username": "dnshared", "password": pw}); rr.Code == http.StatusOK {
		t.Fatalf("login by shared display name succeeded: %s", rr.Body.String())
	}
	if rr := env.login(t, h, map[string]any{"username": "dn.one@example.com", "password": pw}); rr.Code != http.StatusOK {
		t.Fatalf("login by username: status %d body %s", rr.Code, rr.Body.String())
	}
}

// A panel username wins over another account's display name.
func TestSFTPLogin_UsernameBeatsDisplayName(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	ownerPW := insertSFTPUser(t, env, "dnowner", "Owner")
	otherPW := insertSFTPUser(t, env, "dn.other@example.com", "dnowner")
	if rr := env.login(t, h, map[string]any{"username": "dnowner", "password": otherPW}); rr.Code == http.StatusOK {
		t.Fatalf("display name took over a username: %s", rr.Body.String())
	}
	if rr := env.login(t, h, map[string]any{"username": "dnowner", "password": ownerPW}); rr.Code != http.StatusOK {
		t.Fatalf("login by username: status %d body %s", rr.Code, rr.Body.String())
	}
}
