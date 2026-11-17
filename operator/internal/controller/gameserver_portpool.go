package controller

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Port pools (spec 036). A pool is a range of Service ports on one shared
// public endpoint, such as a relay or a port-forwarding edge in front of the
// cluster's load balancer. Every server whose template selects the pool
// competes for its ports, and each (protocol, port) pair goes to at most one
// server. Templates only select a pool; the pool, not the template, decides
// which servers share ports.

// PoolPortsAnnotation records the Service ports a server holds in its port
// pool: "<pool>:<name>=<port>/<protocol>,...", one entry per advertised port
// of the server's effective template, overridden ports included. It is the
// reservation other servers read, so it never depends on which template or
// revision the holder runs. It is an operator-owned annotation, not status,
// for the reason the idle markers are: status is a derived read model a
// `kubectl replace` can drop, and losing the record would move a server to new
// ports, breaking every address players have saved.
const PoolPortsAnnotation = "gameplane.local/pool-ports"

// portPoolExhaustedRetry is how often a server waiting for pool capacity
// checks again; a deleted server frees its block without notifying the
// others.
const portPoolExhaustedRetry = 30 * time.Second

var portPoolNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// PortPool is one shared endpoint's range of Service ports.
type PortPool struct {
	Name  string
	First int32
	Last  int32
}

// PortPools is the operator's pool configuration: the pools, and which pool
// each template's servers draw from.
type PortPools struct {
	pools      map[string]PortPool
	byTemplate map[string]string
}

// ParsePortPools parses --port-pool ("<pool>=<first>-<last>") and
// --port-pool-template ("<template>=<pool>") values.
func ParsePortPools(pools, templates []string) (PortPools, error) {
	out := PortPools{pools: map[string]PortPool{}, byTemplate: map[string]string{}}
	for _, v := range pools {
		name, span, ok := strings.Cut(v, "=")
		first, last, ok2 := strings.Cut(span, "-")
		if !ok || !ok2 || !portPoolNameRE.MatchString(name) {
			return PortPools{}, fmt.Errorf("port pool %q: want <pool>=<first>-<last>, the pool a DNS label", v)
		}
		f, err := strconv.ParseInt(first, 10, 32)
		if err != nil {
			return PortPools{}, fmt.Errorf("port pool %q: %w", v, err)
		}
		l, err := strconv.ParseInt(last, 10, 32)
		if err != nil {
			return PortPools{}, fmt.Errorf("port pool %q: %w", v, err)
		}
		if f < 1 || l > 65535 || l < f {
			return PortPools{}, fmt.Errorf("port pool %q: want 1 <= first <= last <= 65535", v)
		}
		if _, dup := out.pools[name]; dup {
			return PortPools{}, fmt.Errorf("port pool %q: pool %s is defined twice", v, name)
		}
		out.pools[name] = PortPool{Name: name, First: int32(f), Last: int32(l)}
	}
	for _, v := range templates {
		tmpl, pool, ok := strings.Cut(v, "=")
		if !ok || tmpl == "" || pool == "" {
			return PortPools{}, fmt.Errorf("port pool template %q: want <template>=<pool>", v)
		}
		if _, known := out.pools[pool]; !known {
			return PortPools{}, fmt.Errorf("port pool template %q: no pool named %s", v, pool)
		}
		if prev, dup := out.byTemplate[tmpl]; dup {
			return PortPools{}, fmt.Errorf("port pool template %q: template %s already draws from pool %s", v, tmpl, prev)
		}
		out.byTemplate[tmpl] = pool
	}
	return out, nil
}

// forTemplate returns the pool a template's servers draw from.
func (p PortPools) forTemplate(name string) (PortPool, bool) {
	pool, ok := p.pools[p.byTemplate[name]]
	return pool, ok
}

// poolPortKey is one reservable port on a pool's shared endpoint.
type poolPortKey struct {
	protocol corev1.Protocol
	port     int32
}

// poolPortEntry is one advertised port's Service port in a record.
type poolPortEntry struct {
	name     string
	protocol corev1.Protocol
	port     int32
}

// poolRecord is a parsed PoolPortsAnnotation.
type poolRecord struct {
	pool    string
	entries []poolPortEntry
}

func (rec poolRecord) String() string {
	parts := make([]string, 0, len(rec.entries))
	for _, e := range rec.entries {
		parts = append(parts, fmt.Sprintf("%s=%d/%s", e.name, e.port, e.protocol))
	}
	return rec.pool + ":" + strings.Join(parts, ",")
}

// port returns the recorded Service port for an advertised port name.
func (rec poolRecord) port(name string) (int32, bool) {
	for _, e := range rec.entries {
		if e.name == name {
			return e.port, true
		}
	}
	return 0, false
}

// parsePoolRecord reads a server's PoolPortsAnnotation. A malformed record
// reads as none, so the server is given ports again rather than wedged.
func parsePoolRecord(gs *gameplanev1alpha1.GameServer) (poolRecord, bool) {
	raw, ok := gs.Annotations[PoolPortsAnnotation]
	if !ok {
		return poolRecord{}, false
	}
	pool, list, ok := strings.Cut(raw, ":")
	if !ok || pool == "" || list == "" {
		return poolRecord{}, false
	}
	rec := poolRecord{pool: pool}
	for _, item := range strings.Split(list, ",") {
		name, value, ok := strings.Cut(item, "=")
		portStr, proto, ok2 := strings.Cut(value, "/")
		if !ok || !ok2 || name == "" {
			return poolRecord{}, false
		}
		port, err := strconv.ParseInt(portStr, 10, 32)
		if err != nil || port < 1 || port > 65535 {
			return poolRecord{}, false
		}
		protocol := corev1.Protocol(proto)
		if protocol != corev1.ProtocolTCP && protocol != corev1.ProtocolUDP && protocol != corev1.ProtocolSCTP {
			return poolRecord{}, false
		}
		rec.entries = append(rec.entries, poolPortEntry{name: name, protocol: protocol, port: int32(port)})
	}
	return rec, true
}

// poolDemand is what a server needs from its pool: its advertised ports in
// template order, each either fixed by an explicit servicePort override or
// pooled, taking the next port of the server's block.
type poolDemand struct {
	ports []poolDemandPort
}

type poolDemandPort struct {
	name     string
	protocol corev1.Protocol
	fixed    int32 // the override's servicePort, or 0 when pooled
}

// demandFor reads a server's demand from its effective template, so a server
// pinned to an older revision asks for that revision's ports.
func demandFor(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) poolDemand {
	overrides := map[string]int32{}
	for _, o := range gs.Spec.Networking.PortOverrides {
		if o.ServicePort != 0 {
			overrides[o.Name] = o.ServicePort
		}
	}
	var d poolDemand
	for _, p := range tmpl.Spec.Ports {
		if !p.Advertise {
			continue
		}
		protocol := p.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		d.ports = append(d.ports, poolDemandPort{name: p.Name, protocol: protocol, fixed: overrides[p.Name]})
	}
	return d
}

func (d poolDemand) pooledCount() int32 {
	var n int32
	for _, p := range d.ports {
		if p.fixed == 0 {
			n++
		}
	}
	return n
}

// poolHolder is another server's reservation in the same pool.
type poolHolder struct {
	gs  *gameplanev1alpha1.GameServer
	rec poolRecord
}

// olderServer orders servers by creation, then namespace and name, so every
// reconcile settles a contested port the same way.
func olderServer(a, b *gameplanev1alpha1.GameServer) bool {
	if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
		return a.CreationTimestamp.Before(&b.CreationTimestamp)
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	return a.Name < b.Name
}

// allocatePoolPorts decides a server's record. Its fixed ports are taken as
// they are. Its pooled ports keep their recorded ports when the record is for
// this pool, names the same pooled ports and protocols, stays in the range,
// and collides with neither the server's own fixed ports nor an older
// server's record; a younger server holding the same port moves instead.
// Otherwise the pooled ports get one contiguous block, in template order: at
// the recorded first port when it is free, else the lowest free block. Every
// other server's record is avoided, so an assignment is never taken from a
// server that holds it. ok is false when no block is free.
func allocatePoolPorts(
	gs *gameplanev1alpha1.GameServer, demand poolDemand, pool PortPool, holders []poolHolder,
) (poolRecord, bool) {
	fixed := map[poolPortKey]bool{}
	for _, p := range demand.ports {
		if p.fixed != 0 {
			fixed[poolPortKey{p.protocol, p.fixed}] = true
		}
	}
	takenByAny := map[poolPortKey]bool{}
	takenByOlder := map[poolPortKey]bool{}
	for _, h := range holders {
		for _, e := range h.rec.entries {
			k := poolPortKey{e.protocol, e.port}
			takenByAny[k] = true
			if olderServer(h.gs, gs) {
				takenByOlder[k] = true
			}
		}
	}

	build := func(portFor func(i int32) int32) poolRecord {
		rec := poolRecord{pool: pool.Name}
		var i int32
		for _, p := range demand.ports {
			port := p.fixed
			if port == 0 {
				port = portFor(i)
				i++
			}
			rec.entries = append(rec.entries, poolPortEntry{name: p.name, protocol: p.protocol, port: port})
		}
		return rec
	}

	// Keep the recorded pooled ports when they still fit.
	if prev, ok := parsePoolRecord(gs); ok && prev.pool == pool.Name {
		keep := true
		var pooled []int32
		for _, p := range demand.ports {
			if p.fixed != 0 {
				continue
			}
			port, found := prev.port(p.name)
			k := poolPortKey{p.protocol, port}
			if !found || !recordedProtocol(prev, p.name, p.protocol) ||
				port < pool.First || port > pool.Last || fixed[k] || takenByOlder[k] {
				keep = false
				break
			}
			pooled = append(pooled, port)
		}
		if keep {
			return build(func(i int32) int32 { return pooled[i] }), true
		}
	}

	n := demand.pooledCount()
	if n == 0 {
		return build(nil), true
	}
	free := func(start int32) bool {
		if start < pool.First || start+n-1 > pool.Last {
			return false
		}
		var i int32
		for _, p := range demand.ports {
			if p.fixed != 0 {
				continue
			}
			k := poolPortKey{p.protocol, start + i}
			if takenByAny[k] || fixed[k] {
				return false
			}
			i++
		}
		return true
	}
	if prev, ok := parsePoolRecord(gs); ok && prev.pool == pool.Name {
		if start, found := firstPooledPort(prev, demand); found && free(start) {
			return build(func(i int32) int32 { return start + i }), true
		}
	}
	for start := pool.First; start+n-1 <= pool.Last; start++ {
		if free(start) {
			return build(func(i int32) int32 { return start + i }), true
		}
	}
	return poolRecord{}, false
}

func recordedProtocol(rec poolRecord, name string, protocol corev1.Protocol) bool {
	for _, e := range rec.entries {
		if e.name == name {
			return e.protocol == protocol
		}
	}
	return false
}

// firstPooledPort is the lowest recorded port among the demand's pooled
// ports, where a server whose port set changed tries its block first.
func firstPooledPort(rec poolRecord, demand poolDemand) (int32, bool) {
	var lowest int32
	for _, p := range demand.ports {
		if p.fixed != 0 {
			continue
		}
		if port, ok := rec.port(p.name); ok && (lowest == 0 || port < lowest) {
			lowest = port
		}
	}
	return lowest, lowest != 0
}

// portAllocation is one pass's outcome for a server, for its status.
type portAllocation struct {
	pooled    bool
	pool      string
	exhausted bool
	needed    int32
	record    poolRecord
}

// reconcilePoolPorts keeps the server's PoolPortsAnnotation current: set from
// its template's pool, or removed when the template selects none or the pool
// has no room. It updates gs in memory too, so the Service this pass builds
// uses the ports. Allocations are serialized and read servers uncached, so
// two new servers can't be given the same block.
func (r *GameServerReconciler) reconcilePoolPorts(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) (portAllocation, error) {
	pool, pooled := r.PortPools.forTemplate(tmpl.Name)
	if !pooled {
		return portAllocation{}, r.setPoolRecord(ctx, gs, "")
	}

	r.portPoolMu.Lock()
	defer r.portPoolMu.Unlock()

	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var list gameplanev1alpha1.GameServerList
	if err := reader.List(ctx, &list); err != nil {
		return portAllocation{}, fmt.Errorf("list GameServers for port pool %s: %w", pool.Name, err)
	}
	var holders []poolHolder
	for i := range list.Items {
		other := &list.Items[i]
		if other.UID == gs.UID || (other.Namespace == gs.Namespace && other.Name == gs.Name) {
			continue
		}
		if !other.DeletionTimestamp.IsZero() {
			continue
		}
		if rec, ok := parsePoolRecord(other); ok && rec.pool == pool.Name {
			holders = append(holders, poolHolder{gs: other, rec: rec})
		}
	}
	sort.Slice(holders, func(i, j int) bool { return olderServer(holders[i].gs, holders[j].gs) })

	demand := demandFor(gs, tmpl)
	out := portAllocation{pooled: true, pool: pool.Name, needed: demand.pooledCount()}
	rec, ok := allocatePoolPorts(gs, demand, pool, holders)
	if !ok {
		out.exhausted = true
		return out, r.setPoolRecord(ctx, gs, "")
	}
	out.record = rec
	return out, r.setPoolRecord(ctx, gs, rec.String())
}

// setPoolRecord writes (or, for "", removes) the PoolPortsAnnotation,
// guarded by resourceVersion so a concurrent edit isn't overwritten.
func (r *GameServerReconciler) setPoolRecord(ctx context.Context, gs *gameplanev1alpha1.GameServer, want string) error {
	current, has := gs.Annotations[PoolPortsAnnotation]
	if (want == "" && !has) || (has && current == want) {
		return nil
	}
	patch := client.MergeFromWithOptions(gs.DeepCopy(), client.MergeFromWithOptimisticLock{})
	if gs.Annotations == nil {
		gs.Annotations = map[string]string{}
	}
	if want == "" {
		delete(gs.Annotations, PoolPortsAnnotation)
	} else {
		gs.Annotations[PoolPortsAnnotation] = want
	}
	if err := r.Patch(ctx, gs, patch); err != nil {
		return fmt.Errorf("record pool ports: %w", err)
	}
	return nil
}

// poolServicePort is the Service port recorded for an advertised port, or 0
// when the server has no record for it.
func poolServicePort(gs *gameplanev1alpha1.GameServer, name string) int32 {
	rec, ok := parsePoolRecord(gs)
	if !ok {
		return 0
	}
	port, _ := rec.port(name)
	return port
}

// portAllocationCondition reports a pass's outcome on the PortAllocation
// condition, or removes it for a server outside any pool.
func portAllocationCondition(
	conds []metav1.Condition, gs *gameplanev1alpha1.GameServer, alloc portAllocation,
) []metav1.Condition {
	if !alloc.pooled {
		return removeCondition(conds, gameplanev1alpha1.GameServerConditionPortAllocation)
	}
	cond := metav1.Condition{
		Type:               gameplanev1alpha1.GameServerConditionPortAllocation,
		ObservedGeneration: gs.Generation,
	}
	if alloc.exhausted {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "PoolExhausted"
		cond.Message = fmt.Sprintf(
			"Port pool '%s' has no free block of %d ports for this server, so it has no Service until a block frees up.",
			alloc.pool, alloc.needed)
	} else {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Allocated"
		cond.Message = fmt.Sprintf("Ports %s assigned from port pool '%s'.", poolPortsSummary(alloc.record), alloc.pool)
	}
	if existing := meta.FindStatusCondition(conds, cond.Type); existing != nil &&
		existing.Status == cond.Status && existing.Reason == cond.Reason && existing.Message == cond.Message {
		cond.LastTransitionTime = existing.LastTransitionTime
	}
	meta.SetStatusCondition(&conds, cond)
	return conds
}

// poolPortsSummary lists a record's ports for the condition message, such as
// "2456/UDP, 2457/UDP".
func poolPortsSummary(rec poolRecord) string {
	parts := make([]string, 0, len(rec.entries))
	for _, e := range rec.entries {
		parts = append(parts, fmt.Sprintf("%d/%s", e.port, e.protocol))
	}
	return strings.Join(parts, ", ")
}
