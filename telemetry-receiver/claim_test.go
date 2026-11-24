package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// claimBitsForTest keeps the solves in these tests fast.
const claimBitsForTest = 6

// claimPost POSTs a signed body to /ingest from 192.0.2.1 with an optional
// proof-of-work header.
func claimPost(t *testing.T, s *server, body, sig, pow string) *httptest.ResponseRecorder {
	t.Helper()
	extra := map[string]string{telemetryschema.SignatureHeader: sig}
	return powPost(t, s, strings.NewReader(body), pow, extra)
}

// claimSolve fetches a challenge for 192.0.2.1 through the endpoint and
// returns its solved header value and difficulty.
func claimSolve(t *testing.T, s *server) (string, int) {
	t.Helper()
	w := powGet(t, s, "192.0.2.1:1000")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/challenge = %d, want 200", w.Code)
	}
	var ch challengeReply
	if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	nonce, err := telemetryschema.SolvePoW(t.Context(), ch.Challenge, ch.Bits)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	return telemetryschema.FormatPoW(ch.Challenge, nonce), ch.Bits
}

func refusalCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Error
}

// TestNewInstallNeedsClaimPoWWithPoWOff walks the reporter's flow with
// INGEST_POW off: no challenge offered, 428 pow_claim, then a claim-grade
// challenge for the same source, solved and accepted. A known install then
// reports without any work.
func TestNewInstallNeedsClaimPoWWithPoWOff(t *testing.T) {
	s, clock := newExtServer(t, config{claimPoWBits: claimBitsForTest}, extStart)
	in := newExtInstall(1)

	if w := powGet(t, s, "192.0.2.1:1000"); w.Code != http.StatusNotFound {
		t.Fatalf("challenge before any claim = %d, want 404", w.Code)
	}
	body, sig := in.report(t, *clock, nil)
	before := extDump(t, s.store)
	w := claimPost(t, s, body, sig, "")
	if w.Code != http.StatusPreconditionRequired || refusalCode(t, w) != refusePoWClaim {
		t.Fatalf("new install without work = %d %q, want 428 %s", w.Code, refusalCode(t, w), refusePoWClaim)
	}
	if after := extDump(t, s.store); after != before {
		t.Fatal("a refused claim changed stored data")
	}
	if w := powGet(t, s, "198.51.100.9:1000"); w.Code != http.StatusNotFound {
		t.Fatalf("challenge for an unmarked source = %d, want 404", w.Code)
	}
	header, bits := claimSolve(t, s)
	if bits < claimBitsForTest {
		t.Fatalf("claim challenge bits = %d, want at least %d", bits, claimBitsForTest)
	}
	if w := claimPost(t, s, body, sig, header); w.Code != http.StatusNoContent {
		t.Fatalf("new install with claim work = %d %q, want 204", w.Code, refusalCode(t, w))
	}
	if w := powGet(t, s, "192.0.2.1:1000"); w.Code != http.StatusNotFound {
		t.Fatalf("challenge after the claim succeeded = %d, want 404", w.Code)
	}

	*clock = clock.Add(24 * time.Hour)
	body, sig = in.report(t, *clock, nil)
	if w := claimPost(t, s, body, sig, ""); w.Code != http.StatusNoContent {
		t.Fatalf("known install without work = %d %q, want 204", w.Code, refusalCode(t, w))
	}
	if got := counterValue(t, s, "gameplane_telemetry_refused_total", "reason", refusePoWClaim); got != 1 {
		t.Fatalf("refused_total{reason=pow_claim} = %v, want 1", got)
	}
}

// TestNewInstallClaimWithPoWOnNeedsTheClaimDifficulty checks that with
// INGEST_POW on, an easy solution passes the per-report check but not the
// claim, and the next challenge for that source carries the claim
// difficulty.
func TestNewInstallClaimWithPoWOnNeedsTheClaimDifficulty(t *testing.T) {
	cfg := powCfg(0, 22)
	cfg.claimPoWBits = claimBitsForTest
	s, clock := newExtServer(t, cfg, extStart)
	body, sig := newExtInstall(2).report(t, *clock, nil)

	easy, bits := claimSolve(t, s)
	if bits >= claimBitsForTest {
		t.Fatalf("idle challenge bits = %d, want below %d", bits, claimBitsForTest)
	}
	w := claimPost(t, s, body, sig, easy)
	if w.Code != http.StatusPreconditionRequired || refusalCode(t, w) != refusePoWClaim {
		t.Fatalf("claim with easy work = %d %q, want 428 %s", w.Code, refusalCode(t, w), refusePoWClaim)
	}
	hard, bits := claimSolve(t, s)
	if bits < claimBitsForTest {
		t.Fatalf("claim challenge bits = %d, want at least %d", bits, claimBitsForTest)
	}
	if w := claimPost(t, s, body, sig, hard); w.Code != http.StatusNoContent {
		t.Fatalf("claim with claim-grade work = %d %q, want 204", w.Code, refusalCode(t, w))
	}
}

// TestClaimPoWOffByZeroConfig checks the zero config builds no claim state.
func TestClaimPoWOffByZeroConfig(t *testing.T) {
	s, clock := newExtServer(t, config{}, extStart)
	if s.claims != nil || s.pow != nil {
		t.Fatal("zero config built proof-of-work state")
	}
	body, sig := newExtInstall(3).report(t, *clock, nil)
	if w := claimPost(t, s, body, sig, ""); w.Code != http.StatusNoContent {
		t.Fatalf("new install with claim work off = %d, want 204", w.Code)
	}
}

func TestClaimPoWConfig(t *testing.T) {
	if got := loadConfig().claimPoWBits; got != defaultClaimPoWBits {
		t.Fatalf("default INGEST_CLAIM_POW_BITS = %d, want %d", got, defaultClaimPoWBits)
	}
	for _, bad := range []string{"-1", fmt.Sprint(telemetryschema.MaxPoWBits + 1)} {
		t.Setenv("INGEST_CLAIM_POW_BITS", bad)
		if err := loadConfig().validate(); err == nil || !strings.Contains(err.Error(), "INGEST_CLAIM_POW_BITS") {
			t.Fatalf("INGEST_CLAIM_POW_BITS=%s: validate() = %v, want an error", bad, err)
		}
	}
}

// TestJunkVersionsDoNotLockOutARealRelease fills every label with versions
// reported once, then checks a version reported more often takes a slot,
// the least reported label is evicted, and the label count stays bounded.
func TestJunkVersionsDoNotLockOutARealRelease(t *testing.T) {
	s := newServer(config{})
	ingest := func(version string) {
		t.Helper()
		body := fmt.Sprintf(`{"version":%q,"servers":0,"templates":0}`, version)
		w := httptest.NewRecorder()
		s.ingest(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", strings.NewReader(body)))
		if w.Code != http.StatusNoContent {
			t.Fatalf("ingest %q: status %d", version, w.Code)
		}
	}
	for i := range maxVersionLabels {
		ingest(fmt.Sprintf("junk-%03d", i))
	}
	ingest("9.9.9")
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "9.9.9"); got != 0 {
		t.Fatalf("first report of a new version while full got its own label (%v)", got)
	}
	ingest("9.9.9")
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "9.9.9"); got != 1 {
		t.Fatalf("reports_total{9.9.9} = %v, want 1 once it outranks a junk label", got)
	}
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "junk-000"); got != 0 {
		t.Fatalf("evicted junk-000 still has a series (%v)", got)
	}
	families, err := s.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "gameplane_telemetry_reports_total" && len(f.Metric) > maxVersionLabels+2 {
			t.Fatalf("reports_total has %d series, want at most %d", len(f.Metric), maxVersionLabels+2)
		}
	}
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "other"); got != 1 {
		t.Fatalf("reports_total{other} = %v, want 1 (the first 9.9.9)", got)
	}
}

// TestCountReportFoldsAnEvictedLabelIntoOther checks a late increment for a
// label evicted in the meantime does not recreate its series.
func TestCountReportFoldsAnEvictedLabelIntoOther(t *testing.T) {
	s := newServer(config{})
	s.countReport("never-retained")
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "never-retained"); got != 0 {
		t.Fatalf("unretained label got a series (%v)", got)
	}
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "other"); got != 1 {
		t.Fatalf("reports_total{other} = %v, want 1", got)
	}
}

func TestPendingVersionsAreBounded(t *testing.T) {
	s := newServer(config{})
	for i := range maxVersionLabels + maxPendingVersions + 50 {
		s.versionLabel(fmt.Sprintf("v-%05d", i))
	}
	if n := len(s.versions.pending); n > maxPendingVersions {
		t.Fatalf("pending versions = %d, want at most %d", n, maxPendingVersions)
	}
}
