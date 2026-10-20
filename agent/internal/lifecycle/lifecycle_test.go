package lifecycle

import (
	"context"
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
	mu     sync.Mutex
	got    []string
	failOn map[string]error
}

func (f *fakeRcon) Exec(cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, cmd)
	if err, ok := f.failOn[cmd]; ok {
		return "", err
	}
	return "ok", nil
}

func (f *fakeRcon) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.got))
	copy(out, f.got)
	return out
}

func post(t *testing.T, h http.Handler, path string) response {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return r
}

func TestStop_RunsDeclaredSequenceInOrder(t *testing.T) {
	rc := &fakeRcon{}
	r := chi.NewRouter()
	Mount(r, rc, "minecraft", &caps.Lifecycle{Stop: []string{"say bye", "stop"}})

	got := post(t, r, "/lifecycle/stop")
	if !got.Stopped {
		t.Fatalf("expected stopped=true, got %+v", got)
	}
	calls := rc.calls()
	if len(calls) != 2 || calls[0] != "say bye" || calls[1] != "stop" {
		t.Fatalf("commands not run in order: %v", calls)
	}
}

func TestStop_ToleratesRconError(t *testing.T) {
	// The stop command brings RCON down, so an error is the expected outcome
	// — the call must still report success and run every command.
	rc := &fakeRcon{failOn: map[string]error{"stop": errors.New("EOF")}}
	r := chi.NewRouter()
	Mount(r, rc, "minecraft", &caps.Lifecycle{Stop: []string{"save-all", "stop"}})

	got := post(t, r, "/lifecycle/stop")
	if !got.Stopped {
		t.Fatalf("expected stopped=true despite the rcon error, got %+v", got)
	}
	if calls := rc.calls(); len(calls) != 2 {
		t.Fatalf("expected both commands attempted, got %v", calls)
	}
}

func TestStop_UnsupportedWhenNoSequence(t *testing.T) {
	rc := &fakeRcon{}
	r := chi.NewRouter()
	Mount(r, rc, "valheim", nil) // no lifecycle declared

	got := post(t, r, "/lifecycle/stop")
	if got.Stopped {
		t.Fatalf("expected stopped=false for a game with no stop sequence, got %+v", got)
	}
	if calls := rc.calls(); len(calls) != 0 {
		t.Fatalf("expected no rcon calls, got %v", calls)
	}
}

func TestPick(t *testing.T) {
	if Pick(nil).Supported() {
		t.Fatal("nil spec should be unsupported")
	}
	if Pick(&caps.Lifecycle{}).Supported() {
		t.Fatal("empty stop list should be unsupported")
	}
	if !Pick(&caps.Lifecycle{Stop: []string{"stop"}}).Supported() {
		t.Fatal("declared stop should be supported")
	}
}
