package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// sameDayDuplicate accepts install 1's baseline report at extStart, then a
// second report from it two hours later, which is a same-day duplicate. It
// returns the server, its clock, and the duplicate's body and signature.
func sameDayDuplicate(t *testing.T) (*server, *time.Time, string, string) {
	t.Helper()
	s, clock, in, _, _ := signedBaseline(t, config{})
	*clock = extStart.Add(2 * time.Hour)
	body, sig := in.report(t, *clock, nil)
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("duplicate: status = %d (%s)", w.Code, w.Body)
	}
	if got := extCounter(t, s, "gameplane_telemetry_duplicates_total"); got != 1 {
		t.Fatalf("duplicates_total = %v, want 1", got)
	}
	return s, clock, body, sig
}

// assertReplayRefused sends body again and requires a 403 replay refusal that
// leaves every table unchanged.
func assertReplayRefused(t *testing.T, s *server, body, sig string) {
	t.Helper()
	before := extDump(t, s.store)
	w := extSend(t, s, body, sig)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"replay"`) {
		t.Fatalf("replay: status = %d (%s), want 403 replay", w.Code, w.Body)
	}
	if after := extDump(t, s.store); after != before {
		t.Errorf("a refused replay changed the tables:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestReplayOfASameDayDuplicateIsRefused(t *testing.T) {
	s, _, body, sig := sameDayDuplicate(t)
	assertReplayRefused(t, s, body, sig)
	if got := extCounter(t, s, "gameplane_telemetry_duplicates_total"); got != 1 {
		t.Errorf("duplicates_total = %v, want 1", got)
	}
}

func TestReplayOfASameDayDuplicateOnTheNextDayIsRefused(t *testing.T) {
	s, clock, body, sig := sameDayDuplicate(t)
	// 08:00 the next day: the duplicate (sent at 14:00) is 18 hours old, which
	// is inside the 36-hour window, and the install has not reported today.
	*clock = extStart.Add(20 * time.Hour)
	assertReplayRefused(t, s, body, sig)
}

func TestReplayWithAFractionalSendTimeIsRefused(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	in := newExtInstall(1)
	body, _ := in.report(t, extStart, nil)
	whole := `"sentAt":"` + extStart.Format(time.RFC3339) + `"`
	if !strings.Contains(body, whole) {
		t.Fatalf("body has no %s: %s", whole, body)
	}
	// Half a second later. The stored send time must keep the fraction, or a
	// byte-identical replay would look later than the stored whole second.
	frac := `"sentAt":"` + extStart.Add(500*time.Millisecond).Format(time.RFC3339Nano) + `"`
	body = strings.Replace(body, whole, frac, 1)
	priv, err := telemetryschema.DeriveKey(in.secret, in.id)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	sig := telemetryschema.Sign(priv, []byte(body))
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("first: status = %d (%s)", w.Code, w.Body)
	}
	assertReplayRefused(t, s, body, sig)
}

func TestUnreadableLastSendTimeFailsClosed(t *testing.T) {
	s, clock, in, _, _ := signedBaseline(t, config{})
	if _, err := s.store.db.ExecContext(t.Context(), `UPDATE activity SET last_sent_at = 'not a time'`); err != nil {
		t.Fatalf("corrupt last_sent_at: %v", err)
	}
	*clock = extStart.Add(24 * time.Hour)
	body, sig := in.report(t, *clock, nil)
	before := extDump(t, s.store)
	if w := extSend(t, s, body, sig); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500: an unreadable send time must not disable the replay check", w.Code, w.Body)
	}
	if after := extDump(t, s.store); after != before {
		t.Errorf("a failed report changed the tables:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
