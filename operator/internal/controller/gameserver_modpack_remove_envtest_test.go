//go:build envtest

package controller

import (
	"context"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// wipeInitOf returns the modpack wipe init container of the server's StatefulSet, or nil.
func wipeInitOf(t *testing.T, ns, name string) *corev1.Container {
	t.Helper()
	return containerByName(ssOf(t, ns, name).Spec.Template.Spec.InitContainers, modpackWipeInitName)
}

func setRemoveAnnotation(t *testing.T, ns, name, value string) {
	t.Helper()
	mutateGameServer(t, ns, name, func(g *gameplanev1alpha1.GameServer) {
		if g.Annotations == nil {
			g.Annotations = map[string]string{}
		}
		g.Annotations[gameplanev1alpha1.ModpackRemoveAnnotation] = value
	})
}

func removePack(t *testing.T, ns, name, annotation string) {
	t.Helper()
	mutateGameServer(t, ns, name, func(g *gameplanev1alpha1.GameServer) {
		g.Spec.Modpack = nil
		if g.Annotations == nil {
			g.Annotations = map[string]string{}
		}
		g.Annotations[gameplanev1alpha1.ModpackRemoveAnnotation] = annotation
	})
}

// startPackServer creates a forge server running a CurseForge pack, Applied.
func startPackServer(t *testing.T, ns, name string) *gameplanev1alpha1.GameTemplate {
	t.Helper()
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)
	gs := buildGameServer(ns, name, tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "100")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	modpackReadyIs(t, ns, name, "True")
	return tmpl
}

func annotationGone(t *testing.T, ns, name, key string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		if _, ok := getGameServer(t, ns, name).Annotations[key]; ok {
			return false, key + " still set"
		}
		return true, ""
	})
}

// TestGameServer_ModpackRemovalWipesActiveModsMountOnly covers spec 035 T039:
// removal adds the one-shot wipe init step for the active loader's mods mount
// only (no data mount), guarded by .gameplane-mods-wiped-<generation>, and
// consumes the remove annotation once applied; installing or switching packs
// adds no wipe step.
func TestGameServer_ModpackRemovalWipesActiveModsMountOnly(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	tmpl := startPackServer(t, ns, "pack")
	if wipeInitOf(t, ns, "pack") != nil {
		t.Fatal("wipe init step present for an installed pack")
	}

	// Switching packs never wipes.
	setModpack(t, ns, "pack", modpackSpecOf("ftb", "777", "4242"))
	waitGameEnv(t, ns, "pack", "FTB_MODPACK_ID", "777")
	consistently(t, time.Second, func() (bool, string) {
		if wipeInitOf(t, ns, "pack") != nil {
			return false, "wipe init step rendered when switching packs"
		}
		return true, ""
	})

	// Removal: wipe step for the mods mount only, marker = observedGeneration.
	removePack(t, ns, "pack", `{"requestId":"r1","backupFirst":false}`)
	var wipe *corev1.Container
	eventually(t, func() (bool, string) {
		if wipe = wipeInitOf(t, ns, "pack"); wipe == nil {
			return false, "no wipe init step yet"
		}
		return true, ""
	})
	st := getGameServer(t, ns, "pack").Status.Modpack
	if st == nil || st.Applied != nil {
		t.Fatalf("status.modpack = %+v, want applied nil after removal", st)
	}
	modsMount := path.Join(effectiveMountPath(tmpl), "mods")
	marker := path.Join(modsMount, fmt.Sprintf(".gameplane-mods-wiped-%d", st.ObservedGeneration))
	if len(wipe.VolumeMounts) != 1 || wipe.VolumeMounts[0].MountPath != modsMount || wipe.VolumeMounts[0].Name != modVolumeName("1.21.4-forge") {
		t.Fatalf("wipe mounts = %+v, want only the forge mods volume at %s", wipe.VolumeMounts, modsMount)
	}
	if mountPathOf(wipe, "data") != "" {
		t.Fatal("wipe step mounts the data volume")
	}
	if !strings.Contains(strings.Join(wipe.Args, " "), marker) {
		t.Fatalf("wipe args = %v, want marker %s", wipe.Args, marker)
	}
	if !strings.Contains(wipe.Args[0], "[ -e") {
		t.Fatalf("wipe script %q is not guarded by the marker", wipe.Args[0])
	}
	env, _ := gameEnvOf(ns, "pack")
	if envCount(env, "FTB_MODPACK_ID") != 0 {
		t.Fatal("FTB env still rendered after removal")
	}

	// Applied: annotation consumed, step stays (no second rollout), marker unchanged.
	modpackReadyIs(t, ns, "pack", "True")
	annotationGone(t, ns, "pack", gameplanev1alpha1.ModpackRemoveAnnotation)
	if wipeInitOf(t, ns, "pack") == nil {
		t.Fatal("wipe step dropped after Applied")
	}

	// A later install drops the step.
	setModpack(t, ns, "pack", modpackSpecOf("modrinth", "another-pack", "v1"))
	waitGameEnv(t, ns, "pack", "MODRINTH_MODPACK", "another-pack")
	eventually(t, func() (bool, string) {
		if wipeInitOf(t, ns, "pack") != nil {
			return false, "wipe step still rendered after installing a pack"
		}
		return true, ""
	})
}

// TestGameServer_ModpackRemovalBackupFailureKeepsPackAndRetries covers T042 with
// T032: a removal with backupFirst waits for its Backup; a failed backup keeps
// status.modpack.applied and the remove annotation (no wipe, pack env intact); a
// retry (new requestId, same generation) creates a new Backup and, once it
// succeeds, removes the pack and clears the annotation.
func TestGameServer_ModpackRemovalBackupFailureKeepsPackAndRetries(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	seedBackupDestination(t, ns, "dest")
	startPackServer(t, ns, "pack")

	removePack(t, ns, "pack", `{"requestId":"r1","backupFirst":true}`)
	st := waitModpackPhase(t, ns, "pack", gameplanev1alpha1.ModpackPhaseBackingUp)
	firstName := fmt.Sprintf("pack-modpack-%d", st.ObservedGeneration)
	if st.BackupName != firstName || st.Applied == nil || st.Applied.Project != "all-the-mods-10" {
		t.Fatalf("status.modpack = %+v, want BackingUp, backup %s, applied pack kept", st, firstName)
	}
	if owner := metav1.GetControllerOf(getBackup(t, ns, firstName)); owner == nil || owner.Name != "pack" {
		t.Fatalf("backup owner = %+v, want the GameServer", owner)
	}
	consistently(t, time.Second, func() (bool, string) {
		env, why := gameEnvOf(ns, "pack")
		if why != "" {
			return false, why
		}
		if got, _ := envValue(env, "CF_SLUG"); got != "all-the-mods-10" {
			return false, "pack env changed before the backup finished"
		}
		if wipeInitOf(t, ns, "pack") != nil {
			return false, "wipe step rendered before the backup finished"
		}
		return true, ""
	})

	markBackupFailed(t, ns, firstName, "restic: out of space")
	st = waitModpackPhase(t, ns, "pack", gameplanev1alpha1.ModpackPhaseBackupFailed)
	if !strings.Contains(st.Message, "out of space") || st.Applied == nil {
		t.Fatalf("status.modpack = %+v, want BackupFailed with message and applied kept", st)
	}
	consistently(t, time.Second, func() (bool, string) {
		live := getGameServer(t, ns, "pack")
		if _, ok := live.Annotations[gameplanev1alpha1.ModpackRemoveAnnotation]; !ok {
			return false, "remove annotation cleared after a failed backup"
		}
		if wipeInitOf(t, ns, "pack") != nil {
			return false, "wipe step rendered after a failed backup"
		}
		return true, ""
	})

	// Retry: only the annotation changes (generation stays).
	genBefore := getGameServer(t, ns, "pack").Generation
	setRemoveAnnotation(t, ns, "pack", `{"requestId":"r2","backupFirst":true}`)
	var retryName string
	eventually(t, func() (bool, string) {
		st := getGameServer(t, ns, "pack").Status.Modpack
		if st == nil || st.Phase != gameplanev1alpha1.ModpackPhaseBackingUp || st.BackupName == firstName || st.BackupName == "" {
			return false, fmt.Sprintf("status.modpack = %+v, want BackingUp with a new backup", st)
		}
		retryName = st.BackupName
		return true, ""
	})
	if getGameServer(t, ns, "pack").Generation != genBefore || !strings.HasPrefix(retryName, firstName+"-") {
		t.Fatalf("retry backup %q, want %s-<id suffix> at the same generation", retryName, firstName)
	}
	markBackupSucceeded(t, ns, retryName, "snap-1", "")
	eventually(t, func() (bool, string) {
		if wipeInitOf(t, ns, "pack") == nil {
			return false, "no wipe step after the backup succeeded"
		}
		return true, ""
	})
	modpackReadyIs(t, ns, "pack", "True")
	annotationGone(t, ns, "pack", gameplanev1alpha1.ModpackRemoveAnnotation)
	env, _ := gameEnvOf(ns, "pack")
	if envCount(env, "CF_SLUG") != 0 {
		t.Fatal("pack env still rendered after the removal applied")
	}
}

// TestGameServer_ModpackRemovalOfAdoptedUnpinnedPack: an adopted server that was
// never pinned (status.modpack nil) is removed too: the pack identity comes from
// spec.env, its legacy env stops rendering, the mods are wiped and both
// annotations are consumed.
func TestGameServer_ModpackRemovalOfAdoptedUnpinnedPack(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	startLegacyModrinthServer(t, ns, "pack")
	setAgentPlayers(t, ns, "pack", int32Ptr(2)) // keep the pin held
	adoptServer(t, ns, "pack", "v-abc")

	removePack(t, ns, "pack", `{"requestId":"r1","backupFirst":false}`)
	eventually(t, func() (bool, string) {
		env, why := gameEnvOf(ns, "pack")
		if why != "" {
			return false, why
		}
		if envCount(env, "MODRINTH_MODPACK") != 0 {
			return false, "legacy pack env still rendered"
		}
		if got, _ := envValue(env, "TYPE"); got != "FORGE" {
			return false, "TYPE=" + got + ", want the version's FORGE"
		}
		if wipeInitOf(t, ns, "pack") == nil {
			return false, "no wipe init step"
		}
		return true, ""
	})
	modpackReadyIs(t, ns, "pack", "True")
	annotationGone(t, ns, "pack", gameplanev1alpha1.ModpackRemoveAnnotation)
	annotationGone(t, ns, "pack", gameplanev1alpha1.ModpackAdoptedAnnotation)
	if st := getGameServer(t, ns, "pack").Status.Modpack; st == nil || st.Applied != nil {
		t.Fatalf("status.modpack = %+v, want applied nil", st)
	}
}
