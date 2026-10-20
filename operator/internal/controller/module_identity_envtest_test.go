//go:build envtest

package controller

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestModule_ChangedIdentityRepullsPinnedVersion(t *testing.T) {
	_ = newNamespace(t)
	registry := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconciler(registry))
	registry.putBundle("local/first/a", "1.0.0", fixtureBundle("a", "1.0.0", "Original"))
	registry.putBundle("local/first/b", "1.0.0", fixtureBundle("b", "1.0.0", "ChangedName"))
	registry.putBundle("local/second/b", "1.0.0", fixtureBundle("b", "1.0.0", "ChangedSource"))
	entry := func(prefix, name string) gameplanev1alpha1.ModuleEntry {
		return gameplanev1alpha1.ModuleEntry{Name: name, Reference: prefix + "/" + name,
			Versions: []string{"2.0.0", "1.0.0"}, LatestVersion: "2.0.0", Digest: "sha256:latest"}
	}
	first, second := uniqueName("source-a"), uniqueName("source-b")
	createIndexedSource(t, first, "local/first", registry, []gameplanev1alpha1.ModuleEntry{entry("local/first", "a"), entry("local/first", "b")})
	createIndexedSource(t, second, "local/second", registry, []gameplanev1alpha1.ModuleEntry{entry("local/second", "b")})
	mod := &gameplanev1alpha1.Module{ObjectMeta: metav1.ObjectMeta{Name: uniqueName("identity")}, Spec: gameplanev1alpha1.ModuleSpec{
		Source: corev1.LocalObjectReference{Name: first}, Name: "a", Version: "1.0.0",
	}}
	if err := k8sClient.Create(t.Context(), mod); err != nil {
		t.Fatal(err)
	}
	deleteCleanup(t, mod)
	assertApplied := func(displayName, source string) {
		t.Helper()
		eventually(t, func() (bool, string) {
			got := getModule(t, mod.Name)
			if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady || got.Status.ObservedGeneration != got.Generation {
				return false, fmt.Sprintf("phase=%s generation=%d observed=%d", got.Status.Phase, got.Generation, got.Status.ObservedGeneration)
			}
			tmpl := getTemplateByName(t, mod.Name)
			return tmpl.Spec.DisplayName == displayName && tmpl.Labels[gameplanev1alpha1.LabelModuleSource] == source,
				fmt.Sprintf("template=%s source=%s", tmpl.Spec.DisplayName, tmpl.Labels[gameplanev1alpha1.LabelModuleSource])
		})
		pulls := registry.pullCount()
		consistently(t, time.Second, func() (bool, string) {
			return registry.pullCount() == pulls, "unchanged older-version pin re-pulled"
		})
	}
	assertApplied("Original", first)
	for _, change := range []struct{ name, source, display string }{{"b", first, "ChangedName"}, {"b", second, "ChangedSource"}} {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(t.Context(), client.ObjectKeyFromObject(mod), mod); err != nil {
				return err
			}
			mod.Spec.Name, mod.Spec.Source.Name = change.name, change.source
			return k8sClient.Update(t.Context(), mod)
		}); err != nil {
			t.Fatal(err)
		}
		assertApplied(change.display, change.source)
	}
}
