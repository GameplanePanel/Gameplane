package controller

import (
	"context"
	"fmt"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		return r.failVolumeSnapshot(ctx, b, "volume snapshot aborted after an API or creation failure")
	}
	// An uncached read verifies that the snapshot API is served before
	// disabling autosave. Missing CRDs (including NoMatch) and authorization
	// errors fail safely, including cleanup of previously quiesced Backups.
	var existing snapshotv1.VolumeSnapshot
	if err := r.backupAPIReader().Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: b.Name}, &existing); err != nil && !apierrors.IsNotFound(err) {
		return r.abortVolumeSnapshot(ctx, b, fmt.Sprintf("volume snapshot API unavailable: %v", err))
	}
	// Quiesce first (idempotent across requeues). A CSI snapshot is only
	// crash-consistent; flushing the game to disk first (RCON save-all,
	// etc.) makes it application-consistent. No-op when the backup opts out
	// or no agent mTLS is configured.
	if res, err := r.prepareBackupQuiesce(ctx, b); err != nil || res.RequeueAfter > 0 {
		return res, err
	}

	pvcName := gs.Name + "-data"
	vs := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, vs, func() error {
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

	switch {
	case vs.Status != nil && vs.Status.Error != nil && vs.Status.Error.Message != nil:
		// The CSI driver rejected the snapshot. Surface it and unquiesce so
		// the game doesn't stay frozen.
		return r.failVolumeSnapshot(ctx, b, fmt.Sprintf("volume snapshot failed: %s", *vs.Status.Error.Message))
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
	patchBackupAnnotations(b, map[string]string{backupSnapshotAbortAnnotation: "true"})
	if err := r.Update(ctx, b); err != nil {
		return ctrl.Result{}, err
	}
	return r.failVolumeSnapshot(ctx, b, msg)
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
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// completeVolumeSnapshot records the bound snapshot identity + size, marks
// the Backup Succeeded, and runs the terminal unquiesce.
func (r *BackupReconciler) completeVolumeSnapshot(
	ctx context.Context, b *gameplanev1alpha1.Backup, vs *snapshotv1.VolumeSnapshot,
) (ctrl.Result, error) {
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
		now := metav1.Now()
		b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
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
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.runUnquiesce(ctx, b)
}

// failVolumeSnapshot marks the Backup Failed and still runs the terminal
// unquiesce so a quiesced game doesn't stay frozen after a snapshot error.
func (r *BackupReconciler) failVolumeSnapshot(
	ctx context.Context, b *gameplanev1alpha1.Backup, msg string,
) (ctrl.Result, error) {
	if _, err := r.fail(ctx, b, msg); err != nil {
		return ctrl.Result{}, err
	}
	return r.runUnquiesce(ctx, b)
}
