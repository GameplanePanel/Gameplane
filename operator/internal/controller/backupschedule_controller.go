package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/robfig/cron/v3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// conditionRetentionTrimmed reports whether the most recent retention
// pass pruned old backups successfully. Status False (reason TrimFailed)
// means old backups may be accumulating because a List/Delete failed —
// the dashboard surfaces this so an operator can intervene, rather than
// the failure being swallowed into the controller log only.
const conditionRetentionTrimmed = "RetentionTrimmed"

// conditionScheduleValid reports whether Spec.Schedule parsed as a cron
// expression. Status False (reason CronParseError) means the schedule was
// admitted by the CRD pattern but the controller can't interpret it, so it
// never fires — surfaced as a condition instead of only a log line.
const conditionScheduleValid = "ScheduleValid"

// BackupScheduleReconciler computes the next firing time from
// Spec.Schedule and creates Backup objects when due. Retention trimming
// is enforced by deleting oldest Backups that fall outside keep-* rules.
type BackupScheduleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=gameplane.local,resources=backupschedules,verbs=get;list;watch
// +kubebuilder:rbac:groups=gameplane.local,resources=backups,verbs=get;list;watch

func (r *BackupScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var sched gameplanev1alpha1.BackupSchedule
	if err := r.Get(ctx, req.NamespacedName, &sched); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Snapshot the observed status so we can write back exactly once, and
	// only when something actually changed (avoids reconcile churn).
	before := sched.Status.DeepCopy()

	// The controller watches BackupSchedules only; it finds a schedule's
	// Backups by the gameplane.local/backup-schedule label on each reconcile
	// (scheduled requeues).
	if err := r.updateLastSuccessfulTime(ctx, &sched); err != nil {
		return ctrl.Result{}, err
	}

	if sched.Spec.Suspend {
		sched.Status.NextScheduleTime = nil
		return ctrl.Result{}, r.persistStatus(ctx, before, &sched)
	}

	// Five-field cron by default; the CRD pattern also admits an optional
	// sixth field for the seconds-prefix dialect, so enable seconds when six
	// fields are present. Otherwise a six-token schedule is admitted by the
	// CRD but rejected here, and the schedule silently never fires.
	fields := cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow
	if len(strings.Fields(sched.Spec.Schedule)) == 6 {
		fields |= cron.Second
	}
	expr, err := cron.NewParser(fields).Parse(sched.Spec.Schedule)
	if err != nil {
		logger.Error(err, "invalid cron expression", "schedule", sched.Spec.Schedule)
		sched.Status.Conditions = upsertCondition(sched.Status.Conditions, metav1.Condition{
			Type:               conditionScheduleValid,
			Status:             metav1.ConditionFalse,
			Reason:             "CronParseError",
			Message:            err.Error(),
			ObservedGeneration: sched.Generation,
		})
		return ctrl.Result{}, r.persistStatus(ctx, before, &sched)
	}
	sched.Status.Conditions = upsertCondition(sched.Status.Conditions, metav1.Condition{
		Type:               conditionScheduleValid,
		Status:             metav1.ConditionTrue,
		Reason:             "CronValid",
		Message:            "schedule is a valid cron expression",
		ObservedGeneration: sched.Generation,
	})

	now := time.Now()
	// A schedule that has never fired counts missed runs from its creation, not
	// from an arbitrary window in the past, so a brand-new schedule waits for
	// its first real cron tick instead of firing on the first reconcile.
	prev := sched.CreationTimestamp.Time
	if sched.Status.LastScheduleTime != nil {
		prev = sched.Status.LastScheduleTime.Time
	}
	next := expr.Next(prev)

	// Track the in-flight (non-terminal) Backups this schedule owns: they
	// drive the concurrency decision and are surfaced in status.Active.
	active, err := r.inFlightBackups(ctx, &sched)
	if err != nil {
		return ctrl.Result{}, err
	}
	sched.Status.Active = activeBackupRefs(active)

	if !next.After(now) {
		fire, err := r.shouldFire(ctx, &sched, now, next, active)
		if err != nil {
			return ctrl.Result{}, err
		}
		if fire {
			if err := r.fire(ctx, &sched, now); err != nil {
				return ctrl.Result{}, err
			}
		}
		sched.Status.LastScheduleTime = &metav1.Time{Time: now}
		next = expr.Next(now)
	}

	// Retention trimming is best-effort: a transient List/Delete failure
	// must not wedge scheduling, so we record the outcome as a condition
	// instead of returning the error and retrying the whole reconcile.
	if err := r.trimBackups(ctx, &sched); err != nil {
		logger.Error(err, "retention trim")
		sched.Status.Conditions = upsertCondition(sched.Status.Conditions, metav1.Condition{
			Type:               conditionRetentionTrimmed,
			Status:             metav1.ConditionFalse,
			Reason:             "TrimFailed",
			Message:            err.Error(),
			ObservedGeneration: sched.Generation,
		})
	} else if sched.Spec.Retention != nil {
		sched.Status.Conditions = upsertCondition(sched.Status.Conditions, metav1.Condition{
			Type:               conditionRetentionTrimmed,
			Status:             metav1.ConditionTrue,
			Reason:             "TrimSucceeded",
			Message:            "retention policy applied",
			ObservedGeneration: sched.Generation,
		})
	}

	sched.Status.NextScheduleTime = &metav1.Time{Time: next}

	if err := r.persistStatus(ctx, before, &sched); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter := time.Until(next)
	if requeueAfter < time.Second {
		requeueAfter = time.Second
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *BackupScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gameplanev1alpha1.BackupSchedule{}).
		Complete(r)
}

func (r *BackupScheduleReconciler) fire(
	ctx context.Context, sched *gameplanev1alpha1.BackupSchedule, at time.Time,
) error {
	b := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", sched.Name, at.UTC().Format("20060102-150405")),
			Namespace: sched.Namespace,
			Labels:    map[string]string{"gameplane.local/backup-schedule": sched.Name},
		},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: sched.Spec.ServerRef,
			RepoRef:   sched.Spec.RepoRef,
			Strategy:  sched.Spec.Strategy,
			Quiesce:   sched.Spec.Quiesce,
		},
	}
	return r.Create(ctx, b)
}

// inFlightBackups returns the non-terminal (Pending/Running/unstarted) Backups
// this schedule owns, identified by the schedule label fire() stamps.
func (r *BackupScheduleReconciler) inFlightBackups(
	ctx context.Context, sched *gameplanev1alpha1.BackupSchedule,
) ([]gameplanev1alpha1.Backup, error) {
	var list gameplanev1alpha1.BackupList
	if err := r.List(ctx, &list,
		client.InNamespace(sched.Namespace),
		client.MatchingLabels{"gameplane.local/backup-schedule": sched.Name}); err != nil {
		return nil, err
	}
	active := make([]gameplanev1alpha1.Backup, 0, len(list.Items))
	for i := range list.Items {
		switch list.Items[i].Status.Phase {
		case gameplanev1alpha1.BackupPhaseSucceeded, gameplanev1alpha1.BackupPhaseFailed:
			// Terminal — not in flight.
		default:
			active = append(active, list.Items[i])
		}
	}
	return active, nil
}

// updateLastSuccessfulTime advances Status.LastSuccessfulTime to the
// completion time of the newest Succeeded Backup this schedule owns.
// It only ever moves forward: retention trimming (or manual cleanup) can
// delete the Backup that produced the current high-water mark, and
// re-deriving from whichever succeeded Backups remain must never rewind
// the recorded time.
func (r *BackupScheduleReconciler) updateLastSuccessfulTime(
	ctx context.Context, sched *gameplanev1alpha1.BackupSchedule,
) error {
	var list gameplanev1alpha1.BackupList
	if err := r.List(ctx, &list,
		client.InNamespace(sched.Namespace),
		client.MatchingLabels{"gameplane.local/backup-schedule": sched.Name}); err != nil {
		return err
	}
	var newest *metav1.Time
	for i := range list.Items {
		b := &list.Items[i]
		if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || b.Status.CompletionTime == nil {
			continue
		}
		if newest == nil || b.Status.CompletionTime.After(newest.Time) {
			newest = b.Status.CompletionTime
		}
	}
	if newest != nil && (sched.Status.LastSuccessfulTime == nil || newest.After(sched.Status.LastSuccessfulTime.Time)) {
		sched.Status.LastSuccessfulTime = newest
	}
	return nil
}

// shouldFire decides whether a due schedule creates a Backup now, applying
// startingDeadlineSeconds (occurrences later than the deadline are skipped) and
// concurrencyPolicy: Forbid skips while a previous backup is still in flight,
// Replace deletes the in-flight ones first, Allow always fires. An empty policy
// is treated as the CRD default (Forbid).
func (r *BackupScheduleReconciler) shouldFire(
	ctx context.Context, sched *gameplanev1alpha1.BackupSchedule,
	now, scheduled time.Time, active []gameplanev1alpha1.Backup,
) (bool, error) {
	logger := log.FromContext(ctx)
	if d := sched.Spec.StartingDeadlineSeconds; d != nil &&
		now.Sub(scheduled) > time.Duration(*d)*time.Second {
		logger.Info("skipping backup past starting deadline",
			"scheduled", scheduled, "deadlineSeconds", *d)
		return false, nil
	}
	switch sched.Spec.ConcurrencyPolicy {
	case "Allow":
		// No constraint.
	case "Replace":
		for i := range active {
			if err := r.Delete(ctx, &active[i]); err != nil && !apierrors.IsNotFound(err) {
				return false, fmt.Errorf("replace in-flight backup %s: %w", active[i].Name, err)
			}
		}
	default: // "" (CRD default) and "Forbid"
		if len(active) > 0 {
			logger.Info("skipping backup: a previous one is still in flight",
				"policy", "Forbid", "active", len(active))
			return false, nil
		}
	}
	return true, nil
}

// activeBackupRefs builds the status.Active object references from the in-flight
// Backups, sorted by name so unchanged input produces no status churn.
func activeBackupRefs(active []gameplanev1alpha1.Backup) []corev1.ObjectReference {
	if len(active) == 0 {
		return nil
	}
	sorted := append([]gameplanev1alpha1.Backup(nil), active...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	refs := make([]corev1.ObjectReference, 0, len(sorted))
	for i := range sorted {
		refs = append(refs, corev1.ObjectReference{
			APIVersion: gameplanev1alpha1.GroupVersion.String(),
			Kind:       "Backup",
			Namespace:  sorted[i].Namespace,
			Name:       sorted[i].Name,
			UID:        sorted[i].UID,
		})
	}
	return refs
}

// persistStatus writes the schedule's status subresource only when it
// differs from the snapshot taken at the start of Reconcile. Centralising
// the write means a condition flip is persisted even on reconciles where
// NextScheduleTime is unchanged (e.g. a dormant or suspended schedule).
func (r *BackupScheduleReconciler) persistStatus(
	ctx context.Context,
	before *gameplanev1alpha1.BackupScheduleStatus,
	sched *gameplanev1alpha1.BackupSchedule,
) error {
	if scheduleStatusEqual(before, &sched.Status) {
		return nil
	}
	return r.Status().Update(ctx, sched)
}

// scheduleStatusEqual reports whether the two statuses are equivalent for
// the fields this controller manages.
func scheduleStatusEqual(a, b *gameplanev1alpha1.BackupScheduleStatus) bool {
	return metav1TimeEqual(a.LastScheduleTime, b.LastScheduleTime) &&
		metav1TimeEqual(a.LastSuccessfulTime, b.LastSuccessfulTime) &&
		metav1TimeEqual(a.NextScheduleTime, b.NextScheduleTime) &&
		sameConditions(a.Conditions, b.Conditions) &&
		activeRefsEqual(a.Active, b.Active)
}

// activeRefsEqual compares two status.Active slices. Both are produced by
// activeBackupRefs (sorted by name), so a positional compare is sufficient.
func activeRefsEqual(a, b []corev1.ObjectReference) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].UID != b[i].UID {
			return false
		}
	}
	return true
}

func metav1TimeEqual(a, b *metav1.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Time.Equal(b.Time)
	}
}
