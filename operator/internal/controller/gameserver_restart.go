package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	// RestartRequestedAnnotation carries the token of a requested restart (set
	// by the API). RestartCompletedAnnotation echoes it back once the pod has
	// been recycled, so the same request never re-runs.
	//
	// Restart is an operator primitive rather than a client-side suspend/resume
	// pair precisely so it can't be lost: the token persists on the GameServer
	// until the operator has confirmed the pod is gone and brought a fresh one
	// up, whereas two rapid spec.suspend patches can coalesce into a no-op
	// between reconciles.
	RestartRequestedAnnotation = "gameplane.local/restart-requested"
	RestartCompletedAnnotation = "gameplane.local/restart-completed"

	// restartDrainPoll bounds how long the operator waits between checks while a
	// restart drains the pod. The StatefulSet watch already wakes the reconciler
	// on pod deletion; this is only a backstop.
	restartDrainPoll = 2 * time.Second
)

// restartDecision is the state of a pending restart for the current reconcile.
type restartDecision int

const (
	restartNone     restartDecision = iota // no pending token (or already acked)
	restartDraining                        // pending; the pod is still present — bring it down
	restartComplete                        // pending; the pod is gone this pass — ack + bring back up
)

// restartPhase reports whether a restart is pending and, if so, whether the
// pod has finished draining. "Gone" means the StatefulSet reports zero created
// pods (Status.Replicas == 0) or is absent — the strict "pod object deleted"
// signal, distinct from ReadyReplicas == 0 (merely not-ready). Only a real
// 1 → 0 → 1 transition gives pod <name>-0 a new identity, so the resume must
// wait for Status.Replicas to reach 0.
func (r *GameServerReconciler) restartPhase(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
) (restartDecision, error) {
	req := gs.Annotations[RestartRequestedAnnotation]
	done := gs.Annotations[RestartCompletedAnnotation]
	if req == "" || req == done {
		return restartNone, nil
	}

	var ss appsv1.StatefulSet
	switch err := r.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name}, &ss); {
	case apierrors.IsNotFound(err):
		return restartComplete, nil // never started, or already gone
	case err != nil:
		return restartNone, err
	}
	if ss.Status.Replicas == 0 {
		return restartComplete, nil
	}
	return restartDraining, nil
}

// ackRestart records that the pending restart finished (echoing the requested
// token) and clears the soft-stop grace clock, in a single merge patch. Doing
// both here means the grace bookkeeping is dropped exactly at completion rather
// than by the "running" branch mid-drain.
func (r *GameServerReconciler) ackRestart(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	base := gs.DeepCopy()
	if gs.Annotations == nil {
		gs.Annotations = map[string]string{}
	}
	gs.Annotations[RestartCompletedAnnotation] = gs.Annotations[RestartRequestedAnnotation]
	delete(gs.Annotations, stopRequestedAtAnnotation)
	return r.Patch(ctx, gs, client.MergeFrom(base))
}
