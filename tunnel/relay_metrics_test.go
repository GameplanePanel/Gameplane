package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRelayMetricsUnknownStaleAndRecovery(t *testing.T) {
	h := &relayHealth{}
	for _, tc := range []struct {
		state            relayObservation
		age              time.Duration
		connected, ready string
	}{
		{relayObservation{Connection: "unknown", Assigned: true}, 0, "", "0"},
		{relayObservation{Connection: "connected", Assigned: true, Forwards: true}, 0, "1", "1"},
		{relayObservation{Connection: "connected", Assigned: true, Forwards: true}, healthStaleAfter + time.Second, "", "0"},
		{relayObservation{Connection: "unavailable"}, 0, "0", "0"},
	} {
		h.observe(time.Now().Add(-tc.age), tc.state, nil)
		w := httptest.NewRecorder()
		relayHealthHandler(h).ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
		body := w.Body.String()
		if !strings.Contains(body, "gameplane_tunnel_ready "+tc.ready+"\n") {
			t.Fatal(body)
		}
		if tc.connected == "" {
			if strings.Contains(body, "gameplane_tunnel_connected ") {
				t.Fatal("unknown fabricated as connected/down")
			}
		} else if !strings.Contains(body, "gameplane_tunnel_connected "+tc.connected+"\n") {
			t.Fatal(body)
		}
	}
}
