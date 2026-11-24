package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
)

// insertSFTPUser adds a user with an SFTP password and returns the user's id and that password.
func insertSFTPUser(t *testing.T, env sftpLoginEnv, username, displayName string) (int64, string) {
	t.Helper()
	if _, err := env.store.DB.ExecContext(t.Context(),
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?, ?, ?, ?, ?)`,
		username, displayName, username, "viewer", "x"); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	uid := userIDOf(t, env, username)
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24)
	if err != nil {
		t.Fatalf("set sftp password for %s: %v", username, err)
	}
	return uid, pw
}

func sftpLoginNameOf(t *testing.T, h http.Handler) (string, bool) {
	t.Helper()
	rr := sftpSelfDo(t, h, http.MethodGet, "/me/sftp", "")
	var self struct {
		LoginName       string `json:"loginName"`
		CustomLoginName bool   `json:"customLoginName"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &self); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("get self: status %d err %v body %s", rr.Code, err, rr.Body.String())
	}
	return self.LoginName, self.CustomLoginName
}

// An SSO user logs in with their display name by default, instead of the email address the account is
// named by. The email address keeps working.
func TestSFTPLogin_DisplayNameIsDefault(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	uid, pw := insertSFTPUser(t, env, "dn.unique@example.com", "dnunique")
	if name, custom := sftpLoginNameOf(t, sftpSelfRouter(env.sessions, env.store, uid, false)); name != "dnunique" || custom {
		t.Fatalf("login name = %q custom=%v, want dnunique default", name, custom)
	}
	for _, name := range []string{"dnunique", "dn.unique@example.com"} {
		if rr := env.login(t, h, map[string]any{"username": name, "password": pw}); rr.Code != http.StatusOK {
			t.Fatalf("login as %s: status %d body %s", name, rr.Code, rr.Body.String())
		}
	}
}

// A display name two accounts share is neither account's default; they fall back to the panel username.
func TestSFTPLogin_SharedDisplayNameFallsBack(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	uid, pw := insertSFTPUser(t, env, "dn.one@example.com", "dnshared")
	insertSFTPUser(t, env, "dn.two@example.com", "dnshared")
	if name, _ := sftpLoginNameOf(t, sftpSelfRouter(env.sessions, env.store, uid, false)); name != "dn.one@example.com" {
		t.Fatalf("login name = %q, want the panel username", name)
	}
	if rr := env.login(t, h, map[string]any{"username": "dnshared", "password": pw}); rr.Code == http.StatusOK {
		t.Fatalf("login by shared display name succeeded: %s", rr.Body.String())
	}
	if rr := env.login(t, h, map[string]any{"username": "dn.one@example.com", "password": pw}); rr.Code != http.StatusOK {
		t.Fatalf("login by username: status %d body %s", rr.Code, rr.Body.String())
	}
}

// A display name that is another account's panel username is not a login name.
func TestSFTPLogin_UsernameBeatsDisplayName(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	_, ownerPW := insertSFTPUser(t, env, "dnowner", "Owner")
	_, otherPW := insertSFTPUser(t, env, "dn.other@example.com", "dnowner")
	if rr := env.login(t, h, map[string]any{"username": "dnowner", "password": otherPW}); rr.Code == http.StatusOK {
		t.Fatalf("display name took over a username: %s", rr.Body.String())
	}
	if rr := env.login(t, h, map[string]any{"username": "dnowner", "password": ownerPW}); rr.Code != http.StatusOK {
		t.Fatalf("login by username: status %d body %s", rr.Code, rr.Body.String())
	}
}

// A user can choose their SFTP username in Settings; the default display name then stops logging in, and
// clearing the choice brings it back.
func TestSFTPLoginName_ChooseAndClear(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	uid, pw := insertSFTPUser(t, env, "dn.choose@example.com", "dnchoose")
	self := sftpSelfRouter(env.sessions, env.store, uid, false)

	if rr := sftpSelfDo(t, self, http.MethodPut, "/me/sftp/login-name", `{"loginName":"dnpicked"}`); rr.Code != http.StatusOK {
		t.Fatalf("set login name: status %d body %s", rr.Code, rr.Body.String())
	}
	if name, custom := sftpLoginNameOf(t, self); name != "dnpicked" || !custom {
		t.Fatalf("login name = %q custom=%v, want dnpicked chosen", name, custom)
	}
	if rr := env.login(t, h, map[string]any{"username": "dnpicked", "password": pw}); rr.Code != http.StatusOK {
		t.Fatalf("login by chosen name: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := env.login(t, h, map[string]any{"username": "dnchoose", "password": pw}); rr.Code == http.StatusOK {
		t.Fatalf("display name still logs in after a name was chosen: %s", rr.Body.String())
	}

	if rr := sftpSelfDo(t, self, http.MethodPut, "/me/sftp/login-name", `{"loginName":""}`); rr.Code != http.StatusOK {
		t.Fatalf("clear login name: status %d body %s", rr.Code, rr.Body.String())
	}
	if name, custom := sftpLoginNameOf(t, self); name != "dnchoose" || custom {
		t.Fatalf("login name after clear = %q custom=%v, want dnchoose default", name, custom)
	}
}

// A chosen name may not be another account's username, chosen name or display name, nor an invalid name.
func TestSFTPLoginName_Refusals(t *testing.T) {
	env := newSFTPLoginEnv(t)
	uid, _ := insertSFTPUser(t, env, "dn.me@example.com", "dnme")
	otherID, _ := insertSFTPUser(t, env, "dnother", "dnotherdisplay")
	if err := env.sessions.SetSFTPLoginName(t.Context(), otherID, "dnotherpick"); err != nil {
		t.Fatalf("seed other chosen name: %v", err)
	}
	self := sftpSelfRouter(env.sessions, env.store, uid, false)
	for body, want := range map[string]int{
		`{"loginName":"dnother"}`:        http.StatusConflict,
		`{"loginName":"dnotherdisplay"}`: http.StatusConflict,
		`{"loginName":"dnotherpick"}`:    http.StatusConflict,
		`{"loginName":"has space"}`:      http.StatusBadRequest,
		`{"loginName":"a+b"}`:            http.StatusBadRequest,
		`{"loginName":"a/b"}`:            http.StatusBadRequest,
		`{"loginName":"dnme"}`:           http.StatusOK,
	} {
		if rr := sftpSelfDo(t, self, http.MethodPut, "/me/sftp/login-name", body); rr.Code != want {
			t.Errorf("%s: status %d, want %d (body %s)", body, rr.Code, want, rr.Body.String())
		}
	}
}
