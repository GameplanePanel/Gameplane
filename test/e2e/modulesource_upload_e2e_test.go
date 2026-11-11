//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestModuleSourceUpload is the kubectl-apply-parity check for the
// upload source type: a labeled ConfigMap holding bundle files —
// whether written by the API's upload endpoint or applied by hand —
// must index into an upload-type ModuleSource and install exactly like
// a registry module. No extra infrastructure (registry, oras job) is
// needed.
func TestModuleSourceUpload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		sourceName = "e2e-upload-source"
		moduleName = "e2e-upload-game"
		cmName     = "e2e-upload-bundle"
	)

	// 1. The upload-type source (the chart also ships one named
	// "uploads"; this test brings its own to stay self-contained).
	src := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "ModuleSource",
		"metadata":   map[string]any{"name": sourceName},
		"spec": map[string]any{
			"type":            "upload",
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

	// 2. Hand-apply the bundle ConfigMap, exactly as the API's upload
	// endpoint would write it.
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: credentialsNS,
			Labels:    map[string]string{"gameplane.local/module-upload": "true"},
		},
		BinaryData: map[string][]byte{
			"module.yaml": []byte("apiVersion: gameplane.local/module/v1\n" +
				"name: " + moduleName + "\n" +
				"displayName: E2E Upload Game\n" +
				"version: 0.1.0\n" +
				"game: busybox\n" +
				"summary: upload e2e fixture\n"),
			"template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\n" +
				"kind: GameTemplate\n" +
				"spec:\n" +
				"  displayName: E2E Upload Game\n" +
				"  game: busybox\n" +
				"  version: 0.1.0\n" +
				"  image: busybox:1.36\n" +
				"  command: [\"sh\", \"-c\", \"sleep infinity\"]\n" +
				"  ports:\n" +
				"    - { name: game, containerPort: 7777 }\n"),
		},
	}
	if _, err := envInstance.K8s.CoreV1().ConfigMaps(credentialsNS).
		Create(ctx, cm, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create bundle configmap: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().ConfigMaps(credentialsNS).
			Delete(context.Background(), cmName, metav1.DeleteOptions{})
	})

	// 3. The watch-driven index should pick the bundle up well before
	// the refresh interval.
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(moduleSourceGVR).
			Get(ctx, sourceName, metav1.GetOptions{})
		if err != nil {
			return false, "get modulesource: " + err.Error()
		}
		modules, _, _ := unstructured.NestedSlice(got.Object, "status", "modules")
		for _, raw := range modules {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if m["name"] == moduleName {
				if m["latestVersion"] != "0.1.0" {
					return false, "latestVersion=" + asString(m["latestVersion"])
				}
				if d, _ := m["digest"].(string); d == "" {
					return false, "digest empty"
				}
				return true, ""
			}
		}
		return false, "module not in catalog yet"
	})

	// 4. Install it and confirm the GameTemplate materializes with the
	// bundle's spec.
	mod := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Module",
		"metadata":   map[string]any{"name": moduleName},
		"spec": map[string]any{
			"source": map[string]any{"name": sourceName},
			"name":   moduleName,
		},
	}}
	if _, err := envInstance.Dyn.Resource(moduleGVR).
		Create(ctx, mod, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create module: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(moduleGVR).
			Delete(context.Background(), moduleName, metav1.DeleteOptions{})
	})

	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(moduleGVR).
			Get(ctx, moduleName, metav1.GetOptions{})
		if err != nil {
			return false, "get module: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		if phase != "Ready" {
			lastErr, _, _ := unstructured.NestedString(got.Object, "status", "lastError")
			return false, "module phase=" + phase + " lastError=" + lastErr
		}
		return true, ""
	})

	tmpl, err := envInstance.Dyn.Resource(gameTemplateGVR).
		Get(ctx, moduleName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get materialized template: %v", err)
	}
	image, _, _ := unstructured.NestedString(tmpl.Object, "spec", "image")
	if image != "busybox:1.36" {
		t.Errorf("template.spec.image=%q want busybox:1.36", image)
	}
	labels := tmpl.GetLabels()
	if labels["gameplane.local/managed-by"] != "Module" {
		t.Errorf("template missing managed-by=Module label, got %q", labels["gameplane.local/managed-by"])
	}
	digest := tmpl.GetAnnotations()["gameplane.local/module-digest"]
	if digest == "" {
		t.Error("template missing module-digest annotation")
	}
}
