package controller

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestValidateSidecarPortsRefusesAdvertisedSidecarPorts(t *testing.T) {
	for _, tc := range []struct {
		port int32
		want string
	}{{8090, "agent control"}, {9090, "agent metrics"}, {9091, "capture sidecar"}} {
		for _, proto := range []corev1.Protocol{"", corev1.ProtocolTCP} {
			tmpl := &gameplanev1alpha1.GameTemplate{}
			tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{{Name: "web", ContainerPort: tc.port, Protocol: proto, Advertise: true}}
			err := validateSidecarPorts(tmpl)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("port %d/%q: got %v, want an error naming %q", tc.port, proto, err, tc.want)
			}
			var rejected workloadPolicyRejection
			if !errors.As(err, &rejected) {
				t.Errorf("port %d/%q: %T is not a workloadPolicyRejection", tc.port, proto, err)
			}
		}
	}
}

func TestValidateSidecarPortsAllowsOtherPorts(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 25565, Advertise: true},
		// The sidecars listen on TCP only.
		{Name: "voice", ContainerPort: 9090, Protocol: corev1.ProtocolUDP, Advertise: true},
		// Not advertised: never published.
		{Name: "status", ContainerPort: 9091, Advertise: false},
	}
	if err := validateSidecarPorts(tmpl); err != nil {
		t.Fatalf("validateSidecarPorts: %v", err)
	}
}

func TestImageAllowed(t *testing.T) {
	prefixes := []string{"ghcr.io/gameplanepanel", "docker.io/itzg", "registry.local:5001", "docker.io/library/busybox"}
	for image, want := range map[string]bool{
		"ghcr.io/gameplanepanel/agent:1.0":          true,
		"GHCR.io/GameplanePanel/agent@sha256:abc":   true,
		"ghcr.io/gameplanepanel-evil/agent:1.0":     false,
		"ghcr.io/other/agent:1.0":                   false,
		"itzg/minecraft-server:java21":              true,
		"docker.io/itzg/minecraft-server":           true,
		"index.docker.io/itzg/minecraft-server":     true,
		"itzgevil/minecraft-server":                 false,
		"registry.local:5001/games/valheim:1":       true,
		"registry.local:50011/games/valheim:1":      false,
		"busybox:1.38.0":                            true,
		"docker.io/busybox":                         true,
		"nginx":                                     false,
		"localhost/registry.local:5001/x":           false,
		"evil.example/ghcr.io/gameplanepanel/agent": false,
	} {
		if got := imageAllowed(image, prefixes); got != want {
			t.Errorf("imageAllowed(%q) = %v, want %v", image, got, want)
		}
	}
	if !imageAllowed("ghcr.io/anything/x:1", []string{"ghcr.io"}) {
		t.Error("a bare registry host must allow every image on it")
	}
	if imageAllowed("ghcr.io.evil/x:1", []string{"ghcr.io"}) {
		t.Error("a bare registry host must match on a path boundary")
	}
}

func TestValidateImageOverride(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Spec.Image = "evil.example/miner:latest"
	if err := (&GameServerReconciler{}).validateImageOverride(gs); err != nil {
		t.Fatalf("an empty allowlist must leave the override open: %v", err)
	}
	r := &GameServerReconciler{AllowedImageRegistries: []string{"ghcr.io"}}
	err := r.validateImageOverride(gs)
	var rejected workloadPolicyRejection
	if !errors.As(err, &rejected) {
		t.Fatalf("got %v, want a workloadPolicyRejection", err)
	}
	gs.Spec.Image = "ghcr.io/gameplanepanel/fork:1"
	if err := r.validateImageOverride(gs); err != nil {
		t.Fatalf("allowed registry refused: %v", err)
	}
	gs.Spec.Image = ""
	if err := r.validateImageOverride(gs); err != nil {
		t.Fatalf("no override refused: %v", err)
	}
}

func TestValidateServiceAccountOverride(t *testing.T) {
	s := wipeScheme(t)
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "alpha-uid"}}
	ctrlTrue := true
	owned := func(name, owner string, uid string) *corev1.ServiceAccount {
		return &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "games", OwnerReferences: []metav1.OwnerReference{{
			APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: owner, UID: types.UID(uid), Controller: &ctrlTrue,
		}}}}
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		owned("beta-agent", "beta", "beta-uid"),
		owned("alpha-custom", "alpha", "alpha-uid"),
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "chart-sa", Namespace: "games", Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "game-runner", Namespace: "games"}},
	).Build()
	r := &GameServerReconciler{Client: c, Scheme: s}
	for name, refused := range map[string]bool{
		"":                           false,
		agentServiceAccountName(gs):  false,
		"game-runner":                false,
		"alpha-custom":               false,
		"not-created-yet":            false,
		"beta-agent":                 true,
		"chart-sa":                   true,
		tunnelServiceAccountName(gs): true,
		sentinelSAName(gs):           true,
	} {
		gs.Spec.ServiceAccountName = name
		err := r.validateServiceAccountOverride(t.Context(), gs)
		var rejected workloadPolicyRejection
		if got := errors.As(err, &rejected); got != refused {
			t.Errorf("serviceAccountName %q: got %v, want refused=%v", name, err, refused)
		}
		if !refused && err != nil {
			t.Errorf("serviceAccountName %q: unexpected error %v", name, err)
		}
	}
}

func TestLegacyTunnelEgressPublicOnly(t *testing.T) {
	tcp := corev1.ProtocolTCP
	game := intstr.FromInt(25565)
	advertised := []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &game}}
	backend := metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/instance": "alpha"}}
	frp := func(addr string) *gameplanev1alpha1.GameServerTunnel {
		return &gameplanev1alpha1.GameServerTunnel{Provider: "frp", Frp: &gameplanev1alpha1.FrpTunnelSpec{ServerAddr: addr, ServerPort: 7100}}
	}
	public := []networkingv1.NetworkPolicyPeer{
		{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}}},
		{IPBlock: &networkingv1.IPBlock{CIDR: "::/0", Except: []string{"fc00::/7", "fe80::/10"}}},
	}
	withPeers := func(cidrs ...string) []networkingv1.NetworkPolicyPeer {
		peers := append([]networkingv1.NetworkPolicyPeer(nil), public...)
		for _, cidr := range cidrs {
			peers = append(peers, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: cidr}})
		}
		return peers
	}

	rules := legacyTunnelEgress(frp("relay.example"), advertised, backend, nil, nil)
	if len(rules) != 3 {
		t.Fatalf("frp rules = %+v, want dns, provider and backend", rules)
	}
	if len(rules[0].To) != 0 || len(rules[0].Ports) != 2 {
		t.Fatalf("dns rule = %+v", rules[0])
	}
	if !reflect.DeepEqual(rules[1].To, public) || len(rules[1].Ports) != 1 || rules[1].Ports[0].Port.IntValue() != 7100 {
		t.Fatalf("frp provider rule = %+v, want port 7100 to public addresses", rules[1])
	}
	if len(rules[2].To) != 1 || !reflect.DeepEqual(*rules[2].To[0].PodSelector, backend) || !reflect.DeepEqual(rules[2].Ports, advertised) {
		t.Fatalf("backend rule = %+v", rules[2])
	}

	// A frp server written as a private IP keeps working; loopback and
	// link-local (cloud metadata) are never granted.
	for addr, want := range map[string][]networkingv1.NetworkPolicyPeer{
		"192.168.1.20":    withPeers("192.168.1.20/32"),
		" 10.1.2.3 ":      withPeers("10.1.2.3/32"),
		"[fd00::1]":       withPeers("fd00::1/128"),
		"169.254.169.254": public,
		"127.0.0.1":       public,
		"0.0.0.0":         public,
		"fe80::1":         public,
	} {
		if got := legacyTunnelEgress(frp(addr), advertised, backend, nil, nil)[1].To; !reflect.DeepEqual(got, want) {
			t.Errorf("frp serverAddr %q: To = %+v, want %+v", addr, got, want)
		}
	}

	// Administrator ranges: custom excepts split by family, allows added once.
	got := legacyTunnelEgress(frp("relay.example"), advertised, backend,
		[]string{"10.0.0.0/8", "fd00::/8"}, []string{"192.168.1.0/24", "192.168.1.0/24"})[1].To
	want := []networkingv1.NetworkPolicyPeer{
		{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"10.0.0.0/8"}}},
		{IPBlock: &networkingv1.IPBlock{CIDR: "::/0", Except: []string{"fd00::/8"}}},
		{IPBlock: &networkingv1.IPBlock{CIDR: "192.168.1.0/24"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("custom ranges: To = %+v, want %+v", got, want)
	}

	tailscale := legacyTunnelEgress(&gameplanev1alpha1.GameServerTunnel{Provider: "tailscale"}, advertised, backend, nil, nil)
	if len(tailscale) != 3 || !reflect.DeepEqual(tailscale[1].To, public) || len(tailscale[1].Ports) != 2 {
		t.Fatalf("tailscale rules = %+v", tailscale)
	}

	playit := legacyTunnelEgress(&gameplanev1alpha1.GameServerTunnel{Provider: "playit"}, nil, backend, nil, nil)
	if len(playit) != 2 || len(playit[1].Ports) != 0 || !reflect.DeepEqual(playit[1].To, public) {
		t.Fatalf("playit rules = %+v, want dns and all ports to public addresses only", playit)
	}

	// No provider rule without a provider configuration.
	if rules := legacyTunnelEgress(&gameplanev1alpha1.GameServerTunnel{Provider: "frp"}, nil, backend, nil, nil); len(rules) != 1 {
		t.Fatalf("frp without config = %+v, want dns only", rules)
	}
}

func TestTunnelPodProviderLabelOnlyForPlayit(t *testing.T) {
	s := wipeScheme(t)
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games", UID: "server-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Enabled: true, Provider: "playit", Playit: &gameplanev1alpha1.PlayitTunnelSpec{}}}}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: c, Scheme: s}
	for _, provider := range []string{"playit", "frp"} {
		gs.Spec.Networking.Tunnel.Provider = provider
		if provider == "frp" {
			gs.Spec.Networking.Tunnel.Frp = &gameplanev1alpha1.FrpTunnelSpec{ServerAddr: "relay.example"}
		}
		if err := r.reconcileTunnel(t.Context(), gs, singlePortTemplate(true, "minecraft"), true); err != nil {
			t.Fatal(err)
		}
		var dep appsv1.Deployment
		if err := c.Get(t.Context(), client.ObjectKey{Namespace: "games", Name: "server-tunnel"}, &dep); err != nil {
			t.Fatal(err)
		}
		label, ok := dep.Spec.Template.Labels[tunnelProviderLabel]
		if (provider == "playit") != (ok && label == "playit") {
			t.Fatalf("%s tunnel pod labels = %v", provider, dep.Spec.Template.Labels)
		}
	}
}
