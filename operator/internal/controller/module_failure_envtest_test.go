//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/modsrc"
)

// TestModule_SourceNotFound — referencing a non-existent ModuleSource
// pushes the Module to Phase=Failed (markFailed branch).
func TestModule_SourceNotFound(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconciler(fake))

	modName := uniqueName("mod-no-src")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source: corev1.LocalObjectReference{Name: "ghost-source"},
			Name:   "minecraft-java",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseFailed {
			return false, "phase=" + got.Status.Phase
		}
		return true, ""
	})
}

// TestModule_VersionNotInCatalog — pinning a version the source doesn't
// publish must end in Phase=Failed (markFailed for VersionUnavailable).
func TestModule_VersionNotInCatalog(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconciler(fake))

	const ref = "local/test/minecraft-java"
	srcName := uniqueName("modsrc")
	createIndexedSource(t, srcName, "local/test", fake, []gameplanev1alpha1.ModuleEntry{{
		Name:          "minecraft-java",
		Reference:     ref,
		Versions:      []string{"1.0.0"},
		LatestVersion: "1.0.0",
	}})

	modName := uniqueName("mod-bad-ver")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source:  corev1.LocalObjectReference{Name: srcName},
			Name:    "minecraft-java",
			Version: "9.9.9",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseFailed {
			return false, "phase=" + got.Status.Phase
		}
		return true, ""
	})
}

// TestModule_IncompatibleOperator — a bundle whose gameplaneMinVersion is
// newer than the operator must end in Phase=Failed with a clear message, and
// must NOT materialize a GameTemplate.
func TestModule_IncompatibleOperator(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconcilerVersion(fake, "0.1.0"))

	const ref = "local/test/minecraft-java"
	fake.putBundle(ref, "1.0.0", fakeArtifact{
		digest: "sha256:mc-1.0.0",
		files: map[string][]byte{
			modsrc.FileMetadata: []byte("apiVersion: gameplane.local/module/v1\n" +
				"name: minecraft-java\nversion: 1.0.0\ngame: minecraft-java\n" +
				"gameplaneMinVersion: 9.9.9\n"),
			modsrc.FileTemplate: []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n" +
				"spec:\n  displayName: MC\n  game: minecraft-java\n  version: 1.0.0\n" +
				"  image: ghcr.io/test/mc:1.0.0\n"),
		},
	})

	srcName := uniqueName("modsrc")
	createIndexedSource(t, srcName, "local/test", fake, []gameplanev1alpha1.ModuleEntry{{
		Name:          "minecraft-java",
		Reference:     ref,
		Versions:      []string{"1.0.0"},
		LatestVersion: "1.0.0",
	}})

	modName := uniqueName("mod-incompat")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source: corev1.LocalObjectReference{Name: srcName},
			Name:   "minecraft-java",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseFailed {
			return false, "phase=" + got.Status.Phase
		}
		if !strings.Contains(got.Status.LastError, "requires Gameplane") {
			return false, "lastError=" + got.Status.LastError
		}
		// The bundle was pulled (Pulling=True) before the version check
		// failed, so the failure must have cleared Pulling back to False —
		// otherwise the dashboard shows "Failed" and "Pulling" at once.
		pulling := meta.FindStatusCondition(got.Status.Conditions, gameplanev1alpha1.ModuleConditionPulling)
		if pulling == nil {
			return false, "Pulling condition missing"
		}
		if pulling.Status != metav1.ConditionFalse {
			return false, "Pulling=" + string(pulling.Status) + ", want False after failure"
		}
		return true, ""
	})

	// The incompatible module must not have produced a GameTemplate.
	var tmpl gameplanev1alpha1.GameTemplate
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &tmpl); err == nil {
		t.Fatalf("GameTemplate %q should not exist for an incompatible module", modName)
	}
}

// TestModule_WaitingForCatalog — the source exists but doesn't (yet)
// list the requested module, so the reconciler markPending and waits.
func TestModule_WaitingForCatalog(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleReconciler(fake))

	srcName := uniqueName("modsrc-empty")
	// Source's catalog has only an unrelated module — the one the
	// Module spec requests below is not (yet) indexed.
	createIndexedSource(t, srcName, "local/test", fake, []gameplanev1alpha1.ModuleEntry{{
		Name:          "other",
		Reference:     "local/test/other",
		Versions:      []string{"1.0.0"},
		LatestVersion: "1.0.0",
	}})

	modName := uniqueName("mod-pending")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source: corev1.LocalObjectReference{Name: srcName},
			Name:   "not-yet-indexed",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		got := getModule(t, modName)
		if got.Status.Phase != gameplanev1alpha1.ModulePhasePending {
			return false, "phase=" + got.Status.Phase
		}
		return true, ""
	})
}
