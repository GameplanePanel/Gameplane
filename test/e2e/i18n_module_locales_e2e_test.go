//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestAPI_I18n_ModuleLocales covers module translations end to end: a bundle
// whose "locales/fr.json" layer is valid is served by GET
// /modules/{name}/locales/{tag}; a bundle whose French layer holds one valid
// and one invalid entry installs anyway, serves only the valid entry, and
// reports the problem as a LocalesValid=False condition and a Warning event
// on the GameTemplate.
//
// Budget: one e2e-admin login. It runs on the primary cluster.
func TestAPI_I18n_ModuleLocales(t *testing.T) {
	t.Parallel()
	// Shares the fixed-name registry and the oras push Jobs with the other
	// module tests.
	ociPushMu.Lock()
	defer ociPushMu.Unlock()

	ctx := context.Background()

	envInstance.ApplyYAML(t, "oci-registry.yaml")
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		dep, err := envInstance.K8s.AppsV1().Deployments("gameplane-system").
			Get(ctx, "gameplane-test-registry", metav1.GetOptions{})
		if err != nil {
			return false, "get registry deploy: " + err.Error()
		}
		if dep.Status.ReadyReplicas >= 1 {
			return true, ""
		}
		return false, "registry not ready yet"
	})

	envInstance.OCIPushFromFixture(t, "gameplane-system", "oras-push-i18n-fr", "oras-push-i18n-fr.yaml")
	envInstance.OCIPushFromFixture(t, "gameplane-system", "oras-push-i18n-bad", "oras-push-i18n-bad.yaml")

	const (
		sourceName = "e2e-i18n-source"
		goodModule = "e2e-i18n-fr"
		badModule  = "e2e-i18n-bad"
	)

	src := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "ModuleSource",
		"metadata":   map[string]any{"name": sourceName},
		"spec": map[string]any{
			"type": "oci",
			"oci": map[string]any{
				"url":      "gameplane-test-registry.gameplane-system.svc:5000",
				"insecure": true,
				"modules": []any{
					map[string]any{"name": goodModule},
					map[string]any{"name": badModule},
				},
			},
			"refreshInterval": "10m",
		},
	}}
	if _, err := envInstance.Dyn.Resource(moduleSourceGVR).
		Create(ctx, src, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create modulesource: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(moduleSourceGVR).
			Delete(context.Background(), sourceName, metav1.DeleteOptions{})
	})
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(moduleSourceGVR).Get(ctx, sourceName, metav1.GetOptions{})
		if err != nil {
			return false, "get modulesource: " + err.Error()
		}
		modules, _, _ := unstructured.NestedSlice(got.Object, "status", "modules")
		if len(modules) < 2 {
			return false, "status.modules has fewer than two entries"
		}
		return true, ""
	})

	for _, name := range []string{goodModule, badModule} {
		mod := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "Module",
			"metadata":   map[string]any{"name": name},
			"spec": map[string]any{
				"source":  map[string]any{"name": sourceName},
				"name":    name,
				"version": "0.1.0",
			},
		}}
		if _, err := envInstance.Dyn.Resource(moduleGVR).
			Create(ctx, mod, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create module %s: %v", name, err)
		}
		modName := name
		t.Cleanup(func() {
			_ = envInstance.Dyn.Resource(moduleGVR).
				Delete(context.Background(), modName, metav1.DeleteOptions{})
		})
	}
	for _, name := range []string{goodModule, badModule} {
		modName := name
		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			got, err := envInstance.Dyn.Resource(moduleGVR).Get(ctx, modName, metav1.GetOptions{})
			if err != nil {
				return false, "get module: " + err.Error()
			}
			phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
			if phase != "Ready" {
				lastErr, _, _ := unstructured.NestedString(got.Object, "status", "lastError")
				return false, modName + " phase=" + phase + " lastError=" + lastErr
			}
			return true, ""
		})
	}

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	getLocales := func(module, tag string) (int, map[string]string) {
		t.Helper()
		resp, body, err := admin.Get("/modules/" + module + "/locales/" + tag)
		if err != nil {
			t.Fatalf("GET locales %s/%s: %v", module, tag, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, nil
		}
		var out map[string]string
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode %s/%s: %v (%s)", module, tag, err, body)
		}
		return resp.StatusCode, out
	}

	t.Run("ValidTranslationIsServed", func(t *testing.T) {
		code, got := getLocales(goodModule, "fr")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if got["template.displayName"] != "Jeu de test" || got["module.summary"] != "Module de test" {
			t.Errorf("fr = %v", got)
		}
	})

	t.Run("RegionalTagFallsBackToBaseLanguage", func(t *testing.T) {
		_, got := getLocales(goodModule, "fr-CA")
		if got["template.displayName"] != "Jeu de test" {
			t.Errorf("fr-CA = %v", got)
		}
	})

	t.Run("UnknownLanguageIsEmpty", func(t *testing.T) {
		code, got := getLocales(goodModule, "de")
		if code != http.StatusOK || len(got) != 0 {
			t.Errorf("de: status=%d body=%v", code, got)
		}
	})

	t.Run("UnknownModuleIs404", func(t *testing.T) {
		if code, _ := getLocales("does-not-exist", "fr"); code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", code)
		}
	})

	t.Run("InvalidEntryIsDroppedAndReported", func(t *testing.T) {
		code, got := getLocales(badModule, "fr")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if len(got) != 1 || got["template.displayName"] != "Jeu de test" {
			t.Errorf("fr = %v; want only the valid entry", got)
		}

		envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
			tmpl, err := envInstance.Dyn.Resource(gameTemplateGVR).Get(ctx, badModule, metav1.GetOptions{})
			if err != nil {
				return false, "get template: " + err.Error()
			}
			conds, _, _ := unstructured.NestedSlice(tmpl.Object, "status", "conditions")
			for _, c := range conds {
				m, ok := c.(map[string]any)
				if !ok || m["type"] != "LocalesValid" {
					continue
				}
				if m["status"] == "False" && m["reason"] == "InvalidEntries" {
					return true, ""
				}
				return false, "LocalesValid=" + asString(m["status"]) + "/" + asString(m["reason"])
			}
			return false, "no LocalesValid condition"
		})

		// The template is cluster-scoped, so its events land in "default".
		envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
			evs, err := envInstance.K8s.CoreV1().Events(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
				FieldSelector: "involvedObject.name=" + badModule + ",type=Warning",
			})
			if err != nil {
				return false, "list events: " + err.Error()
			}
			for _, ev := range evs.Items {
				if strings.Contains(ev.Message, "fr:") {
					return true, ""
				}
			}
			return false, "no Warning event naming fr: yet"
		})
	})
}
