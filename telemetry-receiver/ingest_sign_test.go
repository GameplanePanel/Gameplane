package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// signedBaseline accepts one report from install 1 and returns the server,
// its clock, the install and the accepted body and signature (so a test can
// replay it). The report was sent at extStart.
func signedBaseline(t *testing.T, cfg config) (*server, *time.Time, extInstall, string, string) {
	t.Helper()
	s, clock := newExtServer(t, cfg, extStart)
	in := newExtInstall(1)
	body, sig := in.report(t, extStart, nil)
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("baseline: status = %d (%s)", w.Code, w.Body)
	}
	return s, clock, in, body, sig
}

// refusalCase is one forged or invalid report and the refusal it must get.
type refusalCase struct {
	name   string
	code   int
	reason string
	build  func(t *testing.T, in extInstall, body, sig string, now time.Time) (string, string)
}

func TestForgeryMatrixIsRefusedWithoutChangingAnyTable(t *testing.T) {
	cases := []refusalCase{
		{"another key claims the ID", http.StatusConflict, refuseIDClaimed,
			func(t *testing.T, in extInstall, _, _ string, now time.Time) (string, string) {
				return in.withSecret(1).report(t, now.Add(time.Minute), nil)
			}},
		{"another key with an older sentAt: the claim is checked before replay", http.StatusConflict, refuseIDClaimed,
			func(t *testing.T, in extInstall, _, _ string, now time.Time) (string, string) {
				return in.withSecret(2).report(t, now.Add(-time.Hour), nil)
			}},
		{"unsigned", http.StatusForbidden, refuseBadSignature,
			func(t *testing.T, in extInstall, _, _ string, now time.Time) (string, string) {
				body, _ := in.report(t, now.Add(time.Minute), nil)
				return body, ""
			}},
		{"malformed signature header", http.StatusForbidden, refuseBadSignature,
			func(t *testing.T, in extInstall, _, _ string, now time.Time) (string, string) {
				body, _ := in.report(t, now.Add(time.Minute), nil)
				return body, "ed25519=!!not-base64!!"
			}},
		{"one byte of the body changed", http.StatusForbidden, refuseBadSignature,
			func(t *testing.T, in extInstall, _, _ string, now time.Time) (string, string) {
				body, sig := in.report(t, now.Add(time.Minute), nil)
				tampered := strings.Replace(body, `"servers":3`, `"servers":4`, 1)
				if tampered == body {
					t.Fatal("the tamper did not change the body")
				}
				return tampered, sig
			}},
		{"a signature from another key", http.StatusForbidden, refuseBadSignature,
			func(t *testing.T, in extInstall, _, _ string, now time.Time) (string, string) {
				body, _ := in.report(t, now.Add(time.Minute), nil)
				_, otherSig := in.withSecret(3).report(t, now.Add(time.Minute), nil)
				return body, otherSig
			}},
		{"sentAt more than 36 hours old", http.StatusForbidden, refuseStale,
			func(t *testing.T, _ extInstall, _, _ string, now time.Time) (string, string) {
				return newExtInstall(7).report(t, now.Add(-36*time.Hour-time.Second), nil)
			}},
		{"sentAt more than an hour ahead", http.StatusForbidden, refuseStale,
			func(t *testing.T, _ extInstall, _, _ string, now time.Time) (string, string) {
				return newExtInstall(7).report(t, now.Add(time.Hour+time.Second), nil)
			}},
		{"a bad signature wins over a stale sentAt", http.StatusForbidden, refuseBadSignature,
			func(t *testing.T, _ extInstall, _, _ string, now time.Time) (string, string) {
				body, _ := newExtInstall(7).report(t, now.Add(-48*time.Hour), nil)
				return body, ""
			}},
		{"a byte-identical replay", http.StatusForbidden, refuseReplay,
			func(_ *testing.T, _ extInstall, body, sig string, _ time.Time) (string, string) {
				return body, sig
			}},
		{"an older sentAt from the right key", http.StatusForbidden, refuseReplay,
			func(t *testing.T, in extInstall, _, _ string, now time.Time) (string, string) {
				return in.report(t, now.Add(-30*time.Minute), nil)
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, clock, in, body, sig := signedBaseline(t, config{})
			*clock = extStart.Add(time.Minute)
			before := extDump(t, s.store)
			reportsBefore := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "1.0.0")
			refusedBefore := counterValue(t, s, "gameplane_telemetry_refused_total", "reason", tc.reason)
			b, g := tc.build(t, in, body, sig, *clock)
			w := extSend(t, s, b, g)
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.code, w.Body)
			}
			if want := `{"error":"` + tc.reason + `"}`; w.Body.String() != want {
				t.Fatalf("body = %q, want %q", w.Body, want)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if after := extDump(t, s.store); after != before {
				t.Fatalf("a refused report changed the store (SC-014):\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if got := counterValue(t, s, "gameplane_telemetry_refused_total", "reason", tc.reason); got != refusedBefore+1 {
				t.Errorf("refused_total{%s} = %v, want %v", tc.reason, got, refusedBefore+1)
			}
			if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "1.0.0"); got != reportsBefore {
				t.Errorf("reports_total changed from %v to %v", reportsBefore, got)
			}
		})
	}
}

func TestRefusedReportsDoNotUseUpTheSourceLimit(t *testing.T) {
	s, clock, in, body, sig := signedBaseline(t, config{ingestSourceDailyLimit: 2})
	*clock = extStart.Add(time.Minute)
	for range 5 {
		if w := extSend(t, s, body, sig); w.Code != http.StatusForbidden { // replays
			t.Fatalf("replay: status = %d, want 403", w.Code)
		}
		forged, _ := in.report(t, *clock, nil)
		if w := extSend(t, s, forged, ""); w.Code != http.StatusForbidden {
			t.Fatalf("unsigned: status = %d, want 403", w.Code)
		}
	}
	// One slot is left (limit 2, one report accepted): a valid report passes.
	next, nextSig := in.report(t, *clock, nil)
	if w := extSend(t, s, next, nextSig); w.Code != http.StatusNoContent {
		t.Fatalf("valid report after forgeries: status = %d, want 204", w.Code)
	}
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "ingest"); got != 0 {
		t.Errorf("rate_limited_total = %v, want 0", got)
	}
}

func TestBasicReportsSkipTheIdentityChecks(t *testing.T) {
	s, _, _, _, _ := signedBaseline(t, config{})
	// No ext part: no header needed, and a stray header is ignored.
	if w := extSend(t, s, okReport, ""); w.Code != http.StatusNoContent {
		t.Fatalf("basic report: status = %d, want 204", w.Code)
	}
	if w := extSend(t, s, okReport, "ed25519=garbage"); w.Code != http.StatusNoContent {
		t.Fatalf("basic report with a stray header: status = %d, want 204", w.Code)
	}
}

func TestSendTimeWindowIncludesItsBounds(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	for name, sentAt := range map[string]time.Time{
		"exactly 36 hours old":   extStart.Add(-36 * time.Hour),
		"exactly one hour ahead": extStart.Add(time.Hour),
		"inside the window":      extStart.Add(-time.Hour),
	} {
		in := newExtInstall(len(name))
		body, sig := in.report(t, sentAt, nil)
		if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
			t.Errorf("%s: status = %d, want 204 (%s)", name, w.Code, w.Body)
		}
	}
}

func TestClaimExpiresWithTheActivityRecordAndCanBeReclaimed(t *testing.T) {
	// FR-038: the claim lives as long as the activity record. The minimum
	// expiry is 31 days.
	s, clock, in, _, _ := signedBaseline(t, config{activityExpiryDays: 31})
	other := in.withSecret(9)

	*clock = extStart.Add(10 * 24 * time.Hour)
	body, sig := other.report(t, *clock, nil)
	if w := extSend(t, s, body, sig); w.Code != http.StatusConflict {
		t.Fatalf("day 10, another key: status = %d, want 409 (the claim is still live)", w.Code)
	}

	// Day 40: the record, last seen on day 0, is older than 31 days.
	*clock = extStart.Add(40 * 24 * time.Hour)
	if err := s.store.lifecycleOnce(t.Context(), *clock, 730); err != nil {
		t.Fatalf("lifecycleOnce: %v", err)
	}
	if got := countRows(t.Context(), t, s.store, `SELECT count(*) FROM activity`); got != 0 {
		t.Fatalf("activity rows after expiry = %d, want 0", got)
	}
	body, sig = other.report(t, *clock, nil)
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("re-claim by another key: status = %d, want 204 (%s)", w.Code, w.Body)
	}
	var fp string
	if err := s.store.db.QueryRowContext(t.Context(), `SELECT key_fp FROM activity`).Scan(&fp); err != nil {
		t.Fatal(err)
	}
	priv, err := telemetryschema.DeriveKey(other.secret, other.id)
	if err != nil {
		t.Fatal(err)
	}
	if fp != telemetryschema.KeyFingerprint(telemetryschema.PublicKeyString(priv)) {
		t.Errorf("key_fp = %s, want the new key's fingerprint", fp)
	}
	// The original key is now the one that is locked out.
	body, sig = in.report(t, clock.Add(time.Minute), nil)
	*clock = clock.Add(time.Minute)
	if w := extSend(t, s, body, sig); w.Code != http.StatusConflict {
		t.Fatalf("the former owner: status = %d, want 409", w.Code)
	}
}
