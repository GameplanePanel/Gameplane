package quiesce

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

type fakeRcon struct {
	mu       sync.Mutex
	got      []string
	respond  func(cmd string) (string, error)
	failNext map[string]error
}

func (f *fakeRcon) Exec(cmd string) (string, error) {
	f.mu.Lock()
	f.got = append(f.got, cmd)
	respond := f.respond
	if err, ok := f.failNext[cmd]; ok {
		delete(f.failNext, cmd)
		f.mu.Unlock()
		return "", err
	}
	f.mu.Unlock()
	if respond == nil {
		return "", nil
	}
	return respond(cmd)
}

func (f *fakeRcon) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.got))
	copy(out, f.got)
	return out
}

func TestPick(t *testing.T) {
	// Quiesce is entirely module-driven: a declared sequence is
	// supported, nothing declared is a no-op, regardless of game.
	if !Pick(minecraftQuiesceSpec()).Supported() {
		t.Error("declared spec should be supported")
	}
	if Pick(nil).Supported() {
		t.Error("nil spec should be unsupported")
	}
	// A half-declared spec (missing unquiesce) is ignored — never pause
	// a game we can't resume.
	if Pick(&caps.Quiesce{Quiesce: []string{"pause"}}).Supported() {
		t.Error("spec without unquiesce should be unsupported")
	}
}

func TestUnsupportedQuiescerNoOp(t *testing.T) {
	rc := &fakeRcon{}
	u := unsupportedQuiescer{}
	if u.Supported() {
		t.Fatal("unsupported.Supported() = true")
	}
	if err := u.Quiesce(rc); err != nil {
		t.Errorf("Quiesce: %v", err)
	}
	if err := u.Unquiesce(rc); err != nil {
		t.Errorf("Unquiesce: %v", err)
	}
	if calls := rc.calls(); len(calls) != 0 {
		t.Errorf("unsupported quiescer should not invoke RCON, got %v", calls)
	}
}

// --- HTTP-level smoke tests --------------------------------------------------

func newTestRouter(rc Rcon, spec *caps.Quiesce) *chi.Mux {
	r := chi.NewRouter()
	Mount(r, rc, "testgame", spec)
	return r
}

func decodeResponse(t *testing.T, body []byte) response {
	t.Helper()
	var got response
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func doPOST(t *testing.T, srv *httptest.Server, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return resp.StatusCode, out
}

func TestQuiesceHandlerMinecraftHappyPath(t *testing.T) {
	rc := &fakeRcon{}
	srv := httptest.NewServer(newTestRouter(rc, minecraftQuiesceSpec()))
	defer srv.Close()

	status, body := doPOST(t, srv, "/quiesce")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	got := decodeResponse(t, body)
	if !got.Quiesced {
		t.Errorf("response = %+v, want Quiesced=true", got)
	}
}

func TestQuiesceHandlerUnsupportedGameDegrades(t *testing.T) {
	rc := &fakeRcon{}
	srv := httptest.NewServer(newTestRouter(rc, nil))
	defer srv.Close()

	status, body := doPOST(t, srv, "/quiesce")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	got := decodeResponse(t, body)
	if got.Quiesced {
		t.Errorf("unsupported game should not report Quiesced=true: %+v", got)
	}
	if got.Reason == "" {
		t.Errorf("expected a non-empty Reason for unsupported game")
	}
	if calls := rc.calls(); len(calls) != 0 {
		t.Errorf("unsupported game must not invoke RCON, got %v", calls)
	}
}

func TestQuiesceHandlerRconErrorIs502(t *testing.T) {
	rc := &fakeRcon{failNext: map[string]error{"save-off": errors.New("connection refused")}}
	srv := httptest.NewServer(newTestRouter(rc, minecraftQuiesceSpec()))
	defer srv.Close()

	status, _ := doPOST(t, srv, "/quiesce")
	if status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", status)
	}
}

func TestUnquiesceHandlerHappyPath(t *testing.T) {
	rc := &fakeRcon{}
	srv := httptest.NewServer(newTestRouter(rc, minecraftQuiesceSpec()))
	defer srv.Close()

	status, body := doPOST(t, srv, "/unquiesce")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	got := decodeResponse(t, body)
	if got.Quiesced {
		t.Errorf("response = %+v, want Quiesced=false after unquiesce", got)
	}
	if calls := rc.calls(); len(calls) != 1 || calls[0] != "save-on" {
		t.Errorf("rcon calls = %v, want [save-on]", calls)
	}
}
