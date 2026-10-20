package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// fakeKubeClient builds a kube.Client wired to fakes for both the
// dynamic and the typed surfaces. Tests pass unstructured objects for
// the dynamic store; for typed objects (Secrets), use fakeKubeClientWithSecrets.
func fakeKubeClient(objs ...runtime.Object) *kube.Client {
	scheme := runtime.NewScheme()
	gvkr := map[schema.GroupVersionResource]string{
		kube.GVRCluster:        "ClusterList",
		kube.GVRModule:         "ModuleList",
		kube.GVRModuleSource:   "ModuleSourceList",
		kube.GVRs["servers"]:   "GameServerList",
		kube.GVRs["templates"]: "GameTemplateList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvkr, objs...)
	return &kube.Client{Dynamic: dyn, Typed: kubefake.NewClientset()}
}

func newModule(name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Module",
		"metadata":   map[string]any{"name": name},
		"spec":       spec,
	}}
}

func newModuleWithStatus(name string, spec, status map[string]any) *unstructured.Unstructured {
	m := newModule(name, spec)
	m.Object["status"] = status
	return m
}

func newSource(name string, modules []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "ModuleSource",
		"metadata":   map[string]any{"name": name},
		"status":     map[string]any{"modules": modules},
	}}
}

func mountModulesRouter(k *kube.Client) http.Handler {
	r := chi.NewRouter()
	MountModules(r, k, "gameplane-system")
	return r
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, buf)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestMountModules_ListSourcesAndInstalled(t *testing.T) {
	k := fakeKubeClient(
		newSource("upstream", []any{map[string]any{"name": "minecraft"}}),
		newModule("mc", map[string]any{"source": map[string]any{"name": "upstream"}, "name": "minecraft", "version": "1.21"}),
	)
	r := mountModulesRouter(k)

	rr := do(t, r, "GET", "/modules/sources", nil)
	if rr.Code != 200 {
		t.Fatalf("sources: %d %s", rr.Code, rr.Body)
	}

	rr = do(t, r, "GET", "/modules/", nil)
	if rr.Code != 200 {
		t.Fatalf("installed: %d %s", rr.Code, rr.Body)
	}
}

func TestMountModules_GetInstalled(t *testing.T) {
	k := fakeKubeClient(newModule("alpha", map[string]any{"source": map[string]any{"name": "u"}, "name": "x", "version": "1"}))
	r := mountModulesRouter(k)

	rr := do(t, r, "GET", "/modules/alpha", nil)
	if rr.Code != 200 {
		t.Fatalf("get: %d %s", rr.Code, rr.Body)
	}

	rr = do(t, r, "GET", "/modules/missing", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", rr.Code)
	}
}

func TestMountModules_Install(t *testing.T) {
	k := fakeKubeClient()
	r := mountModulesRouter(k)

	t.Run("happy path", func(t *testing.T) {
		body := map[string]any{"source": "upstream", "module": "minecraft", "version": "1.21"}
		rr := do(t, r, "POST", "/modules/", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		// F-083 regression: writeJSONCreated sets Content-Type before
		// WriteHeader, so the 201 body isn't sniffed as text/plain.
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
	})

	t.Run("missing source/module", func(t *testing.T) {
		rr := do(t, r, "POST", "/modules/", map[string]any{"module": "x"})
		// F-077: the handler now routes this hand-written validation error
		// through httperr.WriteCode(400), not the httperr.Write default of
		// 500 for an unclassified error.
		if rr.Code == http.StatusCreated {
			t.Fatal("missing source should not create")
		}
	})

	t.Run("missing source/module returns 400", func(t *testing.T) {
		rr := do(t, r, "POST", "/modules/", map[string]any{"module": "x"})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400; body=%s", rr.Code, rr.Body)
		}
	})

	t.Run("invalid name", func(t *testing.T) {
		body := map[string]any{"source": "u", "module": "x", "version": "1", "name": "BAD_NAME"}
		rr := do(t, r, "POST", "/modules/", body)
		if rr.Code == http.StatusCreated {
			t.Fatal("BAD_NAME should be rejected")
		}
	})

	t.Run("invalid name returns 400", func(t *testing.T) {
		body := map[string]any{"source": "u", "module": "x", "version": "1", "name": "BAD_NAME"}
		rr := do(t, r, "POST", "/modules/", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400; body=%s", rr.Code, rr.Body)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/modules/", strings.NewReader("not json"))
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code == http.StatusCreated {
			t.Fatal("bogus body created a Module")
		}
	})
}

func TestMountModules_Upgrade(t *testing.T) {
	k := fakeKubeClient(newModule("alpha", map[string]any{"source": map[string]any{"name": "u"}, "name": "x", "version": "1.0"}))
	r := mountModulesRouter(k)
	rr := do(t, r, "PATCH", "/modules/alpha", map[string]any{"version": "1.1"})
	if rr.Code != 200 {
		t.Fatalf("upgrade: %d %s", rr.Code, rr.Body)
	}
}

func TestMountModules_Uninstall(t *testing.T) {
	k := fakeKubeClient(newModule("alpha", map[string]any{"source": map[string]any{"name": "u"}, "name": "x", "version": "1.0"}))
	r := mountModulesRouter(k)
	rr := do(t, r, "DELETE", "/modules/alpha", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("uninstall: %d %s", rr.Code, rr.Body)
	}
}

func TestMountModules_Catalog(t *testing.T) {
	k := fakeKubeClient(
		newSource("upstream", []any{
			map[string]any{
				"name":          "minecraft",
				"displayName":   "Minecraft",
				"summary":       "vanilla",
				"categories":    []any{"Sandbox"},
				"latestVersion": "1.21",
				"versions":      []any{"1.21", "1.20"},
			},
		}),
		newModuleWithStatus("minecraft", map[string]any{
			"source":  map[string]any{"name": "upstream"},
			"name":    "minecraft",
			"version": "1.21",
		}, map[string]any{
			"phase":           "Ready",
			"appliedVersion":  "1.21",
			"appliedDigest":   "sha256:aaa",
			"previousVersion": "1.20",
			"previousDigest":  "sha256:bbb",
		}),
	)
	r := mountModulesRouter(k)
	rr := do(t, r, "GET", "/modules/catalog", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var resp catalogResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Name != "minecraft" {
		t.Fatalf("got %+v", resp.Items)
	}
	// The rollback/digest status fields must pass through verbatim from
	// Module.status so the dashboard can surface the pin and rollback target.
	got := resp.Items[0]
	if !reflect.DeepEqual(got.Categories, []string{"Sandbox"}) {
		t.Errorf("categories = %v, want [Sandbox] (author-declared, from module.yaml)", got.Categories)
	}
	if got.AppliedDigest != "sha256:aaa" {
		t.Errorf("appliedDigest = %q, want sha256:aaa", got.AppliedDigest)
	}
	if got.PreviousVersion != "1.20" {
		t.Errorf("previousVersion = %q, want 1.20", got.PreviousVersion)
	}
	if got.PreviousDigest != "sha256:bbb" {
		t.Errorf("previousDigest = %q, want sha256:bbb", got.PreviousDigest)
	}
}

// TestMountModules_Catalog_InstalledOnlyHasEmptySourcesNotNull covers the
// installed-but-uncatalogued branch: a Module CR whose name isn't (yet) in
// any ModuleSource's catalog. Its CatalogEntry is built without ever
// touching Sources, so the field must still serialize as "[]", never
// "null" — the web client types it as a required array and iterates it
// unguarded, so a null there crashes the Modules page.
func TestMountModules_Catalog_InstalledOnlyHasEmptySourcesNotNull(t *testing.T) {
	k := fakeKubeClient(
		newModuleWithStatus("orphan", map[string]any{
			"source":  map[string]any{"name": "gone"},
			"name":    "orphan",
			"version": "1.0",
		}, map[string]any{
			"phase":          "Ready",
			"appliedVersion": "1.0",
		}),
	)
	r := mountModulesRouter(k)
	rr := do(t, r, "GET", "/modules/catalog", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), `"sources":null`) {
		t.Fatalf("body = %s, sources must never serialize as null", rr.Body)
	}
	var resp catalogResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Name != "orphan" {
		t.Fatalf("got %+v", resp.Items)
	}
	if resp.Items[0].Sources == nil || len(resp.Items[0].Sources) != 0 {
		t.Errorf("sources = %#v, want a non-nil empty slice", resp.Items[0].Sources)
	}
}
