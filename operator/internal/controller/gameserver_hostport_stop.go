package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const hostportStopFenceAnnotation = "gameplane.local/hostport-stop-fence"
const hostportStopSchedulingGate = "gameplane.local/hostport-endpoint-draining"

type hostportStopFence struct {
	PodUID   types.UID                        `json:"podUID"`
	NodeUID  types.UID                        `json:"nodeUID"`
	Strategy appsv1.StatefulSetUpdateStrategy `json:"strategy"`
}

// A deleting original node may still run the game's save sequence. Retaining
// replicas alone would also permit an independently recreated Pod to start.
// Persist OnDelete and a scheduling gate first: the original Pod is untouched,
// while every replacement remains unschedulable, even after operator restart.
// Called only inside the StatefulSet mutation after a live identity read.
func (r *GameServerReconciler) stopHostportOnDeletingNode(ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, ss *appsv1.StatefulSet) error {
	zero := int32(0)
	if ss.ResourceVersion == "" || ss.Spec.Replicas == nil || *ss.Spec.Replicas == 0 {
		ss.Spec.Replicas = &zero
		return nil
	}
	var node corev1.Node
	if err := r.hostportReader().Get(ctx, client.ObjectKey{Name: gs.Annotations[hostportNodeAnnotation]}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			ss.Spec.Replicas = &zero
		}
		return client.IgnoreNotFound(err)
	}
	if node.DeletionTimestamp.IsZero() || string(node.UID) != gs.Annotations[hostportNodeUIDAnnotation] {
		ss.Spec.Replicas = &zero
		return nil
	}
	var pod corev1.Pod
	if err := r.hostportReader().Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			ss.Spec.Replicas = &zero
		}
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(ss, gs) || !metav1.IsControlledBy(&pod, ss) || pod.UID == "" || pod.Spec.NodeName != node.Name || !pod.DeletionTimestamp.IsZero() || pod.Status.Phase != corev1.PodRunning {
		ss.Spec.Replicas = &zero
		return nil
	}
	fence := hostportStopFence{PodUID: pod.UID, NodeUID: node.UID, Strategy: *ss.Spec.UpdateStrategy.DeepCopy()}
	if raw := ss.Annotations[hostportStopFenceAnnotation]; raw != "" {
		saved, valid := decodeHostportStopFence(raw)
		if !valid || saved.PodUID != pod.UID || saved.NodeUID != node.UID {
			ss.Spec.Replicas = &zero
			return nil
		}
		fence = saved
		if ss.Spec.UpdateStrategy.Type == appsv1.OnDeleteStatefulSetStrategyType && slices.ContainsFunc(ss.Spec.Template.Spec.SchedulingGates, func(gate corev1.PodSchedulingGate) bool { return gate.Name == hostportStopSchedulingGate }) {
			if ss.Status.ObservedGeneration < ss.Generation {
				return nil // Wait until the StatefulSet controller has observed the fence.
			}
			op, held, err := lifecycleOwner(gs)
			if err != nil {
				return err
			}
			if held && (op.Kind == "Backup" || len(op.Recovery) != 0) {
				// A save/stop RPC must not write while another copy owns the
				// world. Preserve that owner's cleanup contract; orphaned
				// Restore recovery still requires the existing zero-replica stop.
				if lifecycleRecoveryNeedsStop(op) {
					ss.Spec.Replicas = &zero
				}
				return nil
			}
			replicas, _, err := r.softStop(ctx, gs, tmpl)
			if err != nil {
				return err
			}
			ss.Spec.Replicas = &replicas
			return nil
		}
	}
	data, err := json.Marshal(fence)
	if err != nil {
		return fmt.Errorf("encode Hostport stop fence: %w", err)
	}
	if ss.Annotations == nil {
		ss.Annotations = map[string]string{}
	}
	ss.Annotations[hostportStopFenceAnnotation] = string(data)
	ss.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
	if !slices.ContainsFunc(ss.Spec.Template.Spec.SchedulingGates, func(gate corev1.PodSchedulingGate) bool { return gate.Name == hostportStopSchedulingGate }) {
		ss.Spec.Template.Spec.SchedulingGates = append(ss.Spec.Template.Spec.SchedulingGates, corev1.PodSchedulingGate{Name: hostportStopSchedulingGate})
	}
	return nil // Commit the replacement fence before issuing the stop RPC.
}

// Malformed persisted state denies graceful-stop permission. It is a validation
// failure, rather than an operational error that could prevent scaling to zero.
func decodeHostportStopFence(raw string) (hostportStopFence, bool) {
	var fence hostportStopFence
	if err := json.Unmarshal([]byte(raw), &fence); err != nil {
		return fence, false
	}
	return fence, true
}

func clearHostportStopFence(ss *appsv1.StatefulSet) error {
	raw := ss.Annotations[hostportStopFenceAnnotation]
	if raw == "" {
		return nil
	}
	var fence hostportStopFence
	if err := json.Unmarshal([]byte(raw), &fence); err != nil {
		return fmt.Errorf("decode Hostport stop fence: %w", err)
	}
	ss.Spec.UpdateStrategy = fence.Strategy
	ss.Spec.Template.Spec.SchedulingGates = slices.DeleteFunc(ss.Spec.Template.Spec.SchedulingGates, func(gate corev1.PodSchedulingGate) bool { return gate.Name == hostportStopSchedulingGate })
	delete(ss.Annotations, hostportStopFenceAnnotation)
	return nil
}
