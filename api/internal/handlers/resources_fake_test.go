package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func mountResourcesRouter(k *kube.Client) *chi.Mux {
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountResources(r, reg, nil)
	return r
}

func TestResources_NamespacedCRUD(t *testing.T) {
	k := fakeKubeClient(newServerObj("gameplane-games", "alpha"))
	r := mountResourcesRouter(k)

	t.Run("list namespaced", func(t *testing.T) {
		rr := do(t, r, "GET", "/servers/", nil)
		if rr.Code != 200 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("get one", func(t *testing.T) {
		rr := do(t, r, "GET", "/servers/alpha", nil)
		if rr.Code != 200 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("get missing → 404", func(t *testing.T) {
		rr := do(t, r, "GET", "/servers/ghost", nil)
		if rr.Code != 404 {
			t.Fatalf("got %d", rr.Code)
		}
	})

	t.Run("create", func(t *testing.T) {
		body := map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata":   map[string]any{"name": "beta"},
			"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}},
		}
		rr := do(t, r, "POST", "/servers/", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s, want 201", rr.Code, rr.Body)
		}
	})

	t.Run("delete", func(t *testing.T) {
		rr := do(t, r, "DELETE", "/servers/alpha", nil)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})
}

func TestResources_DecodeBadJSON(t *testing.T) {
	k := fakeKubeClient()
	r := mountResourcesRouter(k)
	rr := doRaw(t, r, "POST", "/servers/", "not json")
	if rr.Code == 200 {
		t.Fatal("bad json should not create")
	}
}

func TestResources_ClusterScoped(t *testing.T) {
	// gametemplates are cluster-scoped — no namespace in path.
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": "minecraft"},
		"spec":       map[string]any{"image": "x", "game": "minecraft", "version": "1"},
	}
	k := fakeKubeClient()
	r := mountResourcesRouter(k)
	rr := do(t, r, "POST", "/templates/", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s, want 201", rr.Code, rr.Body)
	}
	rr = do(t, r, "GET", "/templates/minecraft", nil)
	if rr.Code != 200 {
		t.Fatalf("get: %d %s", rr.Code, rr.Body)
	}
}

func TestResources_ManagedTemplateBlocked(t *testing.T) {
	tmpl := newServerObj("", "minecraft")
	// Re-shape into a GameTemplate-shaped object with the managed-by label.
	tmpl.Object["apiVersion"] = "gameplane.local/v1alpha1"
	tmpl.Object["kind"] = "GameTemplate"
	delete(tmpl.Object["metadata"].(map[string]any), "namespace")
	tmpl.SetLabels(map[string]string{
		"gameplane.local/managed-by":  "Module",
		"gameplane.local/module-name": "minecraft-vanilla",
	})

	k := fakeKubeClient(tmpl)
	r := mountResourcesRouter(k)

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": "minecraft"},
		"spec":       map[string]any{"image": "x"},
	}

	rr := do(t, r, "PUT", "/templates/minecraft", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("put managed: got %d %s", rr.Code, rr.Body)
	}

	rr = do(t, r, "DELETE", "/templates/minecraft", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete managed: got %d", rr.Code)
	}
}

func TestDecode_OversizeRejected(t *testing.T) {
	// >1MiB body is rejected by io.LimitReader inside decode().
	huge := strings.Repeat("a", (1<<20)+10)
	body := `{"apiVersion":"gameplane.local/v1alpha1","kind":"GameServer","metadata":{"name":"x"},"spec":{"junk":"` + huge + `"}}`
	k := fakeKubeClient()
	r := mountResourcesRouter(k)
	rr := doRaw(t, r, "POST", "/servers/", body)
	if rr.Code == 200 {
		t.Fatal("oversize body should not create")
	}
}

// doRaw posts a literal string body, bypassing JSON-encoding so we can
// send malformed payloads to test the parser.
func doRaw(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
