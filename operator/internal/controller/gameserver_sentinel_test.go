package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// singlePortTemplate builds a GameTemplate with one port, letting each case
// control whether it's advertised and what wakeProtocol it declares.
func singlePortTemplate(advertise bool, wakeProtocol string) *gameplanev1alpha1.GameTemplate {
	return &gameplanev1alpha1.GameTemplate{
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Ports: []gameplanev1alpha1.GamePort{{
				Name:          "game",
				ContainerPort: 25565,
				Protocol:      corev1.ProtocolTCP,
				Advertise:     advertise,
				WakeProtocol:  wakeProtocol,
			}},
		},
	}
}

// TestWakeOnConnectEligible covers the eligibility gate shared by the
// sentinel Deployment (reconcileSentinel, via planSentinel) and the
// game-direct Service (reconcileGameDirectServiceFromTemplate). It used to
// be tested only indirectly, through envtest subtests that called
// reconcileSentinel with an idleState and asserted on the resulting K8s
// object — this is the fast, deterministic equivalent of those eligibility
// cases (armed/disarmed, wakeProtocol: none), pulled out as a pure-function
// table test the same way gameserver_idle_test.go tests idleDecide.
func TestWakeOnConnectEligible(t *testing.T) {
	cases := []struct {
		name string
		idle *gameplanev1alpha1.IdleSpec
		tmpl *gameplanev1alpha1.GameTemplate
		want bool
	}{
		{
			name: "armed with a wakeable advertised port",
			idle: &gameplanev1alpha1.IdleSpec{Enabled: true, WakeOnConnect: true},
			tmpl: singlePortTemplate(true, "minecraft"),
			want: true,
		},
		{
			name: "idle spec unset",
			idle: nil,
			tmpl: singlePortTemplate(true, "minecraft"),
			want: false,
		},
		{
			name: "idle policy disabled",
			idle: &gameplanev1alpha1.IdleSpec{Enabled: false, WakeOnConnect: true},
			tmpl: singlePortTemplate(true, "minecraft"),
			want: false,
		},
		{
			name: "wakeOnConnect not armed",
			idle: &gameplanev1alpha1.IdleSpec{Enabled: true, WakeOnConnect: false},
			tmpl: singlePortTemplate(true, "minecraft"),
			want: false,
		},
		{
			name: "every advertised port has wakeProtocol none",
			idle: &gameplanev1alpha1.IdleSpec{Enabled: true, WakeOnConnect: true},
			tmpl: singlePortTemplate(true, "none"),
			want: false,
		},
		{
			name: "wakeable port exists but is not advertised",
			idle: &gameplanev1alpha1.IdleSpec{Enabled: true, WakeOnConnect: true},
			tmpl: singlePortTemplate(false, "minecraft"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := &gameplanev1alpha1.GameServer{
				Spec: gameplanev1alpha1.GameServerSpec{Idle: tc.idle},
			}
			if got := wakeOnConnectEligible(gs, tc.tmpl); got != tc.want {
				t.Errorf("wakeOnConnectEligible() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBuildSentinelPortConfig covers the PORTS_CONFIG env var building logic.
// It exercises the fix for the index-based separator bug: when the first
// advertised port is not at index 0, the old code would produce a leading comma.
func TestBuildSentinelPortConfig(t *testing.T) {
	cases := []struct {
		name string
		tmpl *gameplanev1alpha1.GameTemplate
		want string
	}{
		{
			name: "advertised port first",
			tmpl: &gameplanev1alpha1.GameTemplate{
				Spec: gameplanev1alpha1.GameTemplateSpec{
					Ports: []gameplanev1alpha1.GamePort{{
						Name:          "game",
						ContainerPort: 25565,
						Protocol:      corev1.ProtocolTCP,
						Advertise:     true,
						WakeProtocol:  "minecraft",
					}},
				},
			},
			want: "25565:TCP:minecraft",
		},
		{
			name: "non-advertised port first, advertised port second (the bug)",
			tmpl: &gameplanev1alpha1.GameTemplate{
				Spec: gameplanev1alpha1.GameTemplateSpec{
					Ports: []gameplanev1alpha1.GamePort{
						{
							Name:          "rcon",
							ContainerPort: 25575,
							Protocol:      corev1.ProtocolTCP,
							Advertise:     false,
							WakeProtocol:  "none",
						},
						{
							Name:          "game",
							ContainerPort: 25565,
							Protocol:      corev1.ProtocolTCP,
							Advertise:     true,
							WakeProtocol:  "minecraft",
						},
					},
				},
			},
			want: "25565:TCP:minecraft",
		},
		{
			name: "multiple advertised ports with non-advertised ports interleaved",
			tmpl: &gameplanev1alpha1.GameTemplate{
				Spec: gameplanev1alpha1.GameTemplateSpec{
					Ports: []gameplanev1alpha1.GamePort{
						{
							Name:          "rcon",
							ContainerPort: 25575,
							Protocol:      corev1.ProtocolTCP,
							Advertise:     false,
							WakeProtocol:  "none",
						},
						{
							Name:          "game",
							ContainerPort: 25565,
							Protocol:      corev1.ProtocolTCP,
							Advertise:     true,
							WakeProtocol:  "minecraft",
						},
						{
							Name:          "query",
							ContainerPort: 25566,
							Protocol:      corev1.ProtocolUDP,
							Advertise:     false,
							WakeProtocol:  "none",
						},
						{
							Name:          "listeningport",
							ContainerPort: 19133,
							Protocol:      corev1.ProtocolUDP,
							Advertise:     true,
							WakeProtocol:  "generic",
						},
					},
				},
			},
			want: "25565:TCP:minecraft,19133:UDP:generic",
		},
		{
			name: "all ports non-advertised",
			tmpl: &gameplanev1alpha1.GameTemplate{
				Spec: gameplanev1alpha1.GameTemplateSpec{
					Ports: []gameplanev1alpha1.GamePort{
						{
							Name:          "rcon",
							ContainerPort: 25575,
							Protocol:      corev1.ProtocolTCP,
							Advertise:     false,
							WakeProtocol:  "none",
						},
					},
				},
			},
			want: "",
		},
		{
			name: "advertised port with empty Protocol (defaults to TCP)",
			tmpl: &gameplanev1alpha1.GameTemplate{
				Spec: gameplanev1alpha1.GameTemplateSpec{
					Ports: []gameplanev1alpha1.GamePort{
						{
							Name:          "game",
							ContainerPort: 25565,
							Protocol:      "",
							Advertise:     true,
							WakeProtocol:  "minecraft",
						},
					},
				},
			},
			want: "25565:TCP:minecraft",
		},
		{
			name: "wakeProtocol value is carried through correctly",
			tmpl: &gameplanev1alpha1.GameTemplate{
				Spec: gameplanev1alpha1.GameTemplateSpec{
					Ports: []gameplanev1alpha1.GamePort{
						{
							Name:          "game",
							ContainerPort: 7777,
							Protocol:      corev1.ProtocolUDP,
							Advertise:     true,
							WakeProtocol:  "valheim",
						},
					},
				},
			},
			want: "7777:UDP:valheim",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSentinelPortConfig(tc.tmpl)
			if got != tc.want {
				t.Errorf("buildSentinelPortConfig() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSentinelShutdownSettings pins the F-179 drain wiring: in the
// Service-backed expose modes the waker pod's grace period must outlast
// the sentinel's shutdown drain, so a handed-through session survives the
// waker being deleted when the game pod goes Ready; in Hostport mode there
// is no drain and the pod releases the host port promptly.
func TestSentinelShutdownSettings(t *testing.T) {
	for _, expose := range []string{"ClusterIP", "NodePort", "LoadBalancer", ""} {
		gs := &gameplanev1alpha1.GameServer{}
		gs.Spec.Networking.Expose = expose
		drain, grace := sentinelShutdownSettings(gs)
		if drain != sentinelShutdownDrainTimeout {
			t.Errorf("expose=%q: drain = %v, want %v", expose, drain, sentinelShutdownDrainTimeout)
		}
		if time.Duration(grace)*time.Second <= drain {
			t.Errorf("expose=%q: grace %ds does not outlast drain %v", expose, grace, drain)
		}
	}

	gs := &gameplanev1alpha1.GameServer{}
	gs.Spec.Networking.Expose = "Hostport"
	drain, grace := sentinelShutdownSettings(gs)
	if drain != 0 {
		t.Errorf("Hostport: drain = %v, want 0", drain)
	}
	if grace != 30 {
		t.Errorf("Hostport: grace = %ds, want 30s", grace)
	}
}
