package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// postIngest posts body to /ingest from remote and returns the status.
func postIngest(t *testing.T, h http.Handler, remote, body string, headers map[string]string) int {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", strings.NewReader(body))
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusTooManyRequests && rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
	return rec.Code
}

// TestIngestRequestBudgetCountsRefusedRequests checks that the per-source
// request budget counts every request, so malformed and unauthorized reports
// cannot be sent at an unbounded rate, and that other sources keep their own
// budget.
func TestIngestRequestBudgetCountsRefusedRequests(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	s := newServer(config{ingestSourcePerMinute: 3, authToken: "Bearer s3cret"})
	s.now = func() time.Time { return now }
	h := s.routes()
	good := map[string]string{"Authorization": "Bearer s3cret"}
	report := okReport

	if got := postIngest(t, h, "192.0.2.1:1000", "not json", good); got != http.StatusBadRequest {
		t.Fatalf("malformed: status = %d, want 400", got)
	}
	if got := postIngest(t, h, "192.0.2.1:1000", report, map[string]string{"Authorization": "wrong"}); got != http.StatusUnauthorized {
		t.Fatalf("bad token: status = %d, want 401", got)
	}
	if got := postIngest(t, h, "192.0.2.1:1000", report, good); got != http.StatusNoContent {
		t.Fatalf("third request: status = %d, want 204", got)
	}
	if got := postIngest(t, h, "192.0.2.1:1000", report, good); got != http.StatusTooManyRequests {
		t.Fatalf("fourth request: status = %d, want 429", got)
	}
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "ingest"); got != 1 {
		t.Fatalf("rate_limited_total{route=ingest} = %v, want 1", got)
	}
	if got := postIngest(t, h, "198.51.100.7:1000", report, good); got != http.StatusNoContent {
		t.Fatalf("other source: status = %d, want 204", got)
	}
	now = now.Add(time.Minute)
	if got := postIngest(t, h, "192.0.2.1:1000", report, good); got != http.StatusNoContent {
		t.Fatalf("after refill: status = %d, want 204", got)
	}
}

// TestIngestRequestBudgetZeroMeansUnlimited checks the zero config keeps no
// request budget.
func TestIngestRequestBudgetZeroMeansUnlimited(t *testing.T) {
	s := newServer(config{})
	if s.ingestLimiter != nil {
		t.Fatal("zero config built an ingest request limiter")
	}
	h := s.routes()
	for i := range 100 {
		if got := postIngest(t, h, "192.0.2.1:1000", "not json", nil); got != http.StatusBadRequest {
			t.Fatalf("request %d: status = %d, want 400", i, got)
		}
	}
}

func TestIngestSourcePerMinuteConfig(t *testing.T) {
	if got := loadConfig().ingestSourcePerMinute; got != defaultIngestSourcePerMinute {
		t.Fatalf("default = %d, want %d", got, defaultIngestSourcePerMinute)
	}
	t.Setenv("INGEST_SOURCE_PER_MINUTE", "0")
	if got := loadConfig().ingestSourcePerMinute; got != 0 {
		t.Fatalf("INGEST_SOURCE_PER_MINUTE=0 gave %d", got)
	}
	t.Setenv("INGEST_SOURCE_PER_MINUTE", "-1")
	if err := loadConfig().validate(); err == nil || !strings.Contains(err.Error(), "INGEST_SOURCE_PER_MINUTE") {
		t.Fatalf("negative value: validate() = %v, want an INGEST_SOURCE_PER_MINUTE error", err)
	}
}

func TestNewHTTPServerBoundsHeaderBytes(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if srv.MaxHeaderBytes != maxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, maxHeaderBytes)
	}
}

// TestIngestAuthTokenOfAnotherLengthIsRefused checks the hashed comparison
// still refuses a prefix or an extension of AUTH_TOKEN.
func TestIngestAuthTokenOfAnotherLengthIsRefused(t *testing.T) {
	h := newServer(config{authToken: "Bearer s3cret"}).routes()
	report := okReport
	for _, auth := range []string{"Bearer s3cre", "Bearer s3cret2", ""} {
		if got := postIngest(t, h, "192.0.2.1:1000", report, map[string]string{"Authorization": auth}); got != http.StatusUnauthorized {
			t.Fatalf("Authorization %q: status = %d, want 401", auth, got)
		}
	}
}
