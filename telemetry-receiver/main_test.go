package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// testDashboardToken is the dashboard credential of every testServer.
const testDashboardToken = "test-dashboard-token"

// testDashboards maps each public test server to the dashboard handler of
// the same receiver, because /metrics is served only on the dashboard
// listener (FR-030, spec Q8).
var testDashboards sync.Map

func testServer(t *testing.T, cfg config) *httptest.Server {
	t.Helper()
	if cfg.dashboardToken == "" {
		cfg.dashboardToken = testDashboardToken
	}
	s := newServer(cfg)
	srv := httptest.NewServer(s.routes())
	testDashboards.Store(srv, s.dashboardRoutes())
	t.Cleanup(func() {
		testDashboards.Delete(srv)
		srv.Close()
	})
	return srv
}

// dashboardHandler returns the dashboard handler paired with a test server.
func dashboardHandler(t *testing.T, srv *httptest.Server) http.Handler {
	t.Helper()
	h, ok := testDashboards.Load(srv)
	if !ok {
		t.Fatal("no dashboard handler registered for the test server")
	}
	return h.(http.Handler)
}

func post(t *testing.T, srv *httptest.Server, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/ingest", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

func metrics(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+testDashboardToken)
	rec := httptest.NewRecorder()
	dashboardHandler(t, srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get metrics: status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func TestIngestCountsReport(t *testing.T) {
	srv := testServer(t, config{})
	resp := post(t, srv, `{"version":"0.2.0-beta.5","servers":3,"templates":7}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	m := metrics(t, srv)
	if !strings.Contains(m, `gameplane_telemetry_reports_total{version="0.2.0-beta.5"} 1`) {
		t.Fatalf("reports counter missing from metrics:\n%s", m)
	}
	if !strings.Contains(m, `gameplane_telemetry_servers_bucket{le="5"} 1`) {
		t.Fatalf("servers histogram missing:\n%s", m)
	}
	if !strings.Contains(m, `gameplane_telemetry_templates_sum 7`) {
		t.Fatalf("templates histogram missing:\n%s", m)
	}
}

func TestIngestSanitizesVersionLabel(t *testing.T) {
	srv := testServer(t, config{})
	// A hostile version string must not become a raw label value.
	resp := post(t, srv, `{"version":"<script>alert(1)</script>","servers":0,"templates":0}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	resp2 := post(t, srv, `{"version":"`+strings.Repeat("x", 200)+`","servers":0,"templates":0}`, nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("long version: status not 204")
	}
	m := metrics(t, srv)
	if !strings.Contains(m, `gameplane_telemetry_reports_total{version="invalid"} 2`) {
		t.Fatalf("sanitized counter missing:\n%s", m)
	}
	if strings.Contains(m, "script") {
		t.Fatal("hostile version string leaked into metrics output")
	}
}

func TestIngestRejects(t *testing.T) {
	srv := testServer(t, config{})
	cases := []struct {
		name string
		body string
		want int
	}{
		{"bad json", `{not json`, http.StatusBadRequest},
		{"unknown field", `{"version":"1","servers":1,"templates":1,"hostname":"leaky"}`, http.StatusBadRequest},
		{"negative servers", `{"version":"1","servers":-1,"templates":0}`, http.StatusBadRequest},
		{"negative templates", `{"version":"1","servers":0,"templates":-2}`, http.StatusBadRequest},
		{"oversized", `{"version":"` + strings.Repeat("a", maxBody+1) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := post(t, srv, tc.body, nil)
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
	// Nothing above must have been counted.
	if m := metrics(t, srv); strings.Contains(m, "gameplane_telemetry_reports_total{") {
		t.Fatalf("rejected reports were counted:\n%s", m)
	}
}

func TestIngestMethodNotAllowed(t *testing.T) {
	srv := testServer(t, config{})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/ingest", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestMetricsMethodNotAllowed(t *testing.T) {
	srv := testServer(t, config{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()
	dashboardHandler(t, srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// /metrics is not served on the public listener at all (FR-030, spec Q8).
func TestMetricsNotServedOnPublicListener(t *testing.T) {
	srv := testServer(t, config{})
	for _, auth := range []string{"", "Bearer " + testDashboardToken} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/metrics", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("Authorization %q: status = %d, want 404", auth, resp.StatusCode)
		}
	}
}

func TestHealthzMethodNotAllowed(t *testing.T) {
	srv := testServer(t, config{})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestIngestAuth(t *testing.T) {
	srv := testServer(t, config{authToken: "Bearer s3cret"})
	body := `{"version":"1.0.0","servers":1,"templates":1}`
	resp := post(t, srv, body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no auth: status = %d, want 401", resp.StatusCode)
	}
	resp = post(t, srv, body, map[string]string{"Authorization": "Bearer wrong"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad auth: status = %d, want 401", resp.StatusCode)
	}
	resp = post(t, srv, body, map[string]string{"Authorization": "Bearer s3cret"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("good auth: status = %d, want 204", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv := testServer(t, config{})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(b) != "ok" {
		t.Fatalf("healthz = %d %q", resp.StatusCode, b)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg := loadConfig()
	if cfg.listen != ":8080" || cfg.authToken != "" {
		t.Fatalf("defaults = %+v", cfg)
	}
	t.Setenv("LISTEN_ADDR", ":9999")
	t.Setenv("AUTH_TOKEN", "tok")
	cfg = loadConfig()
	if cfg.listen != ":9999" || cfg.authToken != "tok" {
		t.Fatalf("env override = %+v", cfg)
	}
}

func TestServeShutsDownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, config{listen: "127.0.0.1:0"}) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not shut down after context cancel")
	}
}

func TestServeBadListenAddr(t *testing.T) {
	err := serve(context.Background(), config{listen: "not-an-addr"})
	if err == nil {
		t.Fatal("expected listen error")
	}
}

// Only a single JSON object carrying all three required fields is accepted;
// anything else gets 400 and is not counted.
func TestIngestRequiresCompleteSingleReport(t *testing.T) {
	srv := testServer(t, config{})
	valid := `{"version":"1.0.0","servers":1,"templates":1}`
	cases := []struct {
		name string
		body string
	}{
		{"missing version", `{"servers":1,"templates":1}`},
		{"missing servers", `{"version":"1.0.0","templates":1}`},
		{"missing templates", `{"version":"1.0.0","servers":1}`},
		{"empty object", `{}`},
		{"null field", `{"version":"1.0.0","servers":null,"templates":1}`},
		{"null body", `null`},
		{"array body", `[` + valid + `]`},
		{"number body", `42`},
		{"empty body", ``},
		{"trailing garbage", valid + `xyz`},
		{"second object", valid + valid},
		{"trailing value", valid + ` 1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := post(t, srv, tc.body, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
	if m := metrics(t, srv); strings.Contains(m, "gameplane_telemetry_reports_total{") {
		t.Fatalf("rejected reports were counted:\n%s", m)
	}
}

// A complete report with zero counts, or followed only by whitespace, is
// still accepted.
func TestIngestAcceptsZeroCountsAndTrailingWhitespace(t *testing.T) {
	srv := testServer(t, config{})
	for _, body := range []string{
		`{"version":"1.0.0","servers":0,"templates":0}`,
		"{\"version\":\"1.0.0\",\"servers\":2,\"templates\":3}\n",
		"  {\"version\":\"1.0.0\",\"servers\":2,\"templates\":3}  \r\n\t",
	} {
		resp := post(t, srv, body, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("body %q: status = %d, want 204", body, resp.StatusCode)
		}
	}
	if m := metrics(t, srv); !strings.Contains(m, `gameplane_telemetry_reports_total{version="1.0.0"} 3`) {
		t.Fatalf("accepted reports not counted:\n%s", m)
	}
}

// The 16 KiB limit applies to the whole body, including anything after the
// report, and a rejected body is not counted.
func TestIngestBodyLimitCoversWholeBody(t *testing.T) {
	srv := testServer(t, config{})
	valid := `{"version":"1.0.0","servers":1,"templates":1}`
	for name, body := range map[string]string{
		"padding after report":  valid + strings.Repeat(" ", maxBody),
		"content after report":  valid + strings.Repeat("x", maxBody),
		"exactly one byte over": valid + strings.Repeat(" ", maxBody-len(valid)+1),
	} {
		t.Run(name, func(t *testing.T) {
			resp := post(t, srv, body, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", resp.StatusCode)
			}
		})
	}
	// A body exactly at the limit is still read and judged on its content.
	atLimit := valid + strings.Repeat(" ", maxBody-len(valid))
	resp := post(t, srv, atLimit, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("body at the limit: status = %d, want 204", resp.StatusCode)
	}
	if m := metrics(t, srv); !strings.Contains(m, `gameplane_telemetry_reports_total{version="1.0.0"} 1`) {
		t.Fatalf("expected only the at-limit report to be counted:\n%s", m)
	}
}

// TestDecodePayloadKeepsTrailingDecodeError verifies that trailing malformed
// JSON after a valid report is detected and that the root error is preserved.
func TestDecodePayloadKeepsTrailingDecodeError(t *testing.T) {
	// Valid report followed by trailing text that is not JSON (an unfinished
	// value such as "{" would give io.ErrUnexpectedEOF, not a SyntaxError).
	body := []byte(`{"version":"v","servers":1,"templates":1} x`)
	_, err := decodePayload(body)
	if !errors.Is(err, errInvalidPayload) {
		t.Fatalf("error chain missing errInvalidPayload: %v", err)
	}
	// Verify that the root error is a json.SyntaxError.
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("error chain missing json.SyntaxError: %v", err)
	}

	// Valid report followed by a second valid object.
	body = []byte(`{"version":"v","servers":1,"templates":1}{"version":"v2","servers":2,"templates":2}`)
	_, err = decodePayload(body)
	if !errors.Is(err, errInvalidPayload) {
		t.Fatalf("second object: error chain missing errInvalidPayload: %v", err)
	}

	// Valid report followed by a second value that decodes cleanly.
	body = []byte(`{"version":"v","servers":1,"templates":1} {}`)
	_, err = decodePayload(body)
	if !errors.Is(err, errInvalidPayload) {
		t.Fatalf("trailing empty object: error chain missing errInvalidPayload: %v", err)
	}
}

// TestDecodePayloadRequiresExactKeys verifies that JSON object keys are
// matched exactly (case-sensitive), duplicates are rejected, and all required
// fields must be present with valid types.
func TestDecodePayloadRequiresExactKeys(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want payload
		err  bool
	}{
		{
			name: "valid minimal",
			body: []byte(`{"version":"1.0.0","servers":1,"templates":2}`),
			want: payload{Version: "1.0.0", Servers: 1, Templates: 2},
			err:  false,
		},
		{
			name: "capitalized key Servers",
			body: []byte(`{"version":"1.0.0","Servers":1,"templates":2}`),
			err:  true,
		},
		{
			name: "all-caps VERSION",
			body: []byte(`{"VERSION":"1.0.0","servers":1,"templates":2}`),
			err:  true,
		},
		{
			name: "duplicate servers",
			body: []byte(`{"version":"1.0.0","servers":1,"servers":2,"templates":3}`),
			err:  true,
		},
		{
			name: "duplicate version",
			body: []byte(`{"version":"1.0.0","version":"2.0.0","servers":1,"templates":2}`),
			err:  true,
		},
		{
			name: "null version",
			body: []byte(`{"version":null,"servers":1,"templates":2}`),
			err:  true,
		},
		{
			name: "null servers",
			body: []byte(`{"version":"1.0.0","servers":null,"templates":2}`),
			err:  true,
		},
		{
			name: "null templates",
			body: []byte(`{"version":"1.0.0","servers":1,"templates":null}`),
			err:  true,
		},
		{
			name: "servers as string",
			body: []byte(`{"version":"1.0.0","servers":"1","templates":2}`),
			err:  true,
		},
		{
			name: "templates as string",
			body: []byte(`{"version":"1.0.0","servers":1,"templates":"2"}`),
			err:  true,
		},
		{
			name: "version as integer",
			body: []byte(`{"version":1,"servers":1,"templates":2}`),
			err:  true,
		},
		{
			name: "missing version",
			body: []byte(`{"servers":1,"templates":2}`),
			err:  true,
		},
		{
			name: "missing servers",
			body: []byte(`{"version":"1.0.0","templates":2}`),
			err:  true,
		},
		{
			name: "missing templates",
			body: []byte(`{"version":"1.0.0","servers":1}`),
			err:  true,
		},
		{
			name: "unknown field hostname",
			body: []byte(`{"version":"1.0.0","servers":1,"templates":2,"hostname":"prod"}`),
			err:  true,
		},
		{
			name: "unknown field at start",
			body: []byte(`{"unknown":"field","version":"1.0.0","servers":1,"templates":2}`),
			err:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodePayload(tc.body)
			if tc.err {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, errInvalidPayload) {
					t.Fatalf("error chain missing errInvalidPayload: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.want {
					t.Fatalf("got %+v, want %+v", got, tc.want)
				}
			}
		})
	}
}

// TestNewHTTPServer_BoundsEveryPhase verifies that newHTTPServer sets all three
// timeouts correctly.
func TestNewHTTPServer_BoundsEveryPhase(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	if srv.ReadHeaderTimeout != readHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, readHeaderTimeout)
	}
	if srv.ReadTimeout != readTimeout {
		t.Errorf("ReadTimeout = %v, want %v", srv.ReadTimeout, readTimeout)
	}
	if srv.IdleTimeout != idleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", srv.IdleTimeout, idleTimeout)
	}

	// Verify all are non-zero.
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.IdleTimeout == 0 {
		t.Error("one or more timeouts are zero")
	}
}
