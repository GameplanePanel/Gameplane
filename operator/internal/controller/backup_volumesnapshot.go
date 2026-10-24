package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// reconcileVolumeSnapshot drives a Backup whose Strategy is
// "volume-snapshot". Instead of running restic it asks the CSI layer for a
// VolumeSnapshot of the data PVC. It mirrors the restic flow's quiesce
// bracketing and status/condition shape so both strategies look identical
// to users: quiesce → create VolumeSnapshot → wait for readyToUse → record
// the snapshot identity → unquiesce.
//
// gs is the (already-resolved) source GameServer; its data PVC is the
// snapshot source.
func (r *BackupReconciler) reconcileVolumeSnapshot(
	ctx context.Context, b *gameplanev1alpha1.Backup, gs *gameplanev1alpha1.GameServer,
) (ctrl.Result, error) {
	if b.Annotations[backupSnapshotAbortAnnotation] == "true" {
		return r.abortVolumeSnapshot(ctx, b, "volume snapshot capture was aborted")
	}
	// An uncached read verifies that the snapshot API is served before
	// disabling autosave. Missing CRDs (including NoMatch) and authorization
	// errors fail safely, including cleanup of previously quiesced Backups.
	var existing snapshotv1.VolumeSnapshot
	if err := r.backupAPIReader().Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: b.Name}, &existing); err != nil && !apierrors.IsNotFound(err) {
		return r.abortVolumeSnapshot(ctx, b, fmt.Sprintf("volume snapshot API unavailable: %v", err))
	}
	if existing.Name != "" {
		if err := r.rememberBackupSnapshot(ctx, b, &existing); err != nil {
			return r.abortVolumeSnapshot(ctx, b, err.Error())
		}
	}
	if !legacyBackupOwesUnquiesce(b) {
		if res, err := r.prepareBackupWorkload(ctx, b); err != nil || res.RequeueAfter > 0 {
			return res, err
		}
	}
	deadline, err := r.backupDeadline(ctx, b, backupCSIDeadlineAnnotation, backupTimeout(r.CSICaptureTimeout, DefaultBackupCSICaptureTimeout))
	if err != nil {
		return r.abortVolumeSnapshot(ctx, b, err.Error())
	}
	if !time.Now().Before(deadline) {
		return r.abortVolumeSnapshot(ctx, b, "CSI capture deadline exceeded; incomplete snapshot invalidated")
	}
	if legacyBackupOwesUnquiesce(b) && metav1.IsControlledBy(&existing, b) {
		return r.observeVolumeSnapshot(ctx, b, &existing)
	}
	// The durable capture budget starts before any save-off RPC. Quiesce
	// confirmation describes the agent action, not universal game consistency.
	if res, err := r.prepareBackupQuiesce(ctx, b); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if !time.Now().Before(deadline) {
		return r.abortVolumeSnapshot(ctx, b, "CSI capture deadline exceeded during quiesce")
	}

	pvcName := gs.Name + "-data"
	vs := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, vs, func() error {
		if !vs.CreationTimestamp.IsZero() && !metav1.IsControlledBy(vs, b) {
			return fmt.Errorf("VolumeSnapshot is not owned by this Backup")
		}
		// Source + class are immutable on a VolumeSnapshot, so only set
		// them on first creation; SetControllerReference is idempotent.
		if vs.CreationTimestamp.IsZero() {
			vs.Spec = snapshotv1.VolumeSnapshotSpec{
				Source: snapshotv1.VolumeSnapshotSource{
					PersistentVolumeClaimName: &pvcName,
				},
				VolumeSnapshotClassName: b.Spec.VolumeSnapshotClassName,
			}
		}
		return controllerutil.SetControllerReference(b, vs, r.Scheme)
	}); err != nil {
		// Do not retry creation indefinitely with saving disabled. Even an
		// ambiguous transport failure is surfaced as Failed; cleanup retries
		// independently until save-on is confirmed.
		return r.abortVolumeSnapshot(ctx, b, fmt.Sprintf("volume snapshot creation failed: %v", err))
	}
	if err := r.rememberBackupSnapshot(ctx, b, vs); err != nil {
		return r.abortVolumeSnapshot(ctx, b, err.Error())
	}

	return r.observeVolumeSnapshot(ctx, b, vs)
}

func (r *BackupReconciler) observeVolumeSnapshot(ctx context.Context, b *gameplanev1alpha1.Backup, vs *snapshotv1.VolumeSnapshot) (ctrl.Result, error) {
	if b.Annotations[backupSnapshotAbortAnnotation] == "true" {
		return r.abortVolumeSnapshot(ctx, b, "volume snapshot capture was aborted")
	}
	if value := b.Annotations[backupCSIDeadlineAnnotation]; value != "" {
		deadline, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || !time.Now().Before(deadline) {
			return r.abortVolumeSnapshot(ctx, b, "CSI capture deadline exceeded; late readiness is invalid")
		}
	}
	switch {
	case vs.Status != nil && vs.Status.Error != nil && vs.Status.Error.Message != nil:
		// The CSI driver rejected the snapshot. Surface it and unquiesce so
		// the game doesn't stay frozen.
		return r.abortVolumeSnapshot(ctx, b, fmt.Sprintf("volume snapshot failed: %s", *vs.Status.Error.Message))
	case vs.Status != nil && vs.Status.ReadyToUse != nil && *vs.Status.ReadyToUse:
		return r.completeVolumeSnapshot(ctx, b, vs)
	default:
		// Still provisioning — mark Running once, then poll.
		return r.markVolumeSnapshotRunning(ctx, b)
	}
}

// An API/creation failure must restore saving even if an ambiguous request
// created a snapshot. Persist that abort decision before terminal cleanup so
// worker-drain checks cannot trap this failed/nonrestorable Backup forever.
func (r *BackupReconciler) abortVolumeSnapshot(ctx context.Context, b *gameplanev1alpha1.Backup, msg string) (ctrl.Result, error) {
	if err := r.recordSnapshotAbort(ctx, b, msg); err != nil {
		return ctrl.Result{}, err
	}
	unquiesce, unquiesceErr := r.runUnquiesce(ctx, b)
	cleanup, cleanupErr := r.cleanupAbortedSnapshot(ctx, b)
	if unquiesce.RequeueAfter > 0 && (cleanup.RequeueAfter == 0 || unquiesce.RequeueAfter < cleanup.RequeueAfter) {
		cleanup = unquiesce
	}
	return cleanup, errors.Join(unquiesceErr, cleanupErr)
}

// markVolumeSnapshotRunning flips the Backup to Running on first observation
// and requeues to poll the VolumeSnapshot's readyToUse (the Owns() watch
// also wakes us on status transitions).
func (r *BackupReconciler) markVolumeSnapshotRunning(
	ctx context.Context, b *gameplanev1alpha1.Backup,
) (ctrl.Result, error) {
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseRunning {
		b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
		b.Status.ObservedGeneration = b.Generation
		if b.Status.StartTime == nil {
			now := metav1.Now()
			b.Status.StartTime = &now
		}
		b.Status.Conditions = upsertCondition(b.Status.Conditions, metav1.Condition{
			Type:               "Completed",
			Status:             metav1.ConditionFalse,
			Reason:             string(gameplanev1alpha1.BackupPhaseRunning),
			ObservedGeneration: b.Generation,
		})
		b.Status.Conditions = upsertCondition(b.Status.Conditions, backupConsistencyCondition(b))
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.setBackupProgress(ctx, b, "SnapshotCapturePending", "waiting for CSI capture; deadline "+b.Annotations[backupCSIDeadlineAnnotation]); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// completeVolumeSnapshot records the bound snapshot identity + size, marks
// the Backup Succeeded, and runs the terminal unquiesce.
func (r *BackupReconciler) completeVolumeSnapshot(
	ctx context.Context, b *gameplanev1alpha1.Backup, vs *snapshotv1.VolumeSnapshot,
) (ctrl.Result, error) {
	if b.Annotations[backupSnapshotAbortAnnotation] == "true" {
		return r.abortVolumeSnapshot(ctx, b, "volume snapshot capture was aborted")
	}
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
		now := metav1.Now()
		b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
		meta.RemoveStatusCondition(&b.Status.Conditions, "CaptureReady")
		b.Status.ObservedGeneration = b.Generation
		b.Status.SnapshotID = vs.Name
		if vs.Status.BoundVolumeSnapshotContentName != nil {
			b.Status.VolumeSnapshotContentName = *vs.Status.BoundVolumeSnapshotContentName
		}
		if vs.Status.RestoreSize != nil && !vs.Status.RestoreSize.IsZero() {
			sz := vs.Status.RestoreSize.DeepCopy()
			b.Status.Size = &sz
		}
		if b.Status.StartTime == nil {
			if vs.Status.CreationTime != nil {
				b.Status.StartTime = vs.Status.CreationTime
			} else {
				b.Status.StartTime = &now
			}
		}
		if b.Status.CompletionTime == nil {
			b.Status.CompletionTime = &now
		}
		b.Status.Conditions = upsertCondition(b.Status.Conditions, metav1.Condition{
			Type:               "Completed",
			Status:             metav1.ConditionTrue,
			Reason:             "Succeeded",
			ObservedGeneration: b.Generation,
		})
		b.Status.Conditions = upsertCondition(b.Status.Conditions, backupConsistencyCondition(b))
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.runUnquiesce(ctx, b)
}
