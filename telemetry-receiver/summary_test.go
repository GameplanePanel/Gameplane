package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"
)

// summaryNow is the clock of the summary tests: the latest complete day is
// 2026-10-06.
var summaryNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// newSummaryServer returns a receiver with PUBLIC_SUMMARY on (unless cfg says
// otherwise) and a controllable clock, seeded with a known dataset:
//   - 2026-10-06: 2 reports; 2026-10-05: 1; 2026-08-01 (outside 30 days): 1
//   - three activity records, of which two were seen in the last 30 days.
func newSummaryServer(t *testing.T, cfg config) (*server, *time.Time) {
	t.Helper()
	s, clock := newExtServer(t, cfg, summaryNow)
	ctx := context.Background()
	for _, day := range []string{"2026-10-06", "2026-10-06", "2026-10-05", "2026-08-01"} {
		if err := s.store.recordBasic(ctx, day, "1.0.0", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	for id, lastSeen := range map[string]string{"a": "2026-10-06", "b": "2026-09-08", "c": "2026-09-06"} {
		if _, err := s.store.db.ExecContext(ctx,
			`INSERT INTO activity (id_hmac, key_fp, first_seen, last_seen, last_sent_at, last_version)
			 VALUES (?, 'k', '2026-01-01', ?, '2026-01-01T00:00:00Z', '1.0.0')`, id, lastSeen); err != nil {
			t.Fatal(err)
		}
	}
	return s, clock
}

func getSummary(t *testing.T, s *server, remote string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/summary", nil)
	r.RemoteAddr = remote
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}

func TestSummaryHasExactlyFiveKeys(t *testing.T) {
	s, _ := newSummaryServer(t, config{publicSummary: true})
	w := getSummary(t, s, "192.0.2.1:1000", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != 5 {
		t.Fatalf("summary has %d keys, want exactly 5: %s", len(raw), w.Body)
	}
	var keys []string
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got := keys; len(got) != 5 || got[0] != "activeInstalls30d" || got[1] != "asOf" ||
		got[2] != "reports30d" || got[3] != "reportsLatestDay" || got[4] != "reportsTotal" {
		t.Fatalf("keys = %v", got)
	}
	var v summaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	want := summaryResponse{AsOf: "2026-10-06", ReportsLatestDay: 2, Reports30d: 3, ReportsTotal: 4, ActiveInstalls30d: 2}
	if v != want {
		t.Fatalf("summary = %+v, want %+v", v, want)
	}
}

func TestSummaryIs404WhenDisabled(t *testing.T) {
	s, _ := newSummaryServer(t, config{})
	if w := getSummary(t, s, "192.0.2.1:1000", nil); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	// A disabled summary does not use up the limiter either.
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "summary"); got != 0 {
		t.Fatalf("rate_limited_total{summary} = %v, want 0", got)
	}
}

func TestSummaryHeadersETagAndNotModified(t *testing.T) {
	s, _ := newSummaryServer(t, config{publicSummary: true})
	w := getSummary(t, s, "192.0.2.1:1000", nil)
	for h, want := range map[string]string{
		"Content-Type":                "application/json",
		"Cache-Control":               "public, max-age=3600",
		"Access-Control-Allow-Origin": "*",
	} {
		if got := w.Header().Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	etag := w.Header().Get("ETag")
	if len(etag) < 3 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		t.Fatalf("ETag = %q, want a quoted tag", etag)
	}
	for name, inm := range map[string]string{"exact": etag, "weak": "W/" + etag, "list": `"other", ` + etag, "star": "*"} {
		n := getSummary(t, s, "192.0.2.1:1000", map[string]string{"If-None-Match": inm})
		if n.Code != http.StatusNotModified || n.Body.Len() != 0 {
			t.Errorf("%s: status = %d with %d body bytes, want an empty 304", name, n.Code, n.Body.Len())
		}
		for _, h := range []string{"ETag", "Cache-Control", "Access-Control-Allow-Origin"} {
			if n.Header().Get(h) == "" {
				t.Errorf("%s: 304 lacks %s", name, h)
			}
		}
	}
	if other := getSummary(t, s, "192.0.2.1:1000", map[string]string{"If-None-Match": `"nope"`}); other.Code != http.StatusOK {
		t.Errorf("a different tag: status = %d, want 200", other.Code)
	}
}

func TestSummarySnapshotIsRecomputedAtMostEveryFiveMinutes(t *testing.T) {
	s, clock := newSummaryServer(t, config{publicSummary: true})
	first := getSummary(t, s, "192.0.2.1:1000", nil)
	if err := s.store.recordBasic(context.Background(), "2026-10-06", "1.0.0", 1, 1); err != nil {
		t.Fatal(err)
	}
	*clock = summaryNow.Add(4 * time.Minute)
	if again := getSummary(t, s, "192.0.2.1:1000", nil); again.Body.String() != first.Body.String() {
		t.Fatalf("snapshot changed within 5 minutes:\n%s\n%s", first.Body, again.Body)
	}
	*clock = summaryNow.Add(5 * time.Minute)
	later := getSummary(t, s, "192.0.2.1:1000", nil)
	var v summaryResponse
	if err := json.Unmarshal(later.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.ReportsLatestDay != 3 || v.ReportsTotal != 5 {
		t.Fatalf("after 5 minutes: %+v, want the new report counted", v)
	}
	if later.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatal("the ETag did not change with the content")
	}
}

func TestSummaryLimiterIsTenBurstPerSource(t *testing.T) {
	s, clock := newSummaryServer(t, config{publicSummary: true})
	for i := range summaryBurst {
		if w := getSummary(t, s, "192.0.2.1:1000", nil); w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, w.Code)
		}
	}
	w := getSummary(t, s, "192.0.2.1:2000", nil) // same source, another port
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over the burst: status = %d Retry-After %q, want 429 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "summary"); got != 1 {
		t.Fatalf("rate_limited_total{summary} = %v, want 1", got)
	}
	if other := getSummary(t, s, "192.0.2.9:1000", nil); other.Code != http.StatusOK {
		t.Fatalf("another source: status = %d, want 200", other.Code)
	}
	*clock = summaryNow.Add(2 * time.Second) // 60 per minute refills one token a second
	if w := getSummary(t, s, "192.0.2.1:1000", nil); w.Code != http.StatusOK {
		t.Fatalf("after a refill: status = %d, want 200", w.Code)
	}
}

func TestSummaryTotalSurvivesARetentionSweep(t *testing.T) {
	s, clock := newSummaryServer(t, config{publicSummary: true})
	before := getSummary(t, s, "192.0.2.1:1000", nil)
	// Sweep with a retention of 365 days from a "now" so late that every
	// daily aggregate is deleted.
	*clock = time.Date(2028, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := s.store.lifecycleOnce(context.Background(), *clock, 365); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t.Context(), t, s.store, `SELECT count(*) FROM daily_basic`); got != 0 {
		t.Fatalf("daily_basic rows after the sweep = %d, want 0", got)
	}
	after := getSummary(t, s, "192.0.2.1:1000", nil)
	var b, a summaryResponse
	if err := json.Unmarshal(before.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if a.ReportsTotal != b.ReportsTotal || a.ReportsTotal != 4 {
		t.Fatalf("reportsTotal = %d after the sweep, was %d, want 4 unchanged", a.ReportsTotal, b.ReportsTotal)
	}
	if a.Reports30d != 0 || a.ReportsLatestDay != 0 {
		t.Fatalf("after the sweep %+v, want the windowed counts at 0", a)
	}
}

func TestSummaryWithoutDataIsZeros(t *testing.T) {
	s, _ := newExtServer(t, config{publicSummary: true}, summaryNow)
	w := getSummary(t, s, "192.0.2.1:1000", nil)
	var v summaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v != (summaryResponse{AsOf: "2026-10-06"}) {
		t.Fatalf("empty summary = %+v, want zeros and asOf 2026-10-06", v)
	}
}

func TestSummaryFailureIs500ButServesTheLastSnapshot(t *testing.T) {
	s, _ := newSummaryServer(t, config{publicSummary: true})
	if _, err := s.store.db.ExecContext(context.Background(), "DROP TABLE daily_basic"); err != nil {
		t.Fatal(err)
	}
	if w := getSummary(t, s, "192.0.2.1:1000", nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("no snapshot yet: status = %d, want 500", w.Code)
	}
	s2, clock2 := newSummaryServer(t, config{publicSummary: true})
	good := getSummary(t, s2, "192.0.2.1:1000", nil)
	*clock2 = summaryNow.Add(10 * time.Minute)
	if _, err := s2.store.db.ExecContext(context.Background(), "DROP TABLE daily_basic"); err != nil {
		t.Fatal(err)
	}
	if w := getSummary(t, s2, "192.0.2.1:1000", nil); w.Code != http.StatusOK || w.Body.String() != good.Body.String() {
		t.Fatalf("recomputation failed: status = %d body %q, want the last snapshot", w.Code, w.Body)
	}
}

func TestEtagMatches(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"", false}, {`"a"`, true}, {`W/"a"`, true}, {`"b", "a"`, true}, {"*", true}, {`"b"`, false},
	} {
		if got := etagMatches(tc.header, `"a"`); got != tc.want {
			t.Errorf("etagMatches(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}
