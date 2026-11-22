//go:build envtest

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func modpackBackupCount(t *testing.T, ns string) int {
	t.Helper()
	var backups gameplanev1alpha1.BackupList
	if err := k8sClient.List(context.Background(), &backups, client.InNamespace(ns)); err != nil {
		t.Fatalf("list backups: %v", err)
	}
	return len(backups.Items)
}

// startBackingUpInstall creates a server running pack version 100 and moves it
// to version 200 with backupFirst and install request id req-1, returning the
// name of the Backup of that change.
func startBackingUpInstall(t *testing.T, ns string) string {
	t.Helper()
	startMgr(t, ns, withGameServerReconciler(t, ns))
	seedBackupDestination(t, ns, "dest")
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)
	gs := buildGameServer(ns, "pack", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "100")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	modpackReadyIs(t, ns, "pack", "True")
	mutateGameServer(t, ns, "pack", func(g *gameplanev1alpha1.GameServer) {
		g.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "200")
		g.Spec.Modpack.BackupFirst = true
		g.Spec.Modpack.RequestID = "req-1"
	})
	st := waitModpackPhase(t, ns, "pack", gameplanev1alpha1.ModpackPhaseBackingUp)
	if st.RequestID != "req-1" || st.Target == nil || st.Target.Version != "200" {
		t.Fatalf("status.modpack = %+v, want target 200 and requestId req-1", st)
	}
	return st.BackupName
}

// A BackupFailed install retried with the identical choice carries a new
// spec.modpack.requestId and starts a new Backup.
func TestGameServer_ModpackInstallRetryAfterBackupFailed(t *testing.T) {
	ns := newNamespace(t)
	firstName := startBackingUpInstall(t, ns)
	markBackupFailed(t, ns, firstName, "restic: repository unreachable")
	waitModpackPhase(t, ns, "pack", gameplanev1alpha1.ModpackPhaseBackupFailed)

	mutateGameServer(t, ns, "pack", func(g *gameplanev1alpha1.GameServer) {
		g.Spec.Modpack.RequestID = "req-2"
	})
	eventually(t, func() (bool, string) {
		st := getGameServer(t, ns, "pack").Status.Modpack
		if st == nil || st.Phase != gameplanev1alpha1.ModpackPhaseBackingUp || st.RequestID != "req-2" ||
			st.BackupName == "" || st.BackupName == firstName {
			return false, fmt.Sprintf("status.modpack = %+v, want BackingUp with a new backup for req-2", st)
		}
		return true, ""
	})
}

// An unrelated spec edit while BackingUp or BackupFailed does not supersede the
// change: no second Backup is started.
func TestGameServer_ModpackUnrelatedEditDoesNotSupersede(t *testing.T) {
	ns := newNamespace(t)
	firstName := startBackingUpInstall(t, ns)

	edit := func(name string) {
		mutateGameServer(t, ns, "pack", func(g *gameplanev1alpha1.GameServer) {
			g.Spec.Env = append(g.Spec.Env, corev1.EnvVar{Name: name, Value: "1"})
		})
	}
	edit("UNRELATED_ONE")
	consistently(t, 2*time.Second, func() (bool, string) {
		st := getGameServer(t, ns, "pack").Status.Modpack
		if st == nil || st.Phase != gameplanev1alpha1.ModpackPhaseBackingUp || st.BackupName != firstName {
			return false, fmt.Sprintf("status.modpack = %+v, want BackingUp with backup %s", st, firstName)
		}
		if n := modpackBackupCount(t, ns); n != 1 {
			return false, fmt.Sprintf("%d backups after an unrelated edit, want 1", n)
		}
		return true, ""
	})

	markBackupFailed(t, ns, firstName, "restic: repository unreachable")
	waitModpackPhase(t, ns, "pack", gameplanev1alpha1.ModpackPhaseBackupFailed)
	edit("UNRELATED_TWO")
	consistently(t, 2*time.Second, func() (bool, string) {
		st := getGameServer(t, ns, "pack").Status.Modpack
		if st == nil || st.Phase != gameplanev1alpha1.ModpackPhaseBackupFailed {
			return false, fmt.Sprintf("status.modpack = %+v, want BackupFailed", st)
		}
		if n := modpackBackupCount(t, ns); n != 1 {
			return false, fmt.Sprintf("%d backups after an unrelated edit, want 1", n)
		}
		return true, ""
	})
}
