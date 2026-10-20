package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// nodeAnnotation records which node the game pod is currently scheduled on.
// The dashboard's Servers table reads it directly. It carries observed runtime
// state as an annotation rather than a CRD status field so surfacing node
// placement needs no schema change; the operator refreshes it each reconcile.
const nodeAnnotation = "gameplane.local/node"

// reconcileNodePlacement keeps the gameplane.local/node annotation in sync with
// the node the game pod (<name>-0) is scheduled on: set once the pod has a
// node, cleared when no pod is running (stopped / suspended / not yet
// scheduled). It patches metadata only — separate from the status subresource
// and the agent's heartbeat patch, so there is nothing to race — and only when
// the value actually changes, so it never loops on its own update event.
func (r *GameServerReconciler) reconcileNodePlacement(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	node := ""
	var pod corev1.Pod
	err := r.Get(ctx, types.NamespacedName{Name: gs.Name + "-0", Namespace: gs.Namespace}, &pod)
	switch {
	case apierrors.IsNotFound(err):
		// No pod (stopped/suspended, or not created yet) — placement unknown.
	case err != nil:
		return fmt.Errorf("get pod %s-0: %w", gs.Name, err)
	default:
		node = pod.Spec.NodeName
	}

	// Up to date already (an absent annotation reads as "", matching an
	// unscheduled pod) — nothing to write, so no self-triggered reconcile.
	if gs.Annotations[nodeAnnotation] == node {
		return nil
	}

	base := gs.DeepCopy()
	if node == "" {
		delete(gs.Annotations, nodeAnnotation)
	} else {
		if gs.Annotations == nil {
			gs.Annotations = map[string]string{}
		}
		gs.Annotations[nodeAnnotation] = node
	}
	return r.Patch(ctx, gs, client.MergeFrom(base))
}
