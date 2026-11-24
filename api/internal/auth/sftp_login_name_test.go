package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func addNameUser(ctx context.Context, t *testing.T, s *SessionStore, username, display string) int64 {
	t.Helper()
	res, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?,?,?,?,?)`,
		username, display, username, "viewer", "x")
	if err != nil {
		t.Fatalf("insert %s: %v", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func TestValidSFTPLoginName(t *testing.T) {
	for name, want := range map[string]bool{
		"valgul":                true,
		"valgul@example.com":    true,
		"":                      false,
		"a b":                   false,
		"a+b":                   false,
		"a/b":                   false,
		"tab\t":                 false,
		"\xff":                  false,
		strings.Repeat("a", 64): true,
		strings.Repeat("a", 65): false,
	} {
		if got := ValidSFTPLoginName(name); got != want {
			t.Errorf("ValidSFTPLoginName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSFTPLoginName_DefaultsAndResolve(t *testing.T) {
	ctx := context.Background()
	s, alice := sftpFault(ctx, t) // alice: username = display name = "alice"
	sso := addNameUser(ctx, t, s, "sso@example.com", "ssoname")
	shared1 := addNameUser(ctx, t, s, "s1@example.com", "twin")
	addNameUser(ctx, t, s, "s2@example.com", "twin")
	spaced := addNameUser(ctx, t, s, "sp@example.com", "Has Space")
	clash := addNameUser(ctx, t, s, "clash@example.com", "alice")

	for _, tc := range []struct {
		id   int64
		want string
	}{
		{alice, "alice"},
		{sso, "ssoname"},
		{shared1, "s1@example.com"},
		{spaced, "sp@example.com"},
		{clash, "clash@example.com"},
	} {
		got, custom, err := s.SFTPLoginName(ctx, tc.id)
		if err != nil || got != tc.want || custom {
			t.Errorf("SFTPLoginName(%d) = %q, %v, %v; want %q default", tc.id, got, custom, err, tc.want)
		}
	}

	for in, want := range map[string]string{
		"ssoname":         "sso@example.com",
		"sso@example.com": "sso@example.com",
		"alice":           "alice",
		"twin":            "twin",
		"nobody":          "nobody",
		"Has Space":       "Has Space",
	} {
		got, err := s.ResolveSFTPLoginName(ctx, in)
		if err != nil || got != want {
			t.Errorf("ResolveSFTPLoginName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	// A chosen name wins, and the default display name stops resolving.
	if err := s.SetSFTPLoginName(ctx, sso, "picked"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, custom, err := s.SFTPLoginName(ctx, sso); err != nil || got != "picked" || !custom {
		t.Fatalf("after set = %q, %v, %v", got, custom, err)
	}
	if got, _ := s.ResolveSFTPLoginName(ctx, "picked"); got != "sso@example.com" {
		t.Fatalf("resolve picked = %q", got)
	}
	if got, _ := s.ResolveSFTPLoginName(ctx, "ssoname"); got != "ssoname" {
		t.Fatalf("display name still resolves after a name was chosen: %q", got)
	}

	for name, want := range map[string]error{
		"alice":     ErrSFTPLoginNameTaken, // another user's username
		"twin":      ErrSFTPLoginNameTaken, // another user's display name
		"picked":    ErrSFTPLoginNameTaken, // another user's chosen name
		"a+b":       ErrSFTPLoginNameInvalid,
		"Has Space": ErrSFTPLoginNameInvalid,
	} {
		if err := s.SetSFTPLoginName(ctx, alice, name); !errors.Is(err, want) {
			t.Errorf("SetSFTPLoginName(alice, %q) = %v, want %v", name, err, want)
		}
	}
	if err := s.SetSFTPLoginName(ctx, sso, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, custom, _ := s.SFTPLoginName(ctx, sso); got != "ssoname" || custom {
		t.Fatalf("after clear = %q, %v", got, custom)
	}
}

func TestSFTPLoginName_DatabaseErrors(t *testing.T) {
	ctx := context.Background()
	for name, fn := range map[string]func(*SessionStore, int64) error{
		"login name": func(s *SessionStore, id int64) error { _, _, err := s.SFTPLoginName(ctx, id); return err },
		"set":        func(s *SessionStore, id int64) error { return s.SetSFTPLoginName(ctx, id, "x") },
		"clear":      func(s *SessionStore, id int64) error { return s.SetSFTPLoginName(ctx, id, "") },
		"resolve":    func(s *SessionStore, _ int64) error { _, err := s.ResolveSFTPLoginName(ctx, "nobody"); return err },
	} {
		s, uid := sftpFault(ctx, t, sftpCloseDB)
		if err := fn(s, uid); err == nil {
			t.Errorf("%s: want an error on a closed database", name)
		}
	}
}
