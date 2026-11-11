package controller

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestRestrictTunnelEgressReplacesLegacyPlayitRules(t *testing.T) {
	scheme := testScheme(t)
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	r := &GameServerReconciler{Scheme: scheme, Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "one"}}
	gs.Spec.Networking.Tunnel = &gameplanev1alpha1.GameServerTunnel{Provider: "playit"}
	tmpl := singlePortTemplate(true, "minecraft")
	reconcile := func() []networkingv1.NetworkPolicyEgressRule {
		t.Helper()
		if err := r.reconcileTunnelNetworkPolicy(t.Context(), gs, tmpl, tunnelPlan{wantTunnel: true}); err != nil {
			t.Fatal(err)
		}
		var np networkingv1.NetworkPolicy
		if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-tunnel-egress"}, &np); err != nil {
			t.Fatal(err)
		}
		return np.Spec.Egress
	}
	if rules := reconcile(); len(rules) != 2 {
		t.Fatalf("unexpected compatibility rules: %+v", rules)
	}
	r.RestrictTunnelEgress = true
	rules := reconcile()
	if len(rules) != 1 || len(rules[0].To) != 1 || !reflect.DeepEqual(rules[0].Ports, networkPolicyPortsFromTemplate(tmpl)) {
		t.Fatalf("unexpected restricted rules: %+v", rules)
	}
	peer := rules[0].To[0]
	if peer.PodSelector == nil || peer.NamespaceSelector != nil || peer.IPBlock != nil {
		t.Fatalf("expected a pod peer in the local namespace: %+v", peer)
	}
	sel, err := metav1.LabelSelectorAsSelector(peer.PodSelector)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		name, instance string
		allowed        bool
	}{
		{"gameplane-game", "alpha", true}, {"gameplane-waker", "alpha", true},
		{"gameplane-game", "beta", false}, {"gameplane-operator", "alpha", false},
	} {
		if sel.Matches(labels.Set{"app.kubernetes.io/name": target.name, "app.kubernetes.io/instance": target.instance}) != target.allowed {
			t.Fatalf("unexpected peer match for %+v", target)
		}
	}
	var ingress networkingv1.NetworkPolicy
	inKey := client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-tunnel-ingress"}
	if err := r.Get(t.Context(), inKey, &ingress); err != nil {
		t.Fatal(err)
	}
	wantTunnel := metav1.LabelSelector{MatchLabels: map[string]string{
		tunnelLabel: tunnelValue, "app.kubernetes.io/instance": gs.Name,
	}}
	wantRules := []networkingv1.NetworkPolicyIngressRule{{
		From: []networkingv1.NetworkPolicyPeer{{PodSelector: &wantTunnel}}, Ports: rules[0].Ports,
	}}
	if !reflect.DeepEqual(ingress.Spec.PodSelector, *peer.PodSelector) ||
		!reflect.DeepEqual(ingress.Spec.Ingress, wantRules) || !metav1.IsControlledBy(&ingress, gs) {
		t.Fatalf("unexpected ingress policy: %+v", ingress)
	}
	absent := func(key client.ObjectKey) {
		t.Helper()
		if err := r.Get(t.Context(), key, &networkingv1.NetworkPolicy{}); !apierrors.IsNotFound(err) {
			t.Fatalf("expected %s to be absent, got %v", key, err)
		}
	}
	r.RestrictTunnelEgress = false
	reconcile()
	absent(inKey)
	r.RestrictTunnelEgress = true
	reconcile()
	tmpl.Spec.Ports = nil
	if rules := reconcile(); len(rules) != 0 {
		t.Fatalf("empty advertised port set grants egress: %+v", rules)
	}
	absent(inKey)
	tmpl = singlePortTemplate(true, "minecraft")
	reconcile()
	if err := r.reconcileTunnelNetworkPolicy(t.Context(), gs, tmpl, tunnelPlan{}); err != nil {
		t.Fatal(err)
	}
	absent(inKey)
	absent(client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-tunnel-egress"})

	foreign := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: inKey.Name, Namespace: inKey.Namespace, UID: "foreign"}}
	if err := r.Create(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileTunnelNetworkPolicy(t.Context(), gs, tmpl, tunnelPlan{wantTunnel: true}); err == nil {
		t.Fatal("accepted foreign ingress policy")
	}
	if err := r.reconcileTunnelNetworkPolicy(t.Context(), gs, tmpl, tunnelPlan{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(t.Context(), inKey, &ingress); err != nil || ingress.UID != foreign.UID || len(ingress.OwnerReferences) != 0 {
		t.Fatalf("changed foreign ingress policy: %+v, %v", ingress, err)
	}
}

// TestBuildFrpRemotePortsConfig covers F-052: the BACKING_SERVICE_PORT env
// var must carry the template port's own containerPort and protocol
// alongside the operator-chosen public remotePort, not just the remotePort
// alone (which only worked when it happened to equal the Service port and
// the port was TCP).
func TestBuildFrpRemotePortsConfig(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Ports: []gameplanev1alpha1.GamePort{
				{Name: "game", ContainerPort: 34197, Protocol: corev1.ProtocolUDP, Advertise: true},
				{Name: "query", ContainerPort: 34197, Protocol: corev1.ProtocolUDP, Advertise: false},
				{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
			},
		},
	}

	t.Run("udp port carries local port and protocol", func(t *testing.T) {
		frp := &gameplanev1alpha1.FrpTunnelSpec{
			RemotePorts: []gameplanev1alpha1.RemotePortMapping{{Name: "game", RemotePort: 30000}},
		}
		got := buildFrpRemotePortsConfig(frp, tmpl)
		want := "game:34197:30000:udp"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("tcp port defaults protocol to tcp when unset", func(t *testing.T) {
		tcpTmpl := &gameplanev1alpha1.GameTemplate{
			Spec: gameplanev1alpha1.GameTemplateSpec{
				Ports: []gameplanev1alpha1.GamePort{
					{Name: "game", ContainerPort: 25565, Advertise: true},
				},
			},
		}
		frp := &gameplanev1alpha1.FrpTunnelSpec{
			RemotePorts: []gameplanev1alpha1.RemotePortMapping{{Name: "game", RemotePort: 25565}},
		}
		got := buildFrpRemotePortsConfig(frp, tcpTmpl)
		want := "game:25565:25565:tcp"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("multiple mappings joined with comma", func(t *testing.T) {
		multiTmpl := &gameplanev1alpha1.GameTemplate{
			Spec: gameplanev1alpha1.GameTemplateSpec{
				Ports: []gameplanev1alpha1.GamePort{
					{Name: "java", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
					{Name: "bedrock", ContainerPort: 19132, Protocol: corev1.ProtocolUDP, Advertise: true},
				},
			},
		}
		frp := &gameplanev1alpha1.FrpTunnelSpec{
			RemotePorts: []gameplanev1alpha1.RemotePortMapping{
				{Name: "java", RemotePort: 25565},
				{Name: "bedrock", RemotePort: 19133},
			},
		}
		got := buildFrpRemotePortsConfig(frp, multiTmpl)
		want := "java:25565:25565:tcp,bedrock:19132:19133:udp"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("mapping with no matching template port is skipped", func(t *testing.T) {
		frp := &gameplanev1alpha1.FrpTunnelSpec{
			RemotePorts: []gameplanev1alpha1.RemotePortMapping{{Name: "nonexistent", RemotePort: 30000}},
		}
		got := buildFrpRemotePortsConfig(frp, tmpl)
		if got != "" {
			t.Fatalf("got %q, want empty string", got)
		}
	})

	t.Run("nil frp returns empty", func(t *testing.T) {
		if got := buildFrpRemotePortsConfig(nil, tmpl); got != "" {
			t.Fatalf("got %q, want empty string", got)
		}
	})

	t.Run("nil template returns empty", func(t *testing.T) {
		frp := &gameplanev1alpha1.FrpTunnelSpec{
			RemotePorts: []gameplanev1alpha1.RemotePortMapping{{Name: "game", RemotePort: 30000}},
		}
		if got := buildFrpRemotePortsConfig(frp, nil); got != "" {
			t.Fatalf("got %q, want empty string", got)
		}
	})
}
