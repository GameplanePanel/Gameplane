package handlers

import (
	"bytes"
	"net/http"
	"strconv"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

// assertPasswordBusy checks the generic 503 + Retry-After answer for a full
// argon2 queue.
func assertPasswordBusy(t *testing.T, code int, retryAfter string) {
	t.Helper()
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if retryAfter != "5" {
		t.Fatalf("Retry-After = %q, want 5", retryAfter)
	}
}

func busyPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestUsers_CreatePasswordQueueFullAnswers503(t *testing.T) {
	srv, _, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	auth.SaturateArgonQueue(t)
	resp := busyPost(t, srv.URL+"/users", `{"username":"busyuser","password":"longenoughpw1","role":"viewer"}`)
	assertPasswordBusy(t, resp.StatusCode, resp.Header.Get("Retry-After"))
}

func TestUsers_ResetPasswordQueueFullAnswers503(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	id := seedUser(t, store, "busyreset", "viewer", "")
	auth.SaturateArgonQueue(t)
	resp := busyPost(t, srv.URL+"/users/"+strconv.FormatInt(id, 10)+"/reset-password", `{"password":"longenoughpw1"}`)
	assertPasswordBusy(t, resp.StatusCode, resp.Header.Get("Retry-After"))
}

func TestSFTPPassword_GenerateQueueFullAnswers503(t *testing.T) {
	env := newSFTPLoginEnv(t)
	uid, _ := insertSFTPUser(t, env, "busy.sftp@example.com", "busysftp")
	self := sftpSelfRouter(env.sessions, env.store, uid, false)
	auth.SaturateArgonQueue(t)
	rr := sftpSelfDo(t, self, http.MethodPost, "/me/sftp/password", `{"length":`+strconv.Itoa(auth.SFTPPasswordMinLen)+`}`)
	assertPasswordBusy(t, rr.Code, rr.Header().Get("Retry-After"))
}
