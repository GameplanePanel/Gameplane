package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestInstallationInstanceName(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{name: "unset", want: "gameplane"},
		{name: "empty", raw: `{"instanceName":""}`, want: "gameplane"},
		{name: "whitespace", raw: `{"instanceName":" \t "}`, want: "gameplane"},
		{name: "configured", raw: `{"instanceName":"  Baylem's 世界  ","externalURL":"https://private.example","defaultNamespace":"private-ns"}`, want: "Baylem's 世界"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			if tc.raw != "" {
				if _, err := store.DB.ExecContext(t.Context(), "INSERT INTO config(key, value) VALUES ('general', ?)", tc.raw); err != nil {
					t.Fatal(err)
				}
			}
			router := chi.NewRouter()
			MountInstallation(router, store, false, "v0.4.1")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/installation", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["instanceName"] != tc.want || len(body) != 4 {
				t.Fatalf("unexpected installation response: %v", body)
			}
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("installation leaked general configuration")
			}
		})
	}
}

func TestInstallationNameRefreshesAfterConfigSave(t *testing.T) {
	store := newTestStore(t)
	router := chi.NewRouter()
	MountInstallation(router, store, true, "v0.4.1")
	MountConfig(router, store, nil, false, "", nil)
	for _, name := range []string{"First name", "Renamed instance"} {
		saved := httptest.NewRecorder()
		body := `{"instanceName":"` + name + `","defaultNamespace":"games"}`
		router.ServeHTTP(saved, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/config/general", strings.NewReader(body)))
		if saved.Code != http.StatusOK {
			t.Fatalf("save status=%d body=%s", saved.Code, saved.Body.String())
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/installation", nil))
		var got struct {
			InstanceName string `json:"instanceName"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.InstanceName != name {
			t.Fatalf("name=%q want=%q", got.InstanceName, name)
		}
	}
}

func TestInstallationNameErrorsDoNotExposeConfiguration(t *testing.T) {
	for _, brokenDatabase := range []bool{false, true} {
		store := newTestStore(t)
		if brokenDatabase {
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		} else if _, err := store.DB.ExecContext(t.Context(), "INSERT INTO config(key, value) VALUES ('general', ?)", `{"instanceName":"private`); err != nil {
			t.Fatal(err)
		}
		router := chi.NewRouter()
		MountInstallation(router, store, false, "v0.4.1")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/installation", nil))
		if response.Code != http.StatusInternalServerError || response.Body.String() != "internal error\n" {
			t.Fatalf("unexpected safe error: status=%d body=%s", response.Code, response.Body.String())
		}
	}
}
