package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	DefaultBackupPreparationTimeout     = 10 * time.Minute
	DefaultBackupQuiesceTimeout         = 5 * time.Minute
	DefaultBackupCSICaptureTimeout      = 10 * time.Minute
	backupPreparationDeadlineAnnotation = "backup.gameplane.local/preparation-deadline"
	backupQuiesceDeadlineAnnotation     = "backup.gameplane.local/quiesce-deadline"
	backupCSIDeadlineAnnotation         = "backup.gameplane.local/csi-deadline"
	backupCopyAbortAnnotation           = "backup.gameplane.local/copy-aborted"
)

func backupTimeout(configured, fallback time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return fallback
}

// Write once before the bounded action. Reconfiguration and restarts cannot
// extend an admitted attempt. Malformed stored state fails closed.
func (r *BackupReconciler) backupDeadline(ctx context.Context, b *gameplanev1alpha1.Backup, key string, duration time.Duration) (time.Time, error) {
	if value := b.Annotations[key]; value != "" {
		deadline, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid persisted backup deadline %s: %w", key, err)
		}
		return deadline, nil
	}
	deadline := time.Now().UTC().Add(duration)
	patchBackupAnnotations(b, map[string]string{key: deadline.Format(time.RFC3339Nano)})
	return deadline, r.Update(ctx, b)
}

func (r *BackupReconciler) setBackupProgress(ctx context.Context, b *gameplanev1alpha1.Backup, reason, message string) error {
	status := metav1.ConditionFalse
	if reason == "WorkerAuthorized" {
		status = metav1.ConditionTrue
	}
	conditions := upsertCondition(b.Status.Conditions, metav1.Condition{Type: "CaptureReady", Status: status,
		Reason: reason, Message: message, ObservedGeneration: b.Generation})
	if sameConditions(b.Status.Conditions, conditions) {
		return nil
	}
	b.Status.Conditions = conditions
	return r.Status().Update(ctx, b)
}

// Persist invalidation before revoking authorization or cancelling workers.
// A stale mounted token can still be observed until projection catches up, so
// neither token revocation nor a deadline substitutes for actual worker drain.
func (r *BackupReconciler) abortResticCopy(ctx context.Context, b *gameplanev1alpha1.Backup, message string) (ctrl.Result, error) {
	if message == "" {
		message = "backup copy invalidated"
	}
	if b.Annotations[backupCopyAbortAnnotation] == "" {
		patchBackupAnnotations(b, map[string]string{backupCopyAbortAnnotation: message})
		if err := r.Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	message = b.Annotations[backupCopyAbortAnnotation]
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || b.Status.SnapshotID != "" || b.Status.Message != message {
		b.Status.SnapshotID = ""
		if _, err := r.fail(ctx, b, message); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.cleanupResticCopy(ctx, b)
}

func (r *BackupReconciler) cleanupResticCopy(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	if err := r.revokeBackupStart(ctx, b); err != nil {
		return ctrl.Result{}, err
	}
	busy, err := r.backupCleanupWorkersLive(ctx, b, true)
	if err != nil {
		return ctrl.Result{}, err
	}
	if busy {
		if err := r.setBackupProgress(ctx, b, "WorkerDrainPending", "copy invalidated; waiting for workers to stop before restoring saving"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if meta.RemoveStatusCondition(&b.Status.Conditions, "CaptureReady") {
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.runUnquiesce(ctx, b)
}

func (r *BackupReconciler) revokeBackupStart(ctx context.Context, b *gameplanev1alpha1.Backup) error {
	var gate corev1.ConfigMap
	err := r.backupAPIReader().Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: backupStartConfigMapName(b)}, &gate)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err == nil && metav1.IsControlledBy(&gate, b) && gate.Data[backupStartTokenKey] != "" {
		gate.Data[backupStartTokenKey] = ""
		if err := r.Update(ctx, &gate); err != nil {
			return err
		}
	}
	return nil
}
