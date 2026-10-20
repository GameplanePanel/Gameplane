//go:build envtest

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// TestResources_GameServerCRUDRoundTrip — POST a GameServer at /servers,
// confirm GET returns it, PUT mutates it, DELETE removes it. Verifies
// the dynamic client path end-to-end against a real apiserver with
// the Gameplane CRDs installed.
func TestResources_GameServerCRUDRoundTrip(t *testing.T) {
	name := uniqueResourceName("smp")

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
		},
	}

	// CREATE.
	resp := doJSON(t, http.MethodPost, "/servers", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers status = %d, want 201; body=%s", resp.StatusCode, readBody(t, resp))
	}

	// GET.
	resp = doJSON(t, http.MethodGet, "/servers/"+name, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%s status = %d", name, resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()

	if got["spec"].(map[string]any)["templateRef"].(map[string]any)["name"] != "minecraft" {
		t.Fatalf("GET round-trip lost templateRef: %#v", got["spec"])
	}

	// PUT (update). templateRef is immutable (F-047), so exercise the
	// generic update path via a mutable field instead: flip suspended and
	// confirm the on-cluster object reflects it. Changing templateRef via
	// PUT is covered separately by TestResources_GameServerUpdate_TemplateRefImmutable.
	got["spec"].(map[string]any)["suspend"] = true
	resp = doJSON(t, http.MethodPut, "/servers/"+name, got)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /servers/%s status = %d; body=%s", name, resp.StatusCode, readBody(t, resp))
	}

	gs := getDynamic(t, gvrServers(), scope.DefaultNamespace, name)
	suspend, _, _ := unstructured.NestedBool(gs.Object, "spec", "suspend")
	if !suspend {
		t.Errorf("spec.suspend on cluster = %v, want true", suspend)
	}

	// DELETE.
	resp = doJSON(t, http.MethodDelete, "/servers/"+name, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /servers/%s status = %d; body=%s", name, resp.StatusCode, readBody(t, resp))
	}

	if _, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound after DELETE, got err=%v", err)
	}
}

// TestResources_GameServerUpdate_TemplateRefImmutable — PUT /servers/{name}
// with a changed spec.templateRef.name is rejected by the handler itself
// (409, before the request ever reaches the apiserver's CEL rule) with a
// clear message, and the on-cluster object is left untouched (F-047).
func TestResources_GameServerUpdate_TemplateRefImmutable(t *testing.T) {
	name := uniqueResourceName("smp")

	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
		},
	}
	resp := doJSON(t, http.MethodPost, "/servers", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers status = %d, want 201; body=%s", resp.StatusCode, readBody(t, resp))
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})

	body["spec"].(map[string]any)["templateRef"] = map[string]any{"name": "valheim"}
	resp = doJSON(t, http.MethodPut, "/servers/"+name, body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("PUT /servers/%s (templateRef change) status = %d, want 409; body=%s",
			name, resp.StatusCode, readBody(t, resp))
	}

	gs := getDynamic(t, gvrServers(), scope.DefaultNamespace, name)
	gotTmpl, _, _ := unstructured.NestedString(gs.Object, "spec", "templateRef", "name")
	if gotTmpl != "minecraft" {
		t.Errorf("templateRef.name on cluster = %q, want unchanged \"minecraft\"", gotTmpl)
	}
}

// TestResources_TemplateIsClusterScoped — POST /templates without a
// namespace query, GET back. Templates are cluster-scoped; the handler
// must NOT route them through resolveNS.
func TestResources_TemplateIsClusterScoped(t *testing.T) {
	name := uniqueResourceName("tmpl")
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"displayName": "Test " + name,
			"game":        "test",
			"version":     "1",
			"image":       "ghcr.io/test/x:1",
		},
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrTemplates()).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})

	resp := doJSON(t, http.MethodPost, "/templates", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /templates status = %d, want 201; body=%s", resp.StatusCode, readBody(t, resp))
	}

	resp = doJSON(t, http.MethodGet, "/templates/"+name, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /templates/%s status = %d", name, resp.StatusCode)
	}
	resp.Body.Close()

	// Verify cluster-scoped on apiserver: list returns it without ns.
	list, err := kubeC.Dynamic.Resource(gvrTemplates()).
		List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	found := false
	for _, item := range list.Items {
		if item.GetName() == name {
			if item.GetNamespace() != "" {
				t.Errorf("template has unexpected namespace %q", item.GetNamespace())
			}
			found = true
		}
	}
	if !found {
		t.Errorf("template %s not found in cluster-scoped list", name)
	}
}

// TestResources_RejectsBadNamespace — a request with a namespace
// outside scope.AllowedNamespaces is rejected (resolveNS short-circuits
// with an error). We can't 100% predict the response code (it depends
// on httperr.Write's mapping), but it MUST be 4xx and the resource
// MUST NOT have been created.
func TestResources_RejectsBadNamespace(t *testing.T) {
	name := uniqueResourceName("bad")
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name},
		"spec":       map[string]any{"templateRef": map[string]any{"name": "x"}},
	}
	resp := doJSON(t, http.MethodPost, "/servers?namespace=not-on-allowlist", body)
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("expected 4xx, got %d; body=%s", resp.StatusCode, readBody(t, resp))
	}

	if _, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace("not-on-allowlist").
		Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Error("forbidden request created a resource")
	}
}

// TestResources_GetMissingResource404 — GET on a name that doesn't
// exist returns a 4xx, not a 5xx.
func TestResources_GetMissingResource404(t *testing.T) {
	name := uniqueResourceName("ghost")
	resp := doJSON(t, http.MethodGet, "/servers/"+name, nil)
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("GET missing got %d, want 4xx; body=%s", resp.StatusCode, readBody(t, resp))
	}
}

// TestResources_ListReturnsCreated — POST creates one, then LIST
// includes it.
func TestResources_ListReturnsCreated(t *testing.T) {
	name := uniqueResourceName("listed")
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name},
		"spec":       map[string]any{"templateRef": map[string]any{"name": "x"}},
	}
	resp := doJSON(t, http.MethodPost, "/servers", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body=%s", resp.StatusCode, readBody(t, resp))
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})

	resp = doJSON(t, http.MethodGet, "/servers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LIST status = %d", resp.StatusCode)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	resp.Body.Close()
	items, _ := raw["items"].([]any)
	found := false
	for _, it := range items {
		obj, _ := it.(map[string]any)
		md, _ := obj["metadata"].(map[string]any)
		if md["name"] == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("LIST response missing the created server %q", name)
	}
}

// ---------- helpers ----------

func gvrServers() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "gameservers"}
}

func gvrTemplates() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "gametemplates"}
}

func getDynamic(t *testing.T, gvr schema.GroupVersionResource, ns, name string) *unstructured.Unstructured {
	t.Helper()
	obj, err := kubeC.Dynamic.Resource(gvr).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("dynamic get %s/%s: %v", ns, name, err)
	}
	return obj
}

func doJSON(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, apiBase+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http %s %s: %v", method, path, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b))
}
