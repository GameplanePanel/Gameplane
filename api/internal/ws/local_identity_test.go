package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

func TestLocalAgentRouteRejectsAuthorizedReplacement(t *testing.T) {
	k := &kube.Client{}
	_ = streamTestRegistry(k)
	p := &proxy{k: k}
	r := chi.NewRouter()
	r.Use(rbac.Middleware(authorizationSnapshot{uid: "old-uid"}))
	r.Get("/servers/{name}/status", p.agentRoute(func(*proxy) http.HandlerFunc {
		return func(http.ResponseWriter, *http.Request) { t.Error("replacement reached agent operation") }
	}))
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha/status", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestLocalAgentRoutePinsUIDWithoutMutatingSharedProxy(t *testing.T) {
	k := &kube.Client{}
	_ = streamTestRegistry(k)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/targets/gs-uid/files/write" {
			t.Errorf("unbound path: %s", req.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	p := &proxy{k: k, transport: testDirectTransport(srv)}
	r := chi.NewRouter()
	r.Post("/servers/{name}/files/write", p.agentHTTP("/files/write"))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha/files/write", strings.NewReader("data")))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if p.remoteUID != "" {
		t.Fatal("shared proxy identity mutated")
	}
}

func TestDirectTransportUIDRoutesRejectReplacementAgent(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// A new agent only accepts its own immutable UID. The stale operation
		// must carry old-uid even if DNS now resolves to this replacement.
		if !strings.HasPrefix(req.URL.Path, "/v1/targets/old-uid/") {
			t.Errorf("UID guard bypassed by path %s", req.URL.Path)
		}
		http.NotFound(w, req)
	}))
	defer srv.Close()
	tr := testDirectTransport(srv)
	target := agentTarget{name: "alpha", namespace: "gameplane-games", uid: "old-uid"}
	resp, err := tr.Do(t.Context(), agentRequest{target: target, method: "POST", path: "/files/write"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("HTTP status %d", resp.StatusCode)
	}
	conn, response, err := tr.Dial(t.Context(), target, "/console", "")
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("WebSocket accepted replacement: response %v, err %v", response, err)
	}
}

func TestLocalAgentClientUsesOwnershipBoundUID(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/targets/authorized-uid/mods" {
			t.Errorf("unbound path: %s", req.URL.Path)
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	client := testAgentClient(srv)
	r := chi.NewRouter()
	r.Use(rbac.Middleware(authorizationSnapshot{uid: "authorized-uid"}))
	r.Get("/servers/{name}/mods", func(w http.ResponseWriter, req *http.Request) {
		var mods []any
		if err := client.GetJSON(req.Context(), "alpha", "gameplane-games", "/mods", &mods); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha/mods", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}

func TestLocalStdinActionChecksWorkloadOwnership(t *testing.T) {
	k := newActionsFakeClient(t, stdinActionTemplate())
	gs, err := k.GetServer(t.Context(), "gameplane-games", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	gs.SetUID(types.UID("gs-uid"))
	if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").Update(t.Context(), gs, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	// A valid server/template with no owned workload must not reach attach.
	k.Typed = kubefake.NewClientset()
	writer := &fakeStdinWriter{}
	p := &proxy{k: k, stdin: writer}
	r := chi.NewRouter()
	r.Post("/servers/{name}/actions/run", p.agentAction())
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha/actions/run", strings.NewReader(`{"id":"announce","params":{"message":"hello"}}`)))
	if rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != "not found" {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if writer.called {
		t.Fatal("stdin reached a workload without a checked ownership chain")
	}
}
