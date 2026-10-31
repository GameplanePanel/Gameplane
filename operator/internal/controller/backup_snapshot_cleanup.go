package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const backupSnapshotIdentityAnnotation = "backup.gameplane.local/snapshot-identity"
const backupSnapshotAbortMessageAnnotation = "backup.gameplane.local/snapshot-abort-reason"

type backupSnapshotIdentity struct {
	UID      types.UID            `json:"uid"`
	Contents map[string]types.UID `json:"contents,omitempty"`
}

func (r *BackupReconciler) rememberBackupSnapshot(ctx context.Context, b *gameplanev1alpha1.Backup, vs *snapshotv1.VolumeSnapshot) error {
	var id backupSnapshotIdentity
	if value := b.Annotations[backupSnapshotIdentityAnnotation]; value != "" {
		if err := json.Unmarshal([]byte(value), &id); err != nil {
			return fmt.Errorf("invalid snapshot identity: %w", err)
		}
	}
	if !metav1.IsControlledBy(vs, b) || (id.UID != "" && id.UID != vs.UID) {
		return fmt.Errorf("VolumeSnapshot %s/%s identity changed", vs.Namespace, vs.Name)
	}
	if id.UID == vs.UID {
		return nil
	}
	id.UID = vs.UID
	encoded, err := json.Marshal(id)
	if err != nil {
		return err
	}
	patchBackupAnnotations(b, map[string]string{backupSnapshotIdentityAnnotation: string(encoded)})
	return r.Update(ctx, b)
}

func (r *BackupReconciler) recordSnapshotAbort(ctx context.Context, b *gameplanev1alpha1.Backup, message string) error {
	if b.Annotations[backupSnapshotAbortAnnotation] != "true" {
		patchBackupAnnotations(b, map[string]string{backupSnapshotAbortAnnotation: "true", backupSnapshotAbortMessageAnnotation: message})
		if err := r.Update(ctx, b); err != nil {
			return err
		}
	}
	if stored := b.Annotations[backupSnapshotAbortMessageAnnotation]; stored != "" {
		message = stored
	}
	if b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed && b.Status.SnapshotID == "" && b.Status.VolumeSnapshotContentName == "" && b.Status.Message == message {
		return nil
	}
	b.Status.SnapshotID, b.Status.VolumeSnapshotContentName = "", ""
	_, err := r.fail(ctx, b, message)
	return err
}

func (r *BackupReconciler) snapshotCleanupCondition(ctx context.Context, b *gameplanev1alpha1.Backup, status metav1.ConditionStatus, reason, message string) error {
	conditions := upsertCondition(b.Status.Conditions, metav1.Condition{Type: "SnapshotCleaned", Status: status, Reason: reason, Message: message, ObservedGeneration: b.Generation})
	if sameConditions(b.Status.Conditions, conditions) {
		return nil
	}
	b.Status.Conditions = conditions
	if status != metav1.ConditionTrue {
		r.recordEvent(b, corev1.EventTypeWarning, reason, message)
	}
	return r.Status().Update(ctx, b)
}

// Cleanup is independent of save-on once the capture is durably invalidated.
// The original snapshot UID and each content UID are retained on the Backup;
// resource replacements are reported, never adopted or deleted.
func (r *BackupReconciler) cleanupAbortedSnapshot(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	res, err := r.reclaimAbortedSnapshot(ctx, b)
	if err == nil {
		return res, nil
	}
	if conditionErr := r.snapshotCleanupCondition(ctx, b, metav1.ConditionFalse, "SnapshotCleanupPending", err.Error()); conditionErr != nil {
		return ctrl.Result{}, conditionErr
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *BackupReconciler) reclaimAbortedSnapshot(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	var id backupSnapshotIdentity
	if value := b.Annotations[backupSnapshotIdentityAnnotation]; value != "" {
		if err := json.Unmarshal([]byte(value), &id); err != nil {
			return ctrl.Result{}, err
		}
	}
	var vs snapshotv1.VolumeSnapshot
	err := r.backupAPIReader().Get(ctx, client.ObjectKeyFromObject(b), &vs)
	if meta.IsNoMatchError(err) {
		return ctrl.Result{}, fmt.Errorf("snapshot API unavailable; inspect VolumeSnapshot %s/%s and recorded identity %s", b.Namespace, b.Name, b.Annotations[backupSnapshotIdentityAnnotation])
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("read VolumeSnapshot %s/%s (recorded UID %s): %w", b.Namespace, b.Name, id.UID, err)
	}
	present := err == nil
	if present {
		if !metav1.IsControlledBy(&vs, b) || (id.UID != "" && id.UID != vs.UID) {
			return ctrl.Result{}, r.snapshotCleanupCondition(ctx, b, metav1.ConditionFalse, "SnapshotCleanupUnverified", fmt.Sprintf("refusing replacement VolumeSnapshot %s/%s UID %s; original UID %s", vs.Namespace, vs.Name, vs.UID, id.UID))
		}
		if id.UID == "" {
			if vs.UID == "" {
				return ctrl.Result{}, fmt.Errorf("VolumeSnapshot %s/%s has no verifiable UID", vs.Namespace, vs.Name)
			}
			if err := r.rememberBackupSnapshot(ctx, b, &vs); err != nil {
				return ctrl.Result{}, err
			}
			id.UID = vs.UID
		}
		if vs.Annotations == nil {
			vs.Annotations = map[string]string{}
		}
		if vs.Annotations[backupSnapshotAbortAnnotation] != "true" {
			vs.Annotations[backupSnapshotAbortAnnotation] = "true"
			if err := r.Update(ctx, &vs); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	var contents snapshotv1.VolumeSnapshotContentList
	if err := r.backupAPIReader().List(ctx, &contents); err != nil {
		return ctrl.Result{}, fmt.Errorf("list contents for VolumeSnapshot %s/%s UID %s: %w", b.Namespace, b.Name, id.UID, err)
	}
	if id.Contents == nil {
		id.Contents = map[string]types.UID{}
	}
	var retained []string
	unverified := false
	boundName := ""
	if present && vs.Status != nil && vs.Status.BoundVolumeSnapshotContentName != nil {
		boundName = *vs.Status.BoundVolumeSnapshotContentName
	}
	var owned []*snapshotv1.VolumeSnapshotContent
	for i := range contents.Items {
		content := &contents.Items[i]
		ref := content.Spec.VolumeSnapshotRef
		if known, ok := id.Contents[content.Name]; ok && known != content.UID {
			unverified = true
			retained = append(retained, fmt.Sprintf("replacement content %s UID %s (original %s)", content.Name, content.UID, known))
			continue
		}
		if id.UID == "" || ref.UID != id.UID || ref.Name != b.Name || ref.Namespace != b.Namespace {
			if _, known := id.Contents[content.Name]; known || content.Name == boundName {
				// Retain the rejected candidate's UID so deleting the valid
				// snapshot cannot erase the administrator's follow-up identity.
				id.Contents[content.Name] = content.UID
				unverified = true
				retained = append(retained, fmt.Sprintf("unverified content %s UID %s references snapshot %s/%s UID %s", content.Name, content.UID, ref.Namespace, ref.Name, ref.UID))
			}
			continue
		}
		id.Contents[content.Name] = content.UID
		if content.Spec.DeletionPolicy == snapshotv1.VolumeSnapshotContentRetain {
			retained = append(retained, fmt.Sprintf("Retain content %s UID %s for snapshot UID %s", content.Name, content.UID, id.UID))
		} else {
			owned = append(owned, content)
		}
	}
	encoded, err := json.Marshal(id)
	if err != nil {
		return ctrl.Result{}, err
	}
	if string(encoded) != b.Annotations[backupSnapshotIdentityAnnotation] {
		patchBackupAnnotations(b, map[string]string{backupSnapshotIdentityAnnotation: string(encoded)})
		if err := r.Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	if present && vs.DeletionTimestamp.IsZero() {
		uid := vs.UID
		if err := r.Delete(ctx, &vs, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete VolumeSnapshot %s/%s UID %s: %w", vs.Namespace, vs.Name, uid, err)
		}
	}
	for _, content := range owned {
		if !content.DeletionTimestamp.IsZero() {
			continue
		}
		uid := content.UID
		if uid == "" {
			return ctrl.Result{}, fmt.Errorf("content %s has no verifiable UID", content.Name)
		}
		if err := r.Delete(ctx, content, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete VolumeSnapshotContent %s UID %s for snapshot UID %s: %w", content.Name, uid, id.UID, err)
		}
	}
	if len(retained) > 0 {
		if unverified {
			return ctrl.Result{}, r.snapshotCleanupCondition(ctx, b, metav1.ConditionFalse, "SnapshotCleanupUnverified", strings.Join(retained, "; ")+"; administrator verification required")
		}
		return ctrl.Result{}, r.snapshotCleanupCondition(ctx, b, metav1.ConditionFalse, "SnapshotCleanupRetained", strings.Join(retained, "; ")+"; administrator reclamation required")
	}
	if present || len(owned) > 0 {
		if err := r.snapshotCleanupCondition(ctx, b, metav1.ConditionFalse, "SnapshotCleanupPending", fmt.Sprintf("deletion requested for snapshot %s/%s UID %s and contents %v", b.Namespace, b.Name, id.UID, id.Contents)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, r.snapshotCleanupCondition(ctx, b, metav1.ConditionTrue, "SnapshotCleanupComplete", "aborted snapshot resources are absent")
}
