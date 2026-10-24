package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func hostportFixture(t *testing.T) (*GameServerReconciler, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	t.Helper()
	s := wipeScheme(t)
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: "game-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Expose: "Hostport"}, Idle: &gameplanev1alpha1.IdleSpec{Enabled: true, WakeOnConnect: true}}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", UID: "node-uid"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	zero := int32(0)
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace}, Spec: appsv1.StatefulSetSpec{Replicas: &zero}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, node, ss).Build()
	return &GameServerReconciler{Client: c, APIReader: c, Scheme: s}, gs, singlePortTemplate(true, "generic")
}

func TestHostportSleepCapturesNodeAndWaitsForActualPodDeletion(t *testing.T) {
	r, gs, tmpl := hostportFixture(t)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "game-0", Namespace: gs.Namespace, Labels: map[string]string{"app.kubernetes.io/name": "gameplane-game", "app.kubernetes.io/instance": gs.Name}, Finalizers: []string{"test/drain"}}, Spec: corev1.PodSpec{NodeName: "node-a"}}
	if err := r.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	plan, err := r.planSentinel(t.Context(), gs, tmpl, idleAsleep)
	if err != nil {
		t.Fatal(err)
	}
	if plan.wantSentinel || plan.hostportCondition.Reason != "GameDraining" {
		t.Fatalf("plan before actual deletion: %+v", plan)
	}
	var saved gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Annotations[hostportNodeAnnotation] != "node-a" || saved.Annotations[hostportNodeUIDAnnotation] != "node-uid" {
		t.Fatalf("durable endpoint not saved: %v", saved.Annotations)
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(pod), pod); err != nil {
		t.Fatal(err)
	}
	pod.Finalizers = nil
	if err := r.Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	plan, err = r.planSentinel(t.Context(), &saved, tmpl, idleAsleep)
	if err != nil || !plan.wantSentinel {
		t.Fatalf("plan after actual deletion: %+v, %v", plan, err)
	}
}

func TestHostportWakeUsesUncachedPodsAndWaitsForProducerDeletion(t *testing.T) {
	r, gs, tmpl := hostportFixture(t)
	gs.Annotations = map[string]string{hostportNodeAnnotation: "node-a", hostportNodeUIDAnnotation: "node-uid"}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-waker", Namespace: gs.Namespace}}
	if err := r.Create(t.Context(), dep); err != nil {
		t.Fatal(err)
	}
	// A cached view with no Pods must not authorize transfer while the live
	// API still has a terminating listener, even after its Deployment is gone.
	live := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(dep, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "old-waker", Namespace: gs.Namespace, Labels: map[string]string{sentinelLabel: sentinelValue, "app.kubernetes.io/instance": gs.Name}}}, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", UID: "node-uid"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}).Build()
	r.APIReader = live
	plan, err := r.planSentinel(t.Context(), gs, tmpl, idleAwake)
	if err != nil || !plan.blockGame || plan.wantSentinel {
		t.Fatalf("producer still live: %+v, %v", plan, err)
	}
	if err := live.Delete(t.Context(), dep); err != nil {
		t.Fatal(err)
	}
	plan, err = r.planSentinel(t.Context(), gs, tmpl, idleAwake)
	if err != nil || !plan.blockGame {
		t.Fatalf("live Pod bypassed by cache: %+v, %v", plan, err)
	}
	if err := live.Delete(t.Context(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "old-waker", Namespace: gs.Namespace}}); err != nil {
		t.Fatal(err)
	}
	plan, err = r.planSentinel(t.Context(), gs, tmpl, idleAwake)
	if err != nil || plan.blockGame {
		t.Fatalf("drained listener still blocks: %+v, %v", plan, err)
	}
}

func TestHostportNodeLossAndUnschedulableListenerStayUnavailable(t *testing.T) {
	r, gs, tmpl := hostportFixture(t)
	gs.Annotations = map[string]string{hostportNodeAnnotation: "node-a", hostportNodeUIDAnnotation: "node-uid"}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "waker", Namespace: gs.Namespace, Labels: map[string]string{sentinelLabel: sentinelValue, "app.kubernetes.io/instance": gs.Name}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "Insufficient memory"}}}}
	if err := r.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	plan, err := r.planSentinel(t.Context(), gs, tmpl, idleAsleep)
	if err != nil || plan.routeToSentinel || plan.hostportCondition.Reason != "EndpointUnschedulable" {
		t.Fatalf("full node: %+v, %v", plan, err)
	}
	if err := r.Delete(t.Context(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}); err != nil {
		t.Fatal(err)
	}
	plan, err = r.planSentinel(t.Context(), gs, tmpl, idleAsleep)
	if err != nil || plan.wantSentinel || plan.hostportCondition.Reason != "EndpointNodeUnavailable" {
		t.Fatalf("lost node: %+v, %v", plan, err)
	}
}

func TestHostportAffinityConjoinsEveryAlternativeAndPreservesOtherConstraints(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{hostportNodeAnnotation: "node-a"}}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Expose: "Hostport"}, Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "pool", Operator: corev1.NodeSelectorOpIn, Values: []string{"a"}}}}, {MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "pool", Operator: corev1.NodeSelectorOpIn, Values: []string{"b"}}}}}}}, PodAntiAffinity: &corev1.PodAntiAffinity{}}}}
	got := hostportAffinity(gs)
	for _, term := range got.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		if len(term.MatchFields) != 1 || term.MatchFields[0].Key != "metadata.name" || term.MatchFields[0].Values[0] != "node-a" {
			t.Fatalf("unpinned alternative: %+v", term)
		}
	}
	if got.PodAntiAffinity == nil || len(gs.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchFields) != 0 {
		t.Fatal("lost constraints or mutated CR")
	}
	// An explicitly empty selector matches no nodes; pinning must not widen it.
	gs.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = nil
	if len(hostportAffinity(gs).NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) != 0 {
		t.Fatal("empty selector widened")
	}
}
