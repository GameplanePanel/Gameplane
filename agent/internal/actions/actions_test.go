package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
)

type fakeRcon struct {
	mu      sync.Mutex
	calls   []string
	respond func(cmd string) (string, error)
}

func (f *fakeRcon) Exec(cmd string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(cmd)
	}
	return "ok", nil
}

func (f *fakeRcon) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

// sampleActions covers a no-param action, a param action, and one with
// every parameter type.
func sampleActions() []caps.ServerAction {
	return []caps.ServerAction{
		{ID: "save", Command: "save-all"},
		{
			ID:      "broadcast",
			Command: "say {{.Params.message}}",
			Params:  []caps.ActionParam{{Name: "message", Type: "string", Required: true}},
		},
		{
			ID:      "weather",
			Command: "weather {{.Params.kind}} for {{.Params.secs}}s hard={{.Params.hard}}",
			Params: []caps.ActionParam{
				{Name: "kind", Type: "enum", Enum: []string{"clear", "rain"}, Default: "clear"},
				{Name: "secs", Type: "int", Default: "60"},
				{Name: "hard", Type: "bool", Default: "false"},
			},
		},
	}
}

func newSrv(t *testing.T, rc Rcon, specs []caps.ServerAction) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	Mount(r, rc, "testgame", specs)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func run(t *testing.T, srv *httptest.Server, body any) (int, []byte) {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		srv.URL+"/actions/run", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestRun_NoParams(t *testing.T) {
	rc := &fakeRcon{}
	srv := newSrv(t, rc, sampleActions())
	status, body := run(t, srv, runReq{ID: "save"})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if rc.last() != "save-all" {
		t.Errorf("rcon got %q, want save-all", rc.last())
	}
}

func TestRun_RendersParams(t *testing.T) {
	rc := &fakeRcon{}
	srv := newSrv(t, rc, sampleActions())
	status, body := run(t, srv, runReq{ID: "broadcast", Params: map[string]string{"message": "hello world"}})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if rc.last() != "say hello world" {
		t.Errorf("rcon got %q", rc.last())
	}
}

func TestRun_DefaultsApplied(t *testing.T) {
	rc := &fakeRcon{}
	srv := newSrv(t, rc, sampleActions())
	// Provide none of the typed params; defaults fill in.
	status, _ := run(t, srv, runReq{ID: "weather"})
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if want := "weather clear for 60s hard=false"; rc.last() != want {
		t.Errorf("rcon got %q, want %q", rc.last(), want)
	}
}

func TestRun_TypedParamsValidated(t *testing.T) {
	rc := &fakeRcon{}
	srv := newSrv(t, rc, sampleActions())
	cases := []struct {
		name   string
		params map[string]string
	}{
		{"bad int", map[string]string{"secs": "soon"}},
		{"bad bool", map[string]string{"hard": "maybe"}},
		{"bad enum", map[string]string{"kind": "snow"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := run(t, srv, runReq{ID: "weather", Params: tc.params})
			if status != http.StatusBadRequest {
				t.Errorf("status=%d, want 400", status)
			}
			if rc.last() != "" {
				t.Errorf("rcon should not run on invalid params, got %q", rc.last())
			}
		})
	}
}

func TestRun_RequiredParamMissing(t *testing.T) {
	rc := &fakeRcon{}
	srv := newSrv(t, rc, sampleActions())
	status, _ := run(t, srv, runReq{ID: "broadcast"})
	if status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", status)
	}
}

func TestRun_RejectsInjection(t *testing.T) {
	rc := &fakeRcon{}
	srv := newSrv(t, rc, sampleActions())
	// A newline in a string param could chain a second RCON command.
	status, _ := run(t, srv, runReq{ID: "broadcast", Params: map[string]string{"message": "hi\nstop"}})
	if status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", status)
	}
	if rc.last() != "" {
		t.Errorf("rcon should not run, got %q", rc.last())
	}
}

func TestRun_UnknownAction(t *testing.T) {
	srv := newSrv(t, &fakeRcon{}, sampleActions())
	status, _ := run(t, srv, runReq{ID: "nope"})
	if status != http.StatusNotFound {
		t.Errorf("status=%d, want 404", status)
	}
}

func TestRun_BadJSON(t *testing.T) {
	srv := newSrv(t, &fakeRcon{}, sampleActions())
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
		srv.URL+"/actions/run", bytes.NewReader([]byte("{not json")))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestRun_EmptyRenderedCommand(t *testing.T) {
	rc := &fakeRcon{}
	srv := newSrv(t, rc, []caps.ServerAction{
		{ID: "noop", Command: "{{.Params.x}}", Params: []caps.ActionParam{{Name: "x"}}},
	})
	status, _ := run(t, srv, runReq{ID: "noop"})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("status=%d, want 422", status)
	}
	if rc.last() != "" {
		t.Errorf("rcon should not run for empty command, got %q", rc.last())
	}
}

func TestRun_RconDisabled(t *testing.T) {
	srv := newSrv(t, rcon.Disabled{}, sampleActions())
	status, _ := run(t, srv, runReq{ID: "save"})
	if status != http.StatusNotImplemented {
		t.Errorf("status=%d, want 501", status)
	}
}

func TestRun_RconErrorNotLeaked(t *testing.T) {
	rc := &fakeRcon{respond: func(string) (string, error) {
		return "", errors.New("dial 127.0.0.1:25575: connection refused")
	}}
	srv := newSrv(t, rc, sampleActions())
	status, body := run(t, srv, runReq{ID: "save"})
	if status != http.StatusBadGateway {
		t.Fatalf("status=%d", status)
	}
	if bytes.Contains(body, []byte("127.0.0.1")) {
		t.Errorf("response leaked upstream detail: %s", body)
	}
}

func TestCompile_DropsBadTemplate(t *testing.T) {
	// A malformed command template disables only that action.
	rc := &fakeRcon{}
	srv := newSrv(t, rc, []caps.ServerAction{
		{ID: "broken", Command: "say {{.Params"},
		{ID: "ok", Command: "list"},
	})
	if status, _ := run(t, srv, runReq{ID: "broken"}); status != http.StatusNotFound {
		t.Errorf("broken action status=%d, want 404", status)
	}
	if status, _ := run(t, srv, runReq{ID: "ok"}); status != http.StatusOK {
		t.Errorf("ok action status=%d, want 200", status)
	}
}

func TestRun_Sequence(t *testing.T) {
	rc := &fakeRcon{}
	srv := newSrv(t, rc, []caps.ServerAction{
		{ID: "save-cycle", Commands: []string{"save-off", "save-all"}},
	})
	status, body := run(t, srv, runReq{ID: "save-cycle"})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	rc.mu.Lock()
	calls := append([]string(nil), rc.calls...)
	rc.mu.Unlock()
	if want := []string{"save-off", "save-all"}; len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("rcon calls = %v, want %v", calls, want)
	}

	var resp runResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if want := "ok\nok"; resp.Raw != want {
		t.Errorf("raw = %q, want %q", resp.Raw, want)
	}
}

func TestRun_SequenceAbortsOnError(t *testing.T) {
	rc := &fakeRcon{respond: func(cmd string) (string, error) {
		if cmd == "second" {
			return "", errors.New("dial 127.0.0.1:25575: connection refused")
		}
		return "ok", nil
	}}
	srv := newSrv(t, rc, []caps.ServerAction{
		{ID: "three-step", Commands: []string{"first", "second", "third"}},
	})
	status, _ := run(t, srv, runReq{ID: "three-step"})
	if status != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", status)
	}
	rc.mu.Lock()
	calls := append([]string(nil), rc.calls...)
	rc.mu.Unlock()
	if want := []string{"first", "second"}; len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("rcon calls = %v, want %v (third must never run)", calls, want)
	}
}

func TestCompile_SkipsIncomplete(t *testing.T) {
	// Missing id or command are skipped at compile.
	got := compile([]caps.ServerAction{
		{ID: "", Command: "x"},
		{ID: "y", Command: ""},
		{ID: "z", Command: "list"},
	})
	if len(got) != 1 {
		t.Fatalf("compiled %d, want 1", len(got))
	}
	if _, ok := got["z"]; !ok {
		t.Errorf("z should have compiled")
	}
}
