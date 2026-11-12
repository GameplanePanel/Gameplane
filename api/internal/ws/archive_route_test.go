package ws

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/go-chi/chi/v5"
)

func TestArchiveProxyBindsTargetAndPreservesSelection(t *testing.T) {
	calls := 0
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/targets/gs-uid/files/archive" || !slices.Equal(r.URL.Query()["path"], []string{"/one/a", "/two/a"}) {
			t.Errorf("archive request = %s", r.URL)
		}
		if r.Header.Get("X-Gameplane-Server-UID") != "" {
			t.Error("browser precondition leaked upstream")
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("denied"))
	}))
	defer agent.Close()
	k := &kube.Client{}
	_ = streamTestRegistry(k)
	p := &proxy{k: k, transport: testDirectTransport(agent)}
	router := chi.NewRouter()
	router.Get("/servers/{name}/files/archive", p.agentHTTP("/files/archive"))
	for _, uid := range []string{"gs-uid", "previous"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/files/archive?path=%2Fone%2Fa&path=%2Ftwo%2Fa", nil)
		req.Header.Set("X-Gameplane-Server-UID", uid)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if uid == "previous" {
			if res.Code != http.StatusConflict {
				t.Fatalf("replacement status %d", res.Code)
			}
		} else if res.Code != http.StatusForbidden || res.Body.String() != "denied" {
			t.Fatalf("denial = %d %q", res.Code, res.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d", calls)
	}
}
