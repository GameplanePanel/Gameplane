package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestSentinelIsolationPolicies(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := gameplanev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &GameServerReconciler{Client: c, Scheme: scheme}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "original"}}
	tmpl := singlePortTemplate(true, "minecraft")
	tmpl.Spec.SentinelIsolation = true
	tmpl.Spec.Ports = append(tmpl.Spec.Ports,
		gameplanev1alpha1.GamePort{Name: "query", ContainerPort: 19132, Protocol: corev1.ProtocolUDP, Advertise: true},
		gameplanev1alpha1.GamePort{Name: "management", ContainerPort: 25575, Protocol: corev1.ProtocolTCP})
	if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, target, peer string
		ingress            bool
	}{
		{"alpha-sentinel-ingress", "gameplane-game", "gameplane-waker", true},
		{"alpha-sentinel-egress", "gameplane-waker", "gameplane-game", false},
	} {
		var policy networkingv1.NetworkPolicy
		if err := c.Get(t.Context(), client.ObjectKey{Namespace: "games", Name: tc.name}, &policy); err != nil {
			t.Fatal(err)
		}
		if !metav1.IsControlledBy(&policy, gs) {
			t.Fatal("missing server ownership")
		}
		selector := policy.Spec.PodSelector.MatchLabels
		if selector["app.kubernetes.io/name"] != tc.target || selector["app.kubernetes.io/instance"] != "alpha" {
			t.Fatalf("wrong target: %v", selector)
		}
		var peers []networkingv1.NetworkPolicyPeer
		var ports []networkingv1.NetworkPolicyPort
		if tc.ingress {
			if len(policy.Spec.Ingress) != 1 || len(policy.Spec.Egress) != 0 {
				t.Fatal("wrong ingress policy shape")
			}
			peers, ports = policy.Spec.Ingress[0].From, policy.Spec.Ingress[0].Ports
		} else {
			if len(policy.Spec.Egress) != 1 || len(policy.Spec.Ingress) != 0 {
				t.Fatal("wrong egress policy shape")
			}
			peers, ports = policy.Spec.Egress[0].To, policy.Spec.Egress[0].Ports
		}
		if len(peers) != 1 || peers[0].NamespaceSelector != nil || peers[0].IPBlock != nil || peers[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != tc.peer || peers[0].PodSelector.MatchLabels["app.kubernetes.io/instance"] != "alpha" {
			t.Fatalf("cross-server peer grant: %+v", peers)
		}
		if len(ports) != 2 || ports[0].Port.IntVal != 25565 || *ports[0].Protocol != corev1.ProtocolTCP || ports[1].Port.IntVal != 19132 || *ports[1].Protocol != corev1.ProtocolUDP {
			t.Fatalf("wrong protocol/port tuples: %+v", ports)
		}
	}
	// Zero advertised ports removes both grants instead of opening all ports.
	tmpl.Spec.Ports = nil
	if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
		t.Fatal(err)
	}
	var policies networkingv1.NetworkPolicyList
	if err := c.List(t.Context(), &policies); err != nil || len(policies.Items) != 0 {
		t.Fatalf("zero-port grants remain: %v %+v", err, policies.Items)
	}
	// Absent/default false preserves legacy behavior and creates no policies.
	tmpl = singlePortTemplate(true, "minecraft")
	if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
		t.Fatal(err)
	}
	if err := c.List(t.Context(), &policies); err != nil || len(policies.Items) != 0 {
		t.Fatalf("legacy template changed: %v %+v", err, policies.Items)
	}
}

func TestSentinelIsolationDoesNotDeleteForeignPolicy(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	_ = gameplanev1alpha1.AddToScheme(scheme)
	foreign := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "alpha-sentinel-ingress", Namespace: "games"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(foreign).Build()
	r := &GameServerReconciler{Client: c, Scheme: scheme}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "original"}}
	if err := r.reconcileSentinelIsolation(t.Context(), gs, singlePortTemplate(false, "none")); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(foreign), foreign); err != nil {
		t.Fatal("deleted foreign policy", err)
	}
}

type staleSentinelPodClient struct{ client.Client }

func (c staleSentinelPodClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if pods, ok := list.(*corev1.PodList); ok {
		pods.Items = nil
		return nil
	}
	return c.Client.List(ctx, list, opts...)
}

func TestSentinelIsolationOptOutWaitsForBothLivePodLabels(t *testing.T) {
	scheme := testScheme(t)
	_ = networkingv1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &GameServerReconciler{Client: staleSentinelPodClient{c}, APIReader: c, Scheme: scheme}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "original"}}
	tmpl := singlePortTemplate(true, "minecraft")
	tmpl.Spec.SentinelIsolation = true
	if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
		t.Fatal(err)
	}
	pods := []*corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "alpha-0", Namespace: gs.Namespace, Labels: map[string]string{"app.kubernetes.io/name": "gameplane-game", "app.kubernetes.io/instance": gs.Name, sentinelIsolationLabel: "true"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "alpha-waker", Namespace: gs.Namespace, Labels: map[string]string{"app.kubernetes.io/name": "gameplane-waker", "app.kubernetes.io/instance": gs.Name, sentinelIsolationLabel: "true"}}},
	}
	for _, pod := range pods {
		if err := c.Create(t.Context(), pod); err != nil {
			t.Fatal(err)
		}
	}
	tmpl.Spec.SentinelIsolation = false
	for remaining := 2; remaining >= 0; remaining-- {
		if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
			t.Fatal(err)
		}
		var policies networkingv1.NetworkPolicyList
		if err := c.List(t.Context(), &policies); err != nil {
			t.Fatal(err)
		}
		want := 2
		if remaining == 0 {
			want = 0
		}
		if len(policies.Items) != want {
			t.Fatalf("%d labeled pods remain: want %d grants, got %d", remaining, want, len(policies.Items))
		}
		if remaining > 0 {
			pod := pods[2-remaining]
			delete(pod.Labels, sentinelIsolationLabel)
			if err := c.Update(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSentinelIsolationRejectsNoAdvertisedPortsBeforeCreatingWorkloads(t *testing.T) {
	scheme := testScheme(t)
	_ = batchv1.AddToScheme(scheme)
	tmpl := singlePortTemplate(false, "none")
	tmpl.Name = "isolated"
	tmpl.Spec.SentinelIsolation = true
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{{Name: "management", ContainerPort: 25575, Protocol: corev1.ProtocolTCP}}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "original"}}
	gs.Spec.TemplateRef.Name = tmpl.Name
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(gs).WithObjects(gs, tmpl).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: scheme}
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(gs.Status.Conditions, "Ready")
	if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed || condition == nil || condition.Status != metav1.ConditionFalse || !strings.Contains(condition.Message, "sentinelIsolation requires at least one advertised port") {
		t.Fatalf("missing actionable isolation failure: %+v", gs.Status)
	}
	var workloads appsv1.StatefulSetList
	if err := c.List(t.Context(), &workloads); err != nil || len(workloads.Items) != 0 {
		t.Fatalf("invalid template created workloads: %v %+v", err, workloads)
	}
}

func TestSentinelIsolationOptOutWaitsForWorkloadControllers(t *testing.T) {
	scheme := testScheme(t)
	_ = networkingv1.AddToScheme(scheme)
	for _, test := range []struct {
		name   string
		object client.Object
	}{
		{"game-old-template", &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games"}, Spec: appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{sentinelIsolationLabel: "true"}}}}}},
		{"game-unobserved", &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", Generation: 2}, Status: appsv1.StatefulSetStatus{ObservedGeneration: 1}}},
		{"game-old-revision", &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games"}, Status: appsv1.StatefulSetStatus{CurrentRevision: "old", UpdateRevision: "new"}}},
		{"sentinel-old-template", &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "alpha-waker", Namespace: "games"}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{sentinelIsolationLabel: "true"}}}}}},
		{"sentinel-unobserved", &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "alpha-waker", Namespace: "games", Generation: 2}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1}}},
		{"sentinel-old-replicas", &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "alpha-waker", Namespace: "games"}, Status: appsv1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 1}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(test.object).Build()
			r := &GameServerReconciler{Client: c, APIReader: c, Scheme: scheme}
			gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "original"}}
			tmpl := singlePortTemplate(true, "minecraft")
			tmpl.Spec.SentinelIsolation = true
			if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
				t.Fatal(err)
			}
			tmpl.Spec.SentinelIsolation = false
			if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
				t.Fatal(err)
			}
			var policies networkingv1.NetworkPolicyList
			if err := c.List(t.Context(), &policies); err != nil || len(policies.Items) != 2 {
				t.Fatalf("old workload could recreate labeled pods without grants: %v %+v", err, policies.Items)
			}
		})
	}
}
