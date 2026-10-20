package status

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
	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
)

type fakeRcon struct {
	mu      sync.Mutex
	calls   int
	respond func(cmd string) (string, error)
}

func (f *fakeRcon) Exec(cmd string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(cmd)
	}
	return "", nil
}

func (f *fakeRcon) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func sampleStatus() *caps.Status {
	return &caps.Status{Metrics: []caps.StatusMetric{
		{ID: "tps", DisplayName: "TPS", Command: "tps", Regex: `TPS: (?P<value>[0-9.]+)`, Unit: ""},
		{ID: "seed", DisplayName: "Seed", Command: "seed", Regex: `Seed: \[(?P<value>-?[0-9]+)\]`},
	}}
}

func newSrv(t *testing.T, rc Rcon, spec *caps.Status) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	Mount(r, rc, spec)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server) (int, []Result) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/status", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out []Result
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return resp.StatusCode, out
}

func TestServe_ExtractsValues(t *testing.T) {
	rc := &fakeRcon{respond: func(cmd string) (string, error) {
		switch cmd {
		case "tps":
			return "TPS: 19.87", nil
		case "seed":
			return "Seed: [-4096]", nil
		}
		return "", nil
	}}
	status, res := get(t, newSrv(t, rc, sampleStatus()))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results: %+v", len(res), res)
	}
	if res[0].ID != "tps" || res[0].Value != "19.87" || res[0].DisplayName != "TPS" {
		t.Errorf("tps result = %+v", res[0])
	}
	if res[1].Value != "-4096" {
		t.Errorf("seed result = %+v", res[1])
	}
}

func TestServe_NoMatchEmptyValue(t *testing.T) {
	rc := &fakeRcon{respond: func(string) (string, error) { return "unparseable", nil }}
	_, res := get(t, newSrv(t, rc, sampleStatus()))
	if len(res) != 2 {
		t.Fatalf("got %d results", len(res))
	}
	for _, r := range res {
		if r.Value != "" {
			t.Errorf("metric %s value = %q, want empty", r.ID, r.Value)
		}
	}
}

func TestServe_RconErrorSkipsValue(t *testing.T) {
	rc := &fakeRcon{respond: func(cmd string) (string, error) {
		if cmd == "tps" {
			return "", errors.New("boom")
		}
		return "Seed: [7]", nil
	}}
	_, res := get(t, newSrv(t, rc, sampleStatus()))
	if len(res) != 2 {
		t.Fatalf("got %d results", len(res))
	}
	if res[0].Value != "" {
		t.Errorf("errored metric should have empty value, got %q", res[0].Value)
	}
	if res[1].Value != "7" {
		t.Errorf("other metric should still resolve, got %q", res[1].Value)
	}
}

func TestServe_RconDisabledEmpty(t *testing.T) {
	status, res := get(t, newSrv(t, rcon.Disabled{}, sampleStatus()))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if len(res) != 0 {
		t.Errorf("want empty results when rcon disabled, got %+v", res)
	}
}

func TestServe_NilSpecEmpty(t *testing.T) {
	rc := &fakeRcon{}
	status, res := get(t, newSrv(t, rc, nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if len(res) != 0 {
		t.Errorf("want empty results for nil spec, got %+v", res)
	}
	if rc.count() != 0 {
		t.Errorf("no metrics declared, rcon should not be called")
	}
}

func TestServe_Caches(t *testing.T) {
	rc := &fakeRcon{respond: func(string) (string, error) { return "TPS: 20", nil }}
	srv := newSrv(t, rc, &caps.Status{Metrics: []caps.StatusMetric{
		{ID: "tps", Command: "tps", Regex: `TPS: (?P<value>[0-9.]+)`},
	}})
	get(t, srv)
	first := rc.count()
	get(t, srv) // within the 5s TTL → served from cache
	if rc.count() != first {
		t.Errorf("second call re-executed rcon: %d -> %d", first, rc.count())
	}
}

func TestCompile_DropsInvalid(t *testing.T) {
	got := compile([]caps.StatusMetric{
		{ID: "a", Command: "c", Regex: "(unclosed"},      // bad regex
		{ID: "b", Command: "c", Regex: `(?P<other>\d+)`}, // no value group
		{ID: "", Command: "c", Regex: `(?P<value>\d+)`},  // missing id
		{ID: "d", Command: "", Regex: `(?P<value>\d+)`},  // missing command
		{ID: "e", Command: "c", Regex: `(?P<value>\d+)`}, // valid
	})
	if len(got) != 1 || got[0].id != "e" {
		t.Fatalf("compiled = %+v, want only e", got)
	}
}
