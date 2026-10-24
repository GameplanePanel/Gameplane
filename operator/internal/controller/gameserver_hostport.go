package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	// Unlike nodeAnnotation (the current Pod's dashboard location), these
	// survive sleep, Pod deletion and operator restarts. Reusing a node name
	// after replacement must not silently redirect the advertised endpoint.
	hostportNodeAnnotation    = "gameplane.local/hostport-node"
	hostportNodeUIDAnnotation = "gameplane.local/hostport-node-uid"
	hostportWakeCondition     = "HostportWakeReady"
)

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get

func (r *GameServerReconciler) hostportReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func hostportCondition(gs *gameplanev1alpha1.GameServer, reason, message string, ready bool) *metav1.Condition {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	return &metav1.Condition{Type: hostportWakeCondition, Status: status, Reason: reason, Message: message, ObservedGeneration: gs.Generation}
}

// planHostportSentinel never transfers a port based on replica status or a
// cached Pod absence. Producers must first stop, then their actual Pods must
// disappear. All waits return a plan so the remaining reconciliation proceeds.
func (r *GameServerReconciler) planHostportSentinel(ctx context.Context, gs *gameplanev1alpha1.GameServer, idle idleState) (sentinelPlan, error) {
	reader := r.hostportReader()
	var gamePods, sentinelPods corev1.PodList
	for _, entry := range []struct {
		name string
		pods *corev1.PodList
	}{{"gameplane-game", &gamePods}, {sentinelValue, &sentinelPods}} {
		if err := reader.List(ctx, entry.pods, client.InNamespace(gs.Namespace), client.MatchingLabels{"app.kubernetes.io/name": entry.name, "app.kubernetes.io/instance": gs.Name}); err != nil {
			return sentinelPlan{}, err
		}
	}
	plan := sentinelPlan{}
	hostportPending := func(reason, message string) {
		plan.hostportCondition = hostportCondition(gs, reason, message, false)
	}
	nodeName := gs.Annotations[hostportNodeAnnotation]
	// Record the endpoint before the first sleep scales down the game. Doing
	// this during sleep also avoids rolling an already-running game merely to
	// add the new affinity after an operator upgrade.
	if nodeName == "" && idle == idleAsleep {
		for _, pod := range gamePods.Items {
			if pod.Name == gs.Name+"-0" && pod.Spec.NodeName != "" {
				nodeName = pod.Spec.NodeName
				break
			}
		}
		if nodeName != "" {
			var node corev1.Node
			if err := reader.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err != nil {
				if !apierrors.IsNotFound(err) {
					return sentinelPlan{}, err
				}
			}
			base := gs.DeepCopy()
			if gs.Annotations == nil {
				gs.Annotations = map[string]string{}
			}
			gs.Annotations[hostportNodeAnnotation] = nodeName
			gs.Annotations[hostportNodeUIDAnnotation] = string(node.UID)
			if err := r.Patch(ctx, gs, client.MergeFrom(base)); err != nil {
				return sentinelPlan{}, err
			}
		}
	}
	if nodeName == "" && idle == idleAsleep {
		hostportPending("EndpointNodeUnknown", "No game endpoint node has been recorded; start the game before enabling sleep.")
		return plan, nil
	}
	if nodeName != "" {
		var node corev1.Node
		err := reader.Get(ctx, client.ObjectKey{Name: nodeName}, &node)
		nodeMissing := apierrors.IsNotFound(err)
		if err != nil && !nodeMissing {
			return plan, err
		}
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				ready = condition.Status == corev1.ConditionTrue
			}
		}
		if nodeMissing || !node.DeletionTimestamp.IsZero() || !ready || string(node.UID) != gs.Annotations[hostportNodeUIDAnnotation] {
			plan.blockGame = len(gamePods.Items) == 0
			hostportPending("EndpointNodeUnavailable", fmt.Sprintf("Hostport endpoint node %q is missing, replaced or not Ready; restore that node or explicitly migrate the endpoint.", nodeName))
			return plan, nil
		}
	}
	if idle == idleAsleep {
		var ss appsv1.StatefulSet
		err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss)
		if err != nil && !apierrors.IsNotFound(err) {
			return plan, err
		}
		if len(gamePods.Items) != 0 || (err == nil && (ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Status.ObservedGeneration < ss.Generation || ss.Status.Replicas != 0)) {
			hostportPending("GameDraining", "Waiting for the game Pod to disappear before acquiring its Hostport.")
			return plan, nil
		}
		plan.wantSentinel = true
		plan.hostportCondition = hostportListenerCondition(gs, sentinelPods.Items, nodeName)
		plan.routeToSentinel = plan.hostportCondition.Status == metav1.ConditionTrue
		return plan, nil
	}
	var dep appsv1.Deployment
	err := reader.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-waker"}, &dep)
	if err != nil && !apierrors.IsNotFound(err) {
		return plan, err
	}
	if err == nil || len(sentinelPods.Items) != 0 {
		plan.blockGame = true
		hostportPending("SentinelDraining", "Waiting for the sentinel Deployment and Pods to disappear before scheduling the game on its endpoint node.")
		return plan, nil
	}
	plan.hostportCondition = hostportListenerCondition(gs, gamePods.Items, nodeName)
	return plan, nil
}

func hostportListenerCondition(gs *gameplanev1alpha1.GameServer, pods []corev1.Pod, nodeName string) *metav1.Condition {
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse && condition.Reason == corev1.PodReasonUnschedulable {
				return hostportCondition(gs, "EndpointUnschedulable", fmt.Sprintf("Hostport listener cannot schedule on endpoint node %q: %s", nodeName, condition.Message), false)
			}
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue && (nodeName == "" || pod.Spec.NodeName == nodeName) {
				return hostportCondition(gs, "ListenerReady", "The listener is Ready on the Hostport endpoint node.", true)
			}
		}
	}
	return hostportCondition(gs, "ListenerPending", fmt.Sprintf("Waiting for a Ready listener on endpoint node %q.", nodeName), false)
}

// hostportAffinity ANDs node identity into every OR alternative, retaining
// user selectors, pod affinity and anti-affinity. Empty selectors still match
// no nodes; setting PodSpec.NodeName would bypass the scheduler's port checks.
func hostportAffinity(gs *gameplanev1alpha1.GameServer) *corev1.Affinity {
	affinity := gs.Spec.Affinity.DeepCopy()
	node := gs.Annotations[hostportNodeAnnotation]
	if gs.Spec.Networking.Expose != "Hostport" || node == "" {
		return affinity
	}
	if affinity == nil {
		affinity = &corev1.Affinity{}
	}
	if affinity.NodeAffinity == nil {
		affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	if affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{}}}
		// This newly created term intentionally matches the endpoint node.
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchFields = []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{node}}}
		return affinity
	}
	for i := range affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		term := &affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[i]
		if len(term.MatchExpressions) == 0 && len(term.MatchFields) == 0 {
			continue
		}
		term.MatchFields = append(term.MatchFields, corev1.NodeSelectorRequirement{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{node}})
	}
	return affinity
}
