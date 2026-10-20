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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// forgetTestSnapshotID is a well-formed restic short snapshot id.
const forgetTestSnapshotID = "deadbeef"

// forgetBackup is a Succeeded restic Backup that is being deleted and still
// carries only BackupSnapshotFinalizer: the state finalizeSnapshot starts from.
func forgetBackup() *gameplanev1alpha1.Backup {
	b := scrapeBackup()
	b.Spec.RepoRef = &gameplanev1alpha1.SecretKeySelector{Name: "repo", Key: "repo"}
	b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	b.Status.SnapshotID = forgetTestSnapshotID
	b.Finalizers = []string{gameplanev1alpha1.BackupSnapshotFinalizer}
	now := metav1.Now()
	b.DeletionTimestamp = &now
	return b
}

// forgetRepoSecret is a repo Secret carrying both keys the restic Jobs need.
func forgetRepoSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
		Data: map[string][]byte{
			"repo":     []byte("rest:http://restic.local/repo"),
			"password": []byte("test-secret"),
		},
	}
}

// newForgetReconcilerWith builds a BackupReconciler over a fake client seeded
// with objs, wrapped in the given interceptor, plus a fake event recorder.
func newForgetReconcilerWith(
	t *testing.T, funcs interceptor.Funcs, objs ...client.Object,
) (*BackupReconciler, *events.FakeRecorder) {
	t.Helper()
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&gameplanev1alpha1.Backup{}).
		WithInterceptorFuncs(funcs).
		Build()
	rec := events.NewFakeRecorder(16)
	return &BackupReconciler{Client: cl, Scheme: s, EventRecorder: rec}, rec
}

func newForgetReconciler(t *testing.T, objs ...client.Object) (*BackupReconciler, *events.FakeRecorder) {
	t.Helper()
	return newForgetReconcilerWith(t, interceptor.Funcs{}, objs...)
}

func forgetRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "b1"}}
}

// getForgetBackup returns the Backup b1, or false once it is gone.
func getForgetBackup(t *testing.T, r *BackupReconciler) (*gameplanev1alpha1.Backup, bool) {
	t.Helper()
	var got gameplanev1alpha1.Backup
	err := r.Get(context.Background(), forgetRequest().NamespacedName, &got)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}
	return &got, true
}

// getJobNamed returns the Job ns/name, or false when it does not exist.
func getJobNamed(t *testing.T, r *BackupReconciler, name string) (*batchv1.Job, bool) {
	t.Helper()
	var got batchv1.Job
	err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: name}, &got)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get job %s: %v", name, err)
	}
	return &got, true
}

// takeEvents drains the events recorded so far without blocking.
func takeEvents(rec *events.FakeRecorder) []string {
	var out []string
	for {
		select {
		case ev := <-rec.Events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// hasEventPrefix reports whether any recorded event starts with prefix. The
// fake recorder renders events as "<type> <reason> <message>".
func hasEventPrefix(evs []string, prefix string) bool {
	for _, ev := range evs {
		if strings.HasPrefix(ev, prefix) {
			return true
		}
	}
	return false
}

func TestWantsSnapshotFinalizer(t *testing.T) {
	repo := &gameplanev1alpha1.SecretKeySelector{Name: "repo", Key: "repo"}
	tests := []struct {
		name     string
		strategy string
		repoRef  *gameplanev1alpha1.SecretKeySelector
		phase    gameplanev1alpha1.BackupPhase
		want     bool
	}{
		{"restic default strategy, new", "", repo, "", true},
		{"restic explicit strategy, running", "restic-snapshot", repo, gameplanev1alpha1.BackupPhaseRunning, true},
		{"restic, succeeded", "restic-snapshot", repo, gameplanev1alpha1.BackupPhaseSucceeded, true},
		{"restic, failed", "restic-snapshot", repo, gameplanev1alpha1.BackupPhaseFailed, false},
		{"restic without repoRef", "restic-snapshot", nil, "", false},
		{"volume-snapshot", "volume-snapshot", nil, gameplanev1alpha1.BackupPhaseSucceeded, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := scrapeBackup()
			b.Spec.Strategy = tc.strategy
			b.Spec.RepoRef = tc.repoRef
			b.Status.Phase = tc.phase
			if got := wantsSnapshotFinalizer(b); got != tc.want {
				t.Errorf("wantsSnapshotFinalizer = %v, want %v", got, tc.want)
			}
		})
	}
}

// A live restic Backup gets BackupSnapshotFinalizer on its first reconcile, and
// the pass carries on rather than returning early.
func TestReconcile_AddsSnapshotFinalizerForResticBackup(t *testing.T) {
	b := forgetBackup()
	b.DeletionTimestamp = nil
	b.Finalizers = nil
	r, _ := newForgetReconciler(t, b)

	if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, ok := getForgetBackup(t, r)
	if !ok {
		t.Fatal("backup vanished")
	}
	found := false
	for _, f := range got.Finalizers {
		if f == gameplanev1alpha1.BackupSnapshotFinalizer {
			found = true
		}
	}
	if !found {
		t.Errorf("finalizers = %v, want %q present", got.Finalizers, gameplanev1alpha1.BackupSnapshotFinalizer)
	}
}

// Volume-snapshot and Failed Backups never write a restic snapshot, so they
// must not be held on deletion.
func TestReconcile_NoSnapshotFinalizerWhenNothingToForget(t *testing.T) {
	volume := forgetBackup()
	volume.DeletionTimestamp = nil
	volume.Finalizers = nil
	volume.Spec.Strategy = "volume-snapshot"
	volume.Spec.RepoRef = nil
	volume.Status.SnapshotID = "vs-1"

	failed := forgetBackup()
	failed.DeletionTimestamp = nil
	failed.Finalizers = nil
	failed.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
	failed.Status.SnapshotID = ""

	for name, b := range map[string]*gameplanev1alpha1.Backup{"volume-snapshot": volume, "failed": failed} {
		t.Run(name, func(t *testing.T) {
			r, _ := newForgetReconciler(t, b)
			if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			got, ok := getForgetBackup(t, r)
			if !ok {
				t.Fatal("backup vanished")
			}
			if len(got.Finalizers) != 0 {
				t.Errorf("finalizers = %v, want none", got.Finalizers)
			}
		})
	}
}

// Backups with nothing to forget are released at once, without a Job. A
// Warning is emitted only when the Backup may have written a snapshot whose id
// was never recorded.
func TestFinalizeSnapshot_ReleasesWithoutJob(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(b *gameplanev1alpha1.Backup)
		wantEvent string
	}{
		{
			name: "failed backup with no id",
			mutate: func(b *gameplanev1alpha1.Backup) {
				b.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
				b.Status.SnapshotID = ""
			},
		},
		{
			name: "pending backup with no id",
			mutate: func(b *gameplanev1alpha1.Backup) {
				b.Status.Phase = gameplanev1alpha1.BackupPhasePending
				b.Status.SnapshotID = ""
			},
		},
		{
			name: "running backup with no id",
			mutate: func(b *gameplanev1alpha1.Backup) {
				b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
				b.Status.SnapshotID = ""
			},
			wantEvent: "Warning SnapshotForgetSkipped",
		},
		{
			name: "succeeded backup whose id was never read",
			mutate: func(b *gameplanev1alpha1.Backup) {
				b.Status.SnapshotID = ""
			},
			wantEvent: "Warning SnapshotForgetSkipped",
		},
		{
			name: "malformed snapshot id",
			mutate: func(b *gameplanev1alpha1.Backup) {
				b.Status.SnapshotID = "snap-x; rm -rf /"
			},
			wantEvent: "Warning SnapshotForgetSkipped",
		},
		{
			name: "volume-snapshot strategy",
			mutate: func(b *gameplanev1alpha1.Backup) {
				b.Spec.Strategy = "volume-snapshot"
				b.Spec.RepoRef = nil
				b.Status.SnapshotID = "vs-1"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := forgetBackup()
			tc.mutate(b)
			r, rec := newForgetReconciler(t, b, forgetRepoSecret())

			if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if _, ok := getForgetBackup(t, r); ok {
				t.Error("backup still present, want it released")
			}
			if _, ok := getJobNamed(t, r, "b1-forget"); ok {
				t.Error("forget job created, want none")
			}
			evs := takeEvents(rec)
			if tc.wantEvent == "" && len(evs) != 0 {
				t.Errorf("events = %v, want none", evs)
			}
			if tc.wantEvent != "" && !hasEventPrefix(evs, tc.wantEvent) {
				t.Errorf("events = %v, want one starting %q", evs, tc.wantEvent)
			}
		})
	}
}

// Deleting a Succeeded restic Backup creates a forget Job shaped like the
// backup Job's security posture but with no PVC, then waits on it.
func TestFinalizeSnapshot_CreatesForgetJob(t *testing.T) {
	r, _ := newForgetReconciler(t, forgetBackup(), forgetRepoSecret())

	res, err := r.Reconcile(context.Background(), forgetRequest())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != forgetJobPollInterval {
		t.Errorf("RequeueAfter = %s, want %s", res.RequeueAfter, forgetJobPollInterval)
	}

	got, ok := getForgetBackup(t, r)
	if !ok {
		t.Fatal("backup released before the forget job finished")
	}
	if _, ok := got.Annotations[annoSnapshotForgetStartedAt]; !ok {
		t.Error("forget-started-at annotation missing")
	}

	job, ok := getJobNamed(t, r, "b1-forget")
	if !ok {
		t.Fatal("forget job not created")
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 3 {
		t.Errorf("BackoffLimit = %v, want 3", job.Spec.BackoffLimit)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 1800 {
		t.Errorf("ActiveDeadlineSeconds = %v, want 1800", job.Spec.ActiveDeadlineSeconds)
	}
	if v := job.Spec.Template.Labels[backupRestoreJobLabel]; v != backupRestoreJobValue {
		t.Errorf("pod label %s = %q, want %q", backupRestoreJobLabel, v, backupRestoreJobValue)
	}
	if v := job.Spec.Template.Labels[forgetBackupLabel]; v != "b1" {
		t.Errorf("pod label %s = %q, want b1", forgetBackupLabel, v)
	}
	owned := false
	for _, ref := range job.OwnerReferences {
		if ref.Kind == "Backup" && ref.Name == "b1" && ref.Controller != nil && *ref.Controller {
			owned = true
		}
	}
	if !owned {
		t.Errorf("ownerReferences = %+v, want a controller ref to Backup b1", job.OwnerReferences)
	}

	ps := job.Spec.Template.Spec
	if ps.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", ps.RestartPolicy)
	}
	if len(ps.InitContainers) != 0 {
		t.Errorf("InitContainers = %d, want 0", len(ps.InitContainers))
	}
	if len(ps.Containers) != 1 {
		t.Fatalf("Containers = %d, want 1", len(ps.Containers))
	}
	for _, v := range ps.Volumes {
		if v.PersistentVolumeClaim != nil {
			t.Errorf("volume %q is a PVC; the forget job must not mount game data", v.Name)
		}
	}
	if ps.SecurityContext == nil || ps.SecurityContext.RunAsNonRoot == nil || !*ps.SecurityContext.RunAsNonRoot {
		t.Error("pod should RunAsNonRoot=true")
	}
	c := ps.Containers[0]
	if c.Name != "restic" || !strings.HasPrefix(c.Image, "restic/restic:") {
		t.Errorf("container = %q image %q, want restic restic/restic:*", c.Name, c.Image)
	}
	if len(c.Args) != 1 || c.Args[0] != forgetScript {
		t.Errorf("args = %v, want the forget script", c.Args)
	}
	if !strings.Contains(forgetScript, `restic forget "$FORGET_SNAPSHOT_ID" --prune --retry-lock 10m`) {
		t.Errorf("forget script does not run restic forget on the env var: %s", forgetScript)
	}
	env := map[string]corev1.EnvVar{}
	for _, e := range c.Env {
		env[e.Name] = e
	}
	if env["FORGET_SNAPSHOT_ID"].Value != forgetTestSnapshotID {
		t.Errorf("FORGET_SNAPSHOT_ID = %q, want %q", env["FORGET_SNAPSHOT_ID"].Value, forgetTestSnapshotID)
	}
	for _, name := range []string{"RESTIC_REPOSITORY", "RESTIC_PASSWORD"} {
		e := env[name]
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil || e.ValueFrom.SecretKeyRef.Name != "repo" {
			t.Errorf("%s = %+v, want a secretKeyRef to Secret repo", name, e)
		}
	}
	if c.SecurityContext == nil || c.SecurityContext.ReadOnlyRootFilesystem == nil ||
		!*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("container should have ReadOnlyRootFilesystem=true")
	}
	if c.Resources.Limits.Memory().IsZero() {
		t.Error("container should carry a memory limit")
	}
}

func TestFinalizeSnapshot_JobOutcomes(t *testing.T) {
	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1-forget", Namespace: "ns"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Message: "BackoffLimitExceeded",
		}}},
	}
	succeededJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1-forget", Namespace: "ns"},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
	runningJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1-forget", Namespace: "ns"},
		Status:     batchv1.JobStatus{Active: 1},
	}

	tests := []struct {
		name         string
		job          *batchv1.Job
		wantReleased bool
		wantEvent    string
		wantInEvent  string
	}{
		{"succeeded", succeededJob, true, "Normal SnapshotForgotten", forgetTestSnapshotID},
		{"failed", failedJob, true, "Warning SnapshotForgetAbandoned", "BackoffLimitExceeded"},
		{"running", runningJob, false, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, rec := newForgetReconciler(t, forgetBackup(), forgetRepoSecret(), tc.job.DeepCopy())

			res, err := r.Reconcile(context.Background(), forgetRequest())
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			_, present := getForgetBackup(t, r)
			if present == tc.wantReleased {
				t.Errorf("backup present = %v, want %v", present, !tc.wantReleased)
			}
			evs := takeEvents(rec)
			if tc.wantEvent == "" {
				if len(evs) != 0 {
					t.Errorf("events = %v, want none", evs)
				}
				if res.RequeueAfter != forgetJobPollInterval {
					t.Errorf("RequeueAfter = %s, want %s", res.RequeueAfter, forgetJobPollInterval)
				}
				return
			}
			if !hasEventPrefix(evs, tc.wantEvent) {
				t.Fatalf("events = %v, want one starting %q", evs, tc.wantEvent)
			}
			if !strings.Contains(strings.Join(evs, "\n"), tc.wantInEvent) {
				t.Errorf("events = %v, want text %q", evs, tc.wantInEvent)
			}
		})
	}
}

// A repo Secret that is gone or incomplete means no Job can ever authenticate:
// give up at once instead of creating a Job that cannot start.
func TestFinalizeSnapshot_AbandonsWhenRepoSecretUnusable(t *testing.T) {
	noPassword := forgetRepoSecret()
	delete(noPassword.Data, "password")

	tests := []struct {
		name   string
		secret *corev1.Secret
	}{
		{"secret missing", nil},
		{"secret without password", noPassword},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := []client.Object{forgetBackup()}
			if tc.secret != nil {
				objs = append(objs, tc.secret)
			}
			r, rec := newForgetReconciler(t, objs...)

			if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if _, ok := getForgetBackup(t, r); ok {
				t.Error("backup still present, want it released")
			}
			if _, ok := getJobNamed(t, r, "b1-forget"); ok {
				t.Error("forget job created, want none")
			}
			if evs := takeEvents(rec); !hasEventPrefix(evs, "Warning SnapshotForgetAbandoned") {
				t.Errorf("events = %v, want a SnapshotForgetAbandoned warning", evs)
			}
		})
	}
}

// Past maxSnapshotForgetWait the finalizer is released even though a Job is
// still outstanding; a malformed timestamp counts as expired.
func TestFinalizeSnapshot_AbandonsAfterMaxWait(t *testing.T) {
	tests := []struct {
		name  string
		start string
	}{
		{"older than the cap", time.Now().Add(-maxSnapshotForgetWait - time.Minute).UTC().Format(time.RFC3339)},
		{"malformed timestamp", "not-a-time"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := forgetBackup()
			b.Annotations = map[string]string{annoSnapshotForgetStartedAt: tc.start}
			running := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "b1-forget", Namespace: "ns"},
				Status:     batchv1.JobStatus{Active: 1},
			}
			r, rec := newForgetReconciler(t, b, forgetRepoSecret(), running)

			if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if _, ok := getForgetBackup(t, r); ok {
				t.Error("backup still present, want it released")
			}
			if evs := takeEvents(rec); !hasEventPrefix(evs, "Warning SnapshotForgetAbandoned") {
				t.Errorf("events = %v, want a SnapshotForgetAbandoned warning", evs)
			}
		})
	}
}

// A recent start timestamp must not trip the cap.
func TestFinalizeSnapshot_WithinMaxWaitKeepsWaiting(t *testing.T) {
	b := forgetBackup()
	b.Annotations = map[string]string{
		annoSnapshotForgetStartedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	}
	r, _ := newForgetReconciler(t, b, forgetRepoSecret())

	if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := getForgetBackup(t, r); !ok {
		t.Error("backup released within the wait cap")
	}
	if _, ok := getJobNamed(t, r, "b1-forget"); !ok {
		t.Error("forget job not created")
	}
}

// A non-terminal Restore that uses the Backup (by reference, or by the
// snapshot id it pinned) blocks the forget until it finishes.
func TestFinalizeSnapshot_WaitsForPinningRestore(t *testing.T) {
	byRef := &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "rs1", Namespace: "ns"},
		Spec: gameplanev1alpha1.RestoreSpec{
			BackupRef: gameplanev1alpha1.LocalObjectRef{Name: "b1"},
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "gs1"},
		},
		Status: gameplanev1alpha1.RestoreStatus{Phase: gameplanev1alpha1.RestorePhaseRunning},
	}
	bySnapshot := &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "rs1", Namespace: "ns"},
		Spec: gameplanev1alpha1.RestoreSpec{
			BackupRef: gameplanev1alpha1.LocalObjectRef{Name: "some-other-backup"},
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "gs1"},
		},
		Status: gameplanev1alpha1.RestoreStatus{
			Phase:      gameplanev1alpha1.RestorePhaseRunning,
			SnapshotID: forgetTestSnapshotID,
		},
	}

	for name, rs := range map[string]*gameplanev1alpha1.Restore{"by backupRef": byRef, "by pinned snapshot": bySnapshot} {
		t.Run(name, func(t *testing.T) {
			r, _ := newForgetReconciler(t, forgetBackup(), forgetRepoSecret(), rs)

			res, err := r.Reconcile(context.Background(), forgetRequest())
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if res.RequeueAfter != forgetRestorePollInterval {
				t.Errorf("RequeueAfter = %s, want %s", res.RequeueAfter, forgetRestorePollInterval)
			}
			if _, ok := getJobNamed(t, r, "b1-forget"); ok {
				t.Fatal("forget job created while a Restore is in flight")
			}
			if _, ok := getForgetBackup(t, r); !ok {
				t.Fatal("backup released while a Restore is in flight")
			}

			// The Restore finishes; the next pass proceeds to the Job.
			var cur gameplanev1alpha1.Restore
			if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "rs1"}, &cur); err != nil {
				t.Fatalf("get restore: %v", err)
			}
			cur.Status.Phase = gameplanev1alpha1.RestorePhaseSucceeded
			if err := r.Update(context.Background(), &cur); err != nil {
				t.Fatalf("update restore: %v", err)
			}
			if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if _, ok := getJobNamed(t, r, "b1-forget"); !ok {
				t.Error("forget job not created after the Restore finished")
			}
		})
	}
}

// A Backup deleted mid-run has its own restic Job deleted (and awaited) before
// anything is forgotten, so a snapshot cannot appear after the forget.
func TestFinalizeSnapshot_DeletesInFlightBackupJob(t *testing.T) {
	b := forgetBackup()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
	b.Status.SnapshotID = ""
	backupJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     batchv1.JobStatus{Active: 1},
	}
	r, rec := newForgetReconciler(t, b, forgetRepoSecret(), backupJob)

	res, err := r.Reconcile(context.Background(), forgetRequest())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != forgetBackupJobPollInterval {
		t.Errorf("RequeueAfter = %s, want %s", res.RequeueAfter, forgetBackupJobPollInterval)
	}
	if _, ok := getJobNamed(t, r, "b1"); ok {
		t.Error("in-flight backup job still present, want it deleted")
	}
	if _, ok := getForgetBackup(t, r); !ok {
		t.Fatal("backup released while its own job was still being cleared")
	}
	if _, ok := getJobNamed(t, r, "b1-forget"); ok {
		t.Error("forget job created before the backup job was gone")
	}

	// With the Job gone and no snapshot id ever recorded, the Backup is
	// released with a warning rather than forgotten.
	if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := getForgetBackup(t, r); ok {
		t.Error("backup still present, want it released")
	}
	if evs := takeEvents(rec); !hasEventPrefix(evs, "Warning SnapshotForgetSkipped") {
		t.Errorf("events = %v, want a SnapshotForgetSkipped warning", evs)
	}
}

// A backup Job that already finished is left alone.
func TestFinalizeSnapshot_LeavesFinishedBackupJob(t *testing.T) {
	b := forgetBackup()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
	b.Status.SnapshotID = ""
	backupJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
	r, _ := newForgetReconciler(t, b, forgetRepoSecret(), backupJob)

	if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := getJobNamed(t, r, "b1"); !ok {
		t.Error("finished backup job was deleted")
	}
}

// When the namespace is being torn down the apiserver refuses new Jobs; the
// Backup is released instead of retrying until the cap.
func TestFinalizeSnapshot_AbandonsWhenNamespaceTerminating(t *testing.T) {
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*batchv1.Job); ok {
				return &apierrors.StatusError{ErrStatus: metav1.Status{
					Status:  metav1.StatusFailure,
					Code:    403,
					Reason:  metav1.StatusReasonForbidden,
					Message: "unable to create new content in namespace ns because it is being terminated",
					Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{
						Type: corev1.NamespaceTerminatingCause,
					}}},
				}}
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	r, rec := newForgetReconcilerWith(t, funcs, forgetBackup(), forgetRepoSecret())

	if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := getForgetBackup(t, r); ok {
		t.Error("backup still present, want it released")
	}
	if evs := takeEvents(rec); !hasEventPrefix(evs, "Warning SnapshotForgetAbandoned") {
		t.Errorf("events = %v, want a SnapshotForgetAbandoned warning", evs)
	}
}

// The unquiesce stage runs first and holds the snapshot stage back until the
// world is released.
func TestFinalizeDelete_UnquiesceRunsBeforeSnapshotStage(t *testing.T) {
	b := forgetBackup()
	b.Finalizers = []string{gameplanev1alpha1.BackupFinalizer, gameplanev1alpha1.BackupSnapshotFinalizer}
	b.Annotations = map[string]string{annoQuiesceAttempted: "true"}
	b.Spec.ServerRef.Name = "gs1"
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "gs1", Namespace: "ns"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "gs1-0", Namespace: "ns"}}

	r, _ := newForgetReconciler(t, b, forgetRepoSecret(), gs, pod)
	q := &scrapeQuiescer{unquiesceErr: context.DeadlineExceeded}
	r.AgentClient = q

	res, err := r.Reconcile(context.Background(), forgetRequest())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("expected a requeue while the agent is unreachable, got %+v", res)
	}
	got, ok := getForgetBackup(t, r)
	if !ok || len(got.Finalizers) != 2 {
		t.Fatalf("finalizers = %v, want both held while unquiesce is failing", got)
	}
	if _, ok := getJobNamed(t, r, "b1-forget"); ok {
		t.Fatal("forget job created before the world was unquiesced")
	}

	// The agent recovers: the unquiesce releases its finalizer and the same
	// pass moves on to the snapshot stage.
	q.unquiesceErr = nil
	if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if q.unquiesced != 1 {
		t.Errorf("Unquiesce called %d times, want 1", q.unquiesced)
	}
	got, ok = getForgetBackup(t, r)
	if !ok {
		t.Fatal("backup released before the forget job finished")
	}
	if len(got.Finalizers) != 1 || got.Finalizers[0] != gameplanev1alpha1.BackupSnapshotFinalizer {
		t.Errorf("finalizers = %v, want only the snapshot finalizer", got.Finalizers)
	}
	if _, ok := getJobNamed(t, r, "b1-forget"); !ok {
		t.Error("forget job not created once the world was unquiesced")
	}
}

func TestForgetJobName(t *testing.T) {
	if got, want := forgetJobName("smp-manual"), "smp-manual-forget"; got != want {
		t.Errorf("forgetJobName(short) = %q, want %q", got, want)
	}

	long1 := strings.Repeat("a", 70) + "-1"
	long2 := strings.Repeat("a", 70) + "-2"
	n1, n2 := forgetJobName(long1), forgetJobName(long2)
	for _, n := range []string{n1, n2} {
		if len(n) > maxForgetJobNameLen {
			t.Errorf("forgetJobName length = %d, want <= %d: %q", len(n), maxForgetJobNameLen, n)
		}
		if !strings.HasSuffix(n, forgetJobSuffix) {
			t.Errorf("forgetJobName = %q, want suffix %q", n, forgetJobSuffix)
		}
	}
	if n1 == n2 {
		t.Errorf("two different long names map to the same job name %q", n1)
	}
	if n1 != forgetJobName(long1) {
		t.Error("forgetJobName is not deterministic")
	}
}

func TestSnapshotIDPattern(t *testing.T) {
	good := []string{"deadbeef", "0123456789abcdef", strings.Repeat("a", 64)}
	bad := []string{"", "snap-x", "DEADBEEF", "deadbee", "deadbeef;rm", "dead beef", strings.Repeat("a", 65)}
	for _, id := range good {
		if !snapshotIDPattern.MatchString(id) {
			t.Errorf("snapshotIDPattern rejects %q, want accept", id)
		}
	}
	for _, id := range bad {
		if snapshotIDPattern.MatchString(id) {
			t.Errorf("snapshotIDPattern accepts %q, want reject", id)
		}
	}
}
