package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

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
