//go:build envtest

package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestModule_RecreatesTemplateDeletedOutOfBand — F-050: `kubectl delete
// gametemplate <managed>` bypasses the CRD's own no-op-on-managed-templates
// guard (operator/specs.md:29 says a bypass "is not supported — the
// operator's next reconcile will overwrite them"). Before the fix, the
// Module's status fields still matched Ready/AppliedVersion/AppliedDigest,
// so the convergence check returned early without ever checking the
// template still existed, and it stayed gone.
func TestModule_RecreatesTemplateDeletedOutOfBand(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconciler(fake))

	const ref = "local/test/mc"
	fake.putBundle(ref, "1.0.0", fixtureBundle("mc", "1.0.0", "MC"))

	srcName := uniqueName("modsrc")
	createIndexedSource(t, srcName, "local/test", fake, []gameplanev1alpha1.ModuleEntry{{
		Name:          "mc",
		Reference:     ref,
		Versions:      []string{"1.0.0"},
		LatestVersion: "1.0.0",
	}})

	modName := uniqueName("mod-recreate")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source: corev1.LocalObjectReference{Name: srcName},
			Name:   "mc",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady {
			return false, "phase=" + got.Status.Phase
		}
		return true, ""
	})

	// Simulate the out-of-band kubectl delete.
	var tmpl gameplanev1alpha1.GameTemplate
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &tmpl); err != nil {
		t.Fatalf("get template: %v", err)
	}
	if err := k8sClient.Delete(context.Background(), &tmpl); err != nil {
		t.Fatalf("delete template: %v", err)
	}

	// The Module has no way to be notified other than its Owns() watch on
	// GameTemplate, which fires on this delete — no spec change is needed.
	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.GameTemplate
		err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &got)
		if err != nil {
			return false, fmt.Sprintf("template not recreated: %v", err)
		}
		if got.Labels[gameplanev1alpha1.LabelManagedBy] != gameplanev1alpha1.ManagedByModule {
			return false, "recreated template missing managed-by label"
		}
		return true, ""
	})

	// The Module should still report Ready once the template is back.
	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady {
			return false, "phase=" + got.Status.Phase
		}
		return true, ""
	})
}
