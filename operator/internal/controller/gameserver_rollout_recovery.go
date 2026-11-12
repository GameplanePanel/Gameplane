package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gp "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// OrderedReady can wait indefinitely on the broken pod even after the
// StatefulSet controller has observed a corrected template. Evict only the
// obsolete failed ordinal, leaving its replacement to the StatefulSet.
func (r *GameServerReconciler) recoverObsoleteFailedPod(ctx context.Context, gs *gp.GameServer) (time.Duration, error) {
	var pod corev1.Pod
	key := client.ObjectKeyFromObject(gs)
	if err := r.Get(ctx, client.ObjectKey{Namespace: key.Namespace, Name: key.Name + "-0"}, &pod); err != nil {
		return 0, client.IgnoreNotFound(err)
	}
	rev := pod.Labels[appsv1.StatefulSetRevisionLabel]
	if rev == "" || pod.UID == "" || pod.ResourceVersion == "" || !pod.DeletionTimestamp.IsZero() || podReady(&pod) {
		return 0, nil
	}
	if _, _, failed := startupFailure(&pod); !failed {
		return 0, nil
	}

	reader := r.wipeReader()
	var ss appsv1.StatefulSet
	if err := reader.Get(ctx, key, &ss); err != nil {
		return 0, client.IgnoreNotFound(err)
	}
	if ss.UID == "" || !metav1.IsControlledBy(&pod, &ss) || !metav1.IsControlledBy(&ss, gs) || !ss.DeletionTimestamp.IsZero() ||
		ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 || ss.Generation < 1 || ss.Status.ObservedGeneration != ss.Generation ||
		ss.Status.UpdateRevision == "" || ss.Status.UpdateRevision == rev {
		return 0, nil
	}
	strategy := ss.Spec.UpdateStrategy
	if strategy.Type != appsv1.RollingUpdateStatefulSetStrategyType || (strategy.RollingUpdate != nil && strategy.RollingUpdate.Partition != nil && *strategy.RollingUpdate.Partition > 0) ||
		(ss.Spec.Ordinals != nil && ss.Spec.Ordinals.Start != 0) {
		return 0, nil
	}
	for _, key := range []string{lifecycleOperationAnnotation, backupGuardAnnotation, restoreGuardAnnotation, wipeGuardAnnotation, hostportStopFenceAnnotation} {
		if ss.Annotations[key] != "" {
			return 0, nil
		}
	}

	var live gp.GameServer
	if err := reader.Get(ctx, key, &live); err != nil {
		return 0, client.IgnoreNotFound(err)
	}
	_, held, err := lifecycleOwner(&live)
	if err != nil || held || live.UID != gs.UID || live.Generation != gs.Generation ||
		!live.DeletionTimestamp.IsZero() || live.Spec.Suspend || live.Annotations[stopRequestedAtAnnotation] != "" ||
		live.Annotations[IdleAsleepSinceAnnotation] != "" || pendingWipe(&live) || tokenPending(&live, RestartRequestedAnnotation, RestartCompletedAnnotation) {
		return 0, nil
	}

	preconditions := metav1.Preconditions{UID: &pod.UID, ResourceVersion: &pod.ResourceVersion}
	eviction := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}, DeleteOptions: &metav1.DeleteOptions{Preconditions: &preconditions}}
	err = r.SubResource("eviction").Create(ctx, &pod, eviction)
	if apierrors.IsTooManyRequests(err) || apierrors.IsConflict(err) {
		return 5 * time.Second, nil
	}
	return 0, client.IgnoreNotFound(err)
}
