package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// PoolPortAnnotation records the first Service port the operator gave a
// server from its template's port pool (--port-pool). The server's advertised
// ports take that port and the ones after it, in template order. It is an
// operator-owned annotation, not status, for the reason the idle markers are:
// status is a derived read model a `kubectl replace` can drop, and losing the
// record would move a server to new ports, breaking every address players
// have saved.
const PoolPortAnnotation = "gameplane.local/pool-port"

// PortPool is a range of Service ports for one template's servers. Servers
// behind a relay or a port-forwarding edge that routes by port alone, such as
// games whose clients send no hostname, each need ports of their own; the
// pool hands them out.
type PortPool struct {
	First int32
	Last  int32
}

// ParsePortPools parses --port-pool values: "<template>=<first>-<last>".
func ParsePortPools(values []string) (map[string]PortPool, error) {
	pools := map[string]PortPool{}
	for _, v := range values {
		name, span, ok := strings.Cut(v, "=")
		first, last, ok2 := strings.Cut(span, "-")
		if !ok || !ok2 || name == "" {
			return nil, fmt.Errorf("port pool %q: want <template>=<first>-<last>", v)
		}
		f, err := strconv.ParseInt(first, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("port pool %q: %w", v, err)
		}
		l, err := strconv.ParseInt(last, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("port pool %q: %w", v, err)
		}
		if f < 1 || l > 65535 || l < f {
			return nil, fmt.Errorf("port pool %q: want 1 <= first <= last <= 65535", v)
		}
		if _, dup := pools[name]; dup {
			return nil, fmt.Errorf("port pool %q: template %s has two pools", v, name)
		}
		pools[name] = PortPool{First: int32(f), Last: int32(l)}
	}
	return pools, nil
}

// advertisedPortCount is how many Service ports a template's servers have:
// a pool hands each server a block of that many.
func advertisedPortCount(tmpl *gameplanev1alpha1.GameTemplate) int32 {
	var n int32
	for _, p := range tmpl.Spec.Ports {
		if p.Advertise {
			n++
		}
	}
	return n
}

// poolPort reads a server's recorded first pool port, if it is one of the
// pool's block starts.
func poolPort(gs *gameplanev1alpha1.GameServer, pool PortPool, block int32) (int32, bool) {
	raw, ok := gs.Annotations[PoolPortAnnotation]
	if !ok || block < 1 {
		return 0, false
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, false
	}
	port := int32(v)
	if port < pool.First || port+block-1 > pool.Last || (port-pool.First)%block != 0 {
		return 0, false
	}
	return port, true
}

// assignPoolPort picks the server's first pool port among its template's
// servers. A server keeps the block it holds unless an older server holds it
// too (after a restore, say); otherwise it gets the lowest free block. Older
// servers are settled first, so the answer is stable across reconciles. ok is
// false when every block is taken.
func assignPoolPort(
	gs *gameplanev1alpha1.GameServer, servers []gameplanev1alpha1.GameServer, pool PortPool, block int32,
) (int32, bool) {
	if block < 1 {
		return 0, false
	}
	older := func(a, b *gameplanev1alpha1.GameServer) bool {
		if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
			return a.CreationTimestamp.Before(&b.CreationTimestamp)
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	}
	ordered := make([]*gameplanev1alpha1.GameServer, 0, len(servers))
	for i := range servers {
		ordered = append(ordered, &servers[i])
	}
	sort.Slice(ordered, func(i, j int) bool { return older(ordered[i], ordered[j]) })

	taken := map[int32]bool{}
	assigned := map[string]int32{}
	key := func(s *gameplanev1alpha1.GameServer) string { return s.Namespace + "/" + s.Name }
	for _, s := range ordered {
		if port, ok := poolPort(s, pool, block); ok && !taken[port] {
			taken[port] = true
			assigned[key(s)] = port
		}
	}
	for _, s := range ordered {
		if _, ok := assigned[key(s)]; ok {
			continue
		}
		for port := pool.First; port+block-1 <= pool.Last; port += block {
			if !taken[port] {
				taken[port] = true
				assigned[key(s)] = port
				break
			}
		}
	}
	port, ok := assigned[key(gs)]
	return port, ok
}

// reconcilePoolPort keeps the server's pool port annotation current: set from
// its template's pool, or removed when the template has none. It updates gs in
// memory too, so the Service this pass builds uses the ports.
func (r *GameServerReconciler) reconcilePoolPort(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	pool, pooled := r.PortPools[tmpl.Name]
	current, has := gs.Annotations[PoolPortAnnotation]
	want := ""
	if pooled {
		var list gameplanev1alpha1.GameServerList
		if err := r.List(ctx, &list); err != nil {
			return fmt.Errorf("list GameServers for port pool %s: %w", tmpl.Name, err)
		}
		var servers []gameplanev1alpha1.GameServer
		for _, s := range list.Items {
			if s.Spec.TemplateRef.Name == tmpl.Name && s.DeletionTimestamp.IsZero() {
				servers = append(servers, s)
			}
		}
		port, ok := assignPoolPort(gs, servers, pool, advertisedPortCount(tmpl))
		if !ok {
			logf.FromContext(ctx).Info("port pool exhausted; the server keeps its template's ports",
				"template", tmpl.Name, "pool", fmt.Sprintf("%d-%d", pool.First, pool.Last))
		} else {
			want = strconv.Itoa(int(port))
		}
	}
	if (want == "" && !has) || (has && current == want) {
		return nil
	}
	patch := client.MergeFrom(gs.DeepCopy())
	if gs.Annotations == nil {
		gs.Annotations = map[string]string{}
	}
	if want == "" {
		delete(gs.Annotations, PoolPortAnnotation)
	} else {
		gs.Annotations[PoolPortAnnotation] = want
	}
	if err := r.Patch(ctx, gs, patch); err != nil {
		return fmt.Errorf("record pool port: %w", err)
	}
	return nil
}

// poolServicePort is the Service port for the index-th advertised port of a
// server with a recorded pool port, or 0 when it has none.
func poolServicePort(gs *gameplanev1alpha1.GameServer, index int32) int32 {
	raw, ok := gs.Annotations[PoolPortAnnotation]
	if !ok {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || v < 1 || v+int64(index) > 65535 {
		return 0
	}
	return int32(v) + index
}
