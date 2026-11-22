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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Port pools (spec 036). A pool is a range of Service ports on one shared
// public endpoint, such as a relay or a port-forwarding edge in front of the
// cluster's load balancer. Every server whose template selects the pool
// competes for its ports, and each (protocol, port) pair goes to at most one
// server. Templates only select a pool; the pool, not the template, decides
// which servers share ports.

// PoolPortsAnnotation is a server's active record: the Service ports it
// publishes from its port pool, "<pool>:<name>=<port>/<protocol>,...", one
// entry per advertised port of its effective template, overridden ports
// included. The main Service and relays publish exactly these ports, and the
// record is the reservation other servers read, so it never depends on which
// template or revision the holder runs. It is an operator-owned annotation,
// not status, for the reason the idle markers are: status is a derived read
// model a `kubectl replace` can drop, and losing the record would move a
// server to new ports, breaking every address players have saved.
const PoolPortsAnnotation = "gameplane.local/pool-ports"

// PendingPoolPortsAnnotation is a server's pending record, in the same
// format: the ports reserved for a template rollout whose revision can't
// publish through the active record. Both records are reserved until the
// rollout switches the revision and the pending record becomes the active
// one.
const PendingPoolPortsAnnotation = "gameplane.local/pool-ports-pending"

// PortPoolServiceLabel marks a pooled server's main Service with its pool.
// The ports the Service publishes stay reserved, alongside the records,
// until the Service changes or is deleted: a record that moves to a new
// block frees its old ports only once the Service has moved off them.
const PortPoolServiceLabel = "gameplane.local/port-pool"

// portPoolRetry is how often a server waiting for pool capacity or a held
// servicePort checks again; a deleted server frees its ports without
// notifying the others.
const portPoolRetry = 30 * time.Second

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
	e, ok := rec.entry(name)
	return e.port, ok
}

// entry returns the record's entry for an advertised port name. Entries are
// matched by name, never by position, so reordering a template's ports
// leaves each port its assignment.
func (rec poolRecord) entry(name string) (poolPortEntry, bool) {
	for _, e := range rec.entries {
		if e.name == name {
			return e, true
		}
	}
	return poolPortEntry{}, false
}

// parsePoolRecord reads a server's active record.
func parsePoolRecord(gs *gameplanev1alpha1.GameServer) (poolRecord, bool) {
	return parsePoolRecordAt(gs, PoolPortsAnnotation)
}

// parsePoolRecordAt reads the record in annotation key. A malformed record
// reads as none, so the server is given ports again rather than wedged.
func parsePoolRecordAt(gs *gameplanev1alpha1.GameServer, key string) (poolRecord, bool) {
	raw, ok := gs.Annotations[key]
	if !ok {
		return poolRecord{}, false
	}
	pool, list, ok := strings.Cut(raw, ":")
	if !ok || !portPoolNameRE.MatchString(pool) || list == "" {
		return poolRecord{}, false
	}
	rec := poolRecord{pool: pool}
	seen := map[poolPortKey]bool{}
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
		// A Service can't publish one (protocol, port) twice, nor two ports
		// of one name.
		key := poolPortKey{protocol, int32(port)}
		if _, dup := rec.entry(name); dup || seen[key] {
			return poolRecord{}, false
		}
		seen[key] = true
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

// poolHolder is one record another server holds in the same pool: its
// active record, or the pending one reserved for its rollout.
type poolHolder struct {
	gs  *gameplanev1alpha1.GameServer
	rec poolRecord
}

func (h poolHolder) String() string { return h.gs.Namespace + "/" + h.gs.Name }

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

// poolConflict is an explicit servicePort another server already holds.
type poolConflict struct {
	port     int32
	protocol corev1.Protocol
	holder   string
}

func (c *poolConflict) String() string { return fmt.Sprintf("%d/%s", c.port, c.protocol) }

// poolPlan is the allocation a demand can have now.
type poolPlan struct {
	record   poolRecord
	ok       bool
	kept     int // which candidate was kept (0-based), or -1 for a new block
	conflict *poolConflict
}

// compatibleRecord reports whether rec publishes exactly demand's ports: the
// same names and protocols, and each fixed port at its servicePort.
func compatibleRecord(rec poolRecord, demand poolDemand) bool {
	if !compatibleNames(rec, demand) {
		return false
	}
	for _, p := range demand.ports {
		if e, _ := rec.entry(p.name); p.fixed != 0 && e.port != p.fixed {
			return false
		}
	}
	return true
}

// contested reports whether an older server's record holds any of rec's
// ports. Two records only share a port after a restore or a hand edit; the
// older server keeps it.
func contested(gs *gameplanev1alpha1.GameServer, rec poolRecord, holders []poolHolder) bool {
	for _, h := range holders {
		if !olderServer(h.gs, gs) {
			continue
		}
		for _, e := range rec.entries {
			for _, o := range h.rec.entries {
				if e.protocol == o.protocol && e.port == o.port {
					return true
				}
			}
		}
	}
	return false
}

// planPoolPorts decides the record a server's demand can have, given every
// other server's records (holders) and the server's own records
// (candidates, such as its active and pending records):
//
//  1. A candidate that publishes exactly the demand's ports and isn't
//     contested is kept as it is, wherever its ports are: a pool that shrank
//     still leaves an existing server its ports.
//  2. An explicit servicePort another server holds is refused: the plan
//     reports the conflict and allocates nothing, and the holder keeps it.
//  3. Otherwise the pooled ports get one contiguous block, in the range and
//     in template order, that no holder and none of the server's own fixed
//     ports use: at prefer's first pooled port when the block fits there,
//     else the lowest such block. The server's own records don't block it,
//     since the Service moves from them to the new block in one update.
//
// ok is false when no block is free, or on a conflict.
func planPoolPorts(
	gs *gameplanev1alpha1.GameServer, demand poolDemand, pool PortPool, holders []poolHolder,
	candidates []poolRecord, prefer poolRecord,
) poolPlan {
	for i, c := range candidates {
		if c.pool == pool.Name && compatibleRecord(c, demand) && !contested(gs, c, holders) {
			return poolPlan{record: c, ok: true, kept: i}
		}
	}

	taken := map[poolPortKey]string{}
	for _, h := range holders {
		for _, e := range h.rec.entries {
			taken[poolPortKey{e.protocol, e.port}] = h.String()
		}
	}
	fixed := map[poolPortKey]bool{}
	for _, p := range demand.ports {
		if p.fixed == 0 {
			continue
		}
		k := poolPortKey{p.protocol, p.fixed}
		if holder, held := taken[k]; held {
			return poolPlan{kept: -1, conflict: &poolConflict{port: p.fixed, protocol: p.protocol, holder: holder}}
		}
		if fixed[k] {
			// Two of the server's own ports ask for one servicePort, which no
			// Service can publish.
			return poolPlan{kept: -1, conflict: &poolConflict{port: p.fixed, protocol: p.protocol, holder: gs.Namespace + "/" + gs.Name}}
		}
		fixed[k] = true
	}

	build := func(start int32) poolRecord {
		rec := poolRecord{pool: pool.Name}
		next := start
		for _, p := range demand.ports {
			port := p.fixed
			if port == 0 {
				port = next
				next++
			}
			rec.entries = append(rec.entries, poolPortEntry{name: p.name, protocol: p.protocol, port: port})
		}
		return rec
	}
	n := demand.pooledCount()
	if n == 0 {
		return poolPlan{record: build(0), ok: true, kept: -1}
	}
	free := func(start int32) bool {
		if start < pool.First || start+n-1 > pool.Last {
			return false
		}
		next := start
		for _, p := range demand.ports {
			if p.fixed != 0 {
				continue
			}
			k := poolPortKey{p.protocol, next}
			if _, held := taken[k]; held || fixed[k] {
				return false
			}
			next++
		}
		return true
	}
	if start, found := firstPooledPort(prefer, demand); prefer.pool == pool.Name && found && free(start) {
		return poolPlan{record: build(start), ok: true, kept: -1}
	}
	for start := pool.First; start+n-1 <= pool.Last; start++ {
		if free(start) {
			return poolPlan{record: build(start), ok: true, kept: -1}
		}
	}
	return poolPlan{kept: -1}
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

// outsideRange lists a record's pooled ports outside the pool's range: a pool
// that shrank leaves them with the servers that hold them.
func outsideRange(rec poolRecord, demand poolDemand, pool PortPool) []poolPortEntry {
	fixed := map[string]bool{}
	for _, p := range demand.ports {
		fixed[p.name] = p.fixed != 0
	}
	var out []poolPortEntry
	for _, e := range rec.entries {
		if !fixed[e.name] && (e.port < pool.First || e.port > pool.Last) {
			out = append(out, e)
		}
	}
	return out
}

// Reasons for the PortAllocation condition.
const (
	portReasonAllocated     = "Allocated"
	portReasonOutsideRange  = "AllocatedOutsideRange"
	portReasonExhausted     = "PoolExhausted"
	portReasonConflict      = "ServicePortConflict"
	portReasonConflictNoSvc = "ServicePortConflictUnpublished"
	portReasonChangePending = "ChangePending"
	portReasonRollout       = "RolloutPending"
	portReasonRolloutPort   = "RolloutPortConflict"
)

// portAllocation is one pass's outcome for a server: its effective record,
// which its Service and relays publish, and the condition to report.
type portAllocation struct {
	pooled    bool
	pool      string
	published bool // the server has an effective record, so it has a Service
	record    poolRecord
	reason    string
	needed    int32 // the pooled ports a pending change or a new server needs
	conflict  *poolConflict
	outside   []poolPortEntry
}

// retry reports whether the server waits for capacity or a free servicePort,
// which no event announces: a deleted server frees its ports without
// notifying the others.
func (a portAllocation) retry() bool {
	switch a.reason {
	case portReasonExhausted, portReasonConflict, portReasonConflictNoSvc, portReasonChangePending,
		portReasonRollout, portReasonRolloutPort:
		return true
	}
	return false
}

// poolRolloutWait is why a rollout waits to be admitted, kept for the pass's
// status.
type poolRolloutWait struct {
	needed   int32
	conflict *poolConflict
}

// poolHolders lists every other server's records in pool, and the ports
// every other server's main Service publishes through it, uncached. A
// terminating server still holds its ports, since its Service can outlive
// it until garbage collection removes it.
func (r *GameServerReconciler) poolHolders(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, pool PortPool,
) ([]poolHolder, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var list gameplanev1alpha1.GameServerList
	if err := reader.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list GameServers for port pool %s: %w", pool.Name, err)
	}
	var holders []poolHolder
	for i := range list.Items {
		other := &list.Items[i]
		if other.UID == gs.UID || (other.Namespace == gs.Namespace && other.Name == gs.Name) {
			continue
		}
		for _, key := range []string{PoolPortsAnnotation, PendingPoolPortsAnnotation} {
			if rec, ok := parsePoolRecordAt(other, key); ok && rec.pool == pool.Name {
				holders = append(holders, poolHolder{gs: other, rec: rec})
			}
		}
	}
	published, err := r.publishedPoolPorts(ctx, gs, pool, list.Items)
	if err != nil {
		return nil, err
	}
	holders = append(holders, published...)
	sort.Slice(holders, func(i, j int) bool { return olderServer(holders[i].gs, holders[j].gs) })
	return holders, nil
}

// publishedPoolPorts lists the ports other servers' main Services publish
// through pool, each held by the Service's server. A Service whose server is
// already gone is held as of the Service's creation.
func (r *GameServerReconciler) publishedPoolPorts(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, pool PortPool, servers []gameplanev1alpha1.GameServer,
) ([]poolHolder, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var svcs corev1.ServiceList
	if err := reader.List(ctx, &svcs, client.MatchingLabels{PortPoolServiceLabel: pool.Name}); err != nil {
		return nil, fmt.Errorf("list Services for port pool %s: %w", pool.Name, err)
	}
	byUID := make(map[types.UID]*gameplanev1alpha1.GameServer, len(servers))
	for i := range servers {
		byUID[servers[i].UID] = &servers[i]
	}
	var holders []poolHolder
	for i := range svcs.Items {
		svc := &svcs.Items[i]
		owner := metav1.GetControllerOf(svc)
		if owner == nil || owner.Kind != "GameServer" || owner.UID == gs.UID ||
			(svc.Namespace == gs.Namespace && owner.Name == gs.Name) {
			continue
		}
		held := byUID[owner.UID]
		if held == nil {
			held = &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{
				Name: owner.Name, Namespace: svc.Namespace, UID: owner.UID, CreationTimestamp: svc.CreationTimestamp,
			}}
		}
		rec := poolRecord{pool: pool.Name}
		for _, p := range svc.Spec.Ports {
			protocol := p.Protocol
			if protocol == "" {
				protocol = corev1.ProtocolTCP
			}
			rec.entries = append(rec.entries, poolPortEntry{name: p.Name, protocol: protocol, port: p.Port})
		}
		holders = append(holders, poolHolder{gs: held, rec: rec})
	}
	return holders, nil
}

// rolloutHeld reports whether a template rollout owns the server's
// lifecycle, which is what a pending record is reserved for.
func rolloutHeld(gs *gameplanev1alpha1.GameServer) bool {
	op, held, err := lifecycleOwner(gs)
	return err == nil && held && op.Kind == "Rollout"
}

// reconcilePoolPorts keeps the server's records current and returns its
// effective allocation. With a plan (planPoolPorts) the active record
// becomes it, taking over a pending record once the rollout it was reserved
// for has switched the server's revision. Without one, an active record that
// still publishes the effective template's ports and isn't contested stays
// in effect, and the change is reported as pending; otherwise the record is
// removed and the server is unpublished. It updates gs in memory too, so the
// Service this pass builds uses the ports. Allocations are serialized and
// read servers uncached, so two servers can't be given the same block.
func (r *GameServerReconciler) reconcilePoolPorts(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) (portAllocation, error) {
	pool, pooled := r.PortPools.forTemplate(tmpl.Name)
	demand := demandFor(gs, tmpl)
	if !pooled || len(demand.ports) == 0 {
		// A template that advertises no ports publishes none, so it takes
		// nothing from its pool and is treated as outside it.
		return portAllocation{}, r.setPoolRecords(ctx, gs, "", "")
	}

	r.portPoolMu.Lock()
	defer r.portPoolMu.Unlock()
	holders, err := r.poolHolders(ctx, gs, pool)
	if err != nil {
		return portAllocation{}, err
	}

	active, _ := parsePoolRecordAt(gs, PoolPortsAnnotation)
	pending, _ := parsePoolRecordAt(gs, PendingPoolPortsAnnotation)
	out := portAllocation{pooled: true, pool: pool.Name, needed: demand.pooledCount()}
	plan := planPoolPorts(gs, demand, pool, holders, []poolRecord{active, pending}, active)
	// The pending record becomes the active one once it fits the effective
	// revision, and is dropped once no rollout holds it.
	if plan.kept == 1 || !rolloutHeld(gs) {
		pending = poolRecord{}
	}
	switch {
	case plan.ok:
		active = plan.record
		out.published, out.record = true, active
		out.reason = portReasonAllocated
		if out.outside = outsideRange(active, demand, pool); len(out.outside) != 0 {
			out.reason = portReasonOutsideRange
		}
		if wait, waiting := r.poolRolloutWaits.Load(gs.UID); waiting {
			w, _ := wait.(poolRolloutWait)
			out.reason, out.needed, out.conflict = portReasonRollout, w.needed, w.conflict
			if w.conflict != nil {
				out.reason = portReasonRolloutPort
			}
		}
	case active.pool == pool.Name && compatibleNames(active, demand) && !contested(gs, active, holders):
		// The change can't be made yet; the ports in use stay reserved and
		// published.
		out.published, out.record, out.conflict = true, active, plan.conflict
		out.reason = portReasonChangePending
		if plan.conflict != nil {
			out.reason = portReasonConflict
		}
	default:
		active = poolRecord{}
		out.conflict = plan.conflict
		out.reason = portReasonExhausted
		if plan.conflict != nil {
			out.reason = portReasonConflictNoSvc
		}
	}
	return out, r.setPoolRecords(ctx, gs, recordValue(active), recordValue(pending))
}

// compatibleNames reports whether rec still publishes each of demand's ports
// by name and protocol, whatever their servicePorts, so the Service it
// describes still reaches the effective workload.
func compatibleNames(rec poolRecord, demand poolDemand) bool {
	if len(rec.entries) != len(demand.ports) {
		return false
	}
	for _, p := range demand.ports {
		if e, ok := rec.entry(p.name); !ok || e.protocol != p.protocol {
			return false
		}
	}
	return true
}

func recordValue(rec poolRecord) string {
	if rec.pool == "" {
		return ""
	}
	return rec.String()
}

// reservePoolPortsForRollout runs before a template rollout is admitted, and
// again before the rollout switches the server's revision. When the target
// revision can't publish through the active record, it reserves the target's
// ports durably in the pending record, so the rollout never drains a running
// server it then can't publish; the active record stays reserved until the
// switch. It returns false, and keeps the reason for the pass's status, when
// the pool can't reserve them yet; the server keeps its current revision.
func (r *GameServerReconciler) reservePoolPortsForRollout(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, target *gameplanev1alpha1.GameTemplate,
) (bool, error) {
	pool, pooled := r.PortPools.forTemplate(target.Name)
	if !pooled || len(demandFor(gs, target).ports) == 0 {
		return true, nil
	}
	active, hasActive := parsePoolRecordAt(gs, PoolPortsAnnotation)
	if !hasActive || active.pool != pool.Name {
		// Nothing is published through this pool. A record from another
		// pool means the template's pool changed in the operator's
		// configuration; this pass's reconcilePoolPorts moves the server to
		// this pool, or unpublishes it when the pool is full, whether or not
		// a rollout runs. Either way the target is allocated like a new
		// server's.
		return true, r.setPoolRecords(ctx, gs, gs.Annotations[PoolPortsAnnotation], "")
	}

	r.portPoolMu.Lock()
	defer r.portPoolMu.Unlock()
	holders, err := r.poolHolders(ctx, gs, pool)
	if err != nil {
		return false, err
	}
	pending, _ := parsePoolRecordAt(gs, PendingPoolPortsAnnotation)
	demand := demandFor(gs, target)
	plan := planPoolPorts(gs, demand, pool, holders, []poolRecord{active, pending}, active)
	if !plan.ok {
		r.poolRolloutWaits.Store(gs.UID, poolRolloutWait{needed: demand.pooledCount(), conflict: plan.conflict})
		return false, nil
	}
	want := ""
	if plan.kept != 0 {
		want = plan.record.String()
	}
	return true, r.setPoolRecords(ctx, gs, active.String(), want)
}

// setPoolRecords writes the active and pending records ("" removes one),
// guarded by resourceVersion so a concurrent edit isn't overwritten.
func (r *GameServerReconciler) setPoolRecords(ctx context.Context, gs *gameplanev1alpha1.GameServer, active, pending string) error {
	want := map[string]string{PoolPortsAnnotation: active, PendingPoolPortsAnnotation: pending}
	changed := false
	for key, value := range want {
		if current, has := gs.Annotations[key]; (value == "" && has) || (value != "" && current != value) {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	patch := client.MergeFromWithOptions(gs.DeepCopy(), client.MergeFromWithOptimisticLock{})
	if gs.Annotations == nil {
		gs.Annotations = map[string]string{}
	}
	for key, value := range want {
		if value == "" {
			delete(gs.Annotations, key)
		} else {
			gs.Annotations[key] = value
		}
	}
	if err := r.Patch(ctx, gs, patch); err != nil {
		return fmt.Errorf("record pool ports: %w", err)
	}
	return nil
}

// poolServicePort is the Service port the active record gives an advertised
// port, or 0 when the server has no record for it.
func poolServicePort(gs *gameplanev1alpha1.GameServer, name string) int32 {
	rec, ok := parsePoolRecord(gs)
	if !ok {
		return 0
	}
	port, _ := rec.port(name)
	return port
}

// deleteUnpublishedService removes the main Service of a pooled server with
// no effective record, so it publishes nothing rather than ports another
// server in the pool may hold.
func (r *GameServerReconciler) deleteUnpublishedService(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	var svc corev1.Service
	if err := r.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name}, &svc); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&svc, gs) || !svc.DeletionTimestamp.IsZero() {
		return nil
	}
	uid := svc.UID
	return client.IgnoreNotFound(r.Delete(ctx, &svc, client.Preconditions{UID: &uid}))
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
		Status:             metav1.ConditionFalse,
	}
	// Each reason is written as a literal, which the condition-ids table's
	// guard reads.
	switch alloc.reason {
	case portReasonAllocated:
		cond.Reason = "Allocated"
		cond.Status = metav1.ConditionTrue
		cond.Message = fmt.Sprintf("Ports %s assigned from port pool '%s'.", poolPortsSummary(alloc.record.entries), alloc.pool)
	case portReasonOutsideRange:
		cond.Reason = "AllocatedOutsideRange"
		cond.Status = metav1.ConditionTrue
		cond.Message = fmt.Sprintf(
			"Ports %s assigned from port pool '%s'. Ports %s are outside the pool's range and stay in use until the server is moved; keep forwarding them.",
			poolPortsSummary(alloc.record.entries), alloc.pool, poolPortsSummary(alloc.outside))
	case portReasonExhausted:
		cond.Reason = "PoolExhausted"
		cond.Message = fmt.Sprintf(
			"Port pool '%s' has no free block of %d ports for this server, so it has no Service until a block frees up.",
			alloc.pool, alloc.needed)
	case portReasonConflict:
		cond.Reason = "ServicePortConflict"
		cond.Message = fmt.Sprintf(
			"Service port %s is held by %s in port pool '%s', so the server keeps ports %s until it is free.",
			alloc.conflict, alloc.conflict.holder, alloc.pool, poolPortsSummary(alloc.record.entries))
	case portReasonConflictNoSvc:
		cond.Reason = "ServicePortConflictUnpublished"
		cond.Message = fmt.Sprintf(
			"Service port %s is held by %s in port pool '%s', so the server has no Service until it is free.",
			alloc.conflict, alloc.conflict.holder, alloc.pool)
	case portReasonChangePending:
		cond.Reason = "ChangePending"
		cond.Message = fmt.Sprintf(
			"Port pool '%s' has no free block of %d ports for the requested change, so the server keeps ports %s until one frees up.",
			alloc.pool, alloc.needed, poolPortsSummary(alloc.record.entries))
	case portReasonRollout:
		cond.Reason = "RolloutPending"
		cond.Message = fmt.Sprintf(
			"The template update waits for %d free ports in port pool '%s'; the server keeps its current revision on ports %s.",
			alloc.needed, alloc.pool, poolPortsSummary(alloc.record.entries))
	case portReasonRolloutPort:
		cond.Reason = "RolloutPortConflict"
		cond.Message = fmt.Sprintf(
			"The template update waits for Service port %s, held by %s in port pool '%s'; the server keeps its current revision on ports %s.",
			alloc.conflict, alloc.conflict.holder, alloc.pool, poolPortsSummary(alloc.record.entries))
	}
	if existing := meta.FindStatusCondition(conds, cond.Type); existing != nil &&
		existing.Status == cond.Status && existing.Reason == cond.Reason && existing.Message == cond.Message {
		cond.LastTransitionTime = existing.LastTransitionTime
	}
	meta.SetStatusCondition(&conds, cond)
	return conds
}

// poolPortsSummary lists ports for a condition message, such as
// "2456/UDP, 2457/UDP".
func poolPortsSummary(entries []poolPortEntry) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%d/%s", e.port, e.protocol))
	}
	return strings.Join(parts, ", ")
}
