package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

var limiterNow = time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestDailyLimiterCapsPerSourcePerUTCDay(t *testing.T) {
	l := newDailyLimiter(2)
	a, b := addr("192.0.2.1"), addr("192.0.2.2")
	first, second := l.take(a, limiterNow), l.take(a, limiterNow)
	if !first || !second {
		t.Fatal("first two reports from a source must be accepted")
	}
	if l.take(a, limiterNow) {
		t.Fatal("third report from the same source must be refused")
	}
	if !l.take(b, limiterNow) {
		t.Fatal("another source has its own budget")
	}
	if l.take(a, limiterNow.Add(13*time.Hour-time.Second)) {
		t.Fatal("still the same UTC day: must stay refused")
	}
	if !l.take(a, limiterNow.Add(14*time.Hour)) {
		t.Fatal("the counter resets at UTC midnight")
	}
}

func TestDailyLimiterZeroMeansUnlimitedAndKeepsNoState(t *testing.T) {
	l := newDailyLimiter(0)
	for range 500 {
		if !l.take(addr("192.0.2.1"), limiterNow) {
			t.Fatal("limit 0 must never refuse")
		}
	}
	if len(l.counts) != 0 {
		t.Fatalf("limit 0 tracked %d sources, want 0", len(l.counts))
	}
}

func TestDailyLimiterLRUEviction(t *testing.T) {
	l := newDailyLimiter(1)
	l.max = 2
	a, b, c := addr("192.0.2.1"), addr("192.0.2.2"), addr("192.0.2.3")

	// First two sources get tracked
	if !l.take(a, limiterNow) {
		t.Fatal("source a, first report")
	}
	if !l.take(b, limiterNow) {
		t.Fatal("source b, first report")
	}

	// Touch a again to make it recently used
	if l.take(a, limiterNow) {
		t.Fatal("source a is limited after first report")
	}

	// Add c: b should be evicted (least recently used)
	if !l.take(c, limiterNow) {
		t.Fatal("source c gets tracked after eviction")
	}

	// Verify the map never exceeds the cap
	if len(l.counts) != 2 {
		t.Fatalf("counts map size = %d, want 2", len(l.counts))
	}

	// b should be evicted, so we can add it again
	if !l.take(b, limiterNow) {
		t.Fatal("evicted source b can be added again with fresh budget")
	}
}

func TestDailyLimiterEvictedSourceHasFreshBudget(t *testing.T) {
	l := newDailyLimiter(1)
	l.max = 2
	a, b, c := addr("192.0.2.1"), addr("192.0.2.2"), addr("192.0.2.3")

	// Track a
	if !l.take(a, limiterNow) {
		t.Fatal("source a, first report")
	}
	// Track b and use its budget
	if !l.take(b, limiterNow) {
		t.Fatal("source b, first report")
	}
	if l.take(b, limiterNow) {
		t.Fatal("source b is limited")
	}
	// Touch a so that b is the least recently used entry
	if l.take(a, limiterNow) {
		t.Fatal("source a is limited")
	}

	// Add c, evicting b
	if !l.take(c, limiterNow) {
		t.Fatal("source c gets tracked")
	}

	// Now b has a fresh budget if we add it again
	if !l.take(b, limiterNow) {
		t.Fatal("evicted source b starts with fresh budget")
	}
	if l.take(b, limiterNow) {
		t.Fatal("fresh budget of b should be exhausted")
	}
}

func TestDailyLimiterTrackedSourceKeepsSpentBudget(t *testing.T) {
	l := newDailyLimiter(3)
	l.max = 2
	a, b, c := addr("192.0.2.1"), addr("192.0.2.2"), addr("192.0.2.3")

	// Track a and spend one token
	if !l.take(a, limiterNow) {
		t.Fatal("source a, first report")
	}
	// Track b
	if !l.take(b, limiterNow) {
		t.Fatal("source b, first report")
	}

	// Touch a to make it recently used, then add c (evicting b)
	if !l.take(a, limiterNow) {
		t.Fatal("source a, second report")
	}
	if !l.take(c, limiterNow) {
		t.Fatal("source c gets tracked")
	}

	// a should still have only one token left (not reset)
	if !l.take(a, limiterNow) {
		t.Fatal("source a should still have its second token")
	}
	if l.take(a, limiterNow) {
		t.Fatal("source a's spent budget is kept")
	}
}

func TestBucketLimiterBurstRefillAndSeparateSources(t *testing.T) {
	l := newBucketLimiter(60, 2) // one token per second, burst 2
	a, b := addr("192.0.2.1"), addr("192.0.2.2")
	first, second := l.allow(a, limiterNow), l.allow(a, limiterNow)
	if !first || !second {
		t.Fatal("the burst of 2 must be allowed")
	}
	if l.allow(a, limiterNow) {
		t.Fatal("burst exhausted: must be refused")
	}
	if !l.allow(b, limiterNow) {
		t.Fatal("another source has its own bucket")
	}
	if !l.allow(a, limiterNow.Add(time.Second)) {
		t.Fatal("one token is refilled after a second")
	}
	if l.allow(a, limiterNow.Add(time.Second)) {
		t.Fatal("only one token was refilled")
	}
	// The bucket never holds more than the burst, however long it idles.
	later := limiterNow.Add(5 * time.Second)
	first, second, third := l.allow(a, later), l.allow(a, later), l.allow(a, later)
	if !first || !second || third {
		t.Fatal("after a long idle the bucket holds exactly the burst")
	}
}

func TestBucketLimiterPurgesIdleSources(t *testing.T) {
	l := newBucketLimiter(1, 1)
	a, b := addr("192.0.2.1"), addr("192.0.2.2")
	l.allow(a, limiterNow)
	l.allow(b, limiterNow.Add(5*time.Minute))
	if len(l.buckets) != 2 {
		t.Fatalf("buckets = %d, want 2 before the idle purge", len(l.buckets))
	}
	l.allow(b, limiterNow.Add(11*time.Minute))
	if _, ok := l.buckets[sourceKey(a)]; ok {
		t.Fatal("source a idle for 11 minutes must be purged")
	}
	if _, ok := l.buckets[sourceKey(b)]; !ok || len(l.buckets) != 1 {
		t.Fatalf("source b idle for 6 minutes must be kept; buckets = %d", len(l.buckets))
	}
}

func TestBucketLimiterResetsAtUTCMidnight(t *testing.T) {
	l := newBucketLimiter(1, 1) // one token per minute
	a := addr("192.0.2.1")
	late := time.Date(2026, 10, 7, 23, 59, 50, 0, time.UTC)
	if !l.allow(a, late) {
		t.Fatal("first request")
	}
	if l.allow(a, late.Add(10*time.Second-time.Nanosecond)) {
		t.Fatal("before midnight the bucket is still empty")
	}
	if !l.allow(a, late.Add(10*time.Second)) {
		t.Fatal("limiter state is reset at UTC midnight")
	}
}

func TestBucketLimiterLRUEviction(t *testing.T) {
	l := newBucketLimiter(1, 1) // one token per minute, burst 1
	l.max = 2
	a, b, c := addr("192.0.2.1"), addr("192.0.2.2"), addr("192.0.2.3")

	// First two sources get tracked with burst 1 each
	if !l.allow(a, limiterNow) {
		t.Fatal("source a, first request")
	}
	if !l.allow(b, limiterNow) {
		t.Fatal("source b, first request")
	}

	// Touch a to make it recently used, then add c (should evict b)
	if l.allow(a, limiterNow) {
		t.Fatal("source a is limited after using its burst")
	}
	if !l.allow(c, limiterNow) {
		t.Fatal("source c gets tracked after evicting b")
	}

	// Verify the map never exceeds the cap
	if len(l.buckets) != 2 {
		t.Fatalf("buckets map size = %d, want 2", len(l.buckets))
	}

	// b should be evicted, so it can be added again
	if !l.allow(b, limiterNow) {
		t.Fatal("evicted source b can be added again with fresh burst")
	}
}

func TestBucketLimiterEvictedSourceHasFreshBudget(t *testing.T) {
	l := newBucketLimiter(1, 1) // one token per minute, burst 1
	l.max = 2
	a, b, c := addr("192.0.2.1"), addr("192.0.2.2"), addr("192.0.2.3")

	// Track a and use its burst
	if !l.allow(a, limiterNow) {
		t.Fatal("source a, first request")
	}
	if l.allow(a, limiterNow) {
		t.Fatal("source a is limited")
	}

	// Track b with its burst
	if !l.allow(b, limiterNow) {
		t.Fatal("source b, first request")
	}
	// Touch a so that b is the least recently used entry
	if l.allow(a, limiterNow) {
		t.Fatal("source a is still limited")
	}

	// Add c, evicting b
	if !l.allow(c, limiterNow) {
		t.Fatal("source c gets tracked")
	}

	// b starts with fresh burst
	if !l.allow(b, limiterNow) {
		t.Fatal("evicted source b starts with fresh burst")
	}
	if l.allow(b, limiterNow) {
		t.Fatal("fresh burst of b should be exhausted")
	}
}

func TestBucketLimiterTrackedSourceKeepsSpentBudget(t *testing.T) {
	l := newBucketLimiter(60, 3) // one token per second, burst 3
	l.max = 2
	a, b, c := addr("192.0.2.1"), addr("192.0.2.2"), addr("192.0.2.3")

	// Track a and spend one token
	if !l.allow(a, limiterNow) {
		t.Fatal("source a, first request")
	}
	// Track b with its burst
	if !l.allow(b, limiterNow) {
		t.Fatal("source b, first request")
	}

	// Touch a to make it recently used, then add c (evicting b)
	if !l.allow(a, limiterNow) {
		t.Fatal("source a, second request")
	}
	if !l.allow(c, limiterNow) {
		t.Fatal("source c gets tracked")
	}

	// a should still have only one token left (not reset)
	if !l.allow(a, limiterNow) {
		t.Fatal("source a should still have its second token")
	}
	if l.allow(a, limiterNow) {
		t.Fatal("source a's spent budget is kept")
	}
}

func reqFrom(remote string, xff ...string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestSourceIPHonoursForwardedForOnlyFromTrustedPeers(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}
	cases := []struct {
		name    string
		remote  string
		xff     []string
		trusted []netip.Prefix
		want    string // empty means the zero Addr
	}{
		{"untrusted peer ignores header", "192.0.2.1:4000", []string{"203.0.113.9"}, trusted, "192.0.2.1"},
		{"no trusted list ignores header", "10.0.0.1:4000", []string{"203.0.113.9"}, nil, "10.0.0.1"},
		{"trusted peer uses header", "10.0.0.1:4000", []string{"203.0.113.9"}, trusted, "203.0.113.9"},
		{"right-most untrusted entry wins", "10.0.0.1:4000", []string{"198.51.100.1, 203.0.113.9"}, trusted, "203.0.113.9"},
		{"trusted hops are skipped", "10.0.0.1:4000", []string{"203.0.113.9, 10.0.0.5"}, trusted, "203.0.113.9"},
		{"all entries trusted gives the peer", "10.0.0.1:4000", []string{"10.0.0.7"}, trusted, "10.0.0.1"},
		{"no header gives the peer", "10.0.0.1:4000", nil, trusted, "10.0.0.1"},
		{"unparsable entry gives the peer", "10.0.0.1:4000", []string{"garbage"}, trusted, "10.0.0.1"},
		{"empty entries give the peer", "10.0.0.1:4000", []string{", ,"}, trusted, "10.0.0.1"},
		{"several header lines are joined", "10.0.0.1:4000", []string{"198.51.100.1", "203.0.113.9"}, trusted, "203.0.113.9"},
		{"ipv6 trusted peer", "[2001:db8::1]:4000", []string{"203.0.113.9"}, trusted, "203.0.113.9"},
		{"ipv4-mapped peer is unmapped", "[::ffff:10.0.0.1]:4000", []string{"::ffff:203.0.113.9"}, trusted, "203.0.113.9"},
		{"peer without a port", "10.0.0.1", []string{"203.0.113.9"}, trusted, "203.0.113.9"},
		{"unparsable peer is the zero address", "garbage", []string{"203.0.113.9"}, trusted, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sourceIP(reqFrom(tc.remote, tc.xff...), tc.trusted)
			if tc.want == "" {
				if got.IsValid() {
					t.Fatalf("sourceIP = %v, want the zero Addr", got)
				}
				return
			}
			if got != addr(tc.want) {
				t.Fatalf("sourceIP = %v, want %s", got, tc.want)
			}
		})
	}
}

// counterValue reads a counter series from the server's registry; it is 0
// when the series does not exist.
func counterValue(t *testing.T, s *server, name, labelName, labelValue string) float64 {
	t.Helper()
	families, err := s.reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.Metric {
			for _, l := range m.Label {
				if l.GetName() == labelName && l.GetValue() == labelValue {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

const okReport = `{"version":"1.0.0","servers":1,"templates":1}`

func ingestFrom(t *testing.T, s *server, remote, body string, xff ...string) int {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", strings.NewReader(body))
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	w := httptest.NewRecorder()
	s.ingest(w, r)
	return w.Code
}

func TestIngestRateLimitReturns429(t *testing.T) {
	s := newServer(config{ingestSourceDailyLimit: 2})
	now := limiterNow
	s.now = func() time.Time { return now }
	for i := range 2 {
		if code := ingestFrom(t, s, "192.0.2.1:1000", okReport); code != http.StatusNoContent {
			t.Fatalf("report %d: status = %d, want 204", i, code)
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", strings.NewReader(okReport))
	r.RemoteAddr = "192.0.2.1:2000" // another port, same source
	w := httptest.NewRecorder()
	s.ingest(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("third report: status = %d, want 429", w.Code)
	}
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "ingest"); got != 1 {
		t.Fatalf("rate_limited_total{route=ingest} = %v, want 1", got)
	}
	// A refused report changes nothing: two reports are stored and counted.
	if got := mustMeta(t, s.store, "reports_total"); got != "2" {
		t.Fatalf("reports_total = %s, want 2", got)
	}
	if got := countRows(t.Context(), t, s.store, `SELECT reports FROM daily_basic WHERE day = ?`, "2026-10-07"); got != 2 {
		t.Fatalf("daily_basic.reports = %d, want 2", got)
	}
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "1.0.0"); got != 2 {
		t.Fatalf("reports_total{version} = %v, want 2", got)
	}
	// Another source is unaffected, and the next UTC day starts afresh.
	if code := ingestFrom(t, s, "192.0.2.9:1000", okReport); code != http.StatusNoContent {
		t.Fatalf("other source: status = %d, want 204", code)
	}
	now = now.Add(24 * time.Hour)
	if code := ingestFrom(t, s, "192.0.2.1:1000", okReport); code != http.StatusNoContent {
		t.Fatalf("next day: status = %d, want 204", code)
	}
}

func TestIngestRateLimitOnlyCountsAcceptedReports(t *testing.T) {
	s := newServer(config{ingestSourceDailyLimit: 1})
	for range 5 {
		if code := ingestFrom(t, s, "192.0.2.1:1000", `{not json`); code != http.StatusBadRequest {
			t.Fatalf("invalid report: status = %d, want 400", code)
		}
	}
	if code := ingestFrom(t, s, "192.0.2.1:1000", okReport); code != http.StatusNoContent {
		t.Fatalf("valid report after invalid ones: status = %d, want 204", code)
	}
	if code := ingestFrom(t, s, "192.0.2.1:1000", okReport); code != http.StatusTooManyRequests {
		t.Fatalf("second valid report: status = %d, want 429", code)
	}
}

func TestIngestRateLimitZeroIsUnlimited(t *testing.T) {
	s := newServer(config{})
	for i := range 60 {
		if code := ingestFrom(t, s, "192.0.2.1:1000", okReport); code != http.StatusNoContent {
			t.Fatalf("report %d: status = %d, want 204", i, code)
		}
	}
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "ingest"); got != 0 {
		t.Fatalf("rate_limited_total = %v, want 0", got)
	}
}

func TestIngestRateLimitHonoursForwardedForOnlyFromTrustedPeers(t *testing.T) {
	s := newServer(config{
		ingestSourceDailyLimit: 1,
		trustedProxyCIDRs:      []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	})
	// A trusted proxy: the forwarded client is the source.
	if code := ingestFrom(t, s, "10.0.0.1:1000", okReport, "203.0.113.5"); code != http.StatusNoContent {
		t.Fatalf("client 1: status = %d, want 204", code)
	}
	if code := ingestFrom(t, s, "10.0.0.1:1000", okReport, "203.0.113.5"); code != http.StatusTooManyRequests {
		t.Fatalf("client 1 again: status = %d, want 429", code)
	}
	if code := ingestFrom(t, s, "10.0.0.1:1000", okReport, "203.0.113.6"); code != http.StatusNoContent {
		t.Fatalf("client 2 behind the same proxy: status = %d, want 204", code)
	}
	// An untrusted peer cannot choose its source by sending the header.
	if code := ingestFrom(t, s, "192.0.2.1:1000", okReport, "203.0.113.50"); code != http.StatusNoContent {
		t.Fatalf("untrusted peer: status = %d, want 204", code)
	}
	for i := range 3 {
		spoof := fmt.Sprintf("203.0.113.%d", 100+i)
		if code := ingestFrom(t, s, "192.0.2.1:1000", okReport, spoof); code != http.StatusTooManyRequests {
			t.Fatalf("spoofed header %s: status = %d, want 429", spoof, code)
		}
	}
}

func TestPeerAddr(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.1:80":          "192.0.2.1",
		"[2001:db8::1]:80":      "2001:db8::1",
		"192.0.2.1":             "192.0.2.1",
		"[::ffff:192.0.2.1]:80": "192.0.2.1",
		"[fe80::1%eth0]:80":     "fe80::1",
	} {
		if got := peerAddr(in); got != addr(want) {
			t.Errorf("peerAddr(%q) = %v, want %s", in, got, want)
		}
	}
	if peerAddr("not an address").IsValid() {
		t.Error("peerAddr of garbage must be the zero Addr")
	}
}

func TestSourceKeyIPv6SlashSixtyFour(t *testing.T) {
	// Two IPv6 addresses in the same /64 share a key
	a1 := addr("2001:db8::1")
	a2 := addr("2001:db8::2")
	k1 := sourceKey(a1)
	k2 := sourceKey(a2)
	if k1 != k2 {
		t.Fatalf("sourceKey(%v) = %v, sourceKey(%v) = %v, want equal", a1, k1, a2, k2)
	}
	// But a different /64 has its own key
	a3 := addr("2001:db9::1")
	k3 := sourceKey(a3)
	if k3 == k1 {
		t.Fatalf("sourceKey(%v) = %v, sourceKey(%v) = %v, want different", a1, k1, a3, k3)
	}
}

func TestSourceKeyIPv4MappedSharedWithIPv4(t *testing.T) {
	// An IPv4 address and its IPv4-mapped IPv6 form share a key
	a4 := addr("192.0.2.1")
	a6mapped := addr("::ffff:192.0.2.1")
	k4 := sourceKey(a4)
	k6mapped := sourceKey(a6mapped)
	if k4 != k6mapped {
		t.Fatalf("sourceKey(%v) = %v, sourceKey(%v) = %v, want equal", a4, k4, a6mapped, k6mapped)
	}
}

func TestSourceKeyInvalidAddressUsesSentinel(t *testing.T) {
	// Invalid addresses map to the sentinel key
	k := sourceKey(netip.Addr{})
	if k != invalidKey {
		t.Fatalf("sourceKey(invalid) = %v, want %v", k, invalidKey)
	}
}

func TestDailyLimiterIPv6SharingSameBudget(t *testing.T) {
	l := newDailyLimiter(2)
	a1 := addr("2001:db8::1")
	a2 := addr("2001:db8::2")
	// Both addresses share one /64, so they share the budget
	if !l.take(a1, limiterNow) || !l.take(a2, limiterNow) {
		t.Fatal("two addresses in same /64 must share one budget")
	}
	if l.take(a1, limiterNow) {
		t.Fatal("shared budget exhausted")
	}
	if l.take(a2, limiterNow) {
		t.Fatal("shared budget exhausted")
	}
}

func TestDailyLimiterIPv6DifferentSubnets(t *testing.T) {
	l := newDailyLimiter(1)
	a1 := addr("2001:db8::1")
	a2 := addr("2001:db9::1")
	// Different /64 subnets have separate budgets
	if !l.take(a1, limiterNow) || !l.take(a2, limiterNow) {
		t.Fatal("two addresses in different /64 must have separate budgets")
	}
	if l.take(a1, limiterNow) || l.take(a2, limiterNow) {
		t.Fatal("each budget should be exhausted")
	}
}

func TestBucketLimiterIPv6SharingSameBudget(t *testing.T) {
	l := newBucketLimiter(60, 2) // one token per second, burst 2
	a1 := addr("2001:db8::1")
	a2 := addr("2001:db8::2")
	// Both addresses in same /64 share the budget
	if !l.allow(a1, limiterNow) || !l.allow(a2, limiterNow) {
		t.Fatal("two addresses in same /64 must share burst")
	}
	if l.allow(a1, limiterNow) || l.allow(a2, limiterNow) {
		t.Fatal("shared burst should be exhausted")
	}
}
