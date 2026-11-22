//go:build envtest

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// seedBackupDestination creates the labelled restic destination Secret the
// operator discovers in a server's namespace.
func seedBackupDestination(t *testing.T, ns, name string) {
	t.Helper()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{backupDestinationLabel: "true"}},
		StringData: map[string]string{"repo": "rest:http://restic.local/repo", "password": "test-secret"},
	}
	if err := k8sClient.Create(context.Background(), s); err != nil {
		t.Fatalf("create backup destination: %v", err)
	}
}

// markBackupFailed fakes the Backup controller reporting a failed run.
func markBackupFailed(t *testing.T, ns, name, msg string) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var b gameplanev1alpha1.Backup
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &b); err != nil {
			return err
		}
		b.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
		b.Status.Message = msg
		return k8sClient.Status().Update(context.Background(), &b)
	}); err != nil {
		t.Fatalf("mark backup failed: %v", err)
	}
}

// mutateGameServer applies mut to a fresh copy and updates it, retrying conflicts
// (the reconciler writes status and annotations concurrently).
func mutateGameServer(t *testing.T, ns, name string, mut func(gs *gameplanev1alpha1.GameServer)) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		mut(gs)
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver %s: %v", name, err)
	}
}

// waitModpackPhase polls until status.modpack.phase is want and returns that status.
func waitModpackPhase(t *testing.T, ns, name string, want gameplanev1alpha1.ModpackPhase) *gameplanev1alpha1.GameServerModpackStatus {
	t.Helper()
	var got *gameplanev1alpha1.GameServerModpackStatus
	eventually(t, func() (bool, string) {
		st := getGameServer(t, ns, name).Status.Modpack
		if st == nil || st.Phase != want {
			return false, fmt.Sprintf("status.modpack = %+v, want phase %s", st, want)
		}
		got = st
		return true, ""
	})
	return got
}

// waitGameEnv polls until the game container env has key=want.
func waitGameEnv(t *testing.T, ns, name, key, want string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		env, why := gameEnvOf(ns, name)
		if why != "" {
			return false, why
		}
		if got, _ := envValue(env, key); got != want {
			return false, fmt.Sprintf("%s=%q, want %q", key, got, want)
		}
		return true, ""
	})
}

func readyCondition(ns, name string, t *testing.T) *metav1.Condition {
	t.Helper()
	return meta.FindStatusCondition(getGameServer(t, ns, name).Status.Conditions, gameplanev1alpha1.GameServerConditionModpackReady)
}

// TestGameServer_ModpackBackupFirstThenApply covers spec 035 T028: backupFirst
// creates Backup <gs>-modpack-<generation> with no owner (it outlives the server) and the env
// stays on the old version until it succeeds; a failed backup parks the change
// in BackupFailed with the message and applied unchanged; a new change after
// BackupFailed restarts the flow.
func TestGameServer_ModpackBackupFirstThenApply(t *testing.T) {
	ns := newNamespace(t)
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
	waitGameEnv(t, ns, "pack", "CF_FILE_ID", "100")

	// Change with a backup: Backup created, old env keeps rendering.
	mutateGameServer(t, ns, "pack", func(g *gameplanev1alpha1.GameServer) {
		g.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "200")
		g.Spec.Modpack.BackupFirst = true
	})
	st := waitModpackPhase(t, ns, "pack", gameplanev1alpha1.ModpackPhaseBackingUp)
	firstName := fmt.Sprintf("pack-modpack-%d", st.ObservedGeneration)
	if st.BackupName != firstName {
		t.Fatalf("status.modpack.backupName = %q, want %q", st.BackupName, firstName)
	}
	if gen := getGameServer(t, ns, "pack").Generation; st.ObservedGeneration != gen {
		t.Fatalf("observedGeneration = %d, want %d", st.ObservedGeneration, gen)
	}
	b := getBackup(t, ns, firstName)
	if len(b.OwnerReferences) != 0 {
		t.Fatalf("backup ownerReferences = %+v, want none (survives deleting the server)", b.OwnerReferences)
	}
	if b.Labels[modpackBackupLabel] != "true" {
		t.Fatalf("backup labels = %v, want %s=true", b.Labels, modpackBackupLabel)
	}
	if b.Spec.ServerRef.Name != "pack" || b.Spec.RepoRef == nil || b.Spec.RepoRef.Name != "dest" || b.Spec.RepoRef.Key != "repo" {
		t.Fatalf("backup spec = %+v, want serverRef pack, repoRef dest/repo", b.Spec)
	}
	consistently(t, 2*time.Second, func() (bool, string) {
		env, why := gameEnvOf(ns, "pack")
		if why != "" {
			return false, why
		}
		if got, _ := envValue(env, "CF_FILE_ID"); got != "100" {
			return false, "CF_FILE_ID=" + got + " while the backup runs, want 100"
		}
		live := getGameServer(t, ns, "pack").Status.Modpack
		if live == nil || live.Phase != gameplanev1alpha1.ModpackPhaseBackingUp || live.Applied == nil || live.Applied.Version != "100" {
			return false, fmt.Sprintf("status.modpack = %+v, want BackingUp with applied 100", live)
		}
		if c := readyCondition(ns, "pack", t); c == nil || c.Status != metav1.ConditionFalse || c.Reason != "BackingUp" {
			return false, fmt.Sprintf("ModpackReady = %+v, want False/BackingUp", c)
		}
		return true, ""
	})

	markBackupSucceeded(t, ns, firstName, "snap-1", "")
	waitGameEnv(t, ns, "pack", "CF_FILE_ID", "200")
	modpackReadyIs(t, ns, "pack", "True")
	if st := getGameServer(t, ns, "pack").Status.Modpack; st == nil || st.Applied == nil || st.Applied.Version != "200" || st.BackupName != firstName {
		t.Fatalf("status.modpack after backup = %+v, want applied 200, backupName %s", st, firstName)
	}

	// Failed backup: BackupFailed, message set, env and applied unchanged.
	mutateGameServer(t, ns, "pack", func(g *gameplanev1alpha1.GameServer) {
		g.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "300")
		g.Spec.Modpack.BackupFirst = true
	})
	var failedName string
	eventually(t, func() (bool, string) {
		st := getGameServer(t, ns, "pack").Status.Modpack
		if st == nil || st.Phase != gameplanev1alpha1.ModpackPhaseBackingUp || st.BackupName == firstName {
			return false, fmt.Sprintf("status.modpack = %+v, want BackingUp with a new backup", st)
		}
		failedName = st.BackupName
		return true, ""
	})
	markBackupFailed(t, ns, failedName, "restic: repository unreachable")
	st = waitModpackPhase(t, ns, "pack", gameplanev1alpha1.ModpackPhaseBackupFailed)
	if !strings.Contains(st.Message, "repository unreachable") {
		t.Fatalf("message = %q, want the backup's reason", st.Message)
	}
	if st.Applied == nil || st.Applied.Version != "200" {
		t.Fatalf("applied after failed backup = %+v, want version 200", st.Applied)
	}
	consistently(t, 1500*time.Millisecond, func() (bool, string) {
		env, why := gameEnvOf(ns, "pack")
		if why != "" {
			return false, why
		}
		if got, _ := envValue(env, "CF_FILE_ID"); got != "200" {
			return false, "CF_FILE_ID=" + got + " after a failed backup, want 200"
		}
		if live := getGameServer(t, ns, "pack").Status.Modpack; live == nil || live.Phase != gameplanev1alpha1.ModpackPhaseBackupFailed {
			return false, fmt.Sprintf("status.modpack = %+v, want BackupFailed", live)
		}
		return true, ""
	})
	if c := readyCondition(ns, "pack", t); c == nil || c.Status != metav1.ConditionFalse || c.Reason != "BackupFailed" ||
		!strings.Contains(c.Message, "repository unreachable") {
		t.Fatalf("ModpackReady = %+v, want False/BackupFailed with the message", c)
	}

	// A new change restarts the flow with a new Backup.
	mutateGameServer(t, ns, "pack", func(g *gameplanev1alpha1.GameServer) {
		g.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "400")
		g.Spec.Modpack.BackupFirst = true
	})
	var retryName string
	eventually(t, func() (bool, string) {
		st := getGameServer(t, ns, "pack").Status.Modpack
		if st == nil || st.Phase != gameplanev1alpha1.ModpackPhaseBackingUp || st.BackupName == failedName || st.BackupName == "" {
			return false, fmt.Sprintf("status.modpack = %+v, want BackingUp with a newer backup", st)
		}
		retryName = st.BackupName
		return true, ""
	})
	markBackupSucceeded(t, ns, retryName, "snap-2", "")
	waitGameEnv(t, ns, "pack", "CF_FILE_ID", "400")
	modpackReadyIs(t, ns, "pack", "True")
}

// TestGameServer_ModpackBackupFirstWithoutDestinationApplies: backupFirst is
// honoured only when a destination exists; without one the change applies
// directly and no Backup is created.
func TestGameServer_ModpackBackupFirstWithoutDestinationApplies(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "pack", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "100")
	gs.Spec.Modpack.BackupFirst = true
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	waitGameEnv(t, ns, "pack", "CF_FILE_ID", "100")
	modpackReadyIs(t, ns, "pack", "True")
	var backups gameplanev1alpha1.BackupList
	if err := k8sClient.List(context.Background(), &backups, client.InNamespace(ns)); err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups.Items) != 0 {
		t.Fatalf("created %d backups without a destination, want 0", len(backups.Items))
	}
}
