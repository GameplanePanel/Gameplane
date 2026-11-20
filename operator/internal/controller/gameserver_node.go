package controller

import (
	"context"
	"fmt"
	"net"
	"slices"
	"sort"

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

// nodePortExternalIP follows the live Service selector through sleep and
// wake, so the node it reports hosts the selected game or wake sentinel.
func (r *GameServerReconciler) nodePortExternalIP(ctx context.Context, svc *corev1.Service) (string, error) {
	if len(svc.Spec.Selector) == 0 {
		return "", nil
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(svc.Namespace), client.MatchingLabels(svc.Spec.Selector)); err != nil {
		return "", fmt.Errorf("list pods for Service %s: %w", svc.Name, err)
	}
	// A rollout may briefly have two pods. Prefer a ready backend, with a
	// stable tie-break so an informer list's order can't change the address.
	sort.Slice(pods.Items, func(i, j int) bool {
		a, b := &pods.Items[i], &pods.Items[j]
		if aReady, bReady := podReady(a), podReady(b); aReady != bReady {
			return aReady
		}
		return a.Name < b.Name
	})
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName == "" || !pod.DeletionTimestamp.IsZero() ||
			pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			continue
		}
		// Nodes have get-only RBAC. A cached client would start a cluster-wide
		// informer that requires list and watch.
		var node corev1.Node
		if err := r.hostportReader().Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, &node); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return "", fmt.Errorf("get node %s: %w", pod.Spec.NodeName, err)
		}
		if !node.DeletionTimestamp.IsZero() || !hostportNodeReady(&node) {
			continue
		}
		for _, addr := range node.Status.Addresses {
			ip := net.ParseIP(addr.Address)
			if addr.Type != corev1.NodeExternalIP || !ip.IsGlobalUnicast() {
				continue
			}
			family := corev1.IPv6Protocol
			if ip.To4() != nil {
				family = corev1.IPv4Protocol
			}
			if len(svc.Spec.IPFamilies) == 0 || slices.Contains(svc.Spec.IPFamilies, family) {
				return addr.Address, nil
			}
		}
	}
	return "", nil
}
