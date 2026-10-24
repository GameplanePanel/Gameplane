package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestSentinelIsolationPolicies(t *testing.T) {
	scheme := runtime.NewScheme()
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
