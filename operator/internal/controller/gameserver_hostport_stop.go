package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
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
	if fence.PodUID == "" || fence.NodeUID == "" {
		return fence, false
	}
	switch fence.Strategy.Type {
	case appsv1.OnDeleteStatefulSetStrategyType, appsv1.RecreateStatefulSetStrategyType:
		return fence, fence.Strategy.RollingUpdate == nil
	case appsv1.RollingUpdateStatefulSetStrategyType:
		rolling := fence.Strategy.RollingUpdate
		if rolling == nil {
			return fence, true
		}
		if rolling.Partition != nil && *rolling.Partition < 0 {
			return fence, false
		}
		if rolling.MaxUnavailable == nil {
			return fence, true
		}
		// Check the finite saved-strategy fields before restoring them: an
		// API-invalid payload must not reject an otherwise required scale-down.
		value := rolling.MaxUnavailable
		switch value.Type {
		case intstr.Int:
			return fence, value.IntVal > 0
		case intstr.String:
			if len(validation.IsValidPercent(value.StrVal)) != 0 {
				return fence, false
			}
			percent, err := strconv.Atoi(strings.TrimSuffix(value.StrVal, "%"))
			return fence, err == nil && percent > 0 && percent <= 100
		default:
			return fence, false
		}
	default:
		return fence, false
	}
}

func clearHostportStopFence(ss *appsv1.StatefulSet) {
	raw := ss.Annotations[hostportStopFenceAnnotation]
	if raw == "" {
		return
	}
	if fence, valid := decodeHostportStopFence(raw); valid {
		ss.Spec.UpdateStrategy = fence.Strategy
	}
	// Unknown saved state must not replace the live strategy or obstruct
	// required scale-down. Keep that strategy and retire only our own fence.
	ss.Spec.Template.Spec.SchedulingGates = slices.DeleteFunc(ss.Spec.Template.Spec.SchedulingGates, func(gate corev1.PodSchedulingGate) bool { return gate.Name == hostportStopSchedulingGate })
	delete(ss.Annotations, hostportStopFenceAnnotation)
}
