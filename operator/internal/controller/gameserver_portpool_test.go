package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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

// allocate plans gs's ports the way a reconcile does, from its active record.
func allocate(t *testing.T, gs gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, holders ...poolHolder) string {
	t.Helper()
	active, _ := parsePoolRecord(&gs)
	plan := planPoolPorts(&gs, demandFor(&gs, tmpl), edgePool, holders, []poolRecord{active}, active)
	if plan.conflict != nil {
		return "conflict " + plan.conflict.String() + " " + plan.conflict.holder
	}
	if !plan.ok {
		return "exhausted"
	}
	return plan.record.String()
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

// A pool that shrank leaves an existing server its ports; only new and
// replacement blocks come from the reduced range.
func TestAllocatePoolPorts_ShrunkPoolKeepsExistingPorts(t *testing.T) {
	record := "edge:game=2467/UDP,query=2468/UDP"
	if got := allocate(t, pooledServer("b", 2, record), valheimTemplate()); got != record {
		t.Errorf("got %s, want the recorded ports kept", got)
	}
	// Its port set changing gives it a block in the range: at its first port
	// when the block fits there, else the lowest free one.
	tmpl := valheimTemplate()
	tmpl.Spec.Ports = tmpl.Spec.Ports[:1]
	if got := allocate(t, pooledServer("b", 2, record), tmpl); got != "edge:game=2467/UDP" {
		t.Errorf("replacement at its first port: got %s", got)
	}
	tmpl = threePortTemplate()
	if got := allocate(t, pooledServer("b", 2, record), tmpl); got != "edge:game=2456/UDP,query=2457/UDP,extra=2458/UDP" {
		t.Errorf("replacement in the range: got %s", got)
	}
}

// An explicit servicePort another server holds is refused, whatever the two
// servers' ages: the holder keeps it.
func TestAllocatePoolPorts_RefusesAHeldServicePort(t *testing.T) {
	for name, age := range map[string]int{"younger holder": 3, "older holder": 0} {
		t.Run(name, func(t *testing.T) {
			gs := pooledServer("b", 2, "edge:game=2456/UDP,query=2457/UDP")
			gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2460}}
			got := allocate(t, gs, valheimTemplate(), holder("a", age, "edge:game=2460/UDP,query=2461/UDP"))
			if got != "conflict 2460/UDP games/a" {
				t.Errorf("got %s", got)
			}
		})
	}
	// An override the server already holds stays with it when a younger
	// duplicate appears.
	gs := pooledServer("b", 2, "edge:game=2456/UDP,query=2460/UDP")
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2460}}
	if got := allocate(t, gs, valheimTemplate(), holder("c", 3, "edge:game=2460/UDP")); got != "edge:game=2456/UDP,query=2460/UDP" {
		t.Errorf("held override: got %s", got)
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
	if plan := planPoolPorts(&gs, demandFor(&gs, valheimTemplate()), small, others, nil, poolRecord{}); plan.ok || plan.conflict != nil {
		t.Fatalf("got %+v, want no block", plan)
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
	// The record already includes the server's overrides, so it wins over
	// one waiting for a port another server holds.
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 3000}}
	if ports := svcPortsFromTemplate(valheimTemplate(), &gs); ports[1].Port != 2461 {
		t.Errorf("waiting override: got %d, want the recorded 2461", ports[1].Port)
	}
	plain := pooledServer("b", 1, "")
	if ports := svcPortsFromTemplate(valheimTemplate(), &plain); ports[0].Port != 2456 {
		t.Errorf("no record: got %d, want the container port", ports[0].Port)
	}
	plain.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 3000}}
	if ports := svcPortsFromTemplate(valheimTemplate(), &plain); ports[1].Port != 3000 {
		t.Errorf("override without a record: got %d, want 3000", ports[1].Port)
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
	if !alloc.pooled || !alloc.published || alloc.reason != portReasonAllocated || alloc.record.String() != want {
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

// A new server in a full pool gets no record and no Service.
func TestReconcilePoolPorts_ExhaustedNewServer(t *testing.T) {
	full := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
	gs := pooledServer("b", 2, "")
	r, c := poolReconciler(t, edgePools(t, "2456-2458"), nil, &full, &gs)
	alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if alloc.published || alloc.reason != portReasonExhausted || alloc.needed != 2 || !alloc.retry() {
		t.Errorf("allocation: %+v", alloc)
	}
	if v, ok := storedPoolRecord(t, c, &gs); ok {
		t.Errorf("stored: got %q, want no record", v)
	}
}

// A serving server whose change can't be made keeps its record, so it keeps
// publishing those ports, reserved.
func TestReconcilePoolPorts_ChangeWaitsOnTheServingRecord(t *testing.T) {
	// a serves 2470-2471, outside the shrunk range, and asks for query on
	// 2480, so game needs a port in the range, which b fills.
	a := pooledServer("a", 1, "edge:game=2470/UDP,query=2471/UDP")
	a.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2480}}
	b := pooledServer("b", 2, "edge:game=2456/UDP,query=2457/UDP")
	r, c := poolReconciler(t, edgePools(t, "2456-2457"), nil, &a, &b)
	alloc, err := r.reconcilePoolPorts(t.Context(), &a, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if !alloc.published || alloc.reason != portReasonChangePending || !alloc.retry() {
		t.Fatalf("allocation: %+v", alloc)
	}
	if v, _ := storedPoolRecord(t, c, &a); v != "edge:game=2470/UDP,query=2471/UDP" {
		t.Errorf("stored: got %q, want the serving record", v)
	}
	if ports := svcPortsFromTemplate(valheimTemplate(), &a); ports[0].Port != 2470 || ports[1].Port != 2471 {
		t.Errorf("Service: got %+v, want the serving ports", ports)
	}
}

// A record an older server also holds isn't kept while the server waits: it
// is unpublished rather than left sharing ports.
func TestReconcilePoolPorts_ContestedRecordIsNotKept(t *testing.T) {
	record := "edge:game=2456/UDP,query=2457/UDP"
	older, gs := pooledServer("a", 1, record), pooledServer("b", 2, record)
	r, c := poolReconciler(t, edgePools(t, "2456-2458"), nil, &older, &gs)
	alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if alloc.published || alloc.reason != portReasonExhausted {
		t.Errorf("allocation: %+v", alloc)
	}
	if v, ok := storedPoolRecord(t, c, &gs); ok {
		t.Errorf("stored: got %q, want no record", v)
	}
}

// A held servicePort is refused: a server with a record keeps it, one
// without stays unpublished.
func TestReconcilePoolPorts_ServicePortConflict(t *testing.T) {
	h := pooledServer("h", 3, "edge:game=2460/UDP,query=2461/UDP")
	withRecord := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
	withRecord.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2461}}
	without := pooledServer("b", 2, "")
	without.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2461}}
	r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &h, &withRecord, &without)

	alloc, err := r.reconcilePoolPorts(t.Context(), &withRecord, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if !alloc.published || alloc.reason != portReasonConflict || alloc.conflict.holder != "games/h" {
		t.Fatalf("with a record: %+v", alloc)
	}
	if ports := svcPortsFromTemplate(valheimTemplate(), &withRecord); ports[1].Port != 2457 {
		t.Errorf("Service: got %d, want the kept 2457", ports[1].Port)
	}
	alloc, err = r.reconcilePoolPorts(t.Context(), &without, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if alloc.published || alloc.reason != portReasonConflictNoSvc {
		t.Fatalf("without a record: %+v", alloc)
	}
	if v, _ := storedPoolRecord(t, c, &h); v != "edge:game=2460/UDP,query=2461/UDP" {
		t.Errorf("holder: got %q, want it unchanged", v)
	}
}

// A pool that shrank keeps existing servers' ports, says which are outside
// the range, and keeps them across restarts (a fresh reconciler).
func TestReconcilePoolPorts_ShrunkPoolKeepsPorts(t *testing.T) {
	gs := pooledServer("a", 1, "edge:game=2470/UDP,query=2471/UDP")
	for pass := range 2 {
		r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &gs)
		alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
		if err != nil {
			t.Fatal(err)
		}
		if alloc.reason != portReasonOutsideRange || poolPortsSummary(alloc.outside) != "2470/UDP, 2471/UDP" {
			t.Fatalf("pass %d: %+v", pass, alloc)
		}
		if v, _ := storedPoolRecord(t, c, &gs); v != "edge:game=2470/UDP,query=2471/UDP" {
			t.Fatalf("pass %d: stored %q", pass, v)
		}
		gs.ResourceVersion = ""
	}
}

// A terminating server still holds its ports: its Service can outlive it.
func TestReconcilePoolPorts_TerminatingServersStillHold(t *testing.T) {
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
	if alloc.record.String() != "edge:game=2458/UDP,query=2459/UDP" {
		t.Errorf("got %s", alloc.record)
	}
}

// Reordering a template's ports leaves each port its assignment.
func TestAllocatePoolPorts_ReorderedPortsKeepTheirAssignments(t *testing.T) {
	gs := pooledServer("a", 1, "edge:game=2460/UDP,query=2461/UDP")
	gs.Spec.Networking.PortOverrides = []gameplanev1alpha1.PortOverride{{Name: "query", ServicePort: 2461}}
	tmpl := valheimTemplate()
	tmpl.Spec.Ports[0], tmpl.Spec.Ports[1] = tmpl.Spec.Ports[1], tmpl.Spec.Ports[0]
	if got := allocate(t, gs, tmpl); got != "edge:game=2460/UDP,query=2461/UDP" {
		t.Errorf("got %s, want the recorded ports", got)
	}
	if out := outsideRange(poolRecord{pool: "edge", entries: []poolPortEntry{
		{name: "game", protocol: corev1.ProtocolUDP, port: 2470}, {name: "query", protocol: corev1.ProtocolUDP, port: 2461},
	}}, demandFor(&gs, tmpl), edgePool); len(out) != 1 || out[0].name != "game" {
		t.Errorf("outside range: got %+v, want game", out)
	}
}

// Ports a server's Service still publishes stay reserved after its record
// moves off them, until the Service moves too.
func TestReconcilePoolPorts_PublishedPortsStayReserved(t *testing.T) {
	moved := pooledServer("a", 1, "edge:game=2458/UDP,query=2459/UDP")
	gs := pooledServer("b", 2, "")
	r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &moved, &gs)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "games", Labels: map[string]string{PortPoolServiceLabel: "edge"}},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{
			{Name: "game", Port: 2456, Protocol: corev1.ProtocolUDP}, {Name: "query", Port: 2457, Protocol: corev1.ProtocolUDP},
		}},
	}
	if err := controllerutil.SetControllerReference(&moved, svc, r.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(t.Context(), svc); err != nil {
		t.Fatal(err)
	}
	alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if alloc.record.String() != "edge:game=2460/UDP,query=2461/UDP" {
		t.Errorf("got %s, want a block clear of both a's record and its Service", alloc.record)
	}
	// a's own Service doesn't hold a's ports against it.
	if alloc, err := r.reconcilePoolPorts(t.Context(), &moved, valheimTemplate()); err != nil || alloc.record.String() != "edge:game=2458/UDP,query=2459/UDP" {
		t.Errorf("a: got %s, %v", alloc.record, err)
	}
}

// The main Service carries its server's pool, and loses it with the record.
func TestReconcileService_LabelsThePool(t *testing.T) {
	gs := pooledServer("a", 1, "edge:game=2460/UDP,query=2461/UDP")
	r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &gs)
	label := func() (string, bool) {
		t.Helper()
		if err := r.reconcileService(t.Context(), &gs, valheimTemplate(), false); err != nil {
			t.Fatal(err)
		}
		var svc corev1.Service
		if err := c.Get(t.Context(), client.ObjectKey{Namespace: "games", Name: "a"}, &svc); err != nil {
			t.Fatal(err)
		}
		v, ok := svc.Labels[PortPoolServiceLabel]
		return v, ok
	}
	if v, _ := label(); v != "edge" {
		t.Errorf("got %q, want edge", v)
	}
	delete(gs.Annotations, PoolPortsAnnotation)
	if v, ok := label(); ok {
		t.Errorf("got %q, want no label", v)
	}
}

const testRevision = "0000000000000000000000000000000000000000000000000000000000000001"

// withRollout gives gs a held template rollout.
func withRollout(gs *gameplanev1alpha1.GameServer) {
	if gs.Annotations == nil {
		gs.Annotations = map[string]string{}
	}
	gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: lifecycleIdentity{
		Kind: "Rollout", Name: gs.Name, UID: gs.UID, TargetUID: gs.UID, Token: testRevision,
	}})
}

func threePortTemplate() *gameplanev1alpha1.GameTemplate {
	tmpl := valheimTemplate()
	tmpl.Spec.Ports = append(tmpl.Spec.Ports, gameplanev1alpha1.GamePort{
		Name: "extra", ContainerPort: 2458, Protocol: corev1.ProtocolUDP, Advertise: true,
	})
	return tmpl
}

// A rollout whose revision needs other ports reserves them first, and the
// running server keeps its own until the switch.
func TestReservePoolPortsForRollout(t *testing.T) {
	t.Run("same ports need no reservation", func(t *testing.T) {
		gs := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
		r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &gs)
		if ok, err := r.reservePoolPortsForRollout(t.Context(), &gs, valheimTemplate()); err != nil || !ok {
			t.Fatalf("got %v, %v", ok, err)
		}
		if _, has := storedPendingRecord(t, c, &gs); has {
			t.Error("want no pending record")
		}
	})
	t.Run("reserved", func(t *testing.T) {
		gs := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
		other := pooledServer("b", 2, "edge:game=2458/UDP")
		r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &gs, &other)
		if ok, err := r.reservePoolPortsForRollout(t.Context(), &gs, threePortTemplate()); err != nil || !ok {
			t.Fatalf("got %v, %v", ok, err)
		}
		pending, _ := storedPendingRecord(t, c, &gs)
		if pending != "edge:game=2459/UDP,query=2460/UDP,extra=2461/UDP" {
			t.Errorf("pending: got %q", pending)
		}
		if v, _ := storedPoolRecord(t, c, &gs); v != "edge:game=2456/UDP,query=2457/UDP" {
			t.Errorf("active: got %q, want it kept", v)
		}
		// Another server avoids both records.
		n := pooledServer("n", 3, "")
		if err := c.Create(t.Context(), &n); err != nil {
			t.Fatal(err)
		}
		tmpl := valheimTemplate()
		tmpl.Spec.Ports = tmpl.Spec.Ports[:1]
		alloc, err := r.reconcilePoolPorts(t.Context(), &n, tmpl)
		if err != nil {
			t.Fatal(err)
		}
		if alloc.record.String() != "edge:game=2462/UDP" {
			t.Errorf("new server: got %s", alloc.record)
		}
	})
	t.Run("no room keeps the current revision", func(t *testing.T) {
		gs := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
		other := pooledServer("b", 2, "edge:game=2458/UDP")
		r, c := poolReconciler(t, edgePools(t, "2456-2458"), nil, &gs, &other)
		if ok, err := r.reservePoolPortsForRollout(t.Context(), &gs, threePortTemplate()); err != nil || ok {
			t.Fatalf("got %v, %v, want it to wait", ok, err)
		}
		if _, has := storedPendingRecord(t, c, &gs); has {
			t.Error("want no pending record")
		}
		alloc, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate())
		if err != nil {
			t.Fatal(err)
		}
		if !alloc.published || alloc.reason != portReasonRollout || alloc.needed != 3 || !alloc.retry() ||
			alloc.record.String() != "edge:game=2456/UDP,query=2457/UDP" {
			t.Errorf("allocation: %+v", alloc)
		}
	})
	t.Run("no record yet", func(t *testing.T) {
		gs := pooledServer("a", 1, "")
		r, _ := poolReconciler(t, edgePools(t, "2456-2458"), nil, &gs)
		if ok, err := r.reservePoolPortsForRollout(t.Context(), &gs, threePortTemplate()); err != nil || !ok {
			t.Fatalf("got %v, %v", ok, err)
		}
	})
}

// Once the rollout switches the revision, the pending record becomes the
// active one; without a rollout, a pending record is dropped.
func TestReconcilePoolPorts_PendingRecord(t *testing.T) {
	gs := pooledServer("a", 1, "edge:game=2456/UDP,query=2457/UDP")
	gs.Annotations[PendingPoolPortsAnnotation] = "edge:game=2459/UDP,query=2460/UDP,extra=2461/UDP"
	withRollout(&gs)
	r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &gs)

	// Before the switch, the old revision keeps its ports and both stay.
	if _, err := r.reconcilePoolPorts(t.Context(), &gs, valheimTemplate()); err != nil {
		t.Fatal(err)
	}
	if v, _ := storedPendingRecord(t, c, &gs); v == "" {
		t.Fatal("pending record dropped before the switch")
	}
	alloc, err := r.reconcilePoolPorts(t.Context(), &gs, threePortTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if alloc.record.String() != "edge:game=2459/UDP,query=2460/UDP,extra=2461/UDP" {
		t.Errorf("after the switch: got %s", alloc.record)
	}
	if v, has := storedPendingRecord(t, c, &gs); has {
		t.Errorf("pending: got %q, want it promoted", v)
	}

	stale := pooledServer("b", 2, "edge:game=2462/UDP,query=2463/UDP")
	stale.Annotations[PendingPoolPortsAnnotation] = "edge:game=2464/UDP,query=2465/UDP,extra=2466/UDP"
	r, c = poolReconciler(t, edgePools(t, "2456-2467"), nil, &stale)
	if _, err := r.reconcilePoolPorts(t.Context(), &stale, valheimTemplate()); err != nil {
		t.Fatal(err)
	}
	if v, has := storedPendingRecord(t, c, &stale); has {
		t.Errorf("stale pending: got %q, want it dropped", v)
	}
}

// A pooled server with no record loses the Service it had; a Service it
// doesn't control is left alone.
func TestDeleteUnpublishedService(t *testing.T) {
	gs := pooledServer("a", 1, "")
	r, c := poolReconciler(t, edgePools(t, "2456-2467"), nil, &gs)
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "games"}}
	if err := controllerutil.SetControllerReference(&gs, svc, r.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(t.Context(), svc); err != nil {
		t.Fatal(err)
	}
	if err := r.deleteUnpublishedService(t.Context(), &gs); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(svc), svc); !apierrors.IsNotFound(err) {
		t.Fatalf("got %v, want the Service deleted", err)
	}
	foreign := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "games"}}
	if err := c.Create(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	if err := r.deleteUnpublishedService(t.Context(), &gs); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(foreign), foreign); err != nil {
		t.Fatalf("foreign Service: %v", err)
	}
	gone := pooledServer("gone", 1, "")
	if err := r.deleteUnpublishedService(t.Context(), &gone); err != nil {
		t.Fatal(err)
	}
}

func storedPendingRecord(t *testing.T, c client.Client, gs *gameplanev1alpha1.GameServer) (string, bool) {
	t.Helper()
	var got gameplanev1alpha1.GameServer
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &got); err != nil {
		t.Fatal(err)
	}
	v, ok := got.Annotations[PendingPoolPortsAnnotation]
	return v, ok
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
	conflict := &poolConflict{port: 2460, protocol: corev1.ProtocolUDP, holder: "games/h"}
	for _, tc := range []struct {
		alloc  portAllocation
		status metav1.ConditionStatus
		text   string
		args   map[string]string
	}{
		{portAllocation{published: true, reason: portReasonAllocated, record: rec}, metav1.ConditionTrue,
			"2456/UDP, 2457/UDP", map[string]string{"ports": "2456/UDP, 2457/UDP"}},
		{portAllocation{published: true, reason: portReasonOutsideRange, record: rec, outside: rec.entries[1:]}, metav1.ConditionTrue,
			"Ports 2457/UDP are outside", map[string]string{"outside": "2457/UDP"}},
		{portAllocation{reason: portReasonExhausted, needed: 2}, metav1.ConditionFalse,
			"no free block of 2 ports", map[string]string{"count": "2"}},
		{portAllocation{published: true, reason: portReasonConflict, record: rec, conflict: conflict}, metav1.ConditionFalse,
			"2460/UDP is held by games/h", map[string]string{"port": "2460/UDP", "holder": "games/h", "ports": "2456/UDP, 2457/UDP"}},
		{portAllocation{reason: portReasonConflictNoSvc, conflict: conflict}, metav1.ConditionFalse,
			"has no Service", map[string]string{"port": "2460/UDP"}},
		{portAllocation{published: true, reason: portReasonChangePending, record: rec, needed: 1}, metav1.ConditionFalse,
			"keeps ports 2456/UDP, 2457/UDP", map[string]string{"count": "1"}},
		{portAllocation{published: true, reason: portReasonRollout, record: rec, needed: 3}, metav1.ConditionFalse,
			"waits for 3 free ports", map[string]string{"count": "3"}},
		{portAllocation{published: true, reason: portReasonRolloutPort, record: rec, conflict: conflict}, metav1.ConditionFalse,
			"waits for Service port 2460/UDP", map[string]string{"holder": "games/h"}},
	} {
		t.Run(tc.alloc.reason, func(t *testing.T) {
			tc.alloc.pooled, tc.alloc.pool = true, "edge"
			conds := portAllocationCondition(nil, &gs, tc.alloc)
			c := meta.FindStatusCondition(conds, gameplanev1alpha1.GameServerConditionPortAllocation)
			if c == nil || c.Status != tc.status || c.Reason != tc.alloc.reason || c.ObservedGeneration != 4 ||
				!strings.Contains(c.Message, tc.text) || !strings.Contains(c.Message, "'edge'") {
				t.Fatalf("got %+v", c)
			}
			args := conditionArgsFor(&gs, c, conditionArgsInput{ports: tc.alloc})
			if args["pool"] != "edge" {
				t.Errorf("args: %v", args)
			}
			for k, v := range tc.args {
				if args[k] != v {
					t.Errorf("arg %s: got %q, want %q", k, args[k], v)
				}
			}
		})
	}
	c := &metav1.Condition{Type: gameplanev1alpha1.GameServerConditionPortAllocation, Reason: "Unknown"}
	if args := conditionArgsFor(&gs, c, conditionArgsInput{}); args != nil {
		t.Errorf("unknown reason: got %v", args)
	}
	c.Reason = portReasonConflict
	if args := conditionArgsFor(&gs, c, conditionArgsInput{ports: portAllocation{pool: "edge"}}); args != nil {
		t.Errorf("conflict without a holder: got %v", args)
	}
	if conds := portAllocationCondition(nil, &gs, portAllocation{}); meta.FindStatusCondition(conds, gameplanev1alpha1.GameServerConditionPortAllocation) != nil {
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
