//go:build envtest

package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// patchModuleVersion re-pins a Module's spec.version, forcing the reconciler
// to converge on a different bundle.
func patchModuleVersion(t *testing.T, name, version string) {
	t.Helper()
	// Retry on conflict — the reconciler patches status concurrently,
	// racing a bare Get+Update's resourceVersion.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var mod gameplanev1alpha1.Module
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &mod); err != nil {
			return err
		}
		mod.Spec.Version = version
		return k8sClient.Update(context.Background(), &mod)
	}); err != nil {
		t.Fatalf("patch module version: %v", err)
	}
}

func installReadyAt(t *testing.T, fake *fakeOCI, version string) (modName string) {
	t.Helper()
	const ref = "local/test/mc"
	fake.putBundle(ref, "1.0.0", fixtureBundle("mc", "1.0.0", "MC"))

	srcName := uniqueName("modsrc")
	createIndexedSource(t, srcName, "local/test", fake, []gameplanev1alpha1.ModuleEntry{{
		Name:          "mc",
		Reference:     ref,
		Versions:      []string{"2.0.0", "1.0.0"},
		LatestVersion: "1.0.0",
		// No digest: convergence keys on version alone, so a pinned-older
		// install doesn't re-pull against the latest version's digest.
	}})

	modName = uniqueName("mod-upgrade")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source:  corev1.LocalObjectReference{Name: srcName},
			Name:    "mc",
			Version: version,
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady || got.Status.AppliedVersion != version {
			return false, fmt.Sprintf("phase=%s applied=%s", got.Status.Phase, got.Status.AppliedVersion)
		}
		return true, ""
	})
	return modName
}

// TestModule_FailedUpgradeKeepsLastGood — when an upgrade's pull fails, the
// Module goes Failed but the previously-applied version and its GameTemplate
// must stay in place (a safe implicit rollback), and PreviousVersion must NOT
// record a change that never happened.
func TestModule_FailedUpgradeKeepsLastGood(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconciler(fake))

	// 2.0.0 is catalogued but its pull always fails.
	fake.errOn["pull:local/test/mc:2.0.0"] = fmt.Errorf("dial tcp: connection refused")
	modName := installReadyAt(t, fake, "1.0.0")

	patchModuleVersion(t, modName, "2.0.0")

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseFailed {
			return false, "phase=" + got.Status.Phase
		}
		if got.Status.AppliedVersion != "1.0.0" {
			return false, "applied moved to " + got.Status.AppliedVersion
		}
		if got.Status.PreviousVersion != "" {
			return false, "previousVersion wrongly set to " + got.Status.PreviousVersion
		}
		return true, ""
	})

	// The 1.0.0 GameTemplate is still present and unchanged.
	var tmpl gameplanev1alpha1.GameTemplate
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &tmpl); err != nil {
		t.Fatalf("GameTemplate should still exist: %v", err)
	}
	if got := tmpl.Labels[gameplanev1alpha1.LabelModuleVersion]; got != "1.0.0" {
		t.Fatalf("template version label = %q, want 1.0.0", got)
	}
}

// TestModule_SuccessfulUpgradeRecordsPrevious — a successful upgrade records
// the prior version as the rollback target.
func TestModule_SuccessfulUpgradeRecordsPrevious(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconciler(fake))

	fake.putBundle("local/test/mc", "2.0.0", fixtureBundle("mc", "2.0.0", "MC"))
	modName := installReadyAt(t, fake, "1.0.0")

	patchModuleVersion(t, modName, "2.0.0")

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady || got.Status.AppliedVersion != "2.0.0" {
			return false, fmt.Sprintf("phase=%s applied=%s", got.Status.Phase, got.Status.AppliedVersion)
		}
		if got.Status.PreviousVersion != "1.0.0" {
			return false, "previousVersion = " + got.Status.PreviousVersion
		}
		return true, ""
	})
}
