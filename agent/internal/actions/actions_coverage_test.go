package actions

import (
	"net/http"
	"testing"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

// TestRun_RenderErrorReturnsBadRequest covers run's c.Render error branch:
// the action's template compiles fine (Compile only parses the template),
// but references a parameter the action declares no default/required
// value for and the request supplies none of, so Render's
// missingkey=error option fails at execution time. The handler must
// answer 400 and must never reach the RCON call.
func TestRun_RenderErrorReturnsBadRequest(t *testing.T) {
	rc := &fakeRcon{}
	specs := []caps.ServerAction{
		{ID: "broken", Command: "say {{.Params.missing}}"}, // no declared Params at all
	}
	srv := newSrv(t, rc, specs)

	status, body := run(t, srv, runReq{ID: "broken"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusBadRequest, body)
	}
	if len(rc.calls) != 0 {
		t.Errorf("a render failure must not issue an RCON command, got calls=%v", rc.calls)
	}
}
