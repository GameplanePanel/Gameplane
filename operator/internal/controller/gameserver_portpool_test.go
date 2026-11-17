package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

var edgePool = PortPool{Name: "edge", First: 2456, Last: 2467}

// valheimTemplate has two advertised UDP ports and one that isn't
// advertised.
func valheimTemplate() *gameplanev1alpha1.GameTemplate {
	tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{Ports: []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 2456, Protocol: corev1.ProtocolUDP, Advertise: true},
		{Name: "query", ContainerPort: 2457, Protocol: corev1.ProtocolUDP, Advertise: true},
		{Name: "status", ContainerPort: 80, Protocol: corev1.ProtocolTCP},
	}}}
	tmpl.Name = "valheim"
	return tmpl
}

func pooledServer(name string, age int, record string) gameplanev1alpha1.GameServer {
	gs := gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "games", UID: types.UID("uid-" + name),
		CreationTimestamp: metav1.NewTime(time.Unix(1_700_000_000, 0).Add(time.Duration(age) * time.Hour)),
	}}
	if record != "" {
		gs.Annotations = map[string]string{PoolPortsAnnotation: record}
	}
	return gs
}

func holder(name string, age int, record string) poolHolder {
	gs := pooledServer(name, age, record)
	rec, ok := parsePoolRecord(&gs)
	if !ok {
		panic("bad test record " + record)
	}
	return poolHolder{gs: &gs, rec: rec}
}

func allocate(t *testing.T, gs gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, holders ...poolHolder) string {
	t.Helper()
	rec, ok := allocatePoolPorts(&gs, demandFor(&gs, tmpl), edgePool, holders)
	if !ok {
		return "exhausted"
	}
	return rec.String()
}

func TestParsePortPools(t *testing.T) {
	pools, err := ParsePortPools(
		[]string{"edge=2456-2467", "lan=30000-30010"},
		[]string{"valheim=edge", "factorio=edge", "terraria=lan"})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := pools.forTemplate("factorio"); !ok || p != edgePool {
		t.Fatalf("factorio: got %+v, %v", p, ok)
	}
	if p, _ := pools.forTemplate("terraria"); p.Name != "lan" {
		t.Fatalf("terraria: got %+v", p)
	}
	if _, ok := pools.forTemplate("minecraft"); ok {
		t.Fatal("minecraft has no pool")
	}
	for _, bad := range []string{"edge", "edge=2456", "=1-2", "Edge=1-2", "e=0-10", "e=10-9", "e=1-70000", "e=a-b", "e=1-b"} {
		if _, err := ParsePortPools([]string{bad}, nil); err == nil {
			t.Errorf("pool %q: want an error", bad)
		}
	}
	if _, err := ParsePortPools([]string{"e=1-2", "e=3-4"}, nil); err == nil {
		t.Error("a pool defined twice: want an error")
	}
	for _, bad := range [][]string{{"valheim"}, {"valheim="}, {"valheim=nowhere"}, {"valheim=e", "valheim=e"}} {
		if _, err := ParsePortPools([]string{"e=1-2"}, bad); err == nil {
			t.Errorf("templates %q: want an error", bad)
		}
	}
}

func TestParsePoolRecord(t *testing.T) {
	gs := pooledServer("a", 0, "edge:game=2456/UDP,query=2457/UDP")
	rec, ok := parsePoolRecord(&gs)
	if !ok || rec.pool != "edge" || len(rec.entries) != 2 || rec.String() != "edge:game=2456/UDP,query=2457/UDP" {
		t.Fatalf("got %+v, %v", rec, ok)
	}
	for _, bad := range []string{"", "edge", "edge:", ":game=1/UDP", "edge:game", "edge:game=1", "edge:=1/UDP",
		"edge:game=x/UDP", "edge:game=0/UDP", "edge:game=70000/UDP", "edge:game=1/ICMP"} {
		gs := pooledServer("a", 0, bad)
		if gs.Annotations == nil {
			gs.Annotations = map[string]string{PoolPortsAnnotation: bad}
		}
		if _, ok := parsePoolRecord(&gs); ok {
			t.Errorf("%q: want no record", bad)
		}
	}
}

func TestAllocatePoolPorts_LowestFreeBlock(t *testing.T) {
	tmpl := valheimTemplate()
	if got := allocate(t, pooledServer("a", 1, ""), tmpl); got != "edge:game=2456/UDP,query=2457/UDP" {
		t.Errorf("first server: %s", got)
	}
	got := allocate(t, pooledServer("b", 2, ""), tmpl, holder("a", 1, "edge:game=2456/UDP,query=2457/UDP"))
	if got != "edge:game=2458/UDP,query=2459/UDP" {
		t.Errorf("second server: %s", got)
	}
	// A deleted server's block is free again.
	got = allocate(t, pooledServer("c", 3, ""), tmpl, holder("b", 2, "edge:game=2458/UDP,query=2459/UDP"))
	if got != "edge:game=2456/UDP,query=2457/UDP" {
		t.Errorf("gap reuse: %s", got)
	}
}

func TestAllocatePoolPorts_KeepsItsRecord(t *testing.T) {
	gs := pooledServer("b", 2, "edge:game=2460/UDP,query=2461/UDP")
	if got := allocate(t, gs, valheimTemplate()); got != "edge:game=2460/UDP,query=2461/UDP" {
		t.Errorf("got %s, want the recorded ports", got)
	}
}

// A younger server never takes ports an older one holds, and an older server
// with no record doesn't take a younger one's either: assignments stay put.
func TestAllocatePoolPorts_NeverTakesHeldPorts(t *testing.T) {
	got := allocate(t, pooledServer("old", 1, ""), valheimTemplate(), holder("young", 2, "edge:game=2456/UDP,query=2457/UDP"))
	if got != "edge:game=2458/UDP,query=2459/UDP" {
		t.Errorf("got %s", got)
	}
}

func TestAllocatePoolPorts_DuplicateStaysWithTheOlder(t *testing.T) {
	record := "edge:game=2456/UDP,query=2457/UDP"
	older := holder("a", 1, record)
	if got := allocate(t, pooledServer("b", 2, record), valheimTemplate(), older); got != "edge:game=2458/UDP,query=2459/UDP" {
		t.Errorf("younger duplicate: %s, want it moved", got)
	}
	younger := holder("b", 2, record)
	if got := allocate(t, pooledServer("a", 1, record), valheimTemplate(), younger); got != record {
		t.Errorf("older duplicate: %s, want it kept", got)
	}
}

// Same-age servers are ordered by namespace, then name.
func TestAllocatePoolPorts_SameAgeTiebreak(t *testing.T) {
	record := "edge:game=2456/UDP,query=2457/UDP"
	a, b := pooledServer("a", 1, record), pooledServer("b", 1, record)
	if !olderServer(&a, &b) || olderServer(&b, &a) {
		t.Fatal("a should sort before b")
	}
	a.Namespace = "zzz"
	if !olderServer(&b, &a) {
		t.Fatal("namespace orders before name")
	}
}

// Reservations come from each holder's record, not the current template, so
// a holder pinned to a revision with more ports keeps all of them.
func TestAllocatePoolPorts_HolderOnAnotherRevision(t *testing.T) {
	threePorts := holder("a", 1, "edge:game=2456/UDP,query=2457/UDP,extra=2458/UDP")
	got := allocate(t, pooledServer("b", 2, ""), valheimTemplate(), threePorts)
	if got != "edge:game=2459/UDP,query=2460/UDP" {
		t.Errorf("got %s, want a block after the three-port reservation", got)
	}
}

// A server whose own port set changed keeps its first port when the new
// block fits there.
func TestAllocatePoolPorts_ChangedPortSetKeepsItsBlockWhenFree(t *testing.T) {
	tmpl := valheimTemplate()
	tmpl.Spec.Ports = append(tmpl.Spec.Ports, gameplanev1alpha1.GamePort{
		Name: "extra", ContainerPort: 2458, Protocol: corev1.ProtocolUDP, Advertise: true,
	})
	gs := pooledServer("b", 2, "edge:game=2460/UDP,query=2461/UDP")
	if got := allocate(t, gs, tmpl); got != "edge:game=2460/UDP,query=2461/UDP,extra=2462/UDP" {
		t.Errorf("got %s", got)
	}
	blocked := holder("c", 3, "edge:x=2462/UDP")
	if got := allocate(t, gs, tmpl, blocked); got != "edge:game=2456/UDP,query=2457/UDP,extra=2458/UDP" {
		t.Errorf("blocked: got %s, want the lowest free block", got)
	}
}

// A record that no longer fits is replaced: at its first port when the new
// block fits there, else at the lowest free block.
func TestAllocatePoolPorts_ReassignedWhenTheRecordNoLongerFits(t *testing.T) {
	for name, tc := range map[string]struct{ record, want string }{
		"out of range":   {"edge:game=2467/UDP,query=2468/UDP", "edge:game=2456/UDP,query=2457/UDP"},
		"another pool":   {"lan:game=2460/UDP,query=2461/UDP", "edge:game=2456/UDP,query=2457/UDP"},
		"protocol":       {"edge:game=2460/TCP,query=2461/UDP", "edge:game=2460/UDP,query=2461/UDP"},
		"missing a port": {"edge:game=2460/UDP", "edge:game=2460/UDP,query=2461/UDP"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := allocate(t, pooledServer("b", 2, tc.record), valheimTemplate()); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// An explicit servicePort is reserved as it is, and the rest of the server's
// ports form the block.
func TestAllocatePoolPorts_OverridesAreReserved(t *testing.T) {
	gs := pooledServer("a", 1, "")
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2457}}
	if got := allocate(t, gs, valheimTemplate()); got != "edge:game=2456/UDP,query=2457/UDP" {
		t.Errorf("own override: %s", got)
	}
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2456}}
	if got := allocate(t, gs, valheimTemplate()); got != "edge:game=2457/UDP,query=2456/UDP" {
		t.Errorf("pooled port must avoid its own override: %s", got)
	}
	// Another server's override is in its record, so it is avoided too.
	other := holder("x", 0, "edge:game=2456/UDP,query=2463/UDP")
	if got := allocate(t, pooledServer("b", 2, ""), valheimTemplate(), other); got != "edge:game=2457/UDP,query=2458/UDP" {
		t.Errorf("other's override: %s", got)
	}
	// A kept record that now collides with the server's own override moves.
	gs = pooledServer("c", 3, "edge:game=2460/UDP,query=2461/UDP")
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2460}}
	if got := allocate(t, gs, valheimTemplate()); got != "edge:game=2456/UDP,query=2460/UDP" {
		t.Errorf("collision with own override: %s", got)
	}
}

// Ports are reserved per protocol: a TCP server can take a port number a UDP
// server holds.
func TestAllocatePoolPorts_PerProtocol(t *testing.T) {
	tcp := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{Ports: []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 7777, Advertise: true},
	}}}
	got := allocate(t, pooledServer("b", 2, ""), tcp, holder("a", 1, "edge:game=2456/UDP,query=2457/UDP"))
	if got != "edge:game=2456/TCP" {
		t.Errorf("got %s", got)
	}
}

func TestAllocatePoolPorts_Exhausted(t *testing.T) {
	small := PortPool{Name: "edge", First: 2456, Last: 2458}
	gs := pooledServer("b", 2, "")
	others := []poolHolder{holder("a", 1, "edge:game=2456/UDP,query=2457/UDP")}
	if _, ok := allocatePoolPorts(&gs, demandFor(&gs, valheimTemplate()), small, others); ok {
		t.Fatal("want no block")
	}
}

func TestAllocatePoolPorts_AllPortsOverridden(t *testing.T) {
	gs := pooledServer("a", 1, "")
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{
		{Name: "game", ServicePort: 3000}, {Name: "query", ServicePort: 3001},
	}
	if got := allocate(t, gs, valheimTemplate()); got != "edge:game=3000/UDP,query=3001/UDP" {
		t.Errorf("got %s", got)
	}
}

func TestSvcPortsFromTemplate_PoolPorts(t *testing.T) {
	gs := pooledServer("a", 1, "edge:game=2460/UDP,query=2461/UDP")
	ports := svcPortsFromTemplate(valheimTemplate(), &gs)
	if len(ports) != 2 || ports[0].Port != 2460 || ports[1].Port != 2461 ||
		ports[0].TargetPort.IntVal != 2456 || ports[1].TargetPort.IntVal != 2457 {
		t.Fatalf("got %+v", ports)
	}
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 3000}}
	if ports := svcPortsFromTemplate(valheimTemplate(), &gs); ports[1].Port != 3000 {
		t.Errorf("override: got %d, want 3000", ports[1].Port)
	}
	plain := pooledServer("b", 1, "")
	if ports := svcPortsFromTemplate(valheimTemplate(), &plain); ports[0].Port != 2456 {
		t.Errorf("no record: got %d, want the container port", ports[0].Port)
	}
}

// Relays dial the main Service, so their backing ports are its ports.
func TestRelayBackingPorts_FollowTheMainService(t *testing.T) {
	gs := pooledServer("a", 1, "edge:game=2460/UDP,query=2461/UDP")
	tmpl := valheimTemplate()
	backing := relayBackingPorts(tmpl, &gs)
	if got := buildTailscalePortsConfig(tmpl, backing); got != "game:2460:udp,query:2461:udp" {
		t.Errorf("tailscale: %s", got)
	}
	if got := buildPlayitPortsConfig(tmpl, backing); got != "game:2460,query:2461" {
		t.Errorf("playit: %s", got)
	}
	frp := &gameplanev1alpha1.FrpTunnelSpec{RemotePorts: []gameplanev1alpha1.RemotePortMapping{{Name: "game", RemotePort: 30000}}}
	if got := buildFrpRemotePortsConfig(frp, tmpl, backing); got != "game:2460:30000:udp" {
		t.Errorf("frp: %s", got)
	}
	if got := buildTailscalePortsConfig(tmpl); got != "game:2456:udp,query:2457:udp" {
		t.Errorf("without backing ports: %s", got)
	}
}

func poolReconciler(t *testing.T, pools PortPools, funcs *interceptor.Funcs, servers ...*gameplanev1alpha1.GameServer) (*GameServerReconciler, client.Client) {
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
	// Read the servers back, so each carries the resourceVersion the
	// record's optimistic-lock patch needs.
	for _, gs := range servers {
		if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
			t.Fatal(err)
		}
	}
	return &GameServerReconciler{Client: c, Scheme: s, PortPools: pools}, c
}

func edgePools(t *testing.T, span string) PortPools {
	t.Helper()
	pools, err := ParsePortPools([]string{"edge=" + span}, []string{"valheim=edge"})
	if err != nil {
		t.Fatal(err)
	}
	return pools
}

func storedPoolRecord(t *testing.T, c client.Client, gs *gameplanev1alpha1.GameServer) (string, bool) {
	t.Helper()
	var got gameplanev1alpha1.GameServer
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &got); err != nil {
		t.Fatal(err)
	}
	v, ok := got.Annotations[PoolPortsAnnotation]
	return v, ok
}

func TestReconcilePoolPorts_RecordsTheBlock(t *testing.T) {
	older, gs := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP"), pooledServer("b", 2, "")
	r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &older, &gs)
	alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	want := "edge:game=2458/UDP,query=2459/UDP"
	if !alloc.pooled || alloc.exhausted || alloc.record.String() != want {
		t.Errorf("allocation: %+v", alloc)
	}
	if gs.Annotations[PoolPortsAnnotation] != want {
		t.Errorf("in memory: got %q", gs.Annotations[PoolPortsAnnotation])
	}
	if v, _ := storedPoolRecord(t, c, &gs); v != want {
		t.Errorf("stored: got %q", v)
	}
	// A second pass finds the record current and leaves it alone.
	if _, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate()); err != nil {
		t.Fatal(err)
	}
}

func TestReconcilePoolPorts_RemovedWithThePool(t *testing.T) {
	gs := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
	r, c := poolReconciler(t, PortPools{}, nil, &gs)
	alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if alloc.pooled {
		t.Error("want no pool")
	}
	if v, ok := storedPoolRecord(t, c, &gs); ok {
		t.Errorf("stored: got %q, want no record", v)
	}
	if _, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate()); err != nil {
		t.Fatal(err)
	}
}

func TestReconcilePoolPorts_ExhaustedRemovesTheRecord(t *testing.T) {
	full := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
	gs := pooledServer("b", 2, "edge:game=2470/UDP,query=2471/UDP")
	r, c := poolReconciler(t, edgePools(t, "2456-2458"), nil, &full, &gs)
	alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if !alloc.exhausted || alloc.needed != 2 {
		t.Errorf("allocation: %+v", alloc)
	}
	if v, ok := storedPoolRecord(t, c, &gs); ok {
		t.Errorf("stored: got %q, want no record", v)
	}
}

// Terminating servers no longer hold ports.
func TestReconcilePoolPorts_IgnoresTerminatingServers(t *testing.T) {
	leaving := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
	leaving.Finalizers = []string{"example.com/hold"}
	gs := pooledServer("b", 2, "")
	r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &leaving, &gs)
	if err := c.Delete(t.Context(), &leaving); err != nil {
		t.Fatal(err)
	}
	alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if alloc.record.String() != "edge:game=2456/UDP,query=2457/UDP" {
		t.Errorf("got %s", alloc.record)
	}
}

func TestReconcilePoolPorts_APIErrors(t *testing.T) {
	boom := errors.New("apiserver down")
	for name, funcs := range map[string]interceptor.Funcs{
		"list": {List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error { return boom }},
		"patch": {Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return boom
		}},
	} {
		t.Run(name, func(t *testing.T) {
			gs := pooledServer("a", 1, "")
			r, _ := poolReconciler(t, edgePools(t, "2456-2467"), &funcs, &gs)
			if _, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate()); !errors.Is(err, boom) {
				t.Fatalf("got %v, want the API error", err)
			}
		})
	}
}

func TestPortAllocationCondition(t *testing.T) {
	gs := pooledServer("a", 1, "")
	gs.Generation = 4
	rec, _ := parsePoolRecord(&gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{PoolPortsAnnotation: "edge:game=2456/UDP,query=2457/UDP"},
	}})
	conds := portAllocationCondition(nil, &gs, portAllocation{pooled: true, pool: "edge", needed: 2, record: rec})
	c := meta.FindStatusCondition(conds, gameplanev1alpha1.GameServerConditionPortAllocation)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "Allocated" || c.ObservedGeneration != 4 ||
		!strings.Contains(c.Message, "2456/UDP, 2457/UDP") {
		t.Fatalf("allocated: %+v", c)
	}
	args := conditionArgsFor(&gs, c, conditionArgsInput{ports: portAllocation{pooled: true, pool: "edge", record: rec}})
	if args["pool"] != "edge" || args["ports"] != "2456/UDP, 2457/UDP" {
		t.Errorf("allocated args: %v", args)
	}

	conds = portAllocationCondition(conds, &gs, portAllocation{pooled: true, pool: "edge", needed: 2, exhausted: true})
	c = meta.FindStatusCondition(conds, gameplanev1alpha1.GameServerConditionPortAllocation)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "PoolExhausted" {
		t.Fatalf("exhausted: %+v", c)
	}
	args = conditionArgsFor(&gs, c, conditionArgsInput{ports: portAllocation{pool: "edge", needed: 2}})
	if args["pool"] != "edge" || args["count"] != "2" {
		t.Errorf("exhausted args: %v", args)
	}

	if conds = portAllocationCondition(conds, &gs, portAllocation{}); meta.FindStatusCondition(conds, gameplanev1alpha1.GameServerConditionPortAllocation) != nil {
		t.Error("want the condition removed outside a pool")
	}
}

// The sentinel dials the game at its container ports, so the game-direct
// Service publishes those even when the main Service uses pool ports.
func TestGameDirectService_PublishesContainerPorts(t *testing.T) {
	gs := pooledServer("a", 1, "edge:game=2460/UDP,query=2461/UDP")
	gs.Spec.Idle = &gameplanev1alpha1.IdleSpec{Enabled: true, WakeOnConnect: true}
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 3000}}
	r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &gs)
	if err := r.reconcileGameDirectServiceFromTemplate(t.Context(), &gs, valheimTemplate()); err != nil {
		t.Fatal(err)
	}
	var svc corev1.Service
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: "games", Name: "a-game-direct"}, &svc); err != nil {
		t.Fatal(err)
	}
	if len(svc.Spec.Ports) != 2 || svc.Spec.Ports[0].Port != 2456 || svc.Spec.Ports[1].Port != 2457 {
		t.Fatalf("got %+v, want the container ports", svc.Spec.Ports)
	}
}
