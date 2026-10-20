package players

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
)

// fakeErrRcon always fails Exec with a plain (non-ErrDisabled) error, to
// drive the "upstream unavailable" 502 branches as opposed to the
// "unsupported" 501/empty-list branches rcon.Disabled{} drives.
type fakeErrRcon struct{ err error }

func (f fakeErrRcon) Exec(string) (string, error) { return "", f.err }

// fakeModCommander is a minimal commander stub used only to reach
// WhitelistList/ParseWhitelist and BanList/ParseBanList directly, without
// depending on templateCommander's coupling between "list command
// declared" and "list regex compiled" (which never lets ParseWhitelist/
// ParseBanList return nil while the *List() ok flag is true). Every other
// commander method is unused by these tests and left as a zero value.
type fakeModCommander struct {
	whitelistCmd string
	whitelistOK  bool
	banListCmd   string
	banListOK    bool
}

func (fakeModCommander) Capabilities() Capabilities            { return Capabilities{} }
func (fakeModCommander) Kick(string, string) (string, bool)    { return "", false }
func (fakeModCommander) Ban(string, string) (string, bool)     { return "", false }
func (fakeModCommander) Unban(string) (string, bool)           { return "", false }
func (f fakeModCommander) BanList() (string, bool)             { return f.banListCmd, f.banListOK }
func (fakeModCommander) ParseBanList(string) []BannedPlayer    { return nil }
func (fakeModCommander) WhitelistAdd(string) (string, bool)    { return "", false }
func (fakeModCommander) WhitelistRemove(string) (string, bool) { return "", false }
func (f fakeModCommander) WhitelistList() (string, bool)       { return f.whitelistCmd, f.whitelistOK }
func (fakeModCommander) ParseWhitelist(string) []string        { return nil }

// TestKick_RconDisabled covers runMod's rcon.ErrDisabled branch (as
// opposed to the generic-error 502 branch other tests already cover): a
// game with RCON disabled entirely must answer 501, not 502.
func TestKick_RconDisabled(t *testing.T) {
	r := newTestRouter(t, "minecraft-java", rcon.Disabled{})
	srv := httptest.NewServer(r)
	defer srv.Close()

	status, body := doJSON(t, srv, http.MethodPost, "/players/kick", modReq{Name: "griefer"})
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusNotImplemented, body)
	}
}

// TestBanned_RconDisabled covers banned's rcon.ErrDisabled branch: an
// empty list rather than an error, so a Banned tab renders uniformly.
func TestBanned_RconDisabled(t *testing.T) {
	r := newTestRouter(t, "minecraft-java", rcon.Disabled{})
	srv := httptest.NewServer(r)
	defer srv.Close()

	status, body := doJSON(t, srv, http.MethodGet, "/players/banned", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("body = %s, want []", body)
	}
}

// TestWhitelistList_RconDisabled covers whitelistList's rcon.ErrDisabled
// branch.
func TestWhitelistList_RconDisabled(t *testing.T) {
	r := newTestRouter(t, "minecraft-java", rcon.Disabled{})
	srv := httptest.NewServer(r)
	defer srv.Close()

	status, body := doJSON(t, srv, http.MethodGet, "/players/whitelist", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("body = %s, want []", body)
	}
}

// TestWhitelistList_RconError covers whitelistList's generic err!=nil
// branch (502), distinct from the ErrDisabled branch above.
func TestWhitelistList_RconError(t *testing.T) {
	r := newTestRouter(t, "minecraft-java", fakeErrRcon{err: errBoom})
	srv := httptest.NewServer(r)
	defer srv.Close()

	status, body := doJSON(t, srv, http.MethodGet, "/players/whitelist", nil)
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusBadGateway, body)
	}
}

var errBoom = errors.New("boom")

// TestWhitelistList_ParseWhitelistNilBecomesEmptyList covers the
// `out == nil` -> `out = []string{}` normalization in whitelistList: a
// commander whose ParseWhitelist legitimately returns nil (while
// WhitelistList still reports ok=true) must still serialize as `[]`, not
// `null`. templateCommander never actually produces this combination (its
// WhitelistList and ParseWhitelist are coupled), so this exercises the
// handler's own defensive normalization directly via the handler struct
// (same package), independent of any one commander implementation.
func TestWhitelistList_ParseWhitelistNilBecomesEmptyList(t *testing.T) {
	h := &handler{
		rcon: &fakeRcon{},
		cmdr: fakeModCommander{whitelistCmd: "whitelist list", whitelistOK: true},
		game: "test",
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/players/whitelist", nil)

	h.whitelistList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != "[]\n" && got != "[]" {
		t.Errorf("body = %q, want an empty JSON array", got)
	}
}

// TestBanned_ParseBanListNilBecomesEmptyList is banned's counterpart to
// the whitelist test above, covering the `out == nil` -> `out =
// []BannedPlayer{}` normalization.
func TestBanned_ParseBanListNilBecomesEmptyList(t *testing.T) {
	h := &handler{
		rcon: &fakeRcon{},
		cmdr: fakeModCommander{banListCmd: "banlist", banListOK: true},
		game: "test",
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/players/banned", nil)

	h.banned(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != "[]\n" && got != "[]" {
		t.Errorf("body = %q, want an empty JSON array", got)
	}
}

// TestParseWhitelist_UndeclaredReturnsNil covers commander.go's
// ParseWhitelist nil branch directly: an undeclared whitelist (no
// listRegex compiled) must report nil, mirroring the existing
// ParseBanList-nil coverage for the undeclared-banlist case.
func TestParseWhitelist_UndeclaredReturnsNil(t *testing.T) {
	c := templateCommander{} // whitelistRE is nil: nothing declared
	if got := c.ParseWhitelist("anything"); got != nil {
		t.Errorf("ParseWhitelist(undeclared) = %#v, want nil", got)
	}
}
