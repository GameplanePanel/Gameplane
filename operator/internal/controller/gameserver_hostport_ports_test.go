package controller

import (
	"errors"
	"strings"
	"testing"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func hostportPortsFixture(expose string, ports ...gameplanev1alpha1.GamePort) (*gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Spec.Networking.Expose = expose
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Spec.Ports = ports
	return gs, tmpl
}

func TestValidateHostportPortsRefusesNodePorts(t *testing.T) {
	for _, tc := range []struct {
		port int32
		want string
	}{
		{22, "below 1024"},
		{80, "below 1024"},
		{1023, "below 1024"},
		{6443, "Kubernetes API server"},
		{10250, "kubelet"},
		{2379, "etcd"},
		{8472, "flannel VXLAN"},
	} {
		gs, tmpl := hostportPortsFixture("Hostport", gameplanev1alpha1.GamePort{Name: "game", ContainerPort: tc.port, Advertise: true})
		err := validateHostportPorts(gs, tmpl)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("port %d: got %v, want an error naming %q", tc.port, err, tc.want)
		}
		// A workload rejection lets a suspended server still drain and
		// refuses a rollout before it starts.
		var rejected workloadPolicyRejection
		if !errors.As(err, &rejected) {
			t.Errorf("port %d: %T is not a workloadPolicyRejection", tc.port, err)
		}
	}
}

func TestValidateHostportPortsAllowsGamePorts(t *testing.T) {
	gs, tmpl := hostportPortsFixture("Hostport",
		gameplanev1alpha1.GamePort{Name: "game", ContainerPort: 25565, Advertise: true},
		gameplanev1alpha1.GamePort{Name: "fivem", ContainerPort: 30120, Advertise: true},
		gameplanev1alpha1.GamePort{Name: "1024", ContainerPort: 1024, Advertise: true},
		// Not advertised: never bound on the node.
		gameplanev1alpha1.GamePort{Name: "status", ContainerPort: 80, Advertise: false},
		gameplanev1alpha1.GamePort{Name: "rcon", ContainerPort: 10250, Advertise: false},
	)
	if err := validateHostportPorts(gs, tmpl); err != nil {
		t.Fatalf("validateHostportPorts: %v", err)
	}
}

func TestValidateHostportPortsIgnoresOtherExposures(t *testing.T) {
	for _, expose := range []string{"", "ClusterIP", "NodePort", "LoadBalancer"} {
		gs, tmpl := hostportPortsFixture(expose, gameplanev1alpha1.GamePort{Name: "web", ContainerPort: 80, Advertise: true})
		if err := validateHostportPorts(gs, tmpl); err != nil {
			t.Errorf("expose %q: %v", expose, err)
		}
	}
}
