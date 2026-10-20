package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	backupTargetAnnotation          = "backup.gameplane.local/target"
	backupGuardAnnotation           = "backup.gameplane.local/owner"
	backupUIDLabel                  = "backup.gameplane.local/uid"
	backupRecoveryAnnotation        = "backup.gameplane.local/recovering-owner"
	backupRecoveryLineageAnnotation = "backup.gameplane.local/recovering-owners"
	backupSnapshotAbortAnnotation   = "backup.gameplane.local/snapshot-aborted"
)

type backupIdentity struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}

func backupIdentityJSON(name string, uid types.UID) string {
	data, _ := json.Marshal(backupIdentity{Name: name, UID: uid})
	return string(data)
}

func parseBackupIdentity(value string) (backupIdentity, error) {
	var id backupIdentity
	if err := json.Unmarshal([]byte(value), &id); err != nil {
		return id, fmt.Errorf("decode backup identity: %w", err)
	}
	if id.Name == "" || id.UID == "" {
		return id, fmt.Errorf("backup identity requires a name and UID")
	}
	return id, nil
}

func (r *BackupReconciler) backupAPIReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// Serialize protected copies across schedules, requests and operator restarts.
// ResourceVersion on the live GameServer Update is the compare-and-swap. Pin
// the target before claiming it so a crash or a same-name replacement cannot
// redirect cleanup to a different game.
func (r *BackupReconciler) claimBackupTarget(ctx context.Context, b *gameplanev1alpha1.Backup) (bool, error) {
	if !b.Spec.Quiesce {
		return true, nil
	}
	legacy, err := r.legacyQuiescedBackups(ctx, b)
	if err != nil {
		return false, err
	}
	if len(legacy) > 0 {
		// Already-running pre-upgrade copies must be allowed to finish, but
		// nobody may start a new protected copy until all of their cleanup is
		// confirmed. Each legacy cleanup also waits for all legacy workers.
		if legacyBackupOwesUnquiesce(b) {
			return true, nil
		}
		return false, nil
	}
	var gs gameplanev1alpha1.GameServer
	if err := r.backupAPIReader().Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: b.Spec.ServerRef.Name}, &gs); err != nil {
		return false, err
	}
	if !gs.DeletionTimestamp.IsZero() {
		return false, fmt.Errorf("backup target is being deleted")
	}
	if value := b.Annotations[backupTargetAnnotation]; value != "" {
		id, err := parseBackupIdentity(value)
		if err != nil {
			return false, err
		}
		if id.Name != gs.Name || id.UID != gs.UID {
			return false, fmt.Errorf("backup target was replaced")
		}
	} else {
		patchBackupAnnotations(b, map[string]string{backupTargetAnnotation: backupIdentityJSON(gs.Name, gs.UID)})
		if err := r.Update(ctx, b); err != nil {
			return false, err
		}
	}
	owner := backupIdentityJSON(b.Name, b.UID)
	guard := gs.Annotations[backupGuardAnnotation]
	if guard == owner {
		return r.recoverBackupTarget(ctx, b)
	}
	if guard != "" {
		id, err := parseBackupIdentity(guard)
		if err != nil {
			return false, err
		} // Malformed guards fail closed.
		var holder gameplanev1alpha1.Backup
		err = r.backupAPIReader().Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: id.Name}, &holder)
		if err == nil && holder.UID == id.UID {
			return false, nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		// Persist recovery intent, then transfer ownership with CAS BEFORE
		// touching the agent. Concurrent contenders cannot both call save-on
		// while one of them has already started a new protected copy.
		patchBackupAnnotations(b, map[string]string{backupRecoveryAnnotation: guard, annoQuiesceAttempted: "pending"})
		if err := r.Update(ctx, b); err != nil {
			return false, err
		}
		// Preserve every predecessor on the surviving GameServer, atomically
		// with the owner transfer. A recovering Backup can itself disappear
		// before an earlier owner's copy worker has stopped.
		lineage, err := parseBackupRecoveryLineage(gs.Annotations[backupRecoveryLineageAnnotation])
		if err != nil {
			return false, err
		}
		found := false
		for _, predecessor := range lineage {
			if predecessor == id {
				found = true
			}
		}
		if !found {
			lineage = append(lineage, id)
		}
		data, _ := json.Marshal(lineage)
		gs.Annotations[backupRecoveryLineageAnnotation] = string(data)
	}
	if gs.Annotations == nil {
		gs.Annotations = make(map[string]string)
	}
	gs.Annotations[backupGuardAnnotation] = owner
	if err := r.Update(ctx, &gs); err != nil {
		return false, err
	}
	return r.recoverBackupTarget(ctx, b)
}

func (r *BackupReconciler) recoverBackupTarget(ctx context.Context, b *gameplanev1alpha1.Backup) (bool, error) {
	ids, err := r.backupRecoveryIdentities(ctx, b)
	if err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return true, nil
	}
	for _, id := range ids {
		busy, err := r.backupWorkersLive(ctx, b.Namespace, id, true)
		if err != nil || busy {
			return false, err
		}
	}
	if r.AgentClient == nil {
		return false, fmt.Errorf("cannot recover orphaned quiesce without an agent client")
	}
	if err := r.AgentClient.Unquiesce(ctx, b.Namespace, b.Spec.ServerRef.Name); err != nil {
		return false, fmt.Errorf("recover orphaned quiesce: %w", err)
	}
	delete(b.Annotations, backupRecoveryAnnotation)
	delete(b.Annotations, annoQuiesceAttempted)
	delete(b.Annotations, annoUnquiescedAt)
	if err := r.Update(ctx, b); err != nil {
		return false, err
	}
	gs, err := r.ownedBackupTarget(ctx, b)
	if err != nil {
		return false, err
	}
	if gs == nil {
		return false, fmt.Errorf("backup lost recovery ownership")
	}
	delete(gs.Annotations, backupRecoveryLineageAnnotation)
	if err := r.Update(ctx, gs); err != nil {
		return false, err
	}
	return true, nil
}

func parseBackupRecoveryLineage(value string) ([]backupIdentity, error) {
	if value == "" {
		return nil, nil
	}
	var ids []backupIdentity
	if err := json.Unmarshal([]byte(value), &ids); err != nil {
		return nil, fmt.Errorf("decode backup recovery lineage: %w", err)
	}
	for _, id := range ids {
		if id.Name == "" || id.UID == "" {
			return nil, fmt.Errorf("backup recovery lineage requires names and UIDs")
		}
	}
	return ids, nil
}

func (r *BackupReconciler) backupRecoveryIdentities(ctx context.Context, b *gameplanev1alpha1.Backup) ([]backupIdentity, error) {
	gs, err := r.ownedBackupTarget(ctx, b)
	if err != nil {
		return nil, err
	}
	if gs != nil && gs.Annotations[backupRecoveryLineageAnnotation] != "" {
		return parseBackupRecoveryLineage(gs.Annotations[backupRecoveryLineageAnnotation])
	}
	if value := b.Annotations[backupRecoveryAnnotation]; value != "" {
		id, err := parseBackupIdentity(value)
		if err != nil {
			return nil, err
		}
		return []backupIdentity{id}, nil
	}
	return nil, nil
}

func (r *BackupReconciler) backupCleanupWorkersLive(ctx context.Context, b *gameplanev1alpha1.Backup, cancel bool) (bool, error) {
	if !b.Spec.Quiesce && b.Annotations[annoQuiesceAttempted] == "" {
		return false, nil
	}
	busy, err := r.backupWorkersLive(ctx, b.Namespace, backupIdentity{Name: b.Name, UID: b.UID}, cancel)
	if err != nil || busy {
		return busy, err
	}
	if legacyBackupOwesUnquiesce(b) {
		legacy, err := r.legacyQuiescedBackups(ctx, b)
		if err != nil {
			return false, err
		}
		for i := range legacy {
			other := &legacy[i]
			busy, err := r.backupWorkersLive(ctx, b.Namespace, backupIdentity{Name: other.Name, UID: other.UID}, false)
			if err != nil || busy {
				return busy, err
			}
		}
	}
	ids, err := r.backupRecoveryIdentities(ctx, b)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		busy, err := r.backupWorkersLive(ctx, b.Namespace, id, true)
		if err != nil || busy {
			return busy, err
		}
	}
	return false, nil
}

func legacyBackupOwesUnquiesce(b *gameplanev1alpha1.Backup) bool {
	state := b.Annotations[annoQuiesceAttempted]
	return b.Annotations[backupTargetAnnotation] == "" && state != "" && state != "unsupported" && b.Annotations[annoUnquiescedAt] == ""
}

func (r *BackupReconciler) legacyQuiescedBackups(ctx context.Context, b *gameplanev1alpha1.Backup) ([]gameplanev1alpha1.Backup, error) {
	var backups gameplanev1alpha1.BackupList
	if err := r.backupAPIReader().List(ctx, &backups, client.InNamespace(b.Namespace)); err != nil {
		return nil, err
	}
	var legacy []gameplanev1alpha1.Backup
	for i := range backups.Items {
		candidate := &backups.Items[i]
		if candidate.Spec.ServerRef.Name == b.Spec.ServerRef.Name && legacyBackupOwesUnquiesce(candidate) {
			legacy = append(legacy, *candidate)
		}
	}
	return legacy, nil
}

func (r *BackupReconciler) prepareBackupQuiesce(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	claimed, err := r.claimBackupTarget(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !claimed {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, r.maybeQuiesce(ctx, b)
}

// Return only the originally bound GameServer. A foreign owner means this
// Backup has no authority to call save-on or clear that owner's guard.
func (r *BackupReconciler) ownedBackupTarget(ctx context.Context, b *gameplanev1alpha1.Backup) (*gameplanev1alpha1.GameServer, error) {
	value := b.Annotations[backupTargetAnnotation]
	if value == "" {
		return nil, nil
	} // Legacy Backups retain their existing cleanup path.
	id, err := parseBackupIdentity(value)
	if err != nil {
		return nil, err
	}
	var gs gameplanev1alpha1.GameServer
	err = r.backupAPIReader().Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: id.Name}, &gs)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if gs.UID != id.UID || gs.Annotations[backupGuardAnnotation] != backupIdentityJSON(b.Name, b.UID) {
		return nil, nil
	}
	return &gs, nil
}

func (r *BackupReconciler) releaseBackupTarget(ctx context.Context, b *gameplanev1alpha1.Backup) error {
	gs, err := r.ownedBackupTarget(ctx, b)
	if err != nil || gs == nil {
		return err
	}
	delete(gs.Annotations, backupGuardAnnotation)
	delete(gs.Annotations, backupRecoveryLineageAnnotation)
	return r.Update(ctx, gs)
}

// Foreground cancellation plus live pod inspection prevents releasing the
// world while a deleting/failed Job still has a restic worker mounted. The
// UID label keeps orphaned workers visible even after their Job disappears.
func (r *BackupReconciler) backupWorkersLive(ctx context.Context, namespace string, id backupIdentity, cancel bool) (bool, error) {
	var jobs batchv1.JobList
	if err := r.backupAPIReader().List(ctx, &jobs, client.InNamespace(namespace)); err != nil {
		return false, err
	}
	jobUIDs := make(map[types.UID]bool)
	busy := false
	for i := range jobs.Items {
		job := &jobs.Items[i]
		owner := metav1.GetControllerOf(job)
		// Only the copy Job, never the repository forget Job.
		if job.Name != id.Name || owner == nil || owner.Kind != "Backup" || owner.UID != id.UID {
			continue
		}
		jobUIDs[job.UID] = true
		finished := job.Status.Succeeded > 0 || jobPermanentlyFailed(job)
		if !finished || job.Status.Active > 0 || !job.DeletionTimestamp.IsZero() {
			busy = true
			if cancel && job.DeletionTimestamp.IsZero() {
				policy := metav1.DeletePropagationForeground
				uid := job.UID
				if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &policy, Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
					return true, err
				}
			}
		}
	}
	var pods corev1.PodList
	if err := r.backupAPIReader().List(ctx, &pods, client.InNamespace(namespace)); err != nil {
		return false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		owner := metav1.GetControllerOf(pod)
		owned := (id.UID != "" && pod.Labels[backupUIDLabel] == string(id.UID)) || (owner != nil && owner.Kind == "Job" && jobUIDs[owner.UID])
		live := !pod.DeletionTimestamp.IsZero() || (pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed)
		if owned && live {
			busy = true
		}
	}
	if busy {
		return true, nil
	}
	return r.backupSnapshotCopyLive(ctx, namespace, id)
}

// CSI captures have no Job or Pod. On normal deletion retain quiesce until
// capture completion/error instead of deleting the pending VolumeSnapshot;
// owner-reference GC will delete it after the Backup's finalizer clears.
func (r *BackupReconciler) backupSnapshotCopyLive(ctx context.Context, namespace string, id backupIdentity) (bool, error) {
	scheme := r.Scheme
	if scheme == nil {
		scheme = r.Client.Scheme()
	}
	if !scheme.Recognizes(snapshotv1.SchemeGroupVersion.WithKind("VolumeSnapshot")) {
		return false, nil
	}
	var b gameplanev1alpha1.Backup
	err := r.backupAPIReader().Get(ctx, types.NamespacedName{Namespace: namespace, Name: id.Name}, &b)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	if b.UID == id.UID && b.Annotations[backupSnapshotAbortAnnotation] == "true" {
		return false, nil
	}
	var vs snapshotv1.VolumeSnapshot
	err = r.backupAPIReader().Get(ctx, types.NamespacedName{Namespace: namespace, Name: id.Name}, &vs)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owner := metav1.GetControllerOf(&vs)
	if owner == nil || owner.Kind != "Backup" || owner.UID != id.UID {
		return false, nil
	}
	if vs.Status != nil && ((vs.Status.ReadyToUse != nil && *vs.Status.ReadyToUse) || (vs.Status.Error != nil && vs.Status.Error.Message != nil)) {
		return false, nil
	}
	return true, nil
}
