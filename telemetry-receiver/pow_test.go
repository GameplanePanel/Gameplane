package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// Spec 022 US7 (T102 to T104): proof-of-work on /ingest, the challenge
// endpoint and the difficulty curve (research R21, OD-5).

// powCfg is a proof-of-work config with the OD-5 normal rate (60 a minute).
func powCfg(minBits, maxBits int) config {
	return config{ingestPoW: true, powTargetPerMin: 60, powMinBits: minBits, powMaxBits: maxBits}
}

// newPoWServer returns a receiver with proof-of-work on, a controllable clock
// starting at extStart, and an in-memory store.
func newPoWServer(t *testing.T, minBits, maxBits int) (*server, *time.Time) {
	t.Helper()
	return newExtServer(t, powCfg(minBits, maxBits), extStart)
}

// powGet sends GET /v1/challenge through the router from remote.
func powGet(t *testing.T, s *server, remote string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/challenge", nil)
	r.RemoteAddr = remote
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}

// powSolved issues a challenge directly (not through the endpoint, so the
// per-source bucket is untouched), solves it and returns the header value.
func powSolved(t *testing.T, s *server) string {
	t.Helper()
	token, bits, _ := s.pow.issue(s.now())
	nonce, err := telemetryschema.SolvePoW(t.Context(), token, bits)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	return telemetryschema.FormatPoW(token, nonce)
}

// powPost POSTs body to /ingest through the router from a fixed source. The
// proof-of-work header is sent when header is not empty; extra headers are
// added as given.
func powPost(t *testing.T, s *server, body io.Reader, header string, extra map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", body)
	r.RemoteAddr = "192.0.2.1:1000"
	if header != "" {
		r.Header.Set(telemetryschema.PoWHeader, header)
	}
	for k, v := range extra {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}

// trackedBody is a request body that records whether it was read.
type trackedBody struct {
	read atomic.Bool
}

func (b *trackedBody) Read([]byte) (int, error) {
	b.read.Store(true)
	return 0, io.EOF
}

// powBitsGauge reads gameplane_telemetry_pow_bits.
func powBitsGauge(t *testing.T, s *server) float64 {
	t.Helper()
	families, err := s.reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "gameplane_telemetry_pow_bits" {
			return f.Metric[0].GetGauge().GetValue()
		}
	}
	t.Fatal("gameplane_telemetry_pow_bits not found")
	return 0
}

// issueN issues n challenges at the clock's current instant and returns the
// difficulty of the last one.
func issueN(s *server, n int) int {
	bits := 0
	for range n {
		_, bits, _ = s.pow.issue(s.now())
	}
	return bits
}

func TestPoWChallengeIsNotFoundWhenOff(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	if w := powGet(t, s, "192.0.2.1:1000"); w.Code != http.StatusNotFound {
		t.Fatalf("GET /v1/challenge with proof-of-work off = %d, want 404", w.Code)
	}
	// Off means /ingest takes reports as before: no header needed.
	if w := powPost(t, s, strings.NewReader(okReport), "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("ingest with proof-of-work off = %d, want 204", w.Code)
	}
	if got := powBitsGauge(t, s); got != 0 {
		t.Fatalf("pow_bits with proof-of-work off = %v, want 0", got)
	}
}

func TestPoWChallengeEndpointServesASolvableChallenge(t *testing.T) {
	s, _ := newPoWServer(t, 8, 22)
	w := powGet(t, s, "192.0.2.1:1000")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/challenge = %d (%s), want 200", w.Code, w.Body)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var c challengeReply
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
	if c.Bits != 8 {
		t.Errorf("bits = %d, want the minimum 8", c.Bits)
	}
	if want := extStart.Add(15 * time.Minute).Format(time.RFC3339); c.ExpiresAt != want {
		t.Errorf("expiresAt = %q, want %q", c.ExpiresAt, want)
	}
	if got := extCounter(t, s, "gameplane_telemetry_pow_challenges_total"); got != 1 {
		t.Errorf("pow_challenges_total = %v, want 1", got)
	}
	nonce, err := telemetryschema.SolvePoW(t.Context(), c.Challenge, c.Bits)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	header := telemetryschema.FormatPoW(c.Challenge, nonce)
	if w := powPost(t, s, strings.NewReader(okReport), header, nil); w.Code != http.StatusNoContent {
		t.Fatalf("report with the solved challenge = %d (%s), want 204", w.Code, w.Body)
	}
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "1.0.0"); got != 1 {
		t.Errorf("reports_total = %v, want 1", got)
	}
}

func TestPoWChallengeRateLimitPerSource(t *testing.T) {
	s, clock := newPoWServer(t, 0, 22)
	for i := range powChallengeBurst {
		if w := powGet(t, s, "192.0.2.1:1000"); w.Code != http.StatusOK {
			t.Fatalf("challenge %d = %d, want 200 (inside the burst)", i, w.Code)
		}
	}
	if w := powGet(t, s, "192.0.2.1:2000"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("challenge past the burst = %d, want 429", w.Code)
	}
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "challenge"); got != 1 {
		t.Errorf("rate_limited_total{route=challenge} = %v, want 1", got)
	}
	if got := extCounter(t, s, "gameplane_telemetry_pow_challenges_total"); got != powChallengeBurst {
		t.Errorf("pow_challenges_total = %v, want %d: a limited request issues nothing", got, powChallengeBurst)
	}
	if w := powGet(t, s, "198.51.100.7:1000"); w.Code != http.StatusOK {
		t.Fatalf("another source = %d, want 200: the bucket is per source", w.Code)
	}
	*clock = extStart.Add(7 * time.Second)
	if w := powGet(t, s, "192.0.2.1:1000"); w.Code != http.StatusOK {
		t.Fatalf("after the bucket refilled = %d, want 200", w.Code)
	}
}

func TestPoWCurveFollowsTheRequestRate(t *testing.T) {
	cases := []struct {
		name     string
		issues   int
		min, max int
		want     int
	}{
		{"at the normal rate", 60, 0, 22, 0},
		{"at the normal rate with a minimum", 60, 3, 22, 3},
		{"just over the normal rate", 61, 0, 22, 1},
		{"10 times the normal rate", 600, 0, 22, 7},
		{"100 times the normal rate", 6000, 0, 22, 14},
		{"1000 times the normal rate", 60000, 0, 22, 20},
		{"capped at the maximum", 6000, 0, 10, 10},
		{"minimum above the curve", 600, 9, 22, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newPoWServer(t, tc.min, tc.max)
			if got := issueN(s, tc.issues); got != tc.want {
				t.Fatalf("bits after %d challenges in one minute = %d, want %d", tc.issues, got, tc.want)
			}
		})
	}
}

func TestPoWDifficultyRisesAtOnceAndDecaysOneBitPerFiveMinutes(t *testing.T) {
	s, clock := newPoWServer(t, 0, 22)
	if got := issueN(s, 6000); got != 14 {
		t.Fatalf("bits at 100 times the rate = %d, want 14", got)
	}
	if got := powBitsGauge(t, s); got != 14 {
		t.Fatalf("pow_bits right after the flood = %v, want 14", got)
	}
	for _, step := range []struct {
		after time.Duration
		want  int
	}{
		{4*time.Minute + 59*time.Second, 14},
		{5 * time.Minute, 13},
		{10 * time.Minute, 12},
		{70 * time.Minute, 0},
	} {
		*clock = extStart.Add(step.after)
		if got := issueN(s, 1); got != step.want {
			t.Fatalf("bits %v after the flood = %d, want %d", step.after, got, step.want)
		}
	}

	// A new flood raises the very next challenge to the new target.
	*clock = extStart.Add(2 * time.Hour)
	if got := issueN(s, 600); got != 7 {
		t.Fatalf("bits at 10 times the rate = %d, want 7", got)
	}
	*clock = extStart.Add(2*time.Hour + 10*time.Minute)
	if got := powBitsGauge(t, s); got != 5 {
		t.Fatalf("pow_bits 10 minutes after a 7-bit peak = %v, want 5", got)
	}
}

func TestPoWMinimumHoldsWhenIdle(t *testing.T) {
	s, clock := newPoWServer(t, 4, 22)
	if got := issueN(s, 1); got != 4 {
		t.Fatalf("bits = %d, want the minimum 4", got)
	}
	*clock = extStart.Add(24 * time.Hour)
	if got := issueN(s, 1); got != 4 {
		t.Fatalf("bits a day later = %d, want the minimum 4", got)
	}
}

func TestPoWAcceptsEachSolvedChallengeOnce(t *testing.T) {
	s, _ := newPoWServer(t, 8, 22)
	header := powSolved(t, s)
	if w := powPost(t, s, strings.NewReader(okReport), header, nil); w.Code != http.StatusNoContent {
		t.Fatalf("first use = %d (%s), want 204", w.Code, w.Body)
	}
	w := powPost(t, s, strings.NewReader(okReport), header, nil)
	if w.Code != http.StatusPreconditionRequired || w.Body.String() != `{"error":"pow_invalid"}` {
		t.Fatalf("second use = %d %q, want 428 pow_invalid", w.Code, w.Body)
	}
}

// powRefusal is one header that must be refused with 428.
type powRefusal struct {
	name   string
	reason string
	// header builds the PoWHeader value; clock may be moved.
	header func(t *testing.T, s *server, clock *time.Time) string
}

func TestPoWRefusalsChangeNoTable(t *testing.T) {
	cases := []powRefusal{
		{"no header", refusePoWRequired,
			func(*testing.T, *server, *time.Time) string { return "" }},
		{"no nonce separator", refusePoWInvalid,
			func(*testing.T, *server, *time.Time) string { return "garbage" }},
		{"nonce is not decimal", refusePoWInvalid,
			func(_ *testing.T, s *server, _ *time.Time) string {
				token, _, _ := s.pow.issue(s.now())
				return token + ":abc"
			}},
		{"tampered MAC", refusePoWInvalid,
			func(t *testing.T, s *server, _ *time.Time) string {
				header := powSolved(t, s)
				token, nonce, err := telemetryschema.ParsePoW(header)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				// Change the first character of the MAC. (The last character of
				// an unpadded base64url string can carry unused bits, so
				// changing it might not change the decoded MAC.)
				i := strings.IndexByte(token, '.') + 1
				flipped := byte('A')
				if token[i] == 'A' {
					flipped = 'B'
				}
				return telemetryschema.FormatPoW(token[:i]+string(flipped)+token[i+1:], nonce)
			}},
		{"bits lowered inside the payload", refusePoWInvalid,
			func(t *testing.T, s *server, _ *time.Time) string {
				token, _, _ := s.pow.issue(s.now())
				rawPayload, rawMAC, _ := strings.Cut(token, ".")
				payload, err := base64.RawURLEncoding.DecodeString(rawPayload)
				if err != nil {
					t.Fatalf("decode payload: %v", err)
				}
				payload[9] = 0
				// Nonce 0 satisfies zero bits, so only the MAC can catch this.
				return telemetryschema.FormatPoW(base64.RawURLEncoding.EncodeToString(payload)+"."+rawMAC, 0)
			}},
		{"token from another receiver", refusePoWInvalid,
			func(t *testing.T, _ *server, _ *time.Time) string {
				other, _ := newPoWServer(t, 0, 22)
				return powSolved(t, other)
			}},
		{"nonce does not meet the difficulty", refusePoWInvalid,
			func(_ *testing.T, s *server, _ *time.Time) string {
				token, bits, _ := s.pow.issue(s.now())
				for nonce := uint64(0); ; nonce++ {
					if !telemetryschema.PoWOK(token, nonce, bits) {
						return telemetryschema.FormatPoW(token, nonce)
					}
				}
			}},
		{"expired", refusePoWInvalid,
			func(t *testing.T, s *server, clock *time.Time) string {
				header := powSolved(t, s)
				*clock = extStart.Add(15*time.Minute + time.Second)
				return header
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, clock := newPoWServer(t, 12, 22)
			header := tc.header(t, s, clock)
			before := extDump(t, s.store)
			refusedBefore := counterValue(t, s, "gameplane_telemetry_refused_total", "reason", tc.reason)

			w := powPost(t, s, strings.NewReader(okReport), header, nil)
			if w.Code != http.StatusPreconditionRequired {
				t.Fatalf("status = %d (%s), want 428", w.Code, w.Body)
			}
			if want := `{"error":"` + tc.reason + `"}`; w.Body.String() != want {
				t.Fatalf("body = %q, want %q", w.Body, want)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if after := extDump(t, s.store); after != before {
				t.Fatalf("a refused report changed the store:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if got := counterValue(t, s, "gameplane_telemetry_refused_total", "reason", tc.reason); got != refusedBefore+1 {
				t.Errorf("refused_total{%s} = %v, want %v", tc.reason, got, refusedBefore+1)
			}
			if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "1.0.0"); got != 0 {
				t.Errorf("reports_total = %v, want 0", got)
			}
		})
	}
}

func TestPoWChallengeIsValidForExactlyFifteenMinutes(t *testing.T) {
	s, clock := newPoWServer(t, 0, 22)
	header := powSolved(t, s)
	*clock = extStart.Add(15 * time.Minute)
	if w := powPost(t, s, strings.NewReader(okReport), header, nil); w.Code != http.StatusNoContent {
		t.Fatalf("at 15 minutes = %d (%s), want 204", w.Code, w.Body)
	}
}

func TestPoWSolutionIsSpentEvenWhenTheReportIsRejected(t *testing.T) {
	s, _ := newPoWServer(t, 0, 22)
	header := powSolved(t, s)
	if w := powPost(t, s, strings.NewReader(`{not json`), header, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed report = %d, want 400 (the proof of work was fine)", w.Code)
	}
	w := powPost(t, s, strings.NewReader(okReport), header, nil)
	if w.Code != http.StatusPreconditionRequired || w.Body.String() != `{"error":"pow_invalid"}` {
		t.Fatalf("reuse after a 400 = %d %q, want 428 pow_invalid", w.Code, w.Body)
	}
}

func TestPoWIsCheckedAfterAuthAndBeforeTheBody(t *testing.T) {
	cfg := powCfg(0, 22)
	cfg.authToken = "secret"
	s, _ := newExtServer(t, cfg, extStart)
	auth := map[string]string{"Authorization": "secret"}

	// AUTH_TOKEN first: a wrong token is 401 even with no proof of work.
	if w := powPost(t, s, strings.NewReader(okReport), "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token and no proof = %d, want 401", w.Code)
	}
	if got := counterValue(t, s, "gameplane_telemetry_refused_total", "reason", refusePoWRequired); got != 0 {
		t.Fatalf("refused_total{pow_required} = %v after a 401, want 0", got)
	}
	// With the token, the missing proof is next, and the body is never read.
	body := &trackedBody{}
	if w := powPost(t, s, body, "", auth); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("token but no proof = %d, want 428", w.Code)
	}
	if body.read.Load() {
		t.Fatal("the body was read before the proof of work was checked")
	}
	// An oversized or malformed body gets 428, not 413 or 400, without proof.
	huge := strings.NewReader(strings.Repeat("a", maxBody+1))
	if w := powPost(t, s, huge, "", auth); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("oversized body without proof = %d, want 428", w.Code)
	}
	// With both, the report goes through.
	if w := powPost(t, s, strings.NewReader(okReport), powSolved(t, s), auth); w.Code != http.StatusNoContent {
		t.Fatalf("token and proof = %d (%s), want 204", w.Code, w.Body)
	}
}

func TestPoWBusyWhenTheUsedSetIsFull(t *testing.T) {
	old := powUsedCap
	powUsedCap = 2
	t.Cleanup(func() { powUsedCap = old })

	s, clock := newPoWServer(t, 0, 22)
	for i := range 2 {
		if w := powPost(t, s, strings.NewReader(okReport), powSolved(t, s), nil); w.Code != http.StatusNoContent {
			t.Fatalf("report %d = %d, want 204", i, w.Code)
		}
	}
	before := extDump(t, s.store)
	full := powSolved(t, s)
	w := powPost(t, s, strings.NewReader(okReport), full, nil)
	if w.Code != http.StatusServiceUnavailable || w.Body.String() != `{"error":"pow_busy"}` {
		t.Fatalf("report with a full used set = %d %q, want 503 pow_busy", w.Code, w.Body)
	}
	if ra := w.Header().Get("Retry-After"); ra != "60" {
		t.Errorf("Retry-After = %q, want 60", ra)
	}
	if got := counterValue(t, s, "gameplane_telemetry_refused_total", "reason", refusePoWBusy); got != 1 {
		t.Errorf("refused_total{pow_busy} = %v, want 1", got)
	}
	if extDump(t, s.store) != before {
		t.Error("a pow_busy refusal changed the store")
	}
	if n := len(s.pow.used); n != 2 {
		t.Fatalf("used set holds %d entries, want 2: pow_busy must mark nothing", n)
	}

	// Entries are forgotten once their challenges have expired, which frees
	// room (the sweep is lazy, at most once a minute).
	*clock = extStart.Add(16 * time.Minute)
	if w := powPost(t, s, strings.NewReader(okReport), powSolved(t, s), nil); w.Code != http.StatusNoContent {
		t.Fatalf("report after the old entries expired = %d (%s), want 204", w.Code, w.Body)
	}
	if n := len(s.pow.used); n != 1 {
		t.Fatalf("used set holds %d entries after the sweep, want 1", n)
	}
}

func TestPoWAcceptsOneConcurrentUseOfAChallenge(t *testing.T) {
	s, _ := newPoWServer(t, 0, 22)
	header := powSolved(t, s)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if s.pow.check(header, s.now()) == powAccepted {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if got := accepted.Load(); got != 1 {
		t.Fatalf("%d concurrent uses of one challenge were accepted, want exactly 1", got)
	}
}

func TestNewPoWClampsItsSettings(t *testing.T) {
	p := newPoW(config{ingestPoW: true, powTargetPerMin: 0, powMinBits: -3, powMaxBits: 99})
	if p.targetPerMin != 1 || p.minBits != 0 || p.maxBits != telemetryschema.MaxPoWBits {
		t.Fatalf("clamped = target %d min %d max %d, want 1, 0, %d", p.targetPerMin, p.minBits, p.maxBits, telemetryschema.MaxPoWBits)
	}
	q := newPoW(config{ingestPoW: true, powTargetPerMin: 60, powMinBits: 9, powMaxBits: 4})
	if q.maxBits != 9 {
		t.Fatalf("max = %d, want it raised to the minimum 9", q.maxBits)
	}
}

// clearPoWEnv unsets every INGEST_POW* variable for the test.
func clearPoWEnv(t *testing.T) {
	t.Helper()
	clearConfigEnv(t)
	for _, k := range []string{"INGEST_POW", "INGEST_POW_TARGET_PER_MIN", "INGEST_POW_MIN_BITS", "INGEST_POW_MAX_BITS"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
}

func TestLoadConfigPoWDefaultsAndEnv(t *testing.T) {
	clearPoWEnv(t)
	cfg := loadConfig()
	if cfg.ingestPoW || cfg.powTargetPerMin != 60 || cfg.powMinBits != 0 || cfg.powMaxBits != 22 {
		t.Fatalf("defaults = %+v, want off, 60, 0 and 22", cfg)
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	t.Setenv("INGEST_POW", " TRUE ")
	t.Setenv("INGEST_POW_TARGET_PER_MIN", "120")
	t.Setenv("INGEST_POW_MIN_BITS", "8")
	t.Setenv("INGEST_POW_MAX_BITS", "26")
	cfg = loadConfig()
	if !cfg.ingestPoW || cfg.powTargetPerMin != 120 || cfg.powMinBits != 8 || cfg.powMaxBits != 26 {
		t.Fatalf("env = %+v, want on, 120, 8 and 26", cfg)
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("the ceiling 26 must validate: %v", err)
	}
	t.Setenv("INGEST_POW", "yes")
	if loadConfig().ingestPoW {
		t.Error("INGEST_POW other than true must stay off")
	}
}

func TestConfigValidatePoWRanges(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"target below 1", map[string]string{"INGEST_POW_TARGET_PER_MIN": "0"}, "INGEST_POW_TARGET_PER_MIN"},
		{"negative target", map[string]string{"INGEST_POW_TARGET_PER_MIN": "-5"}, "INGEST_POW_TARGET_PER_MIN"},
		{"negative minimum", map[string]string{"INGEST_POW_MIN_BITS": "-1"}, "INGEST_POW_MIN_BITS"},
		{"maximum above the cap", map[string]string{"INGEST_POW_MAX_BITS": "27"}, "INGEST_POW_MAX_BITS"},
		{"maximum below the minimum", map[string]string{"INGEST_POW_MIN_BITS": "10", "INGEST_POW_MAX_BITS": "9"}, "INGEST_POW_MAX_BITS"},
		{"non-integer maximum", map[string]string{"INGEST_POW_MAX_BITS": "high"}, "INGEST_POW_MAX_BITS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearPoWEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			err := loadConfig().validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate() = %v, want an error mentioning %s", err, tc.want)
			}
		})
	}
	// The edges are valid.
	clearPoWEnv(t)
	t.Setenv("INGEST_POW_MIN_BITS", "26")
	t.Setenv("INGEST_POW_MAX_BITS", "26")
	t.Setenv("INGEST_POW_TARGET_PER_MIN", "1")
	if err := loadConfig().validate(); err != nil {
		t.Fatalf("min = max = 26 and target 1 must validate: %v", err)
	}
}

func TestDashboardTokenMinimum(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
		ok    bool
	}{
		{"unset", "", true},
		{"31 characters", strings.Repeat("a", 31), false},
		{"32 characters", strings.Repeat("a", 32), true},
		{"31 multi-byte characters", strings.Repeat("é", 31), false},
		{"long", strings.Repeat("a", 64), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := config{dashboardToken: tc.token}.validateDashboardToken()
			if tc.ok && err != nil {
				t.Fatalf("validateDashboardToken() = %v, want nil", err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), "32")) {
				t.Fatalf("validateDashboardToken() = %v, want an error naming the 32-character minimum", err)
			}
		})
	}
}

func TestRunRefusesAShortDashboardToken(t *testing.T) {
	clearPoWEnv(t)
	t.Setenv("DASHBOARD_TOKEN", strings.Repeat("a", 31))
	err := run(loadConfig())
	if err == nil || !strings.Contains(err.Error(), "DASHBOARD_TOKEN") || !strings.Contains(err.Error(), "32") {
		t.Fatalf("run() = %v, want a configuration error naming DASHBOARD_TOKEN and 32", err)
	}
}
