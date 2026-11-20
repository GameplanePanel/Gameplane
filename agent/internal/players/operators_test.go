package players

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
	"github.com/go-chi/chi/v5"
)

func TestOperatorsReadConfiguredFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		want  int
		count int
	}{
		{"configured", `[{"uuid":"b","name":"Zoe","level":4,"bypassesPlayerLimit":true},{"uuid":"a","name":"Alex"}]`, http.StatusOK, 2},
		{"equal-case-names", `[{"uuid":"b","name":"alex"},{"uuid":"a","name":"Alex"}]`, http.StatusOK, 2},
		{"empty", `[]`, http.StatusOK, 0},
		{"missing", "", http.StatusOK, 0},
		{"malformed", `{broken`, http.StatusBadGateway, 0},
		{"null", `null`, http.StatusBadGateway, 0},
		{"missing-identity", `[{}]`, http.StatusBadGateway, 0},
		{"too-large", strings.Repeat(" ", maxOperatorsBytes+1), http.StatusBadGateway, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.body != "" {
				if err := os.WriteFile(filepath.Join(root, "ops.json"), []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := chi.NewRouter()
			Mount(r, &fakeRcon{}, "minecraft-java", nil, root)
			response := httptest.NewRecorder()
			r.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/players/operators?path=server.properties", nil))
			if response.Code != tc.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.want != http.StatusOK {
				if response.Body.String() != "operator list unavailable\n" {
					t.Fatalf("unsafe error: %s", response.Body.String())
				}
				return
			}
			var result []Operator
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result == nil || len(result) != tc.count {
				t.Fatalf("operators=%+v", result)
			}
			if tc.count > 0 && result[0].Name != "Alex" {
				t.Fatalf("list is not alphabetical: %+v", result)
			}
			if strings.Contains(response.Body.String(), "level") || strings.Contains(response.Body.String(), "bypassesPlayerLimit") {
				t.Fatal("non-display metadata exposed")
			}
		})
	}
}

func TestOperatorsRejectUnsafeFile(t *testing.T) {
	t.Parallel()
	for _, directory := range []bool{false, true} {
		root := t.TempDir()
		path := filepath.Join(root, "ops.json")
		if directory {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		} else {
			external := filepath.Join(t.TempDir(), "secret.json")
			if err := os.WriteFile(external, []byte(`[{"name":"secret"}]`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, path); err != nil {
				t.Fatal(err)
			}
		}
		r := chi.NewRouter()
		Mount(r, &fakeRcon{}, "minecraft-java", nil, root)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/players/operators", nil))
		if response.Code != http.StatusBadGateway || response.Body.String() != "operator list unavailable\n" {
			t.Fatalf("unsafe file response=%d %s", response.Code, response.Body.String())
		}
	}
}

func TestOperatorsCapabilityDoesNotDependOnRCON(t *testing.T) {
	t.Parallel()
	for _, disabled := range []bool{false, true} {
		r := chi.NewRouter()
		Mount(r, &fakeRcon{respond: func(string) (string, error) {
			if disabled {
				return "", rcon.ErrDisabled
			}
			return "There are 0 of a max of 20 players online:", nil
		}}, "minecraft-java", nil, t.TempDir())
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/players", nil))
		var result Snapshot
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || !result.Capabilities.Operators {
			t.Fatalf("snapshot=%+v status=%d", result, response.Code)
		}
		if disabled && result.Online != -1 {
			t.Fatal("disabled RCON should report an unknown count")
		}
	}
}

func TestOperatorsUnsupportedGameDoesNotReadFile(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	Mount(r, &fakeRcon{}, "valheim", nil, t.TempDir())
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/players/operators", nil))
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
