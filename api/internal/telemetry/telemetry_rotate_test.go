package telemetry

import (
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// Spec 022 US7 (T084, T087): a 409 id_claimed makes the reporter replace its
// install ID and re-send in the same attempt; a 403 never rotates.

// decodeSent decodes the body of one recorded POST.
func decodeSent(t *testing.T, s sentReport) telemetryschema.Report {
	t.Helper()
	rep, _, err := telemetryschema.Decode(s.body)
	if err != nil || rep.Ext == nil {
		t.Fatalf("decode sent body %q: ext=%v err=%v", s.body, rep.Ext != nil, err)
	}
	return rep
}

func TestReporter_409RotatesTheIDAndResendsInTheSameAttempt(t *testing.T) {
	srv, got := newSink(t)
	var calls atomic.Int32
	got.setRespond(func(sentReport) int {
		if calls.Add(1) == 1 {
			return http.StatusConflict
		}
		return http.StatusNoContent
	})
	clk := newFakeClock()
	store := telStoreExt(t, true, true)
	secretBefore, err := store.EnsureSigningSecret(t.Context())
	if err != nil {
		t.Fatalf("ensure secret: %v", err)
	}
	r := testReporter(store, telKubeExt(), srv.URL, clk)

	if err := sendNow(t, r); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if got.count() != 2 {
		t.Fatalf("posts = %d, want the 409 and one re-POST in the same attempt", got.count())
	}
	first, second := decodeSent(t, got.at(0)), decodeSent(t, got.at(1))
	if first.Ext.InstallID != testInstallID {
		t.Fatalf("first POST id = %q, want the original %q", first.Ext.InstallID, testInstallID)
	}
	if second.Ext.InstallID == first.Ext.InstallID || second.Ext.Key == first.Ext.Key {
		t.Fatalf("re-POST reused the id or key: %q / %q", second.Ext.InstallID, second.Ext.Key)
	}
	if got.at(1).signature == "" || got.at(1).signature == got.at(0).signature {
		t.Fatal("re-POST must carry a new signature")
	}
	if err := telemetryschema.Verify(got.at(1).signature, second.Ext.Key, got.at(1).body); err != nil {
		t.Fatalf("re-POST signature does not verify under its own key: %v", err)
	}
	priv, err := telemetryschema.DeriveKey(secretBefore, second.Ext.InstallID)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	if telemetryschema.PublicKeyString(priv) != second.Ext.Key {
		t.Fatal("re-POST key is not derived from the kept signing secret and the new id")
	}

	st := mustState(t, store)
	if st.InstallID != second.Ext.InstallID {
		t.Fatalf("stored id = %q, want the id that was re-sent (%q)", st.InstallID, second.Ext.InstallID)
	}
	if want := stamp(clk.Now()); st.LastIDRotationAt != want {
		t.Fatalf("last_id_rotation_at = %q, want %q", st.LastIDRotationAt, want)
	}
	if st.LastOutcome != outcomeOK || st.ConsecutiveFailures != 0 {
		t.Fatalf("outcome = %q failures = %d, want ok and none (SC-015)", st.LastOutcome, st.ConsecutiveFailures)
	}
	secretAfter, err := store.EnsureSigningSecret(t.Context())
	if err != nil || !bytes.Equal(secretAfter, secretBefore) {
		t.Fatalf("signing secret changed across the rotation: %v", err)
	}
}

func TestReporter_SecondConflictIsFailedAndRotatesOnlyOnce(t *testing.T) {
	srv, got := newSink(t)
	got.setRespond(func(sentReport) int { return http.StatusConflict })
	clk := newFakeClock()
	store := telStoreExt(t, true, true)
	r := testReporter(store, telKubeExt(), srv.URL, clk)

	if err := sendNow(t, r); err == nil {
		t.Fatal("two 409s in a row must fail the attempt")
	}
	if got.count() != 2 {
		t.Fatalf("posts = %d, want exactly the 409 and one re-POST", got.count())
	}
	first, second := decodeSent(t, got.at(0)), decodeSent(t, got.at(1))
	st := mustState(t, store)
	if first.Ext.InstallID != testInstallID || st.InstallID != second.Ext.InstallID || st.InstallID == testInstallID {
		t.Fatalf("ids: first %q second %q stored %q, want one rotation only", first.Ext.InstallID, second.Ext.InstallID, st.InstallID)
	}
	if st.LastOutcome != outcomeFailed || st.ConsecutiveFailures != 1 || st.LastIDRotationAt == "" {
		t.Fatalf("state = %+v, want failed, one failure and the rotation recorded", st)
	}
}

func TestReporter_403NeverRotatesTheID(t *testing.T) {
	srv, got := newSink(t)
	got.setRespond(func(sentReport) int { return http.StatusForbidden })
	store := telStoreExt(t, true, true)
	r := testReporter(store, telKubeExt(), srv.URL, newFakeClock())

	if err := sendNow(t, r); err == nil {
		t.Fatal("a 403 must fail the attempt")
	}
	if got.count() != 1 {
		t.Fatalf("posts = %d, want one (no re-POST and no basic fallback)", got.count())
	}
	st := mustState(t, store)
	if st.InstallID != testInstallID || st.LastIDRotationAt != "" {
		t.Fatalf("state = %+v, a 403 must keep the id and set no rotation", st)
	}
	if st.LastOutcome != outcomeFailed || st.ConsecutiveFailures != 1 {
		t.Fatalf("outcome = %q failures = %d, want failed with normal backoff", st.LastOutcome, st.ConsecutiveFailures)
	}
}

func TestReporter_RotationFailsWhenExtendedWentOffDuringTheSend(t *testing.T) {
	srv, got := newSink(t)
	store := telStoreExt(t, true, true)
	got.setRespond(func(sentReport) int {
		// The admin turns extended off while the report is in flight.
		if err := store.ClearInstallID(t.Context()); err != nil {
			t.Errorf("clear install id: %v", err)
		}
		return http.StatusConflict
	})
	r := testReporter(store, telKubeExt(), srv.URL, newFakeClock())

	if err := sendNow(t, r); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("attempt error = %v, want the failed rotation (no ID to replace)", err)
	}
	if got.count() != 1 {
		t.Fatalf("posts = %d, want no re-POST after a failed rotation", got.count())
	}
	st := mustState(t, store)
	if st.InstallID != "" || st.LastIDRotationAt != "" || st.LastOutcome != outcomeFailed {
		t.Fatalf("state = %+v, want no ID resurrected and the attempt failed", st)
	}
}

func TestBuildReport_ExtendedFlagControlsTheExtPart(t *testing.T) {
	store := telStoreExt(t, true, true)
	deps := Deps{Kube: telKubeExt(), Store: store, Version: "v1.2.3"}
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

	basic, err := BuildReport(t.Context(), deps, false, now)
	if err != nil || basic.Ext != nil || basic.Version != "v1.2.3" {
		t.Fatalf("basic = %+v, %v, want a report without ext", basic, err)
	}
	full, err := BuildReport(t.Context(), deps, true, now)
	if err != nil || full.Ext == nil || !full.Ext.SentAt.Equal(now) || full.Ext.InstallID != testInstallID {
		t.Fatalf("extended = %+v, %v, want ext stamped with now and the stored id", full, err)
	}
}

func TestExtWithheld_ExportedMatchesTheMarker(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	store := telStoreExt(t, true, true)
	if err := store.SetExtUnsupported(t.Context(), stamp(now.Add(time.Hour)), "http://old"); err != nil {
		t.Fatalf("set marker: %v", err)
	}
	st := mustState(t, store)
	if !ExtWithheld(st, "http://old", now) {
		t.Fatal("the marked endpoint must be withheld until the marker expires")
	}
	if ExtWithheld(st, "http://new", now) || ExtWithheld(st, "http://old", now.Add(2*time.Hour)) {
		t.Fatal("another endpoint, or an expired marker, must not be withheld")
	}
}
