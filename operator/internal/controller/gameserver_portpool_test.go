package controller

import (
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

var valheimPool = PortPool{First: 2456, Last: 2467}

func poolTemplate() *gameplanev1alpha1.GameTemplate {
	return &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{Ports: []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 2456, Protocol: corev1.ProtocolUDP, Advertise: true},
		{Name: "query", ContainerPort: 2457, Protocol: corev1.ProtocolUDP, Advertise: true},
		{Name: "status", ContainerPort: 80, Protocol: corev1.ProtocolTCP},
		{Name: "extra", ContainerPort: 2458, Protocol: corev1.ProtocolUDP, Advertise: true},
	}}}
}

func pooledServer(name string, age int, port int32) gameplanev1alpha1.GameServer {
	gs := gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "games",
		CreationTimestamp: metav1.NewTime(time.Unix(1_700_000_000, 0).Add(time.Duration(age) * time.Hour)),
	}}
	if port != 0 {
		gs.Annotations = map[string]string{PoolPortAnnotation: strconv.Itoa(int(port))}
	}
	return gs
}

func TestParsePortPools(t *testing.T) {
	pools, err := ParsePortPools([]string{"valheim=2456-2467", "factorio=34197-34197"})
	if err != nil {
		t.Fatal(err)
	}
	if pools["valheim"] != valheimPool || pools["factorio"] != (PortPool{34197, 34197}) {
		t.Fatalf("got %+v", pools)
	}
	for _, bad := range []string{"valheim", "valheim=2456", "=1-2", "v=0-10", "v=10-9", "v=1-70000", "v=a-b"} {
		if _, err := ParsePortPools([]string{bad}); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
	if _, err := ParsePortPools([]string{"v=1-2", "v=3-4"}); err == nil {
		t.Error("two pools for one template: want an error")
	}
}

func TestAssignPoolPort_BlocksInAgeOrder(t *testing.T) {
	block := advertisedPortCount(poolTemplate())
	if block != 3 {
		t.Fatalf("block = %d, want 3", block)
	}
	// Listed newest first: the older server still gets the first block.
	servers := []gameplanev1alpha1.GameServer{pooledServer("b", 2, 0), pooledServer("a", 1, 0)}
	if got, ok := assignPoolPort(&servers[1], servers, valheimPool, block); !ok || got != 2456 {
		t.Errorf("a: got %d %v, want 2456", got, ok)
	}
	if got, ok := assignPoolPort(&servers[0], servers, valheimPool, block); !ok || got != 2459 {
		t.Errorf("b: got %d %v, want 2459", got, ok)
	}
}

func TestAssignPoolPort_KeepsHeldBlocksAndReusesGaps(t *testing.T) {
	servers := []gameplanev1alpha1.GameServer{pooledServer("a", 1, 2462), pooledServer("b", 2, 0)}
	if got, _ := assignPoolPort(&servers[0], servers, valheimPool, 3); got != 2462 {
		t.Errorf("a: got %d, want its held 2462", got)
	}
	if got, _ := assignPoolPort(&servers[1], servers, valheimPool, 3); got != 2456 {
		t.Errorf("b: got %d, want the free 2456", got)
	}
}

func TestAssignPoolPort_DuplicateStaysWithTheOlder(t *testing.T) {
	servers := []gameplanev1alpha1.GameServer{pooledServer("new", 2, 2456), pooledServer("old", 1, 2456)}
	if got, _ := assignPoolPort(&servers[1], servers, valheimPool, 3); got != 2456 {
		t.Errorf("old: got %d, want 2456", got)
	}
	if got, _ := assignPoolPort(&servers[0], servers, valheimPool, 3); got != 2459 {
		t.Errorf("new: got %d, want 2459", got)
	}
}

func TestAssignPoolPort_MisalignedOrOutOfRangeIsReassigned(t *testing.T) {
	for _, held := range []int32{2457, 2465 + 3, 1000} {
		gs := pooledServer("a", 1, held)
		if got, _ := assignPoolPort(&gs, []gameplanev1alpha1.GameServer{gs}, valheimPool, 3); got != 2456 {
			t.Errorf("held %d: got %d, want 2456", held, got)
		}
	}
}

func TestAssignPoolPort_Exhausted(t *testing.T) {
	var servers []gameplanev1alpha1.GameServer
	for i := 0; i < 5; i++ {
		servers = append(servers, pooledServer(strconv.Itoa(i), i, 0))
	}
	if _, ok := assignPoolPort(&servers[4], servers, valheimPool, 3); ok {
		t.Error("fifth server in a four-block pool: want no block")
	}
}

func TestSvcPortsFromTemplate_UsesThePoolPort(t *testing.T) {
	gs := pooledServer("a", 1, 2459)
	ports := svcPortsFromTemplate(poolTemplate(), &gs)
	want := []struct{ port, target int32 }{{2459, 2456}, {2460, 2457}, {2461, 2458}}
	if len(ports) != len(want) {
		t.Fatalf("got %d ports, want %d", len(ports), len(want))
	}
	for i, w := range want {
		if ports[i].Port != w.port || ports[i].TargetPort.IntVal != w.target {
			t.Errorf("port %d: got %d->%d, want %d->%d", i, ports[i].Port, ports[i].TargetPort.IntVal, w.port, w.target)
		}
	}
}

func TestSvcPortsFromTemplate_ExplicitOverrideWinsOverThePool(t *testing.T) {
	gs := pooledServer("a", 1, 2459)
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 9999}}
	ports := svcPortsFromTemplate(poolTemplate(), &gs)
	if ports[0].Port != 2459 || ports[1].Port != 9999 {
		t.Errorf("got %d, %d; want 2459, 9999", ports[0].Port, ports[1].Port)
	}
}

func TestSvcPortsFromTemplate_NoPoolPortIsUnchanged(t *testing.T) {
	gs := pooledServer("a", 1, 0)
	ports := svcPortsFromTemplate(poolTemplate(), &gs)
	if ports[0].Port != 2456 || ports[2].Port != 2458 {
		t.Errorf("got %d, %d; want the template's 2456, 2458", ports[0].Port, ports[2].Port)
	}
}
