package main

import (
	"fmt"
	"net/http"
	"time"
)

// Fixed metric names and a closed state enum make the small exposition bounded.
// Scraping never polls a provider. Unknown connection is not encoded as down.
func relayMetrics(h *relayHealth) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		now := time.Now()
		snapshot := h.snapshot(now)
		checked := snapshot.checked
		known := snapshot.Connection == "connected" || snapshot.Connection == "unavailable"
		gauges := []struct {
			name, help string
			value      bool
		}{
			{"gameplane_tunnel_ready", "Whether the relay and all configured forwards are confirmed ready.", snapshot.Ready},
			{"gameplane_tunnel_connection_known", "Whether the provider exposes a fresh confirmed connection state.", known},
			{"gameplane_tunnel_forwards_registered", "Whether every configured forward is confirmed registered.", snapshot.Forwards},
			{"gameplane_tunnel_endpoint_assigned", "Whether the configured endpoint is assigned.", snapshot.Assigned},
		}
		if known {
			gauges = append(gauges, struct {
				name, help string
				value      bool
			}{"gameplane_tunnel_connected", "Whether the relay is connected; absent when unknown.", snapshot.Connection == "connected"})
		}
		for _, g := range gauges {
			n := 0
			if g.value {
				n = 1
			}
			_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", g.name, g.help, g.name, g.name, n)
		}
		if !checked.IsZero() && now.Sub(checked) < healthStaleAfter {
			_, _ = fmt.Fprintf(w, "# HELP gameplane_tunnel_observation_timestamp_seconds Unix time of the latest fresh provider observation.\n# TYPE gameplane_tunnel_observation_timestamp_seconds gauge\ngameplane_tunnel_observation_timestamp_seconds %.3f\n", float64(checked.UnixNano())/1e9)
		}
	})
}
