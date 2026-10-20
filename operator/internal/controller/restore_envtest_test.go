//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestRestore_FailsWhenBackupMissing — Restore referencing a
// non-existent Backup transitions to Failed with a clear message.
func TestRestore_FailsWhenBackupMissing(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", "minecraft")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "missing-backup", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if r.Status.Message == "" {
			return false, "no failure message set"
		}
		return true, ""
	})
}

// TestRestore_StaysPendingUntilBackupSucceeded — While the source
// Backup is not yet in Succeeded, the Restore should sit in Pending
// and not advance to Suspending.
func TestRestore_StaysPendingUntilBackupSucceeded(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", "minecraft")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	// Backup exists but Status is still empty (Phase=="").
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-001", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-001", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	// First wait until Restore observes the Pending phase. (It starts
	// with empty Status; the controller writes Pending.)
	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		return r.Status.Phase == gameplanev1alpha1.RestorePhasePending, describeRestoreStatus(r)
	})

	// Then assert it stays in Pending while the Backup isn't ready.
	consistently(t, time.Second, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		if r.Status.Phase != gameplanev1alpha1.RestorePhasePending {
			return false, "advanced past Pending: " + describeRestoreStatus(r)
		}
		if r.Status.SnapshotID != "" {
			return false, "snapshotID pinned prematurely"
		}
		return true, ""
	})
}

// TestRestore_PinsSnapshotIDOnTransition — When the source Backup
// reaches Succeeded with a snapshotID, the Restore copies it into its
// own Status (so retention can't pull it out from under us mid-run).
func TestRestore_PinsSnapshotIDOnTransition(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", "minecraft")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-001", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-001", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	// Drive the Backup to Succeeded with a known snapshot ID.
	markBackupSucceeded(t, ns, "smp-001", "abc123def", "12Mi")

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		if r.Status.SnapshotID != "abc123def" {
			return false, describeRestoreStatus(r)
		}
		// After pinning, the next reconcile should advance to Suspending
		// (or further — Suspending → Running is a single step away, so
		// we accept Suspending or Running here).
		switch r.Status.Phase {
		case gameplanev1alpha1.RestorePhaseSuspending, gameplanev1alpha1.RestorePhaseRunning:
			return true, ""
		default:
			return false, "phase = " + string(r.Status.Phase)
		}
	})
}

// TestRestore_SuspendsTargetGameServer — Once snapshotID is pinned,
// the controller patches the target GameServer's Spec.Suspend=true.
func TestRestore_SuspendsTargetGameServer(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", "minecraft")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-001", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-001", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	markBackupSucceeded(t, ns, "smp-001", "snap-1", "")

	eventually(t, func() (bool, string) {
		gs := getGameServer(t, ns, "smp")
		if !gs.Spec.Suspend {
			return false, "GameServer.Spec.Suspend still false"
		}
		return true, ""
	})
}

// TestRestore_AdvancesToRunningOnceSuspended — Restore waits in
// Suspending until GameServer.Status.Phase reports Suspended and the
// owned StatefulSet is observed at zero, then creates the restore Job.
func TestRestore_AdvancesToRunningOnceSuspended(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", "minecraft")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-001", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-001", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	markBackupSucceeded(t, ns, "smp-001", "snap-1", "")

	// Wait until Spec.Suspend has been flipped on.
	eventually(t, func() (bool, string) {
		return getGameServer(t, ns, "smp").Spec.Suspend, "spec.suspend not yet true"
	})

	// While GameServer.Status.Phase is anything other than Suspended/Stopped
	// the Restore must NOT create a Job.
	consistently(t, 750*time.Millisecond, func() (bool, string) {
		if _, ok := getJob(t, ns, "restore-rs-1"); ok {
			return false, "restore Job created before GameServer reached Suspended"
		}
		return true, ""
	})

	// Model the workload controller having observed scale-to-zero.
	createStoppedRestoreStatefulSet(t, ns, "smp")
	// Bump GameServer status into Suspended, expect the Job to materialize.
	markGameServerPhase(t, ns, "smp", gameplanev1alpha1.GameServerPhaseSuspended)

	eventually(t, func() (bool, string) {
		j, ok := getJob(t, ns, "restore-rs-1")
		if !ok {
			return false, "restore Job not yet created"
		}
		// Sanity-check the job runs `restic restore <snapshot>`.
		if len(j.Spec.Template.Spec.Containers) == 0 {
			return false, "job has no containers"
		}
		args := j.Spec.Template.Spec.Containers[0].Args
		if len(args) < 2 || args[0] != "restore" || args[1] != "snap-1" {
			return false, "unexpected restic args: " + sprintArgs(args)
		}
		// --target / is load-bearing: the backup snapshots /data
		// absolutely, so restoring to /data would double-prefix to
		// /data/data/<file>. See restore_controller.go.
		var sawTargetRoot bool
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--target" && args[i+1] == "/" {
				sawTargetRoot = true
				break
			}
		}
		if !sawTargetRoot {
			return false, "expected --target / in restic args, got: " + sprintArgs(args)
		}
		return true, ""
	})

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		return r.Status.Phase == gameplanev1alpha1.RestorePhaseRunning, describeRestoreStatus(r)
	})
}

// TestRestore_ResumesGameServerOnSuccess — Job Succeeded ⇒ Restore
// transitions to Succeeded and target GameServer is un-suspended.
func TestRestore_ResumesGameServerOnSuccess(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	bootstrapRestoreToRunning(t, ns)

	patchJobStatus(t, ns, "restore-rs-1", func(s *batchv1.JobStatus) {
		now := metav1.Now()
		s.Succeeded = 1
		s.StartTime = &now
		s.CompletionTime = &now
	})

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseSucceeded {
			return false, describeRestoreStatus(r)
		}
		gs := getGameServer(t, ns, "smp")
		if gs.Spec.Suspend {
			return false, "GameServer was not resumed (spec.suspend still true)"
		}
		if r.Status.CompletionTime == nil {
			return false, "no completionTime"
		}
		return true, ""
	})
}

// TestRestore_LeavesServerSuspendedOnFailure — Job Failed ⇒ Restore
// goes Failed and the GameServer stays suspended (operator
// intentionally does NOT auto-resume; humans should investigate).
func TestRestore_LeavesServerSuspendedOnFailure(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	bootstrapRestoreToRunning(t, ns)

	patchJobStatus(t, ns, "restore-rs-1", func(s *batchv1.JobStatus) {
		s.Failed = 1
	})

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if r.Status.Message == "" {
			return false, "no failure message"
		}
		gs := getGameServer(t, ns, "smp")
		if !gs.Spec.Suspend {
			return false, "GameServer was incorrectly resumed after restore failure"
		}
		return true, ""
	})
}

// TestRestore_FailsWhenServerMissing — Backup OK but referenced
// GameServer doesn't exist ⇒ Restore goes Failed.
func TestRestore_FailsWhenServerMissing(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	// Note: no GameServer created.
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-001", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-001", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}
	markBackupSucceeded(t, ns, "smp-001", "snap-1", "")

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		return true, ""
	})
}

// bootstrapRestoreToRunning sets up a namespace such that a Restore
// named "rs-1" against GameServer "smp" / Backup "smp-001" has
// reached Phase=Running and the restore Job named "restore-rs-1"
// exists in zero status.
func bootstrapRestoreToRunning(t *testing.T, ns string) {
	t.Helper()

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", "minecraft")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-001", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-001", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	markBackupSucceeded(t, ns, "smp-001", "snap-1", "")

	eventually(t, func() (bool, string) {
		return getGameServer(t, ns, "smp").Spec.Suspend, "waiting for spec.suspend"
	})
	markGameServerPhase(t, ns, "smp", gameplanev1alpha1.GameServerPhaseSuspended)
	createStoppedRestoreStatefulSet(t, ns, "smp")

	eventually(t, func() (bool, string) {
		_, ok := getJob(t, ns, "restore-rs-1")
		return ok, "waiting for restore Job"
	})
	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-1")
		return r.Status.Phase == gameplanev1alpha1.RestorePhaseRunning, describeRestoreStatus(r)
	})
}

// These tests run the Restore controller alone; model the StatefulSet
// controller's observed scale-down explicitly instead of assuming its absence
// makes it safe to start a destructive restore.
func createStoppedRestoreStatefulSet(t *testing.T, ns, name string) {
	t.Helper()
	gs := getGameServer(t, ns, name)
	zero := int32(0)
	labels := map[string]string{"app": name}
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "GameServer", Name: name, UID: gs.UID, Controller: ownerBoolPtr(true)}}},
		Spec:       appsv1.StatefulSetSpec{Replicas: &zero, ServiceName: name, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "game", Image: "busybox"}}}}},
	}
	if err := k8sClient.Create(context.Background(), ss); err != nil {
		t.Fatal(err)
	}
	ss.Status.ObservedGeneration = ss.Generation
	if err := k8sClient.Status().Update(context.Background(), ss); err != nil {
		t.Fatal(err)
	}
}

func sprintArgs(a []string) string {
	out := "["
	for i, s := range a {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out + "]"
}

// TestRestore_FailsWhenBackupFailed — a Restore pointed at a terminally
// Failed Backup must fail with a message instead of waiting forever for
// a snapshotID that will never appear.
func TestRestore_FailsWhenBackupFailed(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", "minecraft")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	bk := buildBackup(ns, "smp-dead", "smp", "repo")
	if err := k8sClient.Create(context.Background(), bk); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	bk.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
	bk.Status.Message = "repo Secret missing"
	if err := k8sClient.Status().Update(context.Background(), bk); err != nil {
		t.Fatalf("mark backup failed: %v", err)
	}

	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-dead", "smp-dead", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-dead")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if r.Status.Message == "" {
			return false, "Failed but no message"
		}
		return true, ""
	})

	// The target GameServer was never suspended.
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: "smp"}, &gs); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	if gs.Spec.Suspend {
		t.Error("failed Restore must not flip GameServer.spec.suspend")
	}
}

// TestRestore_UsesTemplateSecurityContext verifies the restore Job pod uses
// the FSGroup from the GameTemplate's security context.
func TestRestore_UsesTemplateSecurityContext(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())
	tmplName := uniqueName("fsg")
	tmpl := buildGameTemplate(tmplName)
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{FSGroup: ptrInt64(2000)}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)
	gs := buildGameServer(ns, "smp", tmplName)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-001", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	markBackupSucceeded(t, ns, "smp-001", "snap-1", "")

	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-001", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	// The restore only creates its Job once the target server is suspended
	// (see TestRestore_AdvancesToRunningOnceSuspended).
	eventually(t, func() (bool, string) {
		return getGameServer(t, ns, "smp").Spec.Suspend, "spec.suspend not yet true"
	})
	markGameServerPhase(t, ns, "smp", gameplanev1alpha1.GameServerPhaseSuspended)

	createStoppedRestoreStatefulSet(t, ns, "smp")
	var ps corev1.PodSpec
	eventually(t, func() (bool, string) {
		j, ok := getJob(t, ns, "restore-rs-1")
		if !ok {
			return false, "job not yet created"
		}
		ps = j.Spec.Template.Spec
		return true, ""
	})

	if ps.SecurityContext == nil {
		t.Fatal("pod SecurityContext nil")
	}
	if ps.SecurityContext.FSGroup == nil {
		t.Fatal("FSGroup nil")
	}
	if *ps.SecurityContext.FSGroup != 2000 {
		t.Errorf("FSGroup = %d, want 2000", *ps.SecurityContext.FSGroup)
	}
}
