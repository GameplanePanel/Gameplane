package controller

import (
	"context"
	"errors"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gp "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	podRecoveryKind         = "PodRecovery"
	evictionRetry           = 5 * time.Second
	evictionRetryAnnotation = "operations.gameplane.local/eviction-retry-after"
)

// OrderedReady can wait indefinitely for a failed ordinal after a correction.
// Admit eviction through the same GameServer resourceVersion CAS as backups
// and restores. The owner identifies the exact old pod across leader changes.
func (r *GameServerReconciler) recoverObsoleteFailedPod(ctx context.Context, gs *gp.GameServer) (time.Duration, error) {
	if _, held, err := lifecycleOwner(gs); held || err != nil || !recoveryPowerAllows(gs) {
		return 0, err
	}
	if retry, err := time.Parse(time.RFC3339Nano, gs.Annotations[evictionRetryAnnotation]); err == nil {
		if wait := time.Until(retry); wait > 0 && wait <= evictionRetry {
			return wait, nil
		}
	}
	var pod corev1.Pod
	if err := r.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod); err != nil {
		return 0, client.IgnoreNotFound(err)
	}
	if ok, err := r.failedOrdinal(ctx, gs, &pod); err != nil || !ok {
		return 0, err
	}
	want := lifecycleIdentity{Kind: podRecoveryKind, Name: gs.Name, UID: gs.UID, TargetUID: gs.UID, Generation: gs.Generation, Token: string(pod.UID)}
	claimed, err := claimLifecycleOperation(ctx, r.Client, r.wipeReader(), gs.DeepCopy(), want, r.OperationQuiescer)
	if err != nil || !claimed {
		return 0, err
	}
	return r.resumeFailedPodEviction(ctx, gs, want)
}

func recoveryPowerAllows(gs *gp.GameServer) bool {
	return gs.DeletionTimestamp.IsZero() && !gs.Spec.Suspend && gs.Annotations[stopRequestedAtAnnotation] == "" &&
		gs.Annotations[IdleAsleepSinceAnnotation] == "" && !pendingWipe(gs) && !tokenPending(gs, RestartRequestedAnnotation, RestartCompletedAnnotation)
}

func (r *GameServerReconciler) failedOrdinal(ctx context.Context, gs *gp.GameServer, pod *corev1.Pod) (bool, error) {
	rev := pod.Labels[appsv1.StatefulSetRevisionLabel]
	if rev == "" || pod.UID == "" || pod.ResourceVersion == "" || !pod.DeletionTimestamp.IsZero() || podReady(pod) {
		return false, nil
	}
	if _, _, failed := startupFailure(pod); !failed {
		return false, nil
	}
	var ss appsv1.StatefulSet
	if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if ss.UID == "" || !metav1.IsControlledBy(pod, &ss) || !metav1.IsControlledBy(&ss, gs) || !ss.DeletionTimestamp.IsZero() ||
		ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 || ss.Generation < 1 || ss.Status.ObservedGeneration != ss.Generation ||
		ss.Status.UpdateRevision == "" || ss.Status.UpdateRevision == rev {
		return false, nil
	}
	strategy := ss.Spec.UpdateStrategy
	if strategy.Type != appsv1.RollingUpdateStatefulSetStrategyType || (strategy.RollingUpdate != nil && strategy.RollingUpdate.Partition != nil && *strategy.RollingUpdate.Partition > 0) ||
		(ss.Spec.Ordinals != nil && ss.Spec.Ordinals.Start != 0) {
		return false, nil
	}
	if value := ss.Annotations[lifecycleOperationAnnotation]; value != "" && value != gs.Annotations[lifecycleOperationAnnotation] {
		return false, nil
	}
	for _, key := range []string{backupGuardAnnotation, restoreGuardAnnotation, wipeGuardAnnotation, hostportStopFenceAnnotation} {
		if ss.Annotations[key] != "" {
			return false, nil
		}
	}
	return true, nil
}

func (r *GameServerReconciler) resumeFailedPodEviction(ctx context.Context, gs *gp.GameServer, want lifecycleIdentity) (time.Duration, error) {
	reader := r.wipeReader()
	var live gp.GameServer
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &live); err != nil {
		return 0, client.IgnoreNotFound(err)
	}
	op, held, err := lifecycleOwner(&live)
	if err != nil || !held || !sameLifecycleOwner(op.lifecycleIdentity, want) {
		return 0, err
	}
	release := func(retry bool) error {
		if err := clearLifecycleOperation(ctx, r.Client, reader, &live, want); err != nil {
			return err
		}
		delete(live.Annotations, evictionRetryAnnotation)
		if retry {
			// Release a refused request for other owners. The metadata update
			// itself wakes the controller; persist the delay to avoid a hot loop.
			live.Annotations[evictionRetryAnnotation] = time.Now().Add(evictionRetry).UTC().Format(time.RFC3339Nano)
		}
		return r.Update(ctx, &live)
	}
	var pod corev1.Pod
	err = reader.Get(ctx, client.ObjectKey{Namespace: live.Namespace, Name: live.Name + "-0"}, &pod)
	if apierrors.IsNotFound(err) || (err == nil && string(pod.UID) != want.Token) {
		return 0, release(false)
	}
	if err != nil {
		return 0, err
	}
	// An accepted eviction may still be saving during its termination grace.
	// Keep the shared owner until this UID has left, including after a restart.
	if !pod.DeletionTimestamp.IsZero() {
		return evictionRetry, nil
	}
	if live.Generation != want.Generation || !recoveryPowerAllows(&live) {
		return 0, release(false)
	}
	ok, err := r.failedOrdinal(ctx, &live, &pod)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, release(false)
	}
	preconditions := metav1.Preconditions{UID: &pod.UID, ResourceVersion: &pod.ResourceVersion}
	eviction := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}, DeleteOptions: &metav1.DeleteOptions{Preconditions: &preconditions}}
	err = r.SubResource("eviction").Create(ctx, &pod, eviction)
	switch {
	case err == nil:
		return evictionRetry, nil
	case apierrors.IsNotFound(err):
		return 0, release(false)
	case apierrors.IsTooManyRequests(err) || apierrors.IsConflict(err):
		return evictionRetry, release(true)
	case apierrors.IsForbidden(err) || apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) || apierrors.IsMethodNotSupported(err):
		return 0, errors.Join(err, release(true))
	default:
		// An indeterminate transport result can represent an accepted request.
		// Leave the owner in place and re-read the exact pod on the next pass.
		return 0, err
	}
}
