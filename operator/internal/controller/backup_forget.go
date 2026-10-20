package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Deleting a restic-strategy Backup must not orphan its snapshot in the
// repository. BackupSnapshotFinalizer holds the Backup until a one-shot
// `restic forget <id> --prune` Job has run (or the operator has given up, see
// maxSnapshotForgetWait), so every delete path — kubectl, the API, a
// BackupSchedule's retention trim, owner-reference GC of a GameServer's
// auto-schedule — cleans up the repository the same way.

// annoSnapshotForgetStartedAt records when finalizeSnapshot first started
// working on a deleting Backup. The whole stage (waiting out the Backup's own
// Job, a Restore that pins the snapshot, the forget Job itself) is bounded by
// maxSnapshotForgetWait measured from this timestamp, and the annotation
// survives operator restarts.
const annoSnapshotForgetStartedAt = "backup.gameplane.local/forget-started-at"

// conditionSnapshotForgotten is the Backup condition finalizeSnapshot writes
// when it finishes: True once the snapshot is forgotten, False when the
// operator gave up and left it in the repository.
const conditionSnapshotForgotten = "SnapshotForgotten"

// maxSnapshotForgetWait bounds how long a deleting Backup may be held by
// BackupSnapshotFinalizer. It is deliberately longer than the forget Job's own
// deadline (forgetJobDeadlineSeconds) so a Job that fails normally is reported
// as such, while a vanished Job or an image that can never be pulled still
// releases the Backup. Past it, the finalizer is released with a Warning event
// and the snapshot stays in the repository.
const maxSnapshotForgetWait = 45 * time.Minute

const (
	// forgetJobBackoffLimit and forgetJobDeadlineSeconds bound one forget Job:
	// three pod retries, thirty minutes in total.
	forgetJobBackoffLimit    int32 = 3
	forgetJobDeadlineSeconds int64 = 1800

	// forgetJobSuffix is appended to the Backup name to name its forget Job.
	forgetJobSuffix = "-forget"
	// maxForgetJobNameLen keeps the Job name (and the job-name pod label derived
	// from it) well inside the 63-character label limit.
	maxForgetJobNameLen = 56
	// forgetBackupLabel names the Backup a forget Job and its pod belong to.
	forgetBackupLabel = "gameplane.local/backup"
	// forgetSnapshotIDEnv carries the snapshot id into the forget container, so
	// the id is never interpolated into the shell script.
	forgetSnapshotIDEnv = "FORGET_SNAPSHOT_ID"

	// Requeue intervals for the three things finalizeSnapshot can wait on.
	forgetBackupJobPollInterval = 5 * time.Second
	forgetJobPollInterval       = 10 * time.Second
	forgetRestorePollInterval   = 15 * time.Second
)

// snapshotIDPattern matches a restic snapshot id as printed by `restic backup
// --json` (the 8-character short id) or in full (64 hex characters). The id
// reaches the forget Job through status.snapshotID, which the operator wrote
// from restic's own output; this is the defense against ever handing anything
// else to a command that deletes repository data.
var snapshotIDPattern = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// forgetScript is the forget container's shell script.
//
//   - `restic unlock` removes only stale locks (restic's own staleness rule), so
//     a lock left behind by a crashed backup does not wedge the forget.
//   - `--retry-lock 10m` makes forget wait for other repository users (a
//     running backup, another Backup's forget) instead of failing at once.
//   - A snapshot that is already gone (forgotten by hand, or by another pruner)
//     counts as success: the goal state, "snapshot absent", already holds.
const forgetScript = `set -u
restic unlock || true
out=$(restic forget "$` + forgetSnapshotIDEnv + `" --prune --retry-lock 10m 2>&1); rc=$?
echo "$out"
[ "$rc" -eq 0 ] && exit 0
echo "$out" | grep -qiE 'no matching ID|could not find snapshot|id not found' && exit 0
exit "$rc"
`

// wantsSnapshotFinalizer reports whether b owns (or may come to own) a snapshot
// in a restic repository and so needs BackupSnapshotFinalizer. Volume-snapshot
// Backups are excluded: their VolumeSnapshot is garbage-collected through its
// owner reference. A Failed Backup is excluded because it never produced a
// snapshot id to forget.
func wantsSnapshotFinalizer(b *gameplanev1alpha1.Backup) bool {
	return b.Spec.Strategy != "volume-snapshot" &&
		b.Spec.RepoRef != nil &&
		b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed
}

// finalizeDelete runs on a Backup marked for deletion, in two stages. Stage 1
// (finalizeUnquiesce) releases a quiesced game world; stage 2 (finalizeSnapshot)
// forgets the restic snapshot. Unquiesce goes first because it is time
// critical: a world left with auto-save off is worse than an orphaned snapshot.
func (r *BackupReconciler) finalizeDelete(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(b, gameplanev1alpha1.BackupFinalizer) {
		res, err := r.finalizeUnquiesce(ctx, b)
		if err != nil || res.RequeueAfter > 0 {
			return res, err
		}
	}
	if !controllerutil.ContainsFinalizer(b, gameplanev1alpha1.BackupSnapshotFinalizer) {
		return ctrl.Result{}, nil
	}
	return r.finalizeSnapshot(ctx, b)
}

// finalizeSnapshot is stage 2 of finalizeDelete: it removes the Backup's
// restic snapshot from the repository by running a one-shot forget Job, then
// releases BackupSnapshotFinalizer. It never blocks a delete forever: every
// wait is bounded by maxSnapshotForgetWait, and when the repository cannot be
// reached (Secret gone, namespace terminating, Job failed, deadline passed)
// the finalizer is released with a Warning event and the snapshot is left in
// place for the administrator to remove.
func (r *BackupReconciler) finalizeSnapshot(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	// Nothing was ever written to a restic repository.
	if b.Spec.Strategy == "volume-snapshot" || b.Spec.RepoRef == nil {
		return r.releaseSnapshotFinalizer(ctx, b)
	}

	expired, err := r.snapshotForgetExpired(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}
	if expired {
		return r.abandonSnapshotForget(ctx, b, fmt.Sprintf("gave up after %s", maxSnapshotForgetWait))
	}

	// A snapshot must not appear after the forget ran, so make sure the
	// Backup's own restic Job is gone first.
	waiting, err := r.clearBackupJob(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}
	if waiting {
		return ctrl.Result{RequeueAfter: forgetBackupJobPollInterval}, nil
	}

	id := b.Status.SnapshotID
	switch {
	case id == "":
		if mayHaveWrittenSnapshot(b) {
			r.recordEvent(b, corev1.EventTypeWarning, "SnapshotForgetSkipped",
				"no snapshot id was recorded for this Backup, so any snapshot it wrote "+
					"to the repository is left in place")
		}
		return r.releaseSnapshotFinalizer(ctx, b)
	case !snapshotIDPattern.MatchString(id):
		r.recordEvent(b, corev1.EventTypeWarning, "SnapshotForgetSkipped",
			fmt.Sprintf("status.snapshotID %q is not a restic snapshot id; leaving it in the repository", id))
		return r.releaseSnapshotFinalizer(ctx, b)
	}

	// `forget --prune` takes the repository's exclusive lock and rewrites packs;
	// running it under an in-flight Restore of the same snapshot could break it.
	pinned, err := r.pinnedByRestore(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pinned {
		return ctrl.Result{RequeueAfter: forgetRestorePollInterval}, nil
	}

	secretName := b.Spec.RepoRef.Name
	var repoSecret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: secretName}, &repoSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return r.abandonSnapshotForget(ctx, b, fmt.Sprintf("repo Secret %q not found", secretName))
		}
		return ctrl.Result{}, fmt.Errorf("get repo secret %s for snapshot forget: %w", secretName, err)
	}
	if missing := missingRepoSecretKeys(&repoSecret); len(missing) > 0 {
		return r.abandonSnapshotForget(ctx, b, fmt.Sprintf("repo Secret %q is missing required key(s): %s",
			secretName, strings.Join(missing, ", ")))
	}

	return r.runForgetJob(ctx, b)
}

// runForgetJob creates (or finds) the Backup's forget Job and acts on its
// outcome: success releases the finalizer, a permanent failure abandons the
// forget, anything else requeues.
func (r *BackupReconciler) runForgetJob(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: forgetJobName(b.Name), Namespace: b.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, job, func() error {
		if job.CreationTimestamp.IsZero() {
			backoff := forgetJobBackoffLimit
			deadline := forgetJobDeadlineSeconds
			job.Spec.BackoffLimit = &backoff
			job.Spec.ActiveDeadlineSeconds = &deadline
			job.Labels = map[string]string{forgetBackupLabel: b.Name}
			// The pod label is what the chart's allow-backup-restore-egress
			// NetworkPolicy selects on; without it the pod cannot reach the repo.
			job.Spec.Template.Labels = map[string]string{
				backupRestoreJobLabel: backupRestoreJobValue,
				forgetBackupLabel:     b.Name,
			}
			job.Spec.Template.Spec = r.buildForgetPodSpec(b)
		}
		return controllerutil.SetControllerReference(b, job, r.Scheme)
	}); err != nil {
		switch {
		case apierrors.HasStatusCause(err, corev1.NamespaceTerminatingCause):
			return r.abandonSnapshotForget(ctx, b, "namespace is terminating")
		case apierrors.IsAlreadyExists(err):
			// The cache has not seen the Job we (or a previous pass) created yet.
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		return ctrl.Result{}, fmt.Errorf("create snapshot forget job: %w", err)
	}

	switch {
	case job.Status.Succeeded > 0:
		return r.snapshotForgotten(ctx, b)
	case jobPermanentlyFailed(job):
		return r.abandonSnapshotForget(ctx, b, "forget job failed: "+jobFailureMessage(job))
	}
	return ctrl.Result{RequeueAfter: forgetJobPollInterval}, nil
}

// snapshotForgetExpired records (on first call) when finalizeSnapshot started
// on b and reports whether maxSnapshotForgetWait has elapsed since. The
// timestamp lives in an annotation so it survives reconciles and operator
// restarts; a malformed value counts as expired so it cannot wedge deletion.
func (r *BackupReconciler) snapshotForgetExpired(ctx context.Context, b *gameplanev1alpha1.Backup) (bool, error) {
	since, ok := b.Annotations[annoSnapshotForgetStartedAt]
	if !ok {
		patchBackupAnnotations(b, map[string]string{
			annoSnapshotForgetStartedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err := r.Update(ctx, b); err != nil {
			return false, fmt.Errorf("record snapshot forget start: %w", err)
		}
		return false, nil
	}
	startedAt, parseErr := time.Parse(time.RFC3339, since)
	if parseErr == nil {
		return time.Since(startedAt) >= maxSnapshotForgetWait, nil
	}
	return true, nil
}

// clearBackupJob makes sure the Backup's own restic Job can no longer write to
// the repository. A terminal Backup's Job is finished already. For a Backup
// deleted mid-run (a user delete, or a BackupSchedule with concurrencyPolicy
// Replace) the Job is deleted with foreground propagation, which keeps the Job
// object until its pods are gone, and waiting reports true until then.
func (r *BackupReconciler) clearBackupJob(ctx context.Context, b *gameplanev1alpha1.Backup) (bool, error) {
	if b.Status.Phase == gameplanev1alpha1.BackupPhaseSucceeded ||
		b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed {
		return false, nil
	}
	var job batchv1.Job
	if err := r.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: b.Name}, &job); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get backup job %s: %w", b.Name, err)
	}
	if !job.DeletionTimestamp.IsZero() {
		return true, nil
	}
	if job.Status.Succeeded > 0 || jobPermanentlyFailed(&job) {
		return false, nil
	}
	if err := r.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil &&
		!apierrors.IsNotFound(err) {
		return false, fmt.Errorf("delete in-flight backup job %s: %w", b.Name, err)
	}
	return true, nil
}

// mayHaveWrittenSnapshot reports whether a Backup with no recorded snapshot id
// may nevertheless have left a snapshot in the repository (it ran, or finished
// without its id being readable), as opposed to one that never got that far.
func mayHaveWrittenSnapshot(b *gameplanev1alpha1.Backup) bool {
	switch b.Status.Phase {
	case gameplanev1alpha1.BackupPhaseRunning, gameplanev1alpha1.BackupPhaseSucceeded:
		return true
	case gameplanev1alpha1.BackupPhaseFailed:
		return b.Status.Message == snapshotUnavailableMsg
	}
	return false
}

// pinnedByRestore reports whether a non-terminal Restore in b's namespace uses
// b as its source, either by reference or by the snapshot id it pinned.
func (r *BackupReconciler) pinnedByRestore(ctx context.Context, b *gameplanev1alpha1.Backup) (bool, error) {
	var restores gameplanev1alpha1.RestoreList
	if err := r.List(ctx, &restores, client.InNamespace(b.Namespace)); err != nil {
		return false, fmt.Errorf("list restores for snapshot forget: %w", err)
	}
	for i := range restores.Items {
		rs := &restores.Items[i]
		if rs.Status.Phase == gameplanev1alpha1.RestorePhaseSucceeded ||
			rs.Status.Phase == gameplanev1alpha1.RestorePhaseFailed {
			continue
		}
		if rs.Spec.BackupRef.Name == b.Name ||
			(rs.Status.SnapshotID != "" && rs.Status.SnapshotID == b.Status.SnapshotID) {
			return true, nil
		}
	}
	return false, nil
}

// snapshotForgotten records a successful forget and releases the finalizer.
func (r *BackupReconciler) snapshotForgotten(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	msg := fmt.Sprintf("restic snapshot %s forgotten from the repository", b.Status.SnapshotID)
	if err := r.setSnapshotForgottenCondition(ctx, b, metav1.ConditionTrue, "Forgotten", msg); err != nil {
		return ctrl.Result{}, err
	}
	r.recordEvent(b, corev1.EventTypeNormal, "SnapshotForgotten", msg)
	return r.releaseSnapshotFinalizer(ctx, b)
}

// abandonSnapshotForget gives up on forgetting the snapshot: it records a
// Warning event and SnapshotForgotten=False, then releases the finalizer so the
// Backup can go. The snapshot remains in the repository, named in the event.
func (r *BackupReconciler) abandonSnapshotForget(
	ctx context.Context, b *gameplanev1alpha1.Backup, reason string,
) (ctrl.Result, error) {
	msg := fmt.Sprintf("snapshot %s was not forgotten and remains in the repository: %s",
		b.Status.SnapshotID, reason)
	ctrl.LoggerFrom(ctx).Info("releasing backup snapshot finalizer without forgetting the snapshot",
		"backup", b.Name, "reason", reason)
	r.recordEvent(b, corev1.EventTypeWarning, "SnapshotForgetAbandoned", msg)
	if err := r.setSnapshotForgottenCondition(ctx, b, metav1.ConditionFalse, "ForgetAbandoned", msg); err != nil {
		// Best effort: the Warning event above already carries the same facts,
		// and failing here must not block the delete.
		ctrl.LoggerFrom(ctx).Error(err, "record SnapshotForgotten=False; releasing anyway", "backup", b.Name)
	}
	return r.releaseSnapshotFinalizer(ctx, b)
}

// releaseSnapshotFinalizer removes BackupSnapshotFinalizer. Once it was the
// last finalizer the object is gone, so a NotFound from the Update is success.
func (r *BackupReconciler) releaseSnapshotFinalizer(
	ctx context.Context, b *gameplanev1alpha1.Backup,
) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(b, gameplanev1alpha1.BackupSnapshotFinalizer)
	if err := r.Update(ctx, b); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("release snapshot finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// setSnapshotForgottenCondition upserts the SnapshotForgotten condition. The
// status subresource accepts writes while the Backup is being deleted.
func (r *BackupReconciler) setSnapshotForgottenCondition(
	ctx context.Context, b *gameplanev1alpha1.Backup, status metav1.ConditionStatus, reason, msg string,
) error {
	cond := metav1.Condition{
		Type:               conditionSnapshotForgotten,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: b.Generation,
	}
	// upsertCondition rewrites a matching entry in place, so hand it a copy to
	// be able to tell whether anything changed.
	newConds := upsertCondition(append([]metav1.Condition(nil), b.Status.Conditions...), cond)
	if sameConditions(b.Status.Conditions, newConds) {
		return nil
	}
	b.Status.Conditions = newConds
	if err := r.Status().Update(ctx, b); err != nil {
		return fmt.Errorf("update %s condition: %w", conditionSnapshotForgotten, err)
	}
	return nil
}

// jobFailureMessage returns the message of a Job's Failed condition.
func jobFailureMessage(job *batchv1.Job) string {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue && c.Message != "" {
			return c.Message
		}
	}
	return "no failure detail reported"
}

// forgetJobName names the forget Job for a Backup: "<backup>-forget", or, when
// that would pass maxForgetJobNameLen, a truncated backup name plus a hash of
// the full name so two long names cannot collide.
func forgetJobName(backupName string) string {
	name := backupName + forgetJobSuffix
	if len(name) <= maxForgetJobNameLen {
		return name
	}
	sum := sha256.Sum256([]byte(backupName))
	prefix := strings.TrimRight(backupName[:maxForgetJobNameLen-len(forgetJobSuffix)-9], "-.")
	return fmt.Sprintf("%s-%x%s", prefix, sum[:4], forgetJobSuffix)
}

// buildForgetPodSpec produces the pod for a snapshot forget. It needs only the
// repository Secret — no PVC, GameServer or GameTemplate — so it works after
// the Backup's server has been deleted. It runs as the same non-root user, with
// the same locked-down container security context and restic image, as the
// backup Job. Memory is bounded because prune can be hungry on a large repo.
func (r *BackupReconciler) buildForgetPodSpec(b *gameplanev1alpha1.Backup) corev1.PodSpec {
	nonRoot := true
	uid := int64(65532)

	env := resticEnv(b.Spec.RepoRef.Name)
	env = append(env, corev1.EnvVar{Name: forgetSnapshotIDEnv, Value: b.Status.SnapshotID})

	return corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot:   &nonRoot,
			RunAsUser:      &uid,
			RunAsGroup:     &uid,
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		Containers: []corev1.Container{{
			Name:    "restic",
			Image:   resticImageOrDefault(r.ResticImage),
			Command: []string{"/bin/sh", "-c"},
			Args:    []string{forgetScript},
			Env:     env,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "cache", MountPath: "/tmp"},
			},
			SecurityContext: resticContainerSecurityContext(),
		}},
		Volumes: []corev1.Volume{{
			Name:         "cache",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}},
	}
}
