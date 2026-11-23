package handlers

import (
	"net/http"
	"strings"
	"testing"
)

// An SSO account is named by its email address. Such a user can log in over SFTP with that name.
func TestSFTPLogin_SSOEmailUsername(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	const name = "valgul.sso@example.com"
	if _, err := env.store.DB.ExecContext(t.Context(),
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?, ?, ?, ?, ?)`,
		name, "valgul", name, "viewer", "x"); err != nil {
		t.Fatalf("insert sso user: %v", err)
	}
	uid := userIDOf(t, env, name)
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24)
	if err != nil {
		t.Fatalf("set sftp password: %v", err)
	}
	if rr := env.login(t, h, map[string]any{"username": name, "password": pw}); rr.Code != http.StatusOK {
		t.Fatalf("login as %s: status %d body %s", name, rr.Code, rr.Body.String())
	}
}

func TestValidSFTPUsername(t *testing.T) {
	for name, want := range map[string]bool{
		"alice":                  true,
		"valgul.sso@example.com": true,
		"":                       false,
		"has space":              false,
		"tab\tname":              false,
		"ctrl\x01":               false,
		"a/b":                    false,
		"\xff":                   false,
		strings.Repeat("a", sftpUsernameMaxRunes):   true,
		strings.Repeat("a", sftpUsernameMaxRunes+1): false,
	} {
		if got := validSFTPUsername(name); got != want {
			t.Errorf("validSFTPUsername(%q) = %v, want %v", name, got, want)
		}
	}
}
