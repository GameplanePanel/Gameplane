package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/ValgulNecron/gameplane/operator/api/v1alpha1"
)

const (
	wipeGuardAnnotation   = "gameplane.local/wipe-owner"
	wipeSuccessAnnotation = "gameplane.local/wipe-succeeded"
	wipeUIDLabel          = "gameplane.local/wipe-server-uid"
	wipeDrainPoll         = 5 * time.Second
	wipeOrphanGuard       = "orphaned-worker"
)

func pendingWipe(gs *gameplanev1alpha1.GameServer) bool {
	return gs.Annotations[WipeRequestedAnnotation] != "" && gs.Annotations[WipeRequestedAnnotation] != gs.Annotations[WipeCompletedAnnotation]
}

type wipeWorkerState struct {
	jobs     []*batchv1.Job
	livePods bool
	busy     bool
	token    string
}

// Inventory bypasses the cache and keeps terminating/orphan pods visible after
// foreground Job deletion. Labels identify new workers; legacy pods use the
// owned Job UID, or conservatively the fixed Job owner name and target PVC.
// A replacement Job at the same name must not hide old legacy worker pods.
// Labels never authorize deleting a Job.
func wipeWorkers(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer) (wipeWorkerState, error) {
	state := wipeWorkerState{}
	var job batchv1.Job
	err := reader.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name + "-wipe"}, &job)
	if err != nil && !apierrors.IsNotFound(err) {
		return state, err
	}
	ownedUIDs := map[types.UID]bool{}
	jobExists := err == nil
	if jobExists && metav1.IsControlledBy(&job, gs) {
		state.jobs = append(state.jobs, &job)
		if job.UID != "" {
			ownedUIDs[job.UID] = true
		}
		if state.token == "" {
			state.token = job.Labels[wipeTokenLabel]
		}
		if !job.DeletionTimestamp.IsZero() || job.Status.Active > 0 || (job.Status.Succeeded == 0 && !jobPermanentlyFailed(&job)) {
			state.busy = true
		}
	}
	var pods corev1.PodList
	if err := reader.List(ctx, &pods, client.InNamespace(gs.Namespace)); err != nil {
		return state, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !liveRestorePod(pod) {
			continue
		}
		owner := metav1.GetControllerOf(pod)
		owned := gs.UID != "" && pod.Labels[wipeUIDLabel] == string(gs.UID)
		if owner != nil && owner.Kind == "Job" {
			owned = owned || ownedUIDs[owner.UID] || (owner.Name == gs.Name+"-wipe" && pod.Labels[wipeUIDLabel] == "" && podMountsRestoreTarget(pod, gs.Name))
		}
		if owned {
			state.livePods, state.busy = true, true
			if state.token == "" {
				state.token = pod.Labels[wipeTokenLabel]
			}
		}
	}
	return state, nil
}

func (r *GameServerReconciler) wipeReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// Called before workload mutation as well as before wipe side effects. Recovery
// from a legacy Job-only/orphan state must fence a start after operator restart.
// The resourceVersion Update arbitrates with restore claims and API writes.
func (r *GameServerReconciler) ensureWipeGuard(ctx context.Context, gs *gameplanev1alpha1.GameServer) (changed bool, err error) {
	var live gameplanev1alpha1.GameServer
	if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &live); err != nil {
		return false, err
	}
	if live.UID != gs.UID {
		return false, fmt.Errorf("GameServer changed identity before wipe acquisition")
	}
	*gs = live
	if gs.Annotations[wipeGuardAnnotation] != "" || gs.Annotations[restoreGuardAnnotation] != "" {
		return false, nil
	}
	state, err := wipeWorkers(ctx, r.wipeReader(), gs)
	if err != nil {
		return false, err
	}
	needsGuard := pendingWipe(gs) || state.busy
	for _, job := range state.jobs {
		// An acknowledged, drained success can be removed without reacquiring.
		if job.Status.Succeeded == 0 || job.Labels[wipeTokenLabel] != gs.Annotations[WipeCompletedAnnotation] {
			needsGuard = true
		}
	}
	if !needsGuard {
		return false, nil
	}
	token := state.token
	if token == "" && pendingWipe(gs) && !state.busy && len(state.jobs) == 0 {
		token = gs.Annotations[WipeRequestedAnnotation]
	}
	if token == "" {
		token = wipeOrphanGuard
	}
	if gs.Annotations == nil {
		gs.Annotations = make(map[string]string)
	}
	gs.Annotations[wipeGuardAnnotation] = token
	// Recovery without a pending request is cleanup, including legacy orphan
	// pods with no token. The guard fences live workers without changing the
	// current resume/stop intent. Pending and failed matching wipes suspend.
	if pendingWipe(gs) {
		gs.Spec.Suspend = true
	}
	if err := r.Update(ctx, gs); err != nil {
		return false, err
	}
	return true, nil
}

// Stamp the owned workload with a checked write before workers launch. A stale
// workload update then conflicts, while callbacks reading the fence stay at 0.
func (r *GameServerReconciler) wipeTargetStopped(ctx context.Context, gs *gameplanev1alpha1.GameServer) (bool, error) {
	var ss appsv1.StatefulSet
	err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss)
	if apierrors.IsNotFound(err) {
		// An absent workload cannot fence an in-flight Create.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return false, fmt.Errorf("wipe target StatefulSet is not controlled by this GameServer")
	}
	if ss.Annotations[wipeGuardAnnotation] != gs.Annotations[wipeGuardAnnotation] {
		if ss.Annotations == nil {
			ss.Annotations = make(map[string]string)
		}
		ss.Annotations[wipeGuardAnnotation] = gs.Annotations[wipeGuardAnnotation]
		zero := int32(0)
		ss.Spec.Replicas = &zero
		return false, r.Update(ctx, &ss)
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Status.Replicas != 0 || ss.Status.ObservedGeneration < ss.Generation {
		return false, nil
	}
	var pods corev1.PodList
	if err := r.wipeReader().List(ctx, &pods, client.InNamespace(gs.Namespace)); err != nil {
		return false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !liveRestorePod(pod) {
			continue
		}
		owner := metav1.GetControllerOf(pod)
		if pod.Name == gs.Name+"-0" || podMountsRestoreTarget(pod, gs.Name) || (owner != nil && ((owner.Kind == "StatefulSet" && owner.Name == gs.Name) || (owner.Kind == "GameServer" && owner.UID == gs.UID))) {
			return false, nil
		}
	}
	var jobs batchv1.JobList
	if err := r.wipeReader().List(ctx, &jobs, client.InNamespace(gs.Namespace)); err != nil {
		return false, err
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if !job.DeletionTimestamp.IsZero() || job.Status.Active > 0 || (job.Status.Succeeded == 0 && !jobPermanentlyFailed(job)) {
			if podMountsRestoreTarget(&corev1.Pod{Spec: job.Spec.Template.Spec}, gs.Name) {
				return false, nil
			}
		}
	}
	return true, nil
}

// Clear the matching workload marker while the GameServer still holds its
// guard. Callbacks always read the live guard, closing a release-conflict gap.
func (r *GameServerReconciler) clearWipeWorkloadGuard(ctx context.Context, gs *gameplanev1alpha1.GameServer, token string) error {
	var ss appsv1.StatefulSet
	err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if metav1.IsControlledBy(&ss, gs) && ss.Annotations[wipeGuardAnnotation] == token {
		delete(ss.Annotations, wipeGuardAnnotation)
		return r.Update(ctx, &ss)
	}
	return nil
}

func (r *GameServerReconciler) releaseWipeGuard(ctx context.Context, gs *gameplanev1alpha1.GameServer, token string, success bool) error {
	var live gameplanev1alpha1.GameServer
	if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &live); err != nil {
		return client.IgnoreNotFound(err)
	}
	if live.UID != gs.UID || live.Annotations[wipeGuardAnnotation] != token || live.Annotations[restoreGuardAnnotation] != "" {
		return nil
	}
	if success && (live.Annotations[WipeRequestedAnnotation] != token || !pendingWipe(&live)) {
		return nil
	}
	if success && live.Annotations[wipeSuccessAnnotation] != token {
		return nil
	}
	if err := r.clearWipeWorkloadGuard(ctx, &live, token); err != nil {
		return err
	}
	if success {
		live.Annotations[WipeCompletedAnnotation] = token
		live.Spec.Suspend = false
		delete(live.Annotations, wipeGuardAnnotation)
	} else if pendingWipe(&live) {
		// Transfer only after old Jobs and live pods have drained; next pass
		// fences the workload for this new token before launching a worker.
		live.Annotations[wipeGuardAnnotation] = live.Annotations[WipeRequestedAnnotation]
		live.Spec.Suspend = true
	} else {
		delete(live.Annotations, wipeGuardAnnotation)
	}
	if live.Annotations[wipeSuccessAnnotation] == token {
		delete(live.Annotations, wipeSuccessAnnotation)
	}
	if err := r.Update(ctx, &live); err != nil {
		return err
	}
	*gs = live
	return nil
}

// Persist success before deleting its Job, so restart during foreground drain
// cannot lose success proof and recreate a destructive writer for this token.
func (r *GameServerReconciler) recordWipeSuccess(ctx context.Context, gs *gameplanev1alpha1.GameServer, token string) error {
	var live gameplanev1alpha1.GameServer
	if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &live); err != nil {
		return client.IgnoreNotFound(err)
	}
	if live.UID != gs.UID || live.Annotations[wipeGuardAnnotation] != token || live.Annotations[WipeRequestedAnnotation] != token || !pendingWipe(&live) || live.Annotations[restoreGuardAnnotation] != "" {
		return nil
	}
	live.Annotations[wipeSuccessAnnotation] = token
	if err := r.Update(ctx, &live); err != nil {
		return err
	}
	*gs = live
	return nil
}
