package controller

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
	for _, bad := range []string{"valheim", "valheim=2456", "=1-2", "v=0-10", "v=10-9", "v=1-70000", "v=a-b", "v=1-b"} {
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

func TestAssignPoolPort_SameAgeOrdersByNamespaceThenName(t *testing.T) {
	a, b, c := pooledServer("b", 1, 0), pooledServer("a", 1, 0), pooledServer("a", 1, 0)
	c.Namespace = "aaa"
	servers := []gameplanev1alpha1.GameServer{a, b, c}
	for i, want := range []int32{2462, 2459, 2456} {
		if got, _ := assignPoolPort(&servers[i], servers, valheimPool, 3); got != want {
			t.Errorf("%s/%s: got %d, want %d", servers[i].Namespace, servers[i].Name, got, want)
		}
	}
}

func TestAssignPoolPort_UnparsableRecordIsReassigned(t *testing.T) {
	gs := pooledServer("a", 1, 0)
	gs.Annotations = map[string]string{PoolPortAnnotation: "not-a-port"}
	if got, _ := assignPoolPort(&gs, []gameplanev1alpha1.GameServer{gs}, valheimPool, 3); got != 2456 {
		t.Errorf("got %d, want 2456", got)
	}
}

func TestAssignPoolPort_NoAdvertisedPorts(t *testing.T) {
	gs := pooledServer("a", 1, 2456)
	if _, ok := assignPoolPort(&gs, []gameplanev1alpha1.GameServer{gs}, valheimPool, 0); ok {
		t.Error("a template with no advertised ports: want no block")
	}
}

func TestPoolServicePort_InvalidRecord(t *testing.T) {
	for _, raw := range []string{"not-a-port", "0", "65535"} {
		gs := pooledServer("a", 1, 0)
		gs.Annotations = map[string]string{PoolPortAnnotation: raw}
		if got := poolServicePort(&gs, 1); got != 0 {
			t.Errorf("%q: got %d, want 0", raw, got)
		}
	}
}

// poolReconciler runs reconcilePoolPort against a fake client holding the
// given servers, all on the named template.
func poolReconciler(t *testing.T, pools map[string]PortPool, funcs *interceptor.Funcs, servers ...*gameplanev1alpha1.GameServer) (*GameServerReconciler, client.Client) {
	t.Helper()
	s := wipeScheme(t)
	builder := fake.NewClientBuilder().WithScheme(s)
	for _, gs := range servers {
		gs.Spec.TemplateRef.Name = "valheim"
		builder = builder.WithObjects(gs)
	}
	if funcs != nil {
		builder = builder.WithInterceptorFuncs(*funcs)
	}
	c := builder.Build()
	return &GameServerReconciler{Client: c, Scheme: s, PortPools: pools}, c
}

func storedPoolPort(t *testing.T, c client.Client, gs *gameplanev1alpha1.GameServer) (string, bool) {
	t.Helper()
	var got gameplanev1alpha1.GameServer
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &got); err != nil {
		t.Fatal(err)
	}
	v, ok := got.Annotations[PoolPortAnnotation]
	return v, ok
}

func TestReconcilePoolPort_RecordsTheBlock(t *testing.T) {
	older, gs := pooledServer("a", 1, 2456), pooledServer("b", 2, 0)
	r, c := poolReconciler(t, map[string]PortPool{"valheim": valheimPool}, nil, &older, &gs)
	tmpl := poolTemplate()
	tmpl.Name = "valheim"
	if err := r.reconcilePoolPort(t.Context(), &gs, tmpl); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[PoolPortAnnotation] != "2459" {
		t.Errorf("in memory: got %q, want 2459", gs.Annotations[PoolPortAnnotation])
	}
	if v, _ := storedPoolPort(t, c, &gs); v != "2459" {
		t.Errorf("stored: got %q, want 2459", v)
	}
	// A second pass finds the record current and leaves it alone.
	if err := r.reconcilePoolPort(t.Context(), &gs, tmpl); err != nil {
		t.Fatal(err)
	}
}

func TestReconcilePoolPort_RemovedWithThePool(t *testing.T) {
	gs := pooledServer("a", 1, 2456)
	r, c := poolReconciler(t, nil, nil, &gs)
	tmpl := poolTemplate()
	tmpl.Name = "valheim"
	if err := r.reconcilePoolPort(t.Context(), &gs, tmpl); err != nil {
		t.Fatal(err)
	}
	if v, ok := storedPoolPort(t, c, &gs); ok {
		t.Errorf("stored: got %q, want no record", v)
	}
	// Without a pool or a record there is nothing to write.
	if err := r.reconcilePoolPort(t.Context(), &gs, tmpl); err != nil {
		t.Fatal(err)
	}
}

func TestReconcilePoolPort_ExhaustedKeepsTemplatePorts(t *testing.T) {
	full, gs := pooledServer("a", 1, 2456), pooledServer("b", 2, 0)
	r, c := poolReconciler(t, map[string]PortPool{"valheim": {First: 2456, Last: 2458}}, nil, &full, &gs)
	tmpl := poolTemplate()
	tmpl.Name = "valheim"
	if err := r.reconcilePoolPort(t.Context(), &gs, tmpl); err != nil {
		t.Fatal(err)
	}
	if v, ok := storedPoolPort(t, c, &gs); ok {
		t.Errorf("stored: got %q, want no record", v)
	}
}

func TestReconcilePoolPort_APIErrors(t *testing.T) {
	boom := errors.New("apiserver down")
	for name, funcs := range map[string]interceptor.Funcs{
		"list": {List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error { return boom }},
		"patch": {Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return boom
		}},
	} {
		t.Run(name, func(t *testing.T) {
			gs := pooledServer("a", 1, 0)
			r, _ := poolReconciler(t, map[string]PortPool{"valheim": valheimPool}, &funcs, &gs)
			tmpl := poolTemplate()
			tmpl.Name = "valheim"
			if err := r.reconcilePoolPort(t.Context(), &gs, tmpl); !errors.Is(err, boom) {
				t.Fatalf("got %v, want the API error", err)
			}
		})
	}
}
