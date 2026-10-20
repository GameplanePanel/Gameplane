//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	telemetryAdminUser = "telemetryadmin"
	telemetryAdminPass = "telemetry-admin-pw-1"
	// telemetryDashSecret holds the receiver's dashboard token; the chart
	// mounts it as DASHBOARD_TOKEN and serves /metrics on port 8081 behind it
	// (spec 022 Q8, T067).
	telemetryDashSecret = "telemetry-dashboard"
	// telemetryDashToken is at least 32 characters: the receiver refuses to
	// start with a shorter DASHBOARD_TOKEN (spec 022 FR-041).
	telemetryDashToken = "e2e-telemetry-dashboard-token-0123456789"
	// telemetryInterval is api.telemetry.interval for this bucket's cluster.
	telemetryInterval = time.Minute
)

// TestTelemetryLifecycle walks the spec 022 US1 path on the bucket's own
// cluster (research R17): a fresh install has a pending notice, "seen" opens
// the gate and the first report reaches the bundled receiver, "all-off" stops
// reports, turning telemetry back on resumes them, and an API restart does
// not bring a report forward. The later subtests cover the extended tier, the
// install-ID reset, forged and replayed reports, the private dashboard, the
// public summary and the operator's redirect and disable switches (US2 to US7).
// The subtests share state, so they run in order; the US2 ones come last
// because they point the API away from the bundled receiver, and
// operator_disabled_sends_nothing is the very last.
//
// It is the bucket's only test because it runs `helm upgrade` on the cluster.
// Admin logins: one. After every API pod replacement the client gets a new
// port-forward (reconnectAPIClient) and keeps its session.
func TestTelemetryLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	parent := t // owns cleanups for resources that must outlive a subtest

	// ---- setup: dashboard token, chart values, one admin ------------------

	if out, err := envInstance.Kubectl(ctx, "create", "secret", "generic", telemetryDashSecret,
		"--namespace", "gameplane-system", "--from-literal=token="+telemetryDashToken); err != nil {
		t.Fatalf("create dashboard token secret: %v\n%s", err, out)
	}
	helmUpgradeTelemetry(ctx, t)
	envInstance.BootstrapAdmin(t, telemetryAdminUser, telemetryAdminPass)

	// The receiver's dashboard port carries /metrics. It is not restarted
	// again, so one port-forward serves the whole test.
	metricsPort, stopMetrics := detachedPortForward(ctx, t, "gameplane-system", "svc/gameplane-telemetry-receiver", 8081)
	defer stopMetrics()
	reports := func() float64 { return receiverReportsTotal(ctx, t, metricsPort) }
	// arrivals counts every report the receiver accepted: new ones (reports_total)
	// plus same-day duplicates of an already counted extended install ID, which the
	// receiver acknowledges with 204 but does not add to reports_total.
	arrivals := func() float64 {
		return reports() + receiverMetric(ctx, t, metricsPort, telemetryDashToken, metricDuplicates, "")
	}

	// The bucket's one admin login. Logging in does not show the notice:
	// only the notice POST (the dashboard rendering it) records that.
	cli := envInstance.APIClient(t, telemetryAdminUser, telemetryAdminPass)
	defer func() { cli.Close() }() // cli is replaced after API pod restarts

	// The receiver's public port takes the test client's reports and serves
	// the public summary; the dashboard port (above) serves /metrics and the
	// views, so every figure below comes from the receiver itself.
	ingestPort, stopIngest := detachedPortForward(ctx, t, "gameplane-system", "svc/gameplane-telemetry-receiver", 8080)
	defer stopIngest()
	tc := newTelemetryTestClient(t, ingestPort)
	rx := receiverScrape{port: metricsPort, token: telemetryDashToken}
	// The login page is captured while the receiver is still empty: a later
	// unauthenticated response must be identical to it (FR-023, SC-011).
	loginBaseline := dashboardLoginPage(ctx, t, metricsPort)

	t.Run("notice_pending_on_fresh_install", func(t *testing.T) {
		status, body, err := tlDo(ctx, cli, http.MethodGet, "/admin/telemetry/notice", nil)
		if err != nil || status != http.StatusOK {
			t.Fatalf("GET notice: %v status %v body %s", err, status, body)
		}
		var n struct {
			Pending     bool `json:"pending"`
			Destination struct {
				Kind string `json:"kind"`
			} `json:"destination"`
		}
		if err := json.Unmarshal(body, &n); err != nil {
			t.Fatalf("decode notice: %v\n%s", err, body)
		}
		if !n.Pending || n.Destination.Kind != "bundled" {
			t.Fatalf("notice = %s, want pending with the bundled destination", body)
		}
		// Nothing may have been sent before the notice was shown (FR-004).
		if got := reports(); got != 0 {
			t.Fatalf("reports before the notice = %v, want 0", got)
		}
	})

	t.Run("seen_opens_gate_and_first_report_arrives", func(t *testing.T) {
		postNoticeAction(ctx, t, cli, "seen", http.StatusNoContent)
		envInstance.Eventually(t, 3*telemetryInterval, func() (bool, string) {
			got := reports()
			return got >= 1, fmt.Sprintf("reports_total = %v, want at least 1", got)
		})
	})

	t.Run("all_off_stops_reports", func(t *testing.T) {
		postNoticeAction(ctx, t, cli, "all-off", http.StatusNoContent)
		time.Sleep(20 * time.Second) // let a report already in flight land
		settled := reports()
		envInstance.Consistently(t, 3*telemetryInterval, 10*time.Second, func() (bool, string) {
			got := reports()
			return got == settled, fmt.Sprintf("reports_total = %v, want it to stay at %v after all-off", got, settled)
		})

		// Turn telemetry back on through the settings route.
		status, body, err := tlDo(ctx, cli, http.MethodPut, "/admin/config/telemetry",
			map[string]bool{"sendMetrics": true, "extended": true})
		if err != nil || status != http.StatusOK {
			t.Fatalf("PUT telemetry on: %v status %v body %s", err, status, body)
		}
		envInstance.Eventually(t, 3*telemetryInterval, func() (bool, string) {
			got := reports()
			return got > settled, fmt.Sprintf("reports_total = %v, want more than %v after turning telemetry back on", got, settled)
		})
	})

	t.Run("api_restart_does_not_resend_early", func(t *testing.T) {
		// Wait for a report and note when it was first seen. The install keeps
		// one ID, so after its first report the receiver counts the rest as
		// same-day duplicates; arrivals() includes those.
		seen := arrivals()
		sawAt := time.Now()
		envInstance.Eventually(t, 3*telemetryInterval, func() (bool, string) {
			got := arrivals()
			if got > seen {
				seen, sawAt = got, time.Now()
				return true, ""
			}
			return false, fmt.Sprintf("reports + duplicates = %v, waiting for a report", got)
		})

		if out, err := envInstance.Kubectl(ctx, "rollout", "restart", "deploy/gameplane-api",
			"--namespace", "gameplane-system"); err != nil {
			t.Fatalf("restart API: %v\n%s", err, out)
		}
		if out, err := envInstance.Kubectl(ctx, "rollout", "status", "deploy/gameplane-api",
			"--namespace", "gameplane-system", "--timeout=3m"); err != nil {
			t.Fatalf("wait for API rollout: %v\n%s", err, out)
		}
		cli = reconnectAPIClient(ctx, t, cli)

		// The schedule is persisted: the restarted API reports one interval
		// after the previous report, not at once. Allow 5s for the two
		// one-second polling observations.
		envInstance.Eventually(t, 4*telemetryInterval, func() (bool, string) {
			got := arrivals()
			return got > seen, fmt.Sprintf("reports + duplicates = %v, waiting for the next report", got)
		})
		if gap := time.Since(sawAt); gap < telemetryInterval-5*time.Second {
			t.Fatalf("next report arrived %v after the previous one, want at least %v (the API restart must not send early)",
				gap, telemetryInterval)
		}
	})

	// reportsMore waits until the bundled receiver has accepted another report,
	// counted or a same-day duplicate.
	reportsMore := func(t *testing.T) {
		t.Helper()
		waitForMore(t, arrivals, 4*telemetryInterval, "reports + duplicates")
	}

	// newReportsMore waits for a report the receiver counts in reports_total
	// and in the fleet histograms, which a same-day duplicate does not touch.
	newReportsMore := func(t *testing.T) {
		t.Helper()
		waitForMore(t, func() float64 { return rx.get(ctx, t, metricReports, "") }, 4*telemetryInterval, metricReports)
	}

	// ---- US3: tiers, reset, preview ---------------------------------------

	t.Run("extended_off_sends_basic_only", func(t *testing.T) {
		putTelemetryConsent(ctx, t, cli, true, false)
		v := getTelemetryView(ctx, t, cli)
		if v.InstallID != nil || v.Consent.Extended || v.Preview == nil || v.Preview.Ext != nil {
			t.Fatalf("view after extended off = %+v, want no install id and a basic-only preview", v)
		}
		time.Sleep(10 * time.Second) // let a report already in flight land
		extBase, dupBase := rx.get(ctx, t, metricExtended, ""), rx.get(ctx, t, metricDuplicates, "")
		reportsMore(t)
		// An extended report would count as a new install or as a duplicate.
		if got := rx.get(ctx, t, metricExtended, ""); got != extBase {
			t.Fatalf("%s = %v, want it to stay at %v while extended is off", metricExtended, got, extBase)
		}
		if got := rx.get(ctx, t, metricDuplicates, ""); got != dupBase {
			t.Fatalf("%s = %v, want it to stay at %v while extended is off", metricDuplicates, got, dupBase)
		}
	})

	t.Run("reset_id_counts_as_new_install", func(t *testing.T) {
		putTelemetryConsent(ctx, t, cli, true, true) // creates install ID A
		a := getTelemetryView(ctx, t, cli).InstallID
		if a == nil {
			t.Fatal("no install id after turning extended on")
		}
		// A's first extended report is a new install. Its next ones the same
		// day would be duplicates, and the counter would not rise.
		waitForMore(t, func() float64 { return rx.get(ctx, t, metricExtended, "") }, 4*telemetryInterval, metricExtended)

		b := resetInstallID(ctx, t, cli)
		if b == *a {
			t.Fatalf("reset returned the same id %q", b)
		}
		dupBase := rx.get(ctx, t, metricDuplicates, "")
		waitForMore(t, func() float64 { return rx.get(ctx, t, metricExtended, "") }, 4*telemetryInterval, metricExtended)
		if got := rx.get(ctx, t, metricDuplicates, ""); got != dupBase {
			t.Fatalf("%s rose from %v to %v: the reset ID was treated as the old install", metricDuplicates, dupBase, got)
		}
		v := getTelemetryView(ctx, t, cli)
		if v.InstallID == nil || *v.InstallID != b || v.Preview == nil || v.Preview.Ext == nil || v.Preview.Ext.InstallID != b {
			t.Fatalf("view after reset = %+v, want the new id %q in installId and in the preview", v, b)
		}
	})

	t.Run("preview_matches_received", func(t *testing.T) {
		// A new install ID makes the next extended report a new install, so the
		// receiver counts it and records its fleet histograms; the ID from
		// reset_id_counts_as_new_install was already counted today.
		resetInstallID(ctx, t, cli)
		v := getTelemetryView(ctx, t, cli)
		if v.Preview == nil {
			t.Fatal("no preview while basic is on")
		}
		// The receiver's record of the next report is read from its fleet
		// histograms and version label: the report's servers and templates
		// must equal the preview's.
		serversCount0 := rx.get(ctx, t, "gameplane_telemetry_servers_count", "")
		serversSum0 := rx.get(ctx, t, "gameplane_telemetry_servers_sum", "")
		templatesSum0 := rx.get(ctx, t, "gameplane_telemetry_templates_sum", "")
		versionLabel := fmt.Sprintf("version=%q", v.Preview.Version)
		version0 := rx.get(ctx, t, metricReports, versionLabel)
		newReportsMore(t)
		n := rx.get(ctx, t, "gameplane_telemetry_servers_count", "") - serversCount0
		if n < 1 {
			t.Fatalf("no report recorded: servers histogram count rose by %v", n)
		}
		if got := (rx.get(ctx, t, "gameplane_telemetry_servers_sum", "") - serversSum0) / n; got != float64(v.Preview.Servers) {
			t.Fatalf("receiver recorded %v servers per report, preview says %d", got, v.Preview.Servers)
		}
		if got := (rx.get(ctx, t, "gameplane_telemetry_templates_sum", "") - templatesSum0) / n; got != float64(v.Preview.Templates) {
			t.Fatalf("receiver recorded %v templates per report, preview says %d", got, v.Preview.Templates)
		}
		if got := rx.get(ctx, t, metricReports, versionLabel); got <= version0 {
			t.Fatalf("%s{%s} = %v, want more than %v: the receiver did not record the preview's version", metricReports, versionLabel, got, version0)
		}
	})

	// ---- US7: forged, tampered and replayed reports, claimed IDs -----------

	t.Run("forged_other_key_gets_409", func(t *testing.T) {
		// The install's current ID was claimed by its own key when the
		// receiver accepted the previous subtests' reports.
		v := getTelemetryView(ctx, t, cli)
		if v.InstallID == nil {
			t.Fatal("no install id: extended must be on")
		}
		asOf, viewsBefore := dashboardViews(ctx, t, metricsPort)
		refusedBase := rx.get(ctx, t, metricRefused, `reason="id_claimed"`)

		status, code := tc.send(ctx, t, tc.report(t, *v.InstallID, "e2e-forged", 7, time.Now()))
		if status != http.StatusConflict || code != "id_claimed" {
			t.Fatalf("report under the install's ID with another key = %d %q, want 409 id_claimed", status, code)
		}
		if got := rx.get(ctx, t, metricRefused, `reason="id_claimed"`); got != refusedBase+1 {
			t.Fatalf("%s{id_claimed} = %v, want %v", metricRefused, got, refusedBase+1)
		}
		if got := rx.get(ctx, t, metricReports, `version="e2e-forged"`); got != 0 {
			t.Fatalf("the refused report was counted: %v", got)
		}
		requireViewsUnchanged(ctx, t, metricsPort, asOf, viewsBefore)
	})

	t.Run("unsigned_tampered_replayed_get_403", func(t *testing.T) {
		id := newTestInstallID(t)
		sentAt := time.Now()
		body, sig := tc.sign(t, tc.report(t, id, "e2e-signed", 4, sentAt))
		if status, code := tc.post(ctx, t, body, sig); status != http.StatusNoContent {
			t.Fatalf("the test client's own signed report = %d %q, want 204", status, code)
		}
		accepted := rx.get(ctx, t, metricReports, `version="e2e-signed"`)
		if accepted != 1 {
			t.Fatalf("accepted reports with the test version = %v, want 1", accepted)
		}
		asOf, viewsBefore := dashboardViews(ctx, t, metricsPort)
		badBase := rx.get(ctx, t, metricRefused, `reason="bad_signature"`)
		replayBase := rx.get(ctx, t, metricRefused, `reason="replay"`)

		// Unsigned: the send time is newer, so only the missing header can fail it.
		unsigned, _ := tc.sign(t, tc.report(t, id, "e2e-signed", 4, sentAt.Add(2*time.Second)))
		if status, code := tc.post(ctx, t, unsigned, ""); status != http.StatusForbidden || code != "bad_signature" {
			t.Fatalf("unsigned extended report = %d %q, want 403 bad_signature", status, code)
		}
		// Tampered: a valid signature for 4 servers on a body that says 5.
		signed, tamperedSig := tc.sign(t, tc.report(t, id, "e2e-signed", 4, sentAt.Add(3*time.Second)))
		tampered := bytes.Replace(signed, []byte(`"servers":4`), []byte(`"servers":5`), 1)
		if bytes.Equal(tampered, signed) {
			t.Fatalf("test bug: the body %s has no servers field to tamper with", signed)
		}
		if status, code := tc.post(ctx, t, tampered, tamperedSig); status != http.StatusForbidden || code != "bad_signature" {
			t.Fatalf("tampered report = %d %q, want 403 bad_signature", status, code)
		}
		// Replayed: the accepted report, byte for byte.
		if status, code := tc.post(ctx, t, body, sig); status != http.StatusForbidden || code != "replay" {
			t.Fatalf("replayed report = %d %q, want 403 replay", status, code)
		}

		if got := rx.get(ctx, t, metricRefused, `reason="bad_signature"`); got != badBase+2 {
			t.Fatalf("%s{bad_signature} = %v, want %v", metricRefused, got, badBase+2)
		}
		if got := rx.get(ctx, t, metricRefused, `reason="replay"`); got != replayBase+1 {
			t.Fatalf("%s{replay} = %v, want %v", metricRefused, got, replayBase+1)
		}
		if got := rx.get(ctx, t, metricReports, `version="e2e-signed"`); got != accepted {
			t.Fatalf("reports with the test version = %v, want %v: a refused report was counted (SC-014)", got, accepted)
		}
		requireViewsUnchanged(ctx, t, metricsPort, asOf, viewsBefore)
	})

	t.Run("claimed_id_rotates_and_recovers", func(t *testing.T) {
		a := getTelemetryView(ctx, t, cli).InstallID
		if a == nil {
			t.Fatal("no install id: extended must be on")
		}
		// Sync on a report the install has just sent: its next one is about
		// one interval away, which leaves time to claim the new ID first.
		reportsMore(t)
		b := resetInstallID(ctx, t, cli)
		if status, code := tc.send(ctx, t, tc.report(t, b, "e2e-claim", 1, time.Now())); status != http.StatusNoContent {
			t.Fatalf("the test client's claim on the new ID = %d %q, want 204", status, code)
		}
		refusedBase := rx.get(ctx, t, metricRefused, `reason="id_claimed"`)

		// The install's next attempt gets 409, replaces the ID and re-sends.
		envInstance.Eventually(t, 4*telemetryInterval, func() (bool, string) {
			v := getTelemetryView(ctx, t, cli)
			ok := v.Status.LastIDRotationAt != nil && v.InstallID != nil && *v.InstallID != b && v.Status.LastOutcome == "ok"
			return ok, fmt.Sprintf("view = %+v, want a rotation stamp, an id other than %q and outcome ok", v, b)
		})
		v := getTelemetryView(ctx, t, cli)
		if *v.InstallID == *a || v.Preview == nil || v.Preview.Ext == nil || v.Preview.Ext.InstallID != *v.InstallID {
			t.Fatalf("view = %+v, want a third id shown in installId and the preview", v)
		}
		if got := rx.get(ctx, t, metricRefused, `reason="id_claimed"`); got != refusedBase+1 {
			t.Fatalf("%s{id_claimed} = %v, want %v (one 409 for the claimed ID)", metricRefused, got, refusedBase+1)
		}
	})

	// ---- US4: the private dashboard ----------------------------------------

	t.Run("dashboard_refuses_unauthenticated", func(t *testing.T) {
		resp, body := dashboardRequest(ctx, t, metricsPort, http.MethodGet, "/", nil, nil)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
			t.Fatalf("GET / = %d to %q, want 303 to /login", resp.StatusCode, resp.Header.Get("Location"))
		}
		if strings.ContainsAny(string(body), "0123456789") {
			t.Fatalf("the redirect body contains digits: %q", body)
		}
		resp, body = dashboardRequest(ctx, t, metricsPort, http.MethodGet, "/login", nil, nil)
		if resp.StatusCode != http.StatusOK || !bytes.Equal(body, loginBaseline) {
			t.Fatalf("GET /login = %d, want 200 and the page captured from the empty receiver (no figures)", resp.StatusCode)
		}
		for _, path := range []string{"/api/v1/views", "/metrics"} {
			resp, body = dashboardRequest(ctx, t, metricsPort, http.MethodGet, path, nil, nil)
			if resp.StatusCode != http.StatusUnauthorized || strings.TrimSpace(string(body)) != `{"error":"unauthorized"}` {
				t.Fatalf("GET %s without credentials = %d %q, want 401 and the fixed body", path, resp.StatusCode, body)
			}
		}
	})

	t.Run("dashboard_shows_reports", func(t *testing.T) {
		cookie := dashboardLogin(ctx, t, metricsPort, telemetryDashToken)
		before := time.Now().UTC().Format("2006-01-02")
		resp, body := dashboardRequest(ctx, t, metricsPort, http.MethodGet, "/api/v1/views", map[string]string{"Cookie": cookie}, nil)
		after := time.Now().UTC().Format("2006-01-02")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/v1/views with the session = %d: %s", resp.StatusCode, body)
		}
		var views map[string]json.RawMessage
		if err := json.Unmarshal(body, &views); err != nil {
			t.Fatalf("decode views %q: %v", body, err)
		}
		for _, key := range []string{"range", "asOf", "empty", "basic", "extended"} {
			if _, ok := views[key]; !ok {
				t.Fatalf("views has no %q key: %s", key, body)
			}
		}
		var asOf string
		var empty bool
		if json.Unmarshal(views["asOf"], &asOf) != nil || (asOf != before && asOf != after) {
			t.Fatalf("asOf = %s, want today (UTC), %s: views end on the current day (OD-6)", views["asOf"], after)
		}
		if json.Unmarshal(views["empty"], &empty) != nil || (empty != (string(views["basic"]) == "null")) {
			t.Fatalf("empty = %s with basic = %s, want basic null exactly when empty", views["empty"], views["basic"])
		}
		// The page itself renders for a signed-in browser, and the Bearer
		// token reads the same JSON.
		if resp, _ := dashboardRequest(ctx, t, metricsPort, http.MethodGet, "/", map[string]string{"Cookie": cookie}, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET / with the session = %d, want 200", resp.StatusCode)
		}
		bearer := map[string]string{"Authorization": "Bearer " + telemetryDashToken}
		if resp, _ := dashboardRequest(ctx, t, metricsPort, http.MethodGet, "/api/v1/views", bearer, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/v1/views with the Bearer token = %d, want 200", resp.StatusCode)
		}
		// Same-day reports are not in the views, so the earlier reports are
		// counted through /metrics. The exact figures are views_test.go's.
		if got := rx.get(ctx, t, metricReports, ""); got < 3 {
			t.Fatalf("%s = %v, want at least 3 reports from the earlier subtests", metricReports, got)
		}
	})

	// ---- US5: one extended report per install per day ----------------------

	t.Run("extended_counted_once_per_day", func(t *testing.T) {
		// Pause the install's extended reports (its own would count as
		// duplicates and move the same counters), measure the test client
		// alone, then turn the tier back on.
		putTelemetryConsent(ctx, t, cli, true, false)
		defer putTelemetryConsent(ctx, t, cli, true, true)
		time.Sleep(10 * time.Second) // let a report already in flight land
		keepClearOfUTCMidnight(t, 2*time.Minute)

		id := newTestInstallID(t)
		now := time.Now()
		extBase, dupBase := rx.get(ctx, t, metricExtended, ""), rx.get(ctx, t, metricDuplicates, "")
		for i, sentAt := range []time.Time{now.Add(-2 * time.Minute), now.Add(-time.Minute)} {
			if status, code := tc.send(ctx, t, tc.report(t, id, "e2e-once", 2, sentAt)); status != http.StatusNoContent {
				t.Fatalf("report %d from one ID = %d %q, want 204 (a same-day duplicate is accepted)", i+1, status, code)
			}
		}
		if got := rx.get(ctx, t, metricExtended, ""); got != extBase+1 {
			t.Fatalf("%s = %v, want %v: two reports from one ID on one day count once", metricExtended, got, extBase+1)
		}
		if got := rx.get(ctx, t, metricDuplicates, ""); got != dupBase+1 {
			t.Fatalf("%s = %v, want %v", metricDuplicates, got, dupBase+1)
		}
	})

	// ---- US6: the public summary -------------------------------------------

	t.Run("public_summary_five_keys", func(t *testing.T) {
		resp, body := dashboardRequest(ctx, t, ingestPort, http.MethodGet, "/v1/summary", nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/summary = %d: %s", resp.StatusCode, body)
		}
		var summary map[string]json.RawMessage
		if err := json.Unmarshal(body, &summary); err != nil {
			t.Fatalf("decode summary %q: %v", body, err)
		}
		if len(summary) != 5 {
			t.Fatalf("summary = %s, want exactly five keys", body)
		}
		for _, key := range []string{"asOf", "reportsLatestDay", "reports30d", "reportsTotal", "activeInstalls30d"} {
			if _, ok := summary[key]; !ok {
				t.Fatalf("summary has no %q key: %s", key, body)
			}
		}
		if resp.Header.Get("Content-Type") != "application/json" || resp.Header.Get("Access-Control-Allow-Origin") != "*" ||
			!strings.Contains(resp.Header.Get("Cache-Control"), "max-age=3600") || resp.Header.Get("ETag") == "" {
			t.Fatalf("summary headers = %v, want JSON, CORS *, a one-hour cache and an ETag", resp.Header)
		}
		// Operational metrics are never on the public listener (FR-030).
		if resp, _ := dashboardRequest(ctx, t, ingestPort, http.MethodGet, "/metrics", nil, nil); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /metrics on the public listener = %d, want 404", resp.StatusCode)
		}
	})

	// ---- US2: the operator's destination and disable switches --------------
	// These point the API away from the bundled receiver, so they come last.

	var oldReceiver struct {
		name string
	}
	// receiverImage is the image the bundled receiver ran, read before the
	// chart stops deploying it; the proof-of-work subtests reuse it.
	var receiverImage string

	t.Run("custom_destination_receives_only", func(t *testing.T) {
		image := bundledReceiverImage(ctx, t)
		receiverImage = image
		name := deployTelemetryReceiver(ctx, t, "custom", image, true)
		bundledBefore := rx.get(ctx, t, metricReports, "")

		endpoint := fmt.Sprintf("%s.gameplane-system.svc:8080", name)
		helmUpgradeReuse(ctx, t, "api.telemetry.endpoint=http://"+endpoint+"/ingest")
		cli = reconnectAPIClient(ctx, t, cli)

		v := getTelemetryView(ctx, t, cli)
		if v.Destination.Kind != "custom" || v.Destination.Host == nil || *v.Destination.Host != endpoint {
			t.Fatalf("destination = %+v, want kind custom at %s", v.Destination, endpoint)
		}
		port, stop := detachedPortForward(ctx, t, "gameplane-system", "svc/"+name, 8081)
		defer stop()
		envInstance.Eventually(t, 4*telemetryInterval, func() (bool, string) {
			got := receiverMetric(ctx, t, port, telemetryDashToken, metricReports, "")
			return got >= 1, fmt.Sprintf("custom receiver %s = %v, want at least 1", metricReports, got)
		})
		// With an explicit endpoint the chart no longer deploys the bundled
		// receiver. If a later chart keeps it, its counter must stay put.
		if out, err := envInstance.Kubectl(ctx, "get", "deploy/gameplane-telemetry-receiver",
			"--namespace", "gameplane-system"); err == nil {
			if got := rx.get(ctx, t, metricReports, ""); got != bundledBefore {
				t.Fatalf("bundled receiver %s = %v, want it to stay at %v", metricReports, got, bundledBefore)
			}
		} else if !strings.Contains(out, "NotFound") {
			t.Fatalf("kubectl get bundled receiver: %v\n%s", err, out)
		}
	})

	t.Run("pow_receiver_accepts_solved_reports", func(t *testing.T) {
		// MIN_BITS=8 makes the install really solve a challenge before every
		// report (spec 022 S10). The deploy cleanup runs when this subtest
		// ends, so the next subtest deploys its own receiver.
		name := deployTelemetryReceiver(ctx, t, "pow", receiverImage, true,
			"INGEST_POW=true", "INGEST_POW_MIN_BITS=8", "INGEST_POW_TARGET_PER_MIN=60")
		helmUpgradeReuse(ctx, t, fmt.Sprintf("api.telemetry.endpoint=http://%s.gameplane-system.svc:8080/ingest", name))
		cli = reconnectAPIClient(ctx, t, cli)

		port, stop := detachedPortForward(ctx, t, "gameplane-system", "svc/"+name, 8081)
		defer stop()
		challenges := func() float64 {
			return receiverMetric(ctx, t, port, telemetryDashToken, metricPoWChallenges, "")
		}
		waitForMore(t, challenges, 4*telemetryInterval, metricPoWChallenges)
		envInstance.Eventually(t, 4*telemetryInterval, func() (bool, string) {
			got := receiverMetric(ctx, t, port, telemetryDashToken, metricReports, "")
			outcome := getTelemetryView(ctx, t, cli).Status.LastOutcome
			return got >= 1 && outcome == "ok",
				fmt.Sprintf("%s = %v, lastOutcome = %q, want a solved report accepted and outcome ok", metricReports, got, outcome)
		})
	})

	t.Run("pow_refuses_missing_and_invalid", func(t *testing.T) {
		name := deployTelemetryReceiver(ctx, t, "powrefuse", receiverImage, true,
			"INGEST_POW=true", "INGEST_POW_MIN_BITS=8", "INGEST_POW_TARGET_PER_MIN=60")
		ingestPort, stopIngest := detachedPortForward(ctx, t, "gameplane-system", "svc/"+name, 8080)
		defer stopIngest()
		metricsPort, stopMetrics := detachedPortForward(ctx, t, "gameplane-system", "svc/"+name, 8081)
		defer stopMetrics()
		powRx := receiverScrape{port: metricsPort, token: telemetryDashToken}
		pc := newTelemetryTestClient(t, ingestPort)
		const version = "e2e-pow"
		versionLabel := `version="` + version + `"`
		id := newTestInstallID(t)
		body, sig := pc.sign(t, pc.report(t, id, version, 3, time.Now()))
		requiredBase := powRx.get(ctx, t, metricRefused, `reason="pow_required"`)
		invalidBase := powRx.get(ctx, t, metricRefused, `reason="pow_invalid"`)

		// No header: 428 pow_required, and nothing is counted.
		if status, code := pc.postPoW(ctx, t, body, sig, ""); status != http.StatusPreconditionRequired || code != "pow_required" {
			t.Fatalf("report with no proof of work = %d %q, want 428 pow_required", status, code)
		}
		if got := powRx.get(ctx, t, metricRefused, `reason="pow_required"`); got != requiredBase+1 {
			t.Fatalf("%s{pow_required} = %v, want %v", metricRefused, got, requiredBase+1)
		}

		// A solved challenge is accepted once.
		solved := pc.solvedPoW(ctx, t, 8)
		if status, code := pc.postPoW(ctx, t, body, sig, solved); status != http.StatusNoContent {
			t.Fatalf("report with a solved challenge = %d %q, want 204", status, code)
		}
		if got := powRx.get(ctx, t, metricReports, versionLabel); got != 1 {
			t.Fatalf("%s{%s} = %v, want 1", metricReports, versionLabel, got)
		}

		// The same solution again: 428 pow_invalid, and nothing changes.
		asOf, viewsBefore := dashboardViews(ctx, t, metricsPort)
		again, againSig := pc.sign(t, pc.report(t, id, version, 3, time.Now().Add(2*time.Second)))
		if status, code := pc.postPoW(ctx, t, again, againSig, solved); status != http.StatusPreconditionRequired || code != "pow_invalid" {
			t.Fatalf("report with a used solution = %d %q, want 428 pow_invalid", status, code)
		}
		if got := powRx.get(ctx, t, metricRefused, `reason="pow_invalid"`); got != invalidBase+1 {
			t.Fatalf("%s{pow_invalid} = %v, want %v", metricRefused, got, invalidBase+1)
		}
		if got := powRx.get(ctx, t, metricReports, versionLabel); got != 1 {
			t.Fatalf("%s{%s} = %v after the refusals, want it unchanged at 1", metricReports, versionLabel, got)
		}
		requireViewsUnchanged(ctx, t, metricsPort, asOf, viewsBefore)
	})

	t.Run("old_receiver_gets_basic", func(t *testing.T) {
		// The beta.8 receiver rejects any field beyond the basic three with 400
		// and exposes /metrics unauthenticated on its one port (FR-016, SC-013).
		name := deployTelemetryReceiverKept(ctx, t, parent, "old", betaReceiverImage, false)
		port, stop := detachedPortForward(ctx, t, "gameplane-system", "svc/"+name, 8080)
		defer stop()
		oldReceiver.name = name

		helmUpgradeReuse(ctx, t, fmt.Sprintf("api.telemetry.endpoint=http://%s.gameplane-system.svc:8080/ingest", name))
		cli = reconnectAPIClient(ctx, t, cli)
		envInstance.Eventually(t, 4*telemetryInterval, func() (bool, string) {
			got := receiverMetric(ctx, t, port, "", metricReports, "")
			return got >= 1, fmt.Sprintf("old receiver %s = %v, want a basic report accepted after the extended one got 400", metricReports, got)
		})
		v := getTelemetryView(ctx, t, cli)
		if v.Status.LastOutcome != "ok" {
			t.Fatalf("status = %+v, want outcome ok: the basic re-send was accepted", v.Status)
		}
		// The fallback marker holds the extended part back for a week, so the
		// preview (the next report) is basic only although extended is on.
		if !v.Consent.Extended || v.Preview == nil || v.Preview.Ext != nil {
			t.Fatalf("view = %+v, want extended on and a basic-only preview for the old receiver", v)
		}
	})

	t.Run("operator_disabled_sends_nothing", func(t *testing.T) {
		helmUpgradeReuse(ctx, t, "api.telemetry.enabled=false")
		cli = reconnectAPIClient(ctx, t, cli)

		v := getTelemetryView(ctx, t, cli)
		if !v.OperatorDisabled || v.Destination.Kind != "disabled" || v.Destination.Host != nil || v.Preview != nil {
			t.Fatalf("view = %+v, want operatorDisabled, kind disabled, no host and no preview", v)
		}
		status, body, err := tlDo(ctx, cli, http.MethodPut, "/admin/config/telemetry", map[string]bool{"sendMetrics": true, "extended": true})
		if err != nil || status != http.StatusConflict {
			t.Fatalf("PUT telemetry while disabled: %v status %v body %s, want 409", err, status, body)
		}
		status, body, err = tlDo(ctx, cli, http.MethodGet, "/admin/telemetry/notice", nil)
		if err != nil || status != http.StatusOK || !strings.Contains(string(body), `"pending":false`) {
			t.Fatalf("GET notice while disabled: %v status %v body %s, want pending false", err, status, body)
		}

		// The old receiver's forward from old_receiver_gets_basic died with
		// that subtest; open one for this subtest.
		port, stop := detachedPortForward(ctx, t, "gameplane-system", "svc/"+oldReceiver.name, 8080)
		defer stop()
		time.Sleep(10 * time.Second) // let a report from the replaced pod land
		settled := receiverMetric(ctx, t, port, "", metricReports, "")
		envInstance.Consistently(t, 3*telemetryInterval, 10*time.Second, func() (bool, string) {
			got := receiverMetric(ctx, t, port, "", metricReports, "")
			return got == settled, fmt.Sprintf("%s = %v, want it to stay at %v while the operator disabled telemetry", metricReports, got, settled)
		})
	})
}

// helmUpgradeTelemetry reconfigures the e2e release for this bucket: a 1m
// report interval, the dashboard token, the public summary, and no per-source
// ingest limit (every report here comes from one address).
func helmUpgradeTelemetry(ctx context.Context, t *testing.T) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "helm", "upgrade", "gameplane",
		filepath.Join("..", "..", "charts", "gameplane"),
		"--kube-context", envInstance.Context,
		"--namespace", "gameplane-system",
		"--reuse-values",
		"--set", "api.telemetry.interval=1m",
		"--set", "api.telemetry.receiver.dashboard.tokenSecretRef.name="+telemetryDashSecret,
		"--set", "api.telemetry.receiver.publicSummary.enabled=true",
		"--set", "api.telemetry.receiver.ingestSourceDailyLimit=0",
		"--wait", "--timeout", "6m",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helm upgrade for the telemetry bucket failed: %v\n%s", err, out)
	}
}

// postNoticeAction POSTs one notice action and requires the given status.
func postNoticeAction(ctx context.Context, t *testing.T, cli *APIClient, action string, want int) {
	t.Helper()
	status, body, err := tlDo(ctx, cli, http.MethodPost, "/admin/telemetry/notice", map[string]string{"action": action})
	if err != nil || status != want {
		t.Fatalf("POST notice %q: %v status %v body %s, want %d", action, err, status, body, want)
	}
}

// receiverReportsTotal sums gameplane_telemetry_reports_total over every
// version label, read from the receiver's dashboard port with the dashboard
// token. It fails the test when /metrics is unreachable.
func receiverReportsTotal(ctx context.Context, t *testing.T, port int) float64 {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/metrics", port), nil)
	if err != nil {
		t.Fatalf("new metrics request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+telemetryDashToken)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET receiver /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET receiver /metrics = %d: %s", resp.StatusCode, body)
	}
	var total float64
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "gameplane_telemetry_reports_total") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse metric line %q: %v", line, err)
		}
		total += v
	}
	return total
}
