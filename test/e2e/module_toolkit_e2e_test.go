//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/gp-module/pkg/archetypes"
	"github.com/GameplanePanel/gameplane/gp-module/pkg/packager"
	"github.com/GameplanePanel/gameplane/gp-module/pkg/scaffold"
	"github.com/GameplanePanel/gameplane/gp-module/pkg/validator"
)

// TestModule_ScaffoldAndPackage validates the entire developer toolkit pipeline:
// 1. In-memory scaffolding from archetypes (gp-module scaffold engine)
// 2. Static offline validation pass (gp-module validator engine)
// 3. Local bundle packaging (gp-module packager engine)
// 4. Ingestion into a live upload-type ModuleSource
// 5. Materialization into a live GameTemplate CR by the Gameplane operator
func TestModule_ScaffoldAndPackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	suffix := fmt.Sprintf("%x", time.Now().UnixNano()%0xffffff)
	sourceName := fmt.Sprintf("e2e-src-%s", suffix)
	moduleName := fmt.Sprintf("e2e-game-%s", suffix)
	cmName := fmt.Sprintf("module-upload-%s", moduleName)
	const ns = "gameplane-system"

	// Step 1: Scaffold using gp-module
	opts := scaffold.Options{
		Name:        moduleName,
		DisplayName: "E2E Toolkit Game",
		Archetype:   "generic",
		Image:       "busybox:1.36@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		Ports: []archetypes.PortDef{
			{Name: "game", ContainerPort: 8888, Protocol: "UDP", Advertise: true},
		},
		Categories: []string{"Shooter", "Co-op"},
		Summary:    "Automated toolkit verification module",
	}

	generated, err := scaffold.GenerateFiles(opts)
	if err != nil {
		t.Fatalf("scaffold.GenerateFiles failed: %v", err)
	}

	files := map[string][]byte{
		"module.yaml":   []byte(generated.ModuleYAML),
		"template.yaml": []byte(generated.TemplateYAML),
		"README.md":     []byte(generated.ReadmeMD),
		"icon.png":      archetypes.PlaceholderIconBytes(),
	}

	// Step 2: Validate using gp-module
	valReport, err := validator.ValidateFiles(moduleName, files, validator.ValidateOptions{
		Offline: true,
		Strict:  false,
	})
	if err != nil {
		t.Fatalf("validator.ValidateFiles failed: %v", err)
	}
	if !valReport.Clean {
		t.Fatalf("expected clean validation, got %d errors: %+v", len(valReport.Findings), valReport.Findings)
	}

	// Step 3: Package using gp-module
	archive, warnings, err := packager.CreateArchiveFromFiles(files, packager.DefaultPackageLimits)
	if err != nil {
		t.Fatalf("packager.CreateArchiveFromFiles failed: %v", err)
	}
	if len(warnings) > 0 {
		t.Logf("package warnings: %+v", warnings)
	}
	if len(archive) == 0 {
		t.Fatal("archive bytes empty")
	}

	// Step 4: Create upload-type ModuleSource
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
		Create(ctx, src, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create modulesource: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(moduleSourceGVR).
			Delete(context.Background(), sourceName, metav1.DeleteOptions{})
	})

	// Step 5: Store bundle ConfigMap as written by upload/builder endpoint
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: ns,
			Labels: map[string]string{
				"gameplane.local/module-upload": "true",
				"gameplane.local/module-name":   moduleName,
			},
		},
		BinaryData: files,
	}
	if _, err := envInstance.K8s.CoreV1().ConfigMaps(ns).
		Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create bundle configmap: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().ConfigMaps(ns).
			Delete(context.Background(), cmName, metav1.DeleteOptions{})
	})

	// Step 6: Wait for upload source indexer to pick up the module
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
				if d, _ := m["digest"].(string); d == "" {
					return false, "digest empty"
				}
				return true, ""
			}
		}
		return false, "module not in catalog yet"
	})

	// Step 7: Install module
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
		Create(ctx, mod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create module: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(moduleGVR).
			Delete(context.Background(), moduleName, metav1.DeleteOptions{})
	})

	// Step 8: Assert Module reaches Ready and GameTemplate is materialized
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

	displayName, _, _ := unstructured.NestedString(tmpl.Object, "spec", "displayName")
	if displayName != "E2E Toolkit Game" {
		t.Errorf("template.spec.displayName=%q want 'E2E Toolkit Game'", displayName)
	}
	labels := tmpl.GetLabels()
	if labels["gameplane.local/managed-by"] != "Module" {
		t.Errorf("template missing managed-by=Module label, got %q", labels["gameplane.local/managed-by"])
	}
}

// TestModule_SteamcmdScaffoldKeepsNonRootSecurity checks that a module
// scaffolded from the steamcmd preset materializes a GameTemplate whose
// spec.security runs the game container as a non-root user. It logs in to
// nothing: every step goes straight to the Kubernetes API.
func TestModule_SteamcmdScaffoldKeepsNonRootSecurity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	suffix := fmt.Sprintf("%x", time.Now().UnixNano()%0xffffff)
	sourceName := fmt.Sprintf("e2e-steam-src-%s", suffix)
	moduleName := fmt.Sprintf("e2e-steam-%s", suffix)
	cmName := fmt.Sprintf("module-upload-%s", moduleName)
	const ns = "gameplane-system"

	generated, err := scaffold.GenerateFiles(scaffold.Options{
		Name:        moduleName,
		DisplayName: "E2E Steam Game",
		Archetype:   "steamcmd",
	})
	if err != nil {
		t.Fatalf("scaffold.GenerateFiles failed: %v", err)
	}
	files := map[string][]byte{
		"module.yaml":   []byte(generated.ModuleYAML),
		"template.yaml": []byte(generated.TemplateYAML),
		"README.md":     []byte(generated.ReadmeMD),
		"icon.png":      archetypes.PlaceholderIconBytes(),
	}

	valReport, err := validator.ValidateFiles(moduleName, files, validator.ValidateOptions{Offline: true})
	if err != nil {
		t.Fatalf("validator.ValidateFiles failed: %v", err)
	}
	if !valReport.Clean {
		t.Fatalf("expected clean validation, got %d findings: %+v", len(valReport.Findings), valReport.Findings)
	}

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
		Create(ctx, src, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create modulesource: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(moduleSourceGVR).
			Delete(context.Background(), sourceName, metav1.DeleteOptions{})
	})

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: ns,
			Labels: map[string]string{
				"gameplane.local/module-upload": "true",
				"gameplane.local/module-name":   moduleName,
			},
		},
		BinaryData: files,
	}
	if _, err := envInstance.K8s.CoreV1().ConfigMaps(ns).
		Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create bundle configmap: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().ConfigMaps(ns).
			Delete(context.Background(), cmName, metav1.DeleteOptions{})
	})

	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(moduleSourceGVR).
			Get(ctx, sourceName, metav1.GetOptions{})
		if err != nil {
			return false, "get modulesource: " + err.Error()
		}
		modules, _, _ := unstructured.NestedSlice(got.Object, "status", "modules")
		for _, raw := range modules {
			if m, ok := raw.(map[string]any); ok && m["name"] == moduleName {
				if d, _ := m["digest"].(string); d == "" {
					return false, "digest empty"
				}
				return true, ""
			}
		}
		return false, "module not in catalog yet"
	})

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
		Create(ctx, mod, metav1.CreateOptions{}); err != nil {
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
	for _, field := range []string{"runAsUser", "runAsGroup", "fsGroup"} {
		v, found, err := unstructured.NestedInt64(tmpl.Object, "spec", "security", field)
		if err != nil || !found {
			t.Errorf("template spec.security.%s missing (found=%v err=%v)", field, found, err)
			continue
		}
		if v == 0 {
			t.Errorf("template spec.security.%s = 0, want a non-root id", field)
		}
	}
}
