package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/recorder"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/agent"
)

// Annotations the BackupReconciler maintains on each Backup to track
// quiesce state across reconcile passes.
const (
	annoQuiesceAttempted = "backup.gameplane.local/quiesce-attempted"
	annoQuiescedAt       = "backup.gameplane.local/quiesced-at"
	annoUnquiescedAt     = "backup.gameplane.local/unquiesced-at"
	// annoUnquiesceRetryFirstFailedAt records when finalizeDelete's
	// unquiesce-on-delete loop first failed against a target that still
	// exists, so the retry can be bounded (maxUnquiesceFinalizeRetry)
	// instead of requeuing forever.
	annoUnquiesceRetryFirstFailedAt = "backup.gameplane.local/unquiesce-retry-first-failed-at"
)

// maxUnquiesceFinalizeRetry bounds how long finalizeDelete keeps retrying an
// unquiesce against a target (GameServer + pod) that still exists but whose
// agent stays unreachable. Past this window the finalizer is released and a
// Warning event is emitted rather than blocking the Backup's deletion
// forever; the world may still have auto-save off until the agent recovers
// or the pod is restarted.
const maxUnquiesceFinalizeRetry = 10 * time.Minute

// Default bounds for the restic backup and restore Jobs (spec 018 OD-027).
// Operators override them via --backup-job-backoff-limit /
// --backup-job-active-deadline-seconds (Helm operator.backupJobBackoffLimit /
// operator.backupJobActiveDeadlineSeconds).
const (
	DefaultBackupJobBackoffLimit          int32 = 2
	DefaultBackupJobActiveDeadlineSeconds int64 = 86400 // 24 hours
)

// resolveJobLimits returns the Job BackoffLimit and ActiveDeadlineSeconds to
// use: a nil backoff selects the default (0 is a valid explicit value, so
// nil, not zero, means unset); a non-positive deadline selects the default.
func resolveJobLimits(backoff *int32, deadline int64) (int32, int64) {
	b, d := DefaultBackupJobBackoffLimit, DefaultBackupJobActiveDeadlineSeconds
	if backoff != nil && *backoff >= 0 {
		b = *backoff
	}
	if deadline > 0 {
		d = deadline
	}
	return b, d
}

// backupRestoreJobLabel/backupRestoreJobValue mark Backup and Restore Job
// pods so the chart's allow-backup-restore-egress NetworkPolicy (F-215)
// can select them. Without this label the Job pod carries only the Job
// controller's own labels, default-deny-egress's podSelector: {} is the
// only policy that applies to it, and it can't reach any restic
// repository. restore_controller.go uses the same two constants.
const (
	backupRestoreJobLabel = "app.kubernetes.io/name"
	backupRestoreJobValue = "gameplane-backup-restore"
)

// AgentQuiescer is the slice of *agent.Client the BackupReconciler
// uses. Defined here as an interface so envtest can swap in a fake
// without standing up an HTTPS listener.
type AgentQuiescer interface {
	Quiesce(ctx context.Context, namespace, server string) error
	Unquiesce(ctx context.Context, namespace, server string) error
}

// BackupLogReader fetches the restic container's logs for a completed
// Backup so the reconciler can extract the trailing JSON summary.
// Production uses a Pods.GetLogs implementation; envtest plugs in a
// fake that returns a canned restic-output string.
type BackupLogReader interface {
	BackupLogs(ctx context.Context, namespace, backupName string) (io.ReadCloser, error)
}

// BackupReconciler drives a Backup to completion by creating a one-shot
// Job that runs restic against the GameServer's data volume.
type BackupReconciler struct {
	client.Client
	// APIReader bypasses informer caches for guard ownership and worker drain checks.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Clientset kubernetes.Interface
	// AgentClient may be nil for installs that haven't configured
	// operator → agent mTLS yet; the reconciler treats that as "no
	// quiesce capability" and proceeds with raw backups.
	AgentClient AgentQuiescer
	// LogReader, if set, overrides the default kubernetes-Clientset
	// based log reader. Used by tests.
	LogReader BackupLogReader
	// ResticImage is the image for the restic backup Job. Set from an
	// operator flag so air-gapped installs can point it at a private
	// registry mirror. Empty falls back to DefaultResticImage.
	ResticImage string
	// JobBackoffLimit / JobActiveDeadlineSeconds bound the restic backup Job.
	// Set from operator flags; nil / zero fall back to
	// DefaultBackupJobBackoffLimit / DefaultBackupJobActiveDeadlineSeconds.
	JobBackoffLimit          *int32
	JobActiveDeadlineSeconds int64
	// SnapshotScrapeGracePeriod bounds how long after the restic Job
	// completes the reconciler keeps retrying to read the snapshot id from
	// the pod logs before failing the Backup. Zero uses
	// defaultSnapshotScrapeGracePeriod; tests set it small.
	SnapshotScrapeGracePeriod time.Duration
	// EventRecorder surfaces the UnquiesceAbandoned warning when
	// finalizeDelete gives up on a bounded unquiesce retry (F-048). May be
	// nil in tests that don't assert on events. Uses controller-runtime's
	// current recorder.EventRecorder (Manager.GetEventRecorder) rather than
	// the deprecated client-go record.EventRecorder that
	// Manager.GetEventRecorderFor returns.
	EventRecorder recorder.EventRecorder
}

// defaultSnapshotScrapeGracePeriod bounds how long, after the restic Job
// finishes, the reconciler keeps retrying to read the snapshot id out of the
// pod logs before giving up. Log availability can lag Job completion by a few
// seconds, but logs that were rotated or garbage-collected never come back —
// and a Backup with no snapshot id is not restorable, so past this window it
// is failed rather than left parked at a misleading Succeeded.
const defaultSnapshotScrapeGracePeriod = 3 * time.Minute

func (r *BackupReconciler) snapshotScrapeGracePeriod() time.Duration {
	if r.SnapshotScrapeGracePeriod > 0 {
		return r.SnapshotScrapeGracePeriod
	}
	return defaultSnapshotScrapeGracePeriod
}

// completedSince reports how long ago the backup Job finished. It prefers the
// Job's own CompletionTime and falls back to the copy mirrored onto the Backup,
// so the grace period stays bounded even once the Job object is gone. Returns 0
// when neither is set, which keeps the caller retrying rather than failing on a
// Job that hasn't recorded a completion time yet.
func completedSince(job *batchv1.Job, b *gameplanev1alpha1.Backup) time.Duration {
	ct := job.Status.CompletionTime
	if ct == nil {
		ct = b.Status.CompletionTime
	}
	if ct == nil {
		return 0
	}
	return time.Since(ct.Time)
}

// snapshotUnavailableMsg explains a Backup that wrote data but can never be
// restored, because its restic snapshot id was never recoverable.
const snapshotUnavailableMsg = "backup job completed but its restic snapshot id could not be " +
	"read from the pod logs; the backup is not restorable"

// retrySnapshotScrape re-reads the restic summary for a Backup that finished
// without yielding a snapshot id, without re-entering the create/validate
// pipeline. If the Job is gone its pod logs went with it, so the id can never be
// recovered and the Backup fails now rather than after the grace period.
func (r *BackupReconciler) retrySnapshotScrape(
	ctx context.Context, b *gameplanev1alpha1.Backup,
) (ctrl.Result, error) {
	var job batchv1.Job
	if err := r.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: b.Name}, &job); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return r.failUnrestorable(ctx, b)
	}
	return r.mirrorJobStatus(ctx, b, &job)
}

// failUnrestorable terminates a Backup whose snapshot id could not be read.
//
// It releases the game world FIRST. If spec.quiesce was set, the world is
// sitting with auto-save off waiting for the post-backup unquiesce; once the
// Backup is Failed the Reconcile guard short-circuits and the unquiesce would
// never run — leaving the world frozen. So retry the unquiesce until it lands
// (runUnquiesce asks for a requeue when the agent is unreachable) and only then
// commit the terminal phase.
func (r *BackupReconciler) failUnrestorable(
	ctx context.Context, b *gameplanev1alpha1.Backup,
) (ctrl.Result, error) {
	res, err := r.runUnquiesce(ctx, b)
	if err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	return r.fail(ctx, b, snapshotUnavailableMsg)
}

// DefaultResticImage is the pinned restic image used by the backup and
// restore Jobs. Overridable via the operator's --restic-image flag for
// air-gapped installs (both reconcilers fall back to it when unset).
const DefaultResticImage = "restic/restic:0.17.1"

// resticImageOrDefault resolves the configured restic image, falling back
// to the pin when the operator wasn't given a --restic-image.
func resticImageOrDefault(image string) string {
	if image == "" {
		return DefaultResticImage
	}
	return image
}

// +kubebuilder:rbac:groups=gameplane.local,resources=backups,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=gameplane.local,resources=backups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gameplane.local,resources=backups/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotcontents,verbs=get;list;watch

func (r *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return requeueOnConflict(r.reconcile(ctx, req))
}

func (r *BackupReconciler) reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var b gameplanev1alpha1.Backup
	if err := r.Get(ctx, req.NamespacedName, &b); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !b.DeletionTimestamp.IsZero() {
		return r.finalizeDelete(ctx, &b)
	}
	// Only Backups that might quiesce the world need to block on releasing
	// it before deletion; everything else deletes immediately.
	if b.Spec.Quiesce && !controllerutil.ContainsFinalizer(&b, gameplanev1alpha1.BackupFinalizer) {
		controllerutil.AddFinalizer(&b, gameplanev1alpha1.BackupFinalizer)
		if err := r.Update(ctx, &b); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Restic Backups own a snapshot in the repository, so hold their deletion
	// until finalizeSnapshot has forgotten it. Unlike the quiesce finalizer above
	// this does not return after the Update: adding it is a metadata-only change,
	// and the pass carries on with the refreshed object. That also covers
	// terminal Backups created before this finalizer existed, which would
	// otherwise short-circuit below and never get it.
	if wantsSnapshotFinalizer(&b) && !controllerutil.ContainsFinalizer(&b, gameplanev1alpha1.BackupSnapshotFinalizer) {
		controllerutil.AddFinalizer(&b, gameplanev1alpha1.BackupSnapshotFinalizer)
		if err := r.Update(ctx, &b); err != nil {
			return ctrl.Result{}, err
		}
	}

	// A Succeeded Backup whose restic snapshot id we never managed to read isn't
	// restorable yet, so keep working on it — but retry only the log scrape.
	// Re-entering the full pipeline would re-validate the GameServer and repo
	// Secret (flipping an already-Succeeded Backup to Failed if either was since
	// deleted) and would re-create a cleaned-up Job, running a second backup.
	if b.Status.Phase == gameplanev1alpha1.BackupPhaseSucceeded && b.Status.SnapshotID == "" {
		return r.retrySnapshotScrape(ctx, &b)
	}
	// Terminal Backups need no further reconciliation of the restic Job — but
	// a terminal phase can be persisted (mirrorJobStatus writes status.phase
	// before the matching unquiesce is confirmed) while a prior unquiesce
	// attempt is still failing (F-044). Retrying that is the only work a
	// terminal Backup still owes; runUnquiesce is a fast no-op once nothing
	// is outstanding, so this stays cheap on the common path.
	if b.Status.Phase == gameplanev1alpha1.BackupPhaseSucceeded ||
		b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed {
		return r.runUnquiesce(ctx, &b)
	}

	// Resolve the target before building the Job: a Backup against a
	// GameServer that doesn't exist would produce a pod waiting forever
	// on a missing PVC instead of a clean Failed phase.
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: b.Namespace, Name: b.Spec.ServerRef.Name,
	}, &gs); err != nil {
		if apierrors.IsNotFound(err) {
			return r.fail(ctx, &b,
				fmt.Sprintf("GameServer %q not found in namespace %s", b.Spec.ServerRef.Name, b.Namespace))
		}
		return ctrl.Result{}, err
	}

	// A missing template is not fatal: it only refines the FSGroup, and
	// buildBackupPodSecurityContext falls back to 65532 for a zero template.
	var tmpl gameplanev1alpha1.GameTemplate
	if err := r.Get(ctx, types.NamespacedName{Name: gs.Spec.TemplateRef.Name}, &tmpl); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// Volume-snapshot backups take a CSI snapshot of the data PVC rather
	// than running restic, so they branch off here — before the restic
	// repo Secret checks, which don't apply to them.
	if b.Spec.Strategy == "volume-snapshot" {
		return r.reconcileVolumeSnapshot(ctx, &b, &gs)
	}

	// restic backups need a repo Secret. The CEL rule on BackupSpec already
	// enforces this at admission, but guard defensively so a nil deref can't
	// happen if an object slips through (e.g. an older CRD revision).
	if b.Spec.RepoRef == nil {
		return r.fail(ctx, &b, "repoRef is required for the restic-snapshot strategy")
	}

	// Same for the restic repo Secret: a missing repoRef leaves the Job
	// pod stuck in CreateContainerConfigError instead of failing.
	var repoSecret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: b.Namespace, Name: b.Spec.RepoRef.Name,
	}, &repoSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return r.fail(ctx, &b,
				fmt.Sprintf("repo Secret %q not found in namespace %s", b.Spec.RepoRef.Name, b.Namespace))
		}
		return ctrl.Result{}, err
	}
	// The Job mounts RESTIC_REPOSITORY / RESTIC_PASSWORD from keys "repo"
	// and "password". If either is missing the pod hits
	// CreateContainerConfigError and the Backup stalls Pending forever;
	// fail fast with a clear message instead.
	if missing := missingRepoSecretKeys(&repoSecret); len(missing) > 0 {
		return r.fail(ctx, &b, fmt.Sprintf("repo Secret %q is missing required key(s): %s",
			b.Spec.RepoRef.Name, strings.Join(missing, ", ")))
	}

	if res, err := r.prepareBackupQuiesce(ctx, &b); err != nil || res.RequeueAfter > 0 {
		return res, err
	}

	backoff, deadline := resolveJobLimits(r.JobBackoffLimit, r.JobActiveDeadlineSeconds)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, job, func() error {
		if job.CreationTimestamp.IsZero() {
			job.Spec.BackoffLimit = &backoff
			job.Spec.ActiveDeadlineSeconds = &deadline
			job.Spec.Template.Labels = map[string]string{backupRestoreJobLabel: backupRestoreJobValue, backupUIDLabel: string(b.UID)}
			job.Spec.Template.Spec = r.buildBackupPodSpec(&b, &tmpl)
			job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
		}
		return controllerutil.SetControllerReference(&b, job, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	return r.mirrorJobStatus(ctx, &b, job)
}

// maybeQuiesce calls the agent's /quiesce endpoint once per Backup.
// The quiesce-attempted annotation makes the call idempotent across
// requeues; ErrUnsupported is recorded as "attempted but unsupported"
// so the matching unquiesce step is skipped on terminal phase.
func (r *BackupReconciler) maybeQuiesce(ctx context.Context, b *gameplanev1alpha1.Backup) error {
	if !b.Spec.Quiesce {
		return nil
	}
	if r.AgentClient == nil {
		return nil
	}
	if state := b.Annotations[annoQuiesceAttempted]; state != "" && state != "pending" {
		return nil
	}
	// Persist cleanup intent BEFORE save-off: a crash or a partial agent failure
	// must still owe save-on even when quiesced-at was never recorded.
	patchBackupAnnotations(b, map[string]string{annoQuiesceAttempted: "pending"})
	if err := r.Update(ctx, b); err != nil {
		return err
	}

	err := r.AgentClient.Quiesce(ctx, b.Namespace, b.Spec.ServerRef.Name)
	switch {
	case err == nil:
		now := time.Now().UTC().Format(time.RFC3339)
		patchBackupAnnotations(b, map[string]string{
			annoQuiesceAttempted: "true",
			annoQuiescedAt:       now,
		})
	case errors.Is(err, agent.ErrUnsupported):
		// Game has no quiesce equivalent. Mark attempted and proceed —
		// games like Valheim write to disk consistently anyway.
		patchBackupAnnotations(b, map[string]string{
			annoQuiesceAttempted: "unsupported",
		})
	default:
		// Hard failure (network, RCON down). Surface as Failed: a
		// backup taken against an unflushed Minecraft world can be
		// inconsistent, so it's safer to fail loudly than to proceed.
		_, ferr := r.fail(ctx, b, fmt.Sprintf("quiesce failed: %v", err))
		if ferr != nil {
			return ferr
		}
		return err
	}
	return r.Update(ctx, b)
}

// patchBackupAnnotations merges in the given key/value pairs without
// dropping pre-existing annotations.
func patchBackupAnnotations(b *gameplanev1alpha1.Backup, kv map[string]string) {
	if b.Annotations == nil {
		b.Annotations = map[string]string{}
	}
	for k, v := range kv {
		b.Annotations[k] = v
	}
}

// mirrorJobStatus copies phase + timestamps from the Job into the
// Backup's status, scrapes the restic JSON summary out of pod logs on
// success, and triggers the matching unquiesce when the Backup
// reaches a terminal phase.
func (r *BackupReconciler) mirrorJobStatus(
	ctx context.Context, b *gameplanev1alpha1.Backup, job *batchv1.Job,
) (ctrl.Result, error) {
	phase := b.Status.Phase
	switch {
	case job.Status.Succeeded > 0:
		phase = gameplanev1alpha1.BackupPhaseSucceeded
	case jobPermanentlyFailed(job):
		phase = gameplanev1alpha1.BackupPhaseFailed
	case job.Status.Active > 0:
		phase = gameplanev1alpha1.BackupPhaseRunning
	default:
		if phase == "" {
			phase = gameplanev1alpha1.BackupPhasePending
		}
	}

	dirty := false
	if b.Status.Phase != phase {
		b.Status.Phase = phase
		dirty = true
	}
	if b.Status.ObservedGeneration != b.Generation {
		b.Status.ObservedGeneration = b.Generation
		dirty = true
	}
	if job.Status.StartTime != nil && b.Status.StartTime == nil {
		b.Status.StartTime = job.Status.StartTime
		dirty = true
	}
	if job.Status.CompletionTime != nil && b.Status.CompletionTime == nil {
		b.Status.CompletionTime = job.Status.CompletionTime
		dirty = true
	}

	// On Succeeded, scrape the restic summary for snapshot id + size.
	// Restore depends on a non-empty snapshotID, so a Succeeded Backup that
	// never yields one is not restorable. Retry across reconciles (log
	// availability can lag Job completion); if the id still can't be read a
	// grace period after the Job finished — e.g. the logs were rotated or
	// GC'd — fail the Backup rather than leaving it parked at a misleading
	// Succeeded.
	snapshotMissing := false
	if phase == gameplanev1alpha1.BackupPhaseSucceeded && b.Status.SnapshotID == "" {
		summary, err := r.readResticSummary(ctx, b)
		switch {
		case err == nil && summary.SnapshotID != "":
			b.Status.SnapshotID = summary.SnapshotID
			if summary.TotalBytesProcessed > 0 {
				q := resource.MustParse(strconv.FormatInt(summary.TotalBytesProcessed, 10))
				b.Status.Size = &q
			}
			if b.Status.Message != "" {
				b.Status.Message = "" // clear any transient scrape error
			}
			dirty = true
		case completedSince(job, b) > r.snapshotScrapeGracePeriod():
			return r.failUnrestorable(ctx, b)
		default:
			// Not yet: keep retrying on requeue until the id lands or the
			// grace period lapses. Surface why for visibility.
			snapshotMissing = true
			if err != nil && !errors.Is(err, ErrNoResticSummary) && b.Status.Message != err.Error() {
				b.Status.Message = err.Error()
				dirty = true
			}
		}
	}

	cond := metav1.Condition{
		Type:               "Completed",
		ObservedGeneration: b.Generation,
	}
	switch phase {
	case gameplanev1alpha1.BackupPhaseSucceeded:
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Succeeded"
	case gameplanev1alpha1.BackupPhaseFailed:
		cond.Status = metav1.ConditionFalse
		cond.Reason = "JobFailed"
		cond.Message = "backup job reported Failed"
		if b.Status.Message == "" {
			b.Status.Message = "backup job reported Failed"
			dirty = true
		}
	default:
		cond.Status = metav1.ConditionFalse
		cond.Reason = string(phase)
	}
	newConds := upsertCondition(b.Status.Conditions, cond)
	if !sameConditions(b.Status.Conditions, newConds) {
		b.Status.Conditions = newConds
		dirty = true
	}

	if dirty {
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Stamp the owning GameServer with this backup's completion time so the
	// dashboard can show "last backup" without scanning the Backup list. Only
	// once the snapshot is confirmed restorable — a Succeeded Backup whose id
	// never lands flips to Failed, and must not have advanced the timestamp.
	if phase == gameplanev1alpha1.BackupPhaseSucceeded && !snapshotMissing {
		if err := r.recordServerBackupTime(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}

	if phase == gameplanev1alpha1.BackupPhaseSucceeded || phase == gameplanev1alpha1.BackupPhaseFailed {
		res, err := r.runUnquiesce(ctx, b)
		if err != nil {
			return res, err
		}
		// A Succeeded Backup whose snapshot id we still couldn't read isn't
		// restorable yet — keep requeuing to retry the scrape (runUnquiesce
		// asks for no requeue on success).
		if snapshotMissing && res.RequeueAfter == 0 {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		return res, nil
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// recordServerBackupTime stamps the owning GameServer's
// status.lastBackupTime with this backup's completion time. Best-effort: a
// missing GameServer (deleted, or a backup without a resolvable server) is
// ignored, and the timestamp only ever advances forward so an out-of-order
// reconcile can't rewind it. Patches (not updates) status so it doesn't race
// the agent's concurrently written status.agent — same rationale as the
// GameServer reconciler's setPhase.
func (r *BackupReconciler) recordServerBackupTime(ctx context.Context, b *gameplanev1alpha1.Backup) error {
	if b.Status.CompletionTime == nil || b.Spec.ServerRef.Name == "" {
		return nil
	}
	gs := &gameplanev1alpha1.GameServer{}
	key := types.NamespacedName{Namespace: b.Namespace, Name: b.Spec.ServerRef.Name}
	if err := r.Get(ctx, key, gs); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get gameserver %s for last-backup time: %w", key.Name, err)
	}
	if gs.Status.LastBackupTime != nil &&
		!b.Status.CompletionTime.After(gs.Status.LastBackupTime.Time) {
		return nil
	}
	base := gs.DeepCopy()
	gs.Status.LastBackupTime = b.Status.CompletionTime.DeepCopy()
	if err := r.Status().Patch(ctx, gs, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch gameserver %s last-backup time: %w", key.Name, err)
	}
	return nil
}

// runUnquiesce performs the terminal-phase unquiesce + Unquiesced condition
// bookkeeping shared by the restic and volume-snapshot strategies. A failed
// unquiesce can leave the game world frozen with auto-save off, so it is
// surfaced as an Unquiesced=False condition (visible in `kubectl describe`
// and the dashboard) and retried — the unquiesced-at annotation makes the
// retry idempotent and stops the loop once it lands.
func (r *BackupReconciler) runUnquiesce(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	busy, err := r.backupCleanupWorkersLive(ctx, b, false)
	if err != nil {
		return ctrl.Result{}, err
	}
	if busy {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if err := r.maybeUnquiesce(ctx, b); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "unquiesce failed; will retry", "backup", b.Name)
		if cerr := r.setUnquiescedCondition(ctx, b, false, err.Error()); cerr != nil {
			return ctrl.Result{}, cerr
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	// When an unquiesce was actually performed, record success so the
	// "world might be frozen" signal clears.
	if _, ok := b.Annotations[annoUnquiescedAt]; ok {
		if cerr := r.setUnquiescedCondition(ctx, b, true, ""); cerr != nil {
			return ctrl.Result{}, cerr
		}
	}
	return ctrl.Result{}, r.releaseBackupTarget(ctx, b)
}

// setUnquiescedCondition upserts the Unquiesced condition on the Backup.
// ok=false (Reason=Failed) flags that the post-backup unquiesce hasn't
// completed — the world may still be frozen with auto-save off.
func (r *BackupReconciler) setUnquiescedCondition(
	ctx context.Context, b *gameplanev1alpha1.Backup, ok bool, msg string,
) error {
	cond := metav1.Condition{Type: "Unquiesced", ObservedGeneration: b.Generation}
	if ok {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Unquiesced"
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "Failed"
		cond.Message = msg
	}
	newConds := upsertCondition(b.Status.Conditions, cond)
	if sameConditions(b.Status.Conditions, newConds) {
		return nil
	}
	b.Status.Conditions = newConds
	return r.Status().Update(ctx, b)
}

// finalizeUnquiesce is stage 1 of finalizeDelete (see backup_forget.go). It releases a
// quiesced world (F-048) before letting the finalizer clear and the
// apiserver garbage-collect the object — without it, deleting a Backup
// while quiesce-attempted=true drops the unquiesce entirely and the game
// stays with auto-save off. Retries (via requeue) until the agent is
// reachable, mirroring runUnquiesce's terminal-phase behavior.
//
// The retry is not unconditional, though: an auto BackupSchedule's owner
// chain (GameServer -> BackupSchedule -> Backup) means deleting the
// GameServer garbage-collects this Backup right along with it, so the
// unquiesce target can already be gone by the time this runs. Retrying
// forever in that case would wedge the Backup's deletion permanently, since
// nothing will ever answer. unquiesceTargetGone checks for that (and for the
// pod alone being gone, e.g. the StatefulSet pod was deleted independently)
// and releases the finalizer immediately when there's nothing left to
// unquiesce — a freshly (re)started pod comes up with auto-save on. When the
// target is still around but the agent stays unreachable, the retry is
// bounded by maxUnquiesceFinalizeRetry so a persistently broken agent can't
// block deletion forever either; past that window a Warning event is
// recorded and the finalizer releases anyway.
func (r *BackupReconciler) finalizeUnquiesce(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(b, gameplanev1alpha1.BackupFinalizer) {
		return ctrl.Result{}, nil
	}
	busy, err := r.backupCleanupWorkersLive(ctx, b, true)
	if err != nil {
		return ctrl.Result{}, err
	}
	if busy {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	confirmed := true
	if unquiesceErr := r.maybeUnquiesce(ctx, b); unquiesceErr != nil {
		confirmed = false
		ctrl.LoggerFrom(ctx).Error(unquiesceErr, "unquiesce failed during backup delete; checking target",
			"backup", b.Name)

		gone, err := r.unquiesceTargetGone(ctx, b)
		if err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "unable to check unquiesce target; will retry", "backup", b.Name)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		expired := false
		if !gone {
			expired, err = r.unquiesceRetryExpired(ctx, b)
			if err != nil {
				return ctrl.Result{}, err
			}
			if !expired {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			r.recordEvent(b, corev1.EventTypeWarning, "UnquiesceAbandoned", fmt.Sprintf(
				"giving up on unquiesce after %s of retries; the world may still have "+
					"auto-save off if its pod is still running: %s", maxUnquiesceFinalizeRetry, unquiesceErr))
		}

		reason := "unquiesce target no longer exists"
		if expired {
			reason = "unquiesce retry window exceeded"
		}
		ctrl.LoggerFrom(ctx).Info("releasing backup finalizer without a confirmed unquiesce",
			"backup", b.Name, "reason", reason)
	}
	// On abandoned cleanup leave the durable owner behind. A subsequent
	// contender must recover saving before starting its own protected copy.
	if confirmed {
		if err := r.releaseBackupTarget(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(b, gameplanev1alpha1.BackupFinalizer)
	if err := r.Update(ctx, b); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// unquiesceTargetGone reports whether there is nothing left to unquiesce:
// the target GameServer no longer exists or is itself being deleted, or its
// pod no longer exists. A GameServer can be gone-together-with-its-Backup
// under an auto BackupSchedule's owner chain, and a pod alone can be gone if
// it was deleted independently of the GameServer; either way a fresh pod
// starts with auto-save on, so retrying the unquiesce would never succeed
// and would only block this Backup's deletion.
func (r *BackupReconciler) unquiesceTargetGone(ctx context.Context, b *gameplanev1alpha1.Backup) (bool, error) {
	targetName := b.Spec.ServerRef.Name
	var targetUID types.UID
	if value := b.Annotations[backupTargetAnnotation]; value != "" {
		identity, err := parseBackupIdentity(value)
		if err != nil {
			return false, err
		}
		targetName, targetUID = identity.Name, identity.UID
	}
	if targetName == "" {
		return true, nil
	}
	gs := &gameplanev1alpha1.GameServer{}
	key := types.NamespacedName{Namespace: b.Namespace, Name: targetName}
	switch err := r.Get(ctx, key, gs); {
	case apierrors.IsNotFound(err):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("get gameserver %s to check unquiesce target: %w", key.Name, err)
	case gs.DeletionTimestamp != nil:
		return true, nil
	}
	if targetUID != "" && gs.UID != targetUID {
		return true, nil
	}

	var pod corev1.Pod
	podKey := types.NamespacedName{Namespace: b.Namespace, Name: gs.Name + "-0"}
	switch err := r.Get(ctx, podKey, &pod); {
	case apierrors.IsNotFound(err):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("get pod %s to check unquiesce target: %w", podKey.Name, err)
	}
	return false, nil
}

// unquiesceRetryExpired records (on first call) when finalizeDelete's
// unquiesce retry against a still-existing target first failed, and reports
// whether maxUnquiesceFinalizeRetry has since elapsed. The timestamp lives
// in an annotation so it survives across reconciles and operator restarts.
func (r *BackupReconciler) unquiesceRetryExpired(ctx context.Context, b *gameplanev1alpha1.Backup) (bool, error) {
	since, ok := b.Annotations[annoUnquiesceRetryFirstFailedAt]
	if !ok {
		patchBackupAnnotations(b, map[string]string{
			annoUnquiesceRetryFirstFailedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err := r.Update(ctx, b); err != nil {
			return false, fmt.Errorf("record unquiesce retry start: %w", err)
		}
		return false, nil
	}
	firstFailedAt, parseErr := time.Parse(time.RFC3339, since)
	if parseErr == nil {
		return time.Since(firstFailedAt) >= maxUnquiesceFinalizeRetry, nil
	}
	// A malformed annotation shouldn't wedge deletion forever.
	return true, nil
}

// recordEvent emits a Kubernetes event on b when EventRecorder is
// configured; it is a no-op otherwise (e.g. in unit tests that don't wire
// one up).
func (r *BackupReconciler) recordEvent(b *gameplanev1alpha1.Backup, eventType, reason, message string) {
	if r.EventRecorder == nil {
		return
	}
	// events.EventRecorder's Eventf has no direct message-only form: reason
	// doubles as the action (both are short UpperCamelCase per its contract)
	// and message is passed through verbatim as the note.
	r.EventRecorder.Eventf(b, nil, eventType, reason, reason, "%s", message)
}

// missingRepoSecretKeys returns the restic Secret keys the backup/restore
// Jobs require (repo, password) that are absent from s.
func missingRepoSecretKeys(s *corev1.Secret) []string {
	var missing []string
	for _, k := range []string{"repo", "password"} {
		if len(s.Data[k]) > 0 {
			continue
		}
		if _, ok := s.StringData[k]; ok {
			continue
		}
		missing = append(missing, k)
	}
	return missing
}

// maybeUnquiesce reverses the earlier quiesce. It is only called once
// the Backup is in a terminal phase, and it skips when:
//   - quiesce was never attempted (no annotation),
//   - quiesce was attempted but the game didn't support it,
//   - unquiesce already succeeded on a prior pass.
func (r *BackupReconciler) maybeUnquiesce(ctx context.Context, b *gameplanev1alpha1.Backup) error {
	state := b.Annotations[annoQuiesceAttempted]
	if state == "" || state == "unsupported" {
		return nil
	}
	if _, ok := b.Annotations[annoUnquiescedAt]; ok {
		return nil
	}
	targetName := b.Spec.ServerRef.Name
	if b.Annotations[backupTargetAnnotation] != "" {
		gs, err := r.ownedBackupTarget(ctx, b)
		if err != nil {
			return err
		}
		if gs == nil {
			return nil // Replaced target or another Backup owns save-on.
		}
		targetName = gs.Name
	} else if b.Spec.ServerRef.Name != "" {
		var gs gameplanev1alpha1.GameServer
		err := r.backupAPIReader().Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: b.Spec.ServerRef.Name}, &gs)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		if gs.Annotations[backupGuardAnnotation] != "" {
			return fmt.Errorf("legacy quiesce cleanup is blocked by a protected backup")
		}
	}
	if r.AgentClient == nil {
		return fmt.Errorf("cannot release quiesced world without an agent client")
	}
	if err := r.AgentClient.Unquiesce(ctx, b.Namespace, targetName); err != nil {
		return err
	}
	patchBackupAnnotations(b, map[string]string{
		annoUnquiescedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return r.Update(ctx, b)
}

// readResticSummary fetches the restic container's logs and parses the
// trailing JSON summary line. Caller must be holding a Backup whose
// matching Job is in Succeeded.
func (r *BackupReconciler) readResticSummary(ctx context.Context, b *gameplanev1alpha1.Backup) (*ResticSummary, error) {
	reader := r.LogReader
	if reader == nil {
		if r.Clientset == nil {
			return nil, errors.New("no log reader configured")
		}
		reader = &clientsetLogReader{cs: r.Clientset}
	}
	rc, err := reader.BackupLogs(ctx, b.Namespace, b.Name)
	if err != nil {
		return nil, fmt.Errorf("read restic logs: %w", err)
	}
	defer func() { _ = rc.Close() }()
	return ParseResticSummary(rc)
}

// fail writes a terminal Failed phase + message + condition to the
// Backup. Mirrors RestoreReconciler.fail so the two controllers
// produce identical user-visible signals.
func (r *BackupReconciler) fail(ctx context.Context, b *gameplanev1alpha1.Backup, msg string) (ctrl.Result, error) {
	now := metav1.Now()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
	b.Status.ObservedGeneration = b.Generation
	if b.Status.Message != msg {
		b.Status.Message = msg
	}
	if b.Status.CompletionTime == nil {
		b.Status.CompletionTime = &now
	}
	b.Status.Conditions = upsertCondition(b.Status.Conditions, metav1.Condition{
		Type:               "Completed",
		Status:             metav1.ConditionFalse,
		Reason:             "Failed",
		Message:            msg,
		ObservedGeneration: b.Generation,
	})
	if err := r.Status().Update(ctx, b); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}
	b := ctrl.NewControllerManagedBy(mgr).
		For(&gameplanev1alpha1.Backup{}).
		Owns(&batchv1.Job{})
	// Only watch VolumeSnapshots when the CSI snapshot CRDs are installed.
	// Without them the informer can never sync, so the manager would exit on
	// startup ("failed to wait for backup caches to sync ... VolumeSnapshot")
	// and crash-loop, taking down ALL reconciliation. Restic-strategy backups
	// don't need snapshots, so the operator stays fully functional on clusters
	// without external-snapshotter (the common single-node homelab case); the
	// volume-snapshot strategy surfaces a clear error only when actually used.
	if volumeSnapshotsAvailable(mgr.GetRESTMapper()) {
		b = b.Owns(&snapshotv1.VolumeSnapshot{})
	} else {
		mgr.GetLogger().Info("VolumeSnapshot CRD not present; skipping its watch " +
			"(volume-snapshot backups unavailable; restic backups unaffected)")
	}
	return b.Complete(r)
}

// volumeSnapshotsAvailable reports whether the cluster has the CSI
// VolumeSnapshot CRD registered, via the manager's RESTMapper.
func volumeSnapshotsAvailable(rm meta.RESTMapper) bool {
	gk := snapshotv1.SchemeGroupVersion.WithKind("VolumeSnapshot").GroupKind()
	_, err := rm.RESTMapping(gk, snapshotv1.SchemeGroupVersion.Version)
	return err == nil
}

// buildBackupPodSpec produces the pod for a single restic backup.
//
// Layout:
//   - initContainer "restic-init": idempotently prepares the repo so a
//     first-ever Backup against an empty bucket succeeds. `restic cat
//     config` returns 0 if the repo exists; otherwise we run `restic
//     init` to create it.
//   - container "restic": runs the actual `restic backup --json` with
//     spec.tags appended to the default "gameplane" tag.
//
// Both containers share the same env (repo + password from the user's
// Secret) and a tmpfs-backed cache to keep the rootfs read-only.
func (r *BackupReconciler) buildBackupPodSpec(b *gameplanev1alpha1.Backup, tmpl *gameplanev1alpha1.GameTemplate) corev1.PodSpec {
	env := resticEnv(b.Spec.RepoRef.Name)
	containerSec := resticContainerSecurityContext()

	// --retry-lock makes the backup wait (up to 10m) for the repository lock
	// instead of failing while a snapshot forget/prune, started by deleting
	// another Backup, holds it exclusively.
	args := []string{"backup", "/data", "--json", "--retry-lock", "10m", "--tag", "gameplane"}
	for _, t := range b.Spec.Tags {
		args = append(args, "--tag", t)
	}

	return corev1.PodSpec{
		SecurityContext: buildBackupPodSecurityContext(tmpl),
		InitContainers: []corev1.Container{{
			Name:    "restic-init",
			Image:   resticImageOrDefault(r.ResticImage),
			Command: []string{"/bin/sh", "-c"},
			// `cat config` is the canonical "does the repo exist" probe;
			// `init` is idempotent against an empty target.
			Args: []string{"restic cat config --no-lock >/dev/null 2>&1 || restic init"},
			Env:  env,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "cache", MountPath: "/tmp"},
			},
			SecurityContext: containerSec,
		}},
		Containers: []corev1.Container{{
			Name:  "restic",
			Image: resticImageOrDefault(r.ResticImage),
			Args:  args,
			Env:   env,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "data", MountPath: "/data", ReadOnly: true},
				{Name: "cache", MountPath: "/tmp"},
			},
			SecurityContext: containerSec,
		}},
		Volumes: []corev1.Volume{
			{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: b.Spec.ServerRef.Name + "-data",
						ReadOnly:  true,
					},
				},
			},
			{
				Name:         "cache",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
		},
	}
}

// resticEnv is the container environment shared by every Job that talks to a
// backup repository (the backup Job and the snapshot-forget Job): the
// repo URL and password from the user's Secret, keys "repo" and "password", plus
// a cache directory under the writable /tmp mount so the root filesystem can
// stay read-only.
func resticEnv(repoSecretName string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "RESTIC_REPOSITORY", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: repoSecretName},
				Key:                  "repo",
			},
		}},
		{Name: "RESTIC_PASSWORD", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: repoSecretName},
				Key:                  "password",
			},
		}},
		{Name: "XDG_CACHE_HOME", Value: "/tmp/restic-cache"},
	}
}

// resticContainerSecurityContext is the locked-down container security context
// for restic Jobs: non-root, read-only root filesystem, no privilege
// escalation, all capabilities dropped.
func resticContainerSecurityContext() *corev1.SecurityContext {
	nonRoot := true
	roRootFS := true
	noPrivEsc := false
	uid := int64(65532)
	return &corev1.SecurityContext{
		RunAsNonRoot:             &nonRoot,
		RunAsUser:                &uid,
		ReadOnlyRootFilesystem:   &roRootFS,
		AllowPrivilegeEscalation: &noPrivEsc,
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

// buildBackupPodSecurityContext returns the pod-level SecurityContext for backup/restore jobs.
// It includes the template's FSGroup (if set) along with standard settings for the backup job.
func buildBackupPodSecurityContext(tmpl *gameplanev1alpha1.GameTemplate) *corev1.PodSecurityContext {
	nonRoot := true
	uid := int64(65532)
	fsGroup := uid
	if tmpl.Spec.Security != nil && tmpl.Spec.Security.FSGroup != nil {
		fsGroup = *tmpl.Spec.Security.FSGroup
	}
	return &corev1.PodSecurityContext{
		RunAsNonRoot:   &nonRoot,
		RunAsUser:      &uid,
		RunAsGroup:     &uid,
		FSGroup:        &fsGroup,
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

// --- production log reader -----------------------------------------------

// clientsetLogReader fetches the logs of the most-recently-completed
// pod for a Backup's Job. The Job controller stamps every pod with a
// `job-name=<jobName>` label, which is what we filter on.
type clientsetLogReader struct {
	cs kubernetes.Interface
}

func (l *clientsetLogReader) BackupLogs(ctx context.Context, namespace, backupName string) (io.ReadCloser, error) {
	pods, err := l.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + backupName,
	})
	if err != nil {
		return nil, err
	}
	var pick *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase != corev1.PodSucceeded {
			continue
		}
		if pick == nil || p.CreationTimestamp.After(pick.CreationTimestamp.Time) {
			pick = p
		}
	}
	if pick == nil {
		return nil, errors.New("no succeeded pod yet for backup job")
	}
	req := l.cs.CoreV1().Pods(namespace).GetLogs(pick.Name, &corev1.PodLogOptions{Container: "restic"})
	return req.Stream(ctx)
}

// Compile-time guards: BackupReconciler integrates with the constants
// declared above; tests rely on them not drifting silently.
var (
	_ = types.NamespacedName{}
	_ = apierrors.IsNotFound
)
