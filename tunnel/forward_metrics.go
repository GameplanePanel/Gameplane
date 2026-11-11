package main

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// One instance per Tailscale supervisor, shared by every listener and retry.
type forwardTelemetry struct {
	limits              forwardLimits
	tcp, udp            flowCounts
	queueDrops          atomic.Uint64
	toBackend, toClient forwardLatency
}

type flowCounts struct {
	active   atomic.Int64
	rejected atomic.Uint64
}

var latencyBounds = [...]time.Duration{100 * time.Microsecond, 500 * time.Microsecond, time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond}

type forwardLatency struct {
	bins  [len(latencyBounds) + 1]atomic.Uint64
	nanos atomic.Int64
}

func (h *forwardLatency) observe(delay time.Duration) {
	i := 0
	for i < len(latencyBounds) && delay > latencyBounds[i] {
		i++
	}
	h.bins[i].Add(1)
	h.nanos.Add(int64(delay))
}

func (m *forwardTelemetry) writeMetrics(w io.Writer) {
	const prefix = "gameplane_tunnel_"
	protocols := [...]struct {
		name string
		f    *flowCounts
	}{{"tcp", &m.tcp}, {"udp", &m.udp}}
	_, _ = fmt.Fprintln(w, "# TYPE "+prefix+"active_flows gauge")
	for _, p := range protocols {
		_, _ = fmt.Fprintf(w, "%sactive_flows{protocol=%q} %d\n", prefix, p.name, p.f.active.Load())
	}
	_, _ = fmt.Fprintln(w, "# TYPE "+prefix+"flow_rejections_total counter")
	for _, p := range protocols {
		_, _ = fmt.Fprintf(w, "%sflow_rejections_total{protocol=%q} %d\n", prefix, p.name, p.f.rejected.Load())
	}
	_, _ = fmt.Fprintf(w, "# TYPE %sudp_queue_drops_total counter\n%sudp_queue_drops_total %d\n", prefix, prefix, m.queueDrops.Load())
	const hist = prefix + "udp_forward_duration_seconds"
	_, _ = fmt.Fprintln(w, "# TYPE "+hist+" histogram")
	dirs := [...]struct {
		name string
		h    *forwardLatency
	}{{"to_backend", &m.toBackend}, {"to_client", &m.toClient}}
	var packets [len(dirs)]uint64
	for i, d := range dirs {
		var total uint64
		for b, limit := range latencyBounds {
			total += d.h.bins[b].Load()
			_, _ = fmt.Fprintf(w, "%s_bucket{direction=%q,le=\"%g\"} %d\n", hist, d.name, limit.Seconds(), total)
		}
		total += d.h.bins[len(latencyBounds)].Load()
		packets[i] = total
		_, _ = fmt.Fprintf(w, "%s_bucket{direction=%q,le=\"+Inf\"} %d\n", hist, d.name, total)
		_, _ = fmt.Fprintf(w, "%s_count{direction=%q} %d\n", hist, d.name, total)
		_, _ = fmt.Fprintf(w, "%s_sum{direction=%q} %g\n", hist, d.name, float64(d.h.nanos.Load())/1e9)
	}
	_, _ = fmt.Fprintln(w, "# TYPE "+prefix+"udp_forwarded_packets_total counter")
	for i, d := range dirs {
		_, _ = fmt.Fprintf(w, "%sudp_forwarded_packets_total{direction=%q} %d\n", prefix, d.name, packets[i])
	}
}
