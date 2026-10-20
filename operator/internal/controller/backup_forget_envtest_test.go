//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// There is no Job controller in envtest, so these tests drive the forget Job to
// its outcome by patching its status, as the existing Backup tests do for the
// backup Job.

// withForgetBackupReconciler is withBackupReconciler with an event recorder, so
// tests can assert on the SnapshotForgotten / SnapshotForgetAbandoned events.
func withForgetBackupReconciler(rec *events.FakeRecorder) setupReconciler {
	return func(mgr manager.Manager) error {
		return (&BackupReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			EventRecorder: rec,
		}).SetupWithManager(mgr)
	}
}

// startForgetFixture boots a manager, seeds a GameServer and repo Secret, creates
// a restic Backup named name, waits for the reconciler to put the snapshot
// finalizer on it, then marks it Succeeded with snapshotID, the state a finished
// backup is in when someone deletes it.
func startForgetFixture(t *testing.T, name, snapshotID string) (string, *events.FakeRecorder) {
	t.Helper()
	ns := newNamespace(t)
	rec := events.NewFakeRecorder(32)
	startMgr(t, ns, withForgetBackupReconciler(rec))
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, name, "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	eventually(t, func() (bool, string) {
		b := getBackup(t, ns, name)
		for _, f := range b.Finalizers {
			if f == gameplanev1alpha1.BackupSnapshotFinalizer {
				return true, ""
			}
		}
		return false, "snapshot finalizer not yet added: " + strings.Join(b.Finalizers, ",")
	})

	// The reconciler also writes status while the backup Job is Pending, so the
	// update can conflict; retry on a fresh read.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var b gameplanev1alpha1.Backup
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &b); err != nil {
			return err
		}
		now := metav1.Now()
		b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
		b.Status.SnapshotID = snapshotID
		b.Status.CompletionTime = &now
		return k8sClient.Status().Update(context.Background(), &b)
	}); err != nil {
		t.Fatalf("mark backup succeeded: %v", err)
	}
	return ns, rec
}

func deleteBackupObject(t *testing.T, ns, name string) {
	t.Helper()
	b := &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	if err := k8sClient.Delete(context.Background(), b); err != nil {
		t.Fatalf("delete backup %s/%s: %v", ns, name, err)
	}
}

func backupGone(t *testing.T, ns, name string) bool {
	t.Helper()
	var b gameplanev1alpha1.Backup
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &b)
	if apierrors.IsNotFound(err) {
		return true
	}
	if err != nil {
		t.Fatalf("get backup %s/%s: %v", ns, name, err)
	}
	return false
}

func waitBackupGone(t *testing.T, ns, name string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		if backupGone(t, ns, name) {
			return true, ""
		}
		return false, "backup " + name + " still present"
	})
}

// waitEventPrefix polls the recorder until an event starting with prefix shows
// up, accumulating what it has seen for the failure message.
func waitEventPrefix(t *testing.T, rec *events.FakeRecorder, prefix string) {
	t.Helper()
	var seen []string
	eventually(t, func() (bool, string) {
		seen = append(seen, takeEvents(rec)...)
		return hasEventPrefix(seen, prefix), "events so far: " + strings.Join(seen, "; ")
	})
}

// restic backups carry the snapshot finalizer; volume-snapshot backups do not.
func TestBackupForget_FinalizerOnlyOnResticBackups(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withForgetBackupReconciler(events.NewFakeRecorder(8)))
	seedGameServer(t, ns, "smp")
	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildBackup(ns, "smp-restic", "smp", "repo")); err != nil {
		t.Fatalf("create restic backup: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildVolumeSnapshotBackup(ns, "smp-vs", "smp")); err != nil {
		t.Fatalf("create volume-snapshot backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		b := getBackup(t, ns, "smp-restic")
		for _, f := range b.Finalizers {
			if f == gameplanev1alpha1.BackupSnapshotFinalizer {
				return true, ""
			}
		}
		return false, "restic backup has no snapshot finalizer yet"
	})

	// Once the volume-snapshot Backup is Running the reconciler has been through
	// the point where it would have added a finalizer.
	eventually(t, func() (bool, string) {
		b := getBackup(t, ns, "smp-vs")
		if b.Status.Phase != gameplanev1alpha1.BackupPhaseRunning {
			return false, describeBackupStatus(b)
		}
		return true, ""
	})
	if got := getBackup(t, ns, "smp-vs").Finalizers; len(got) != 0 {
		t.Errorf("volume-snapshot backup finalizers = %v, want none", got)
	}
}

// Deleting a Succeeded restic Backup runs a forget Job; the Backup is held
// until the Job succeeds, then released with a SnapshotForgotten event.
func TestBackupForget_DeleteRunsForgetJobThenReleases(t *testing.T) {
	ns, rec := startForgetFixture(t, "smp-forget", "deadbeef")
	deleteBackupObject(t, ns, "smp-forget")

	var job *batchv1.Job
	eventually(t, func() (bool, string) {
		j, ok := getJob(t, ns, "smp-forget-forget")
		job = j
		return ok, "forget job not yet created"
	})

	ps := job.Spec.Template.Spec
	if len(ps.Containers) != 1 || ps.Containers[0].Name != "restic" {
		t.Fatalf("containers = %+v, want one restic container", ps.Containers)
	}
	c := ps.Containers[0]
	if !strings.HasPrefix(c.Image, "restic/restic:") {
		t.Errorf("image = %q, want restic/restic:*", c.Image)
	}
	idEnv := ""
	for _, e := range c.Env {
		if e.Name == "FORGET_SNAPSHOT_ID" {
			idEnv = e.Value
		}
	}
	if idEnv != "deadbeef" {
		t.Errorf("FORGET_SNAPSHOT_ID = %q, want deadbeef", idEnv)
	}
	for _, v := range ps.Volumes {
		if v.PersistentVolumeClaim != nil {
			t.Errorf("volume %q is a PVC; the forget job must not mount game data", v.Name)
		}
	}
	if v := job.Spec.Template.Labels[backupRestoreJobLabel]; v != backupRestoreJobValue {
		t.Errorf("pod label %s = %q, want %q", backupRestoreJobLabel, v, backupRestoreJobValue)
	}
	if ps.SecurityContext == nil || ps.SecurityContext.RunAsNonRoot == nil || !*ps.SecurityContext.RunAsNonRoot {
		t.Error("pod should RunAsNonRoot=true")
	}
	if c.SecurityContext == nil || c.SecurityContext.ReadOnlyRootFilesystem == nil ||
		!*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("container should have ReadOnlyRootFilesystem=true")
	}

	// The Backup stays Terminating while the Job runs.
	consistently(t, time.Second, func() (bool, string) {
		if backupGone(t, ns, "smp-forget") {
			return false, "backup released before the forget job finished"
		}
		return true, ""
	})
	if getBackup(t, ns, "smp-forget").DeletionTimestamp == nil {
		t.Error("backup has no deletionTimestamp")
	}

	patchJobStatus(t, ns, "smp-forget-forget", func(s *batchv1.JobStatus) {
		now := metav1.Now()
		s.Succeeded = 1
		s.StartTime = &now
		s.CompletionTime = &now
	})
	waitBackupGone(t, ns, "smp-forget")
	waitEventPrefix(t, rec, "Normal SnapshotForgotten")
}

// A forget Job that fails for good must not wedge the delete: the Backup is
// released with a SnapshotForgetAbandoned warning.
func TestBackupForget_FailedJobReleasesWithWarning(t *testing.T) {
	ns, rec := startForgetFixture(t, "smp-forgetfail", "deadbeef")
	deleteBackupObject(t, ns, "smp-forgetfail")

	eventually(t, func() (bool, string) {
		_, ok := getJob(t, ns, "smp-forgetfail-forget")
		return ok, "forget job not yet created"
	})
	patchJobStatus(t, ns, "smp-forgetfail-forget", func(s *batchv1.JobStatus) {
		s.Failed = 1
	})

	waitBackupGone(t, ns, "smp-forgetfail")
	waitEventPrefix(t, rec, "Warning SnapshotForgetAbandoned")
}

// Past the hard cap the Backup is released even though no Job made progress.
func TestBackupForget_ReleasesAfterHardCap(t *testing.T) {
	ns, rec := startForgetFixture(t, "smp-forgetcap", "deadbeef")

	stale := time.Now().Add(-maxSnapshotForgetWait - time.Minute).UTC().Format(time.RFC3339)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var b gameplanev1alpha1.Backup
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "smp-forgetcap"}, &b); err != nil {
			return err
		}
		patchBackupAnnotations(&b, map[string]string{annoSnapshotForgetStartedAt: stale})
		return k8sClient.Update(context.Background(), &b)
	}); err != nil {
		t.Fatalf("backdate forget-started-at: %v", err)
	}
	deleteBackupObject(t, ns, "smp-forgetcap")

	waitBackupGone(t, ns, "smp-forgetcap")
	waitEventPrefix(t, rec, "Warning SnapshotForgetAbandoned")
	if _, ok := getJob(t, ns, "smp-forgetcap-forget"); ok {
		t.Error("forget job created for a Backup past the hard cap")
	}
}

// With the repo Secret gone no Job could authenticate; release without one.
func TestBackupForget_MissingRepoSecretReleasesWithoutJob(t *testing.T) {
	ns, rec := startForgetFixture(t, "smp-forgetnosecret", "deadbeef")

	if err := k8sClient.Delete(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "repo"},
	}); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	deleteBackupObject(t, ns, "smp-forgetnosecret")

	waitBackupGone(t, ns, "smp-forgetnosecret")
	waitEventPrefix(t, rec, "Warning SnapshotForgetAbandoned")
	if _, ok := getJob(t, ns, "smp-forgetnosecret-forget"); ok {
		t.Error("forget job created although the repo Secret is gone")
	}
}

// A snapshot id that is not a restic id is never handed to restic.
func TestBackupForget_MalformedSnapshotIDReleasesWithoutJob(t *testing.T) {
	ns, rec := startForgetFixture(t, "smp-forgetbadid", "snap-x")
	deleteBackupObject(t, ns, "smp-forgetbadid")

	waitBackupGone(t, ns, "smp-forgetbadid")
	waitEventPrefix(t, rec, "Warning SnapshotForgetSkipped")
	if _, ok := getJob(t, ns, "smp-forgetbadid-forget"); ok {
		t.Error("forget job created for a malformed snapshot id")
	}
}

// A non-terminal Restore that uses the Backup holds the forget back until it
// finishes. The requeue interval is forgetRestorePollInterval, so allow for it.
func TestBackupForget_WaitsForInFlightRestore(t *testing.T) {
	ns, _ := startForgetFixture(t, "smp-forgetpinned", "deadbeef")
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-pin", "smp-forgetpinned", "smp")); err != nil {
		t.Fatalf("create restore: %v", err)
	}
	deleteBackupObject(t, ns, "smp-forgetpinned")

	consistently(t, 3*time.Second, func() (bool, string) {
		if _, ok := getJob(t, ns, "smp-forgetpinned-forget"); ok {
			return false, "forget job created while a Restore is in flight"
		}
		if backupGone(t, ns, "smp-forgetpinned") {
			return false, "backup released while a Restore is in flight"
		}
		return true, ""
	})

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		rs := getRestore(t, ns, "rs-pin")
		rs.Status.Phase = gameplanev1alpha1.RestorePhaseSucceeded
		return k8sClient.Status().Update(context.Background(), rs)
	})
	if err != nil {
		t.Fatalf("finish restore: %v", err)
	}

	eventuallyWith(t, 2*forgetRestorePollInterval+10*time.Second, func() (bool, string) {
		_, ok := getJob(t, ns, "smp-forgetpinned-forget")
		return ok, "forget job not created after the Restore finished"
	})
}
