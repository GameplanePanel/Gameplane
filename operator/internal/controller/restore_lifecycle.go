package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	restoreCleanupFinalizer = "restore.gameplane.local/target-cleanup"
	restoreTargetAnnotation = "restore.gameplane.local/target"
	restoreGuardAnnotation  = "restore.gameplane.local/owner"
	restoreUIDLabel         = "restore.gameplane.local/uid"
)

// The target record on a Restore pins both the name and UID before side effects.
// The owner record on a GameServer uses the same shape to identify the Restore.
type restoreIdentity struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}

func restoreIdentityJSON(name string, uid types.UID) string {
	data, _ := json.Marshal(restoreIdentity{Name: name, UID: uid}) // strings cannot fail to marshal
	return string(data)
}

func parseRestoreIdentity(value string) (restoreIdentity, error) {
	var identity restoreIdentity
	if err := json.Unmarshal([]byte(value), &identity); err != nil {
		return identity, fmt.Errorf("decode restore identity: %w", err)
	}
	if identity.Name == "" || identity.UID == "" {
		return identity, fmt.Errorf("restore identity requires a name and UID")
	}
	return identity, nil
}

func (r *RestoreReconciler) bindRestoreTarget(ctx context.Context, rs *gameplanev1alpha1.Restore, gs *gameplanev1alpha1.GameServer) (bool, error) {
	if rs.Annotations[restoreTargetAnnotation] != "" && controllerutil.ContainsFinalizer(rs, restoreCleanupFinalizer) {
		return false, nil
	}
	if rs.Annotations == nil {
		rs.Annotations = make(map[string]string)
	}
	if rs.Annotations[restoreTargetAnnotation] == "" {
		rs.Annotations[restoreTargetAnnotation] = restoreIdentityJSON(gs.Name, gs.UID)
	}
	controllerutil.AddFinalizer(rs, restoreCleanupFinalizer)
	if err := r.Update(ctx, rs); err != nil {
		return false, err
	}
	return true, nil
}

// claimRestoreTarget uses an Update with the live resourceVersion. Two restores
// cannot both claim the same GameServer, even across operator processes. Suspend
// is written in that same update; a crash cannot leave a lock without suspension.
func (r *RestoreReconciler) claimRestoreTarget(ctx context.Context, rs *gameplanev1alpha1.Restore, gs *gameplanev1alpha1.GameServer) (bool, error) {
	owner := restoreIdentityJSON(rs.Name, rs.UID)
	workers, err := wipeWorkers(ctx, r.apiReader(), gs)
	if err != nil {
		return false, err
	}
	if req := gs.Annotations[WipeRequestedAnnotation]; gs.Annotations[wipeGuardAnnotation] != "" || workers.busy || (gs.Annotations[restoreGuardAnnotation] == "" && pendingWipe(gs)) {
		message := "Waiting for pending data wipe to finish before restoring"
		var job batchv1.Job
		err := r.apiReader().Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name + "-wipe"}, &job)
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		// DataWipe=False may describe an older token. Use the current owned
		// Job only to explain the wait, never to bypass wipe exclusion.
		if err == nil && metav1.IsControlledBy(&job, gs) && job.Labels[wipeTokenLabel] == req && jobPermanentlyFailed(&job) {
			message = "Restore blocked by failed data wipe; inspect the wipe Job logs and resolve the wipe failure before retrying the restore"
		}
		if rs.Status.Message != message {
			rs.Status.Message = message
			if err := r.Status().Update(ctx, rs); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if guard := gs.Annotations[restoreGuardAnnotation]; guard != "" && guard != owner {
		identity, err := parseRestoreIdentity(guard)
		if err != nil {
			return false, err
		} // Malformed guards fail closed.
		var holder gameplanev1alpha1.Restore
		err = r.apiReader().Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: identity.Name}, &holder)
		if err == nil && holder.UID == identity.UID {
			return false, nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		// Force-deleted owners can leave their Job and pods behind. Cancel the
		// old UID's Job and wait for its workers before reclaiming the guard.
		busy, err := r.restoreWorkersLive(ctx, gs.Namespace, identity, true)
		if err != nil || busy {
			return false, err
		}
	}
	if gs.Annotations[restoreGuardAnnotation] == owner && gs.Spec.Suspend {
		return true, nil
	}
	if gs.Annotations == nil {
		gs.Annotations = make(map[string]string)
	}
	gs.Annotations[restoreGuardAnnotation] = owner
	gs.Spec.Suspend = true
	if err := r.Update(ctx, gs); err != nil {
		return false, err
	}
	return true, nil
}

func liveRestorePod(pod *corev1.Pod) bool {
	return !pod.DeletionTimestamp.IsZero() || (pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed)
}

func podMountsRestoreTarget(pod *corev1.Pod, serverName string) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == serverName+"-data" {
			return true
		}
	}
	return false
}

// restoreTargetStopped checks the workload itself, not its readiness-derived
// GameServer phase. All safety reads bypass the informer cache.
// A successful fence write reports progress, so callers immediately requeue
// for fresh safety reads instead of waiting for the external-work polling timer.
func (r *RestoreReconciler) restoreTargetStopped(ctx context.Context, gs *gameplanev1alpha1.GameServer) (stopped, progressed bool, err error) {
	var ss appsv1.StatefulSet
	err = r.apiReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss)
	if apierrors.IsNotFound(err) {
		// An absent object cannot fence an in-flight StatefulSet Create
		// computed before acquisition. Wait for the workload reconciler.
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return false, false, fmt.Errorf("target StatefulSet is not controlled by the bound GameServer")
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Status.Replicas != 0 || ss.Status.ObservedGeneration < ss.Generation {
		return false, false, nil
	}
	var pods corev1.PodList
	if err := r.apiReader().List(ctx, &pods, client.InNamespace(gs.Namespace)); err != nil {
		return false, false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !liveRestorePod(pod) {
			continue
		}
		controller := metav1.GetControllerOf(pod)
		owned := controller != nil && ((controller.Kind == "StatefulSet" && controller.Name == gs.Name) || (controller.Kind == "GameServer" && controller.UID == gs.UID))
		if owned || pod.Name == gs.Name+"-0" || podMountsRestoreTarget(pod, gs.Name) {
			return false, false, nil
		}
	}
	// A writer Job may not have scheduled a pod yet. Waiting only for
	// existing pods would allow that Job to mount the PVC after our check.
	var jobs batchv1.JobList
	if err := r.apiReader().List(ctx, &jobs, client.InNamespace(gs.Namespace)); err != nil {
		return false, false, err
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if (job.Status.Succeeded == 0 && !jobPermanentlyFailed(job)) || job.Status.Active > 0 || !job.DeletionTimestamp.IsZero() {
			pod := &corev1.Pod{Spec: job.Spec.Template.Spec}
			if podMountsRestoreTarget(pod, gs.Name) {
				return false, false, nil
			}
		}
	}
	if ss.Annotations[restoreGuardAnnotation] != gs.Annotations[restoreGuardAnnotation] {
		// Fence a game reconciliation that computed replicas from an older
		// GameServer. Its StatefulSet Update must now conflict, or read and
		// honor this guard inside the mutation callback.
		if ss.Annotations == nil {
			ss.Annotations = make(map[string]string)
		}
		ss.Annotations[restoreGuardAnnotation] = gs.Annotations[restoreGuardAnnotation]
		if err := r.Update(ctx, &ss); err != nil {
			return false, false, err
		}
		return false, true, nil
	}
	return true, false, nil
}

// restoreWorkersLive checks Jobs by controller UID and also their labelled pods,
// so deleting a Job cannot hide a still-terminating worker. Terminal Jobs remain
// available for their logs. Cancellation uses foreground deletion and UID
// preconditions, retaining the guard until both Jobs and live pods have drained.
func (r *RestoreReconciler) restoreWorkersLive(ctx context.Context, namespace string, identity restoreIdentity, cancel bool) (bool, error) {
	var jobs batchv1.JobList
	if err := r.apiReader().List(ctx, &jobs, client.InNamespace(namespace)); err != nil {
		return false, err
	}
	jobUIDs := make(map[types.UID]bool)
	busy := false
	for i := range jobs.Items {
		job := &jobs.Items[i]
		owner := metav1.GetControllerOf(job)
		if owner == nil || owner.Kind != "Restore" || owner.UID != identity.UID || owner.Name != identity.Name {
			continue
		}
		jobUIDs[job.UID] = true
		finished := job.Status.Succeeded > 0 || jobPermanentlyFailed(job)
		if !job.DeletionTimestamp.IsZero() || !finished || job.Status.Active > 0 {
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
	if err := r.apiReader().List(ctx, &pods, client.InNamespace(namespace)); err != nil {
		return false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		owner := metav1.GetControllerOf(pod)
		owned := pod.Labels[restoreUIDLabel] == string(identity.UID) || (owner != nil && owner.Kind == "Job" && jobUIDs[owner.UID])
		if owned && liveRestorePod(pod) {
			busy = true
		}
	}
	return busy, nil
}

// cleanupRestore handles terminal status, deletion and interrupted completion.
// Status is committed before releasing the guard, so a crash after releasing it
// cannot cause the old Restore to acquire it again and overwrite the next run.
func (r *RestoreReconciler) cleanupRestore(ctx context.Context, rs *gameplanev1alpha1.Restore) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(rs, restoreCleanupFinalizer) {
		return ctrl.Result{}, nil
	}
	success := rs.DeletionTimestamp.IsZero() && rs.Status.Phase == gameplanev1alpha1.RestorePhaseSucceeded
	busy, err := r.restoreWorkersLive(ctx, rs.Namespace, restoreIdentity{Name: rs.Name, UID: rs.UID}, !success)
	if err != nil {
		return ctrl.Result{}, err
	}
	if busy {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if value := rs.Annotations[restoreTargetAnnotation]; value != "" {
		target, err := parseRestoreIdentity(value)
		if err != nil {
			return ctrl.Result{}, err
		}
		var gs gameplanev1alpha1.GameServer
		err = r.apiReader().Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: target.Name}, &gs)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if err == nil && gs.UID == target.UID && gs.Annotations[restoreGuardAnnotation] == restoreIdentityJSON(rs.Name, rs.UID) {
			var ss appsv1.StatefulSet
			ssErr := r.apiReader().Get(ctx, client.ObjectKeyFromObject(&gs), &ss)
			if ssErr != nil && !apierrors.IsNotFound(ssErr) {
				return ctrl.Result{}, ssErr
			}
			if ssErr == nil && metav1.IsControlledBy(&ss, &gs) && ss.Annotations[restoreGuardAnnotation] == gs.Annotations[restoreGuardAnnotation] {
				delete(ss.Annotations, restoreGuardAnnotation)
				if err := r.Update(ctx, &ss); err != nil {
					return ctrl.Result{}, err
				}
			}
			delete(gs.Annotations, restoreGuardAnnotation)
			// Partial or canceled restores stay suspended for investigation.
			gs.Spec.Suspend = !success
			if err := r.Update(ctx, &gs); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	controllerutil.RemoveFinalizer(rs, restoreCleanupFinalizer)
	if err := r.Update(ctx, rs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}
