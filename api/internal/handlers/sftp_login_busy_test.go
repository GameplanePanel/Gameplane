package handlers

import (
	"net/http"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

func TestSFTPLogin_PasswordQueueFullAnswers503(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	name, _ := env.seedUser(t, "x")

	auth.SaturateArgonQueue(t)
	rr := env.login(t, h, map[string]any{"username": name, "password": "whatever-123"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After=%q, want 5", got)
	}
	if id := sftpProblemID(t, rr); id != "server.generic.503" {
		t.Fatalf("messageId=%q", id)
	}
}
