package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestSvcPortsFromTemplate(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Ports: []gameplanev1alpha1.GamePort{
				{Name: "game", ContainerPort: 25565, Advertise: true},
				{Name: "internal", ContainerPort: 9999, Advertise: false},
				{Name: "udp", ContainerPort: 19132, Protocol: corev1.ProtocolUDP, Advertise: true},
			},
		},
	}

	t.Run("filters by advertise", func(t *testing.T) {
		gs := &gameplanev1alpha1.GameServer{}
		got := svcPortsFromTemplate(tmpl, gs)
		if len(got) != 2 {
			t.Fatalf("got %d ports", len(got))
		}
		for _, p := range got {
			if p.Name == "internal" {
				t.Fatal("internal port should be filtered out")
			}
		}
	})

	t.Run("default protocol is TCP", func(t *testing.T) {
		gs := &gameplanev1alpha1.GameServer{}
		got := svcPortsFromTemplate(tmpl, gs)
		var game corev1.ServicePort
		for _, p := range got {
			if p.Name == "game" {
				game = p
				break
			}
		}
		if game.Protocol != corev1.ProtocolTCP {
			t.Fatalf("game.Protocol=%q", game.Protocol)
		}
	})

	t.Run("preserves explicit UDP", func(t *testing.T) {
		gs := &gameplanev1alpha1.GameServer{}
		got := svcPortsFromTemplate(tmpl, gs)
		var udp corev1.ServicePort
		for _, p := range got {
			if p.Name == "udp" {
				udp = p
				break
			}
		}
		if udp.Protocol != corev1.ProtocolUDP {
			t.Fatalf("udp.Protocol=%q", udp.Protocol)
		}
	})

	t.Run("port override remaps service port + nodeport", func(t *testing.T) {
		gs := &gameplanev1alpha1.GameServer{
			Spec: gameplanev1alpha1.GameServerSpec{
				Networking: gameplanev1alpha1.GameServerNetworking{
					PortOverrides: []gameplanev1alpha1.PortOverride{
						{Name: "game", ServicePort: 30000, NodePort: 30001},
					},
				},
			},
		}
		got := svcPortsFromTemplate(tmpl, gs)
		var game corev1.ServicePort
		for _, p := range got {
			if p.Name == "game" {
				game = p
				break
			}
		}
		if game.Port != 30000 {
			t.Fatalf("Port=%d, want 30000", game.Port)
		}
		if game.NodePort != 30001 {
			t.Fatalf("NodePort=%d, want 30001", game.NodePort)
		}
		if game.TargetPort.IntValue() != 25565 {
			t.Fatalf("TargetPort=%v, want 25565", game.TargetPort)
		}
	})

	t.Run("override with zero ServicePort keeps the original", func(t *testing.T) {
		gs := &gameplanev1alpha1.GameServer{
			Spec: gameplanev1alpha1.GameServerSpec{
				Networking: gameplanev1alpha1.GameServerNetworking{
					PortOverrides: []gameplanev1alpha1.PortOverride{
						{Name: "game", NodePort: 30002},
					},
				},
			},
		}
		got := svcPortsFromTemplate(tmpl, gs)
		var game corev1.ServicePort
		for _, p := range got {
			if p.Name == "game" {
				game = p
				break
			}
		}
		if game.Port != 25565 {
			t.Fatalf("Port=%d, want 25565 (original)", game.Port)
		}
		if game.NodePort != 30002 {
			t.Fatalf("NodePort=%d, want 30002", game.NodePort)
		}
	})
}
