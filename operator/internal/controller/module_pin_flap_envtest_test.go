//go:build envtest

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestModule_PinnedVersionConvergesDespiteLatestOnlyDigest — F-046: a
// ModuleEntry's Digest only ever describes the catalog's LatestVersion.
// Pinning an older version must still converge to Ready and stay there —
// comparing the pinned install's AppliedDigest against a digest that
// describes a different version can never match, and previously flapped
// the Module between Pulling and Ready forever.
func TestModule_PinnedVersionConvergesDespiteLatestOnlyDigest(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconciler(fake))

	const ref = "local/test/mc"
	fake.putBundle(ref, "1.0.0", fixtureBundle("mc", "1.0.0", "MC"))
	fake.putBundle(ref, "2.0.0", fixtureBundle("mc", "2.0.0", "MC"))

	srcName := uniqueName("modsrc")
	createIndexedSource(t, srcName, "local/test", fake, []gameplanev1alpha1.ModuleEntry{{
		Name:          "mc",
		Reference:     ref,
		Versions:      []string{"2.0.0", "1.0.0"},
		LatestVersion: "2.0.0",
		// Describes only 2.0.0's content — never 1.0.0's, which is what
		// F-046 pins below.
		Digest: "sha256:mc-2.0.0",
	}})

	modName := uniqueName("mod-pin")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source:  corev1.LocalObjectReference{Name: srcName},
			Name:    "mc",
			Version: "1.0.0",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady || got.Status.AppliedVersion != "1.0.0" {
			return false, fmt.Sprintf("phase=%s applied=%s", got.Status.Phase, got.Status.AppliedVersion)
		}
		return true, ""
	})

	pullsAtReady := fake.pullCount()

	// Give the watch/requeue machinery a couple of seconds to prove the
	// bug is gone: no further pulls, and the phase never leaves Ready.
	consistently(t, 2*time.Second, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady {
			return false, "phase flapped to " + got.Status.Phase
		}
		if pulls := fake.pullCount(); pulls != pullsAtReady {
			return false, fmt.Sprintf("kept pulling: %d -> %d", pullsAtReady, pulls)
		}
		return true, ""
	})
}
