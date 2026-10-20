//go:build envtest

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// seedModuleSource creates a ModuleSource and writes a status with the
// given catalog entries. The handler reads status.modules directly, so
// no operator is needed for these tests.
func seedModuleSource(t *testing.T, name string, modules []map[string]any) {
	t.Helper()
	src := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "ModuleSource",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"type": "oci",
			"oci": map[string]any{
				"url":     "registry.example.com/" + name,
				"modules": []map[string]any{{"name": "demo"}},
			},
		},
	}}
	created, err := kubeC.Dynamic.Resource(kube.GVRModuleSource).Create(
		context.Background(), src, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create modulesource: %v", err)
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(kube.GVRModuleSource).Delete(
			context.Background(), name, metav1.DeleteOptions{})
	})
	_ = unstructured.SetNestedSlice(created.Object, asAnySlice(modules), "status", "modules")
	if _, err := kubeC.Dynamic.Resource(kube.GVRModuleSource).
		UpdateStatus(context.Background(), created, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("seed status: %v", err)
	}
}

func asAnySlice(in []map[string]any) []any {
	out := make([]any, 0, len(in))
	for _, m := range in {
		out = append(out, m)
	}
	return out
}

func TestModules_CatalogMergesSourcesAndInstalls(t *testing.T) {
	srcName := uniqueResourceName("src")
	seedModuleSource(t, srcName, []map[string]any{
		{
			"name":          "minecraft-java",
			"displayName":   "Minecraft (Java)",
			"summary":       "Vanilla / Paper / Forge / Fabric",
			"reference":     "registry.example.com/" + srcName + "/minecraft-java",
			"versions":      []any{"1.1.0", "1.0.0"},
			"latestVersion": "1.1.0",
		},
		{
			"name":          "valheim",
			"displayName":   "Valheim",
			"reference":     "registry.example.com/" + srcName + "/valheim",
			"versions":      []any{"0.9.0"},
			"latestVersion": "0.9.0",
		},
	})

	// Pretend Minecraft is already installed at v1.0.0.
	modName := uniqueResourceName("mc")
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(kube.GVRModule).Delete(
			context.Background(), modName, metav1.DeleteOptions{})
	})
	mod := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Module",
		"metadata":   map[string]any{"name": modName},
		"spec": map[string]any{
			"source":  map[string]any{"name": srcName},
			"name":    "minecraft-java",
			"version": "1.0.0",
		},
	}}
	created, err := kubeC.Dynamic.Resource(kube.GVRModule).Create(
		context.Background(), mod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create module: %v", err)
	}
	_ = unstructured.SetNestedField(created.Object, "Ready", "status", "phase")
	_ = unstructured.SetNestedField(created.Object, "1.0.0", "status", "appliedVersion")
	_ = unstructured.SetNestedField(created.Object, modName, "status", "appliedTemplate")
	if _, err := kubeC.Dynamic.Resource(kube.GVRModule).
		UpdateStatus(context.Background(), created, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("seed module status: %v", err)
	}

	resp := doJSON(t, http.MethodGet, "/modules/catalog", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var out catalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()

	mc := findCatalogEntry(out.Items, "minecraft-java")
	if mc == nil {
		t.Fatalf("minecraft-java missing from catalog: %+v", out.Items)
	}
	if !mc.Installed || mc.InstalledVersion != "1.0.0" || mc.LatestVersion != "1.1.0" {
		t.Errorf("minecraft-java = %+v", mc)
	}
	if mc.ModuleName != modName {
		t.Errorf("ModuleName = %q want %q", mc.ModuleName, modName)
	}

	vh := findCatalogEntry(out.Items, "valheim")
	if vh == nil || vh.Installed {
		t.Errorf("valheim should not be installed: %+v", vh)
	}
}

func TestModules_InstallCreatesCR(t *testing.T) {
	srcName := uniqueResourceName("isrc")
	seedModuleSource(t, srcName, []map[string]any{{
		"name":          "valheim",
		"reference":     "registry.example.com/" + srcName + "/valheim",
		"versions":      []any{"0.9.0"},
		"latestVersion": "0.9.0",
	}})

	want := uniqueResourceName("vh")
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(kube.GVRModule).Delete(
			context.Background(), want, metav1.DeleteOptions{})
	})

	resp := doJSON(t, http.MethodPost, "/modules", map[string]any{
		"source":  srcName,
		"module":  "valheim",
		"name":    want,
		"version": "0.9.0",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	got, err := kubeC.Dynamic.Resource(kube.GVRModule).Get(
		context.Background(), want, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get module: %v", err)
	}
	source, _, _ := unstructured.NestedString(got.Object, "spec", "source", "name")
	module, _, _ := unstructured.NestedString(got.Object, "spec", "name")
	version, _, _ := unstructured.NestedString(got.Object, "spec", "version")
	if source != srcName || module != "valheim" || version != "0.9.0" {
		t.Errorf("module spec = source=%q name=%q version=%q", source, module, version)
	}
}

func TestModules_TemplateUpdate409sOnManagedTemplate(t *testing.T) {
	tmplName := uniqueResourceName("guarded")
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata": map[string]any{
			"name": tmplName,
			"labels": map[string]any{
				"gameplane.local/managed-by":  "Module",
				"gameplane.local/module-name": "valheim",
			},
		},
		"spec": map[string]any{
			"displayName": "Guarded",
			"game":        "valheim",
			"version":     "1.0.0",
			"image":       "ghcr.io/test/guarded:1.0.0",
		},
	}}
	if _, err := kubeC.Dynamic.Resource(kube.GVRs["templates"]).
		Create(context.Background(), tmpl, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(kube.GVRs["templates"]).Delete(
			context.Background(), tmplName, metav1.DeleteOptions{})
	})

	// Direct DELETE on the managed template should be refused with 409.
	resp := doJSON(t, http.MethodDelete, "/templates/"+tmplName, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE status = %d (want 409); body=%s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

func findCatalogEntry(items []CatalogEntry, name string) *CatalogEntry {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

// silence unused-import linter for scope when only one of the tests uses it.
var _ = scope.DefaultNamespace
