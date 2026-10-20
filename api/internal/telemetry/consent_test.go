package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

var uuidV4RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var bundledDest = Destination{Kind: KindBundled, URL: "http://receiver.test/ingest", Host: "receiver.test"}

// inTx runs fn in a transaction on store and commits when fn returns nil.
func inTx(t *testing.T, store *db.Store, fn func(ctx context.Context, tx *sql.Tx) error) error {
	t.Helper()
	ctx := t.Context()
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func apply(t *testing.T, store *db.Store, dest Destination, basic, extended bool, source string) error {
	t.Helper()
	return inTx(t, store, func(ctx context.Context, tx *sql.Tx) error {
		return ApplyConsent(ctx, tx, dest, 24*time.Hour, basic, extended, source)
	})
}

func storedConfig(t *testing.T, store *db.Store) string {
	t.Helper()
	v, ok, err := store.ConfigValue(t.Context(), "telemetry")
	if err != nil || !ok {
		t.Fatalf("read config: %q, %v, %v", v, ok, err)
	}
	return v
}

func TestApplyConsent_OperatorDisabledWritesNothing(t *testing.T) {
	store := freshStore(t)
	before := storedConfig(t, store)
	err := apply(t, store, Destination{Kind: KindDisabled}, false, false, db.TelemetryConsentAdmin)
	if !errors.Is(err, ErrOperatorDisabled) {
		t.Fatalf("err = %v, want ErrOperatorDisabled", err)
	}
	if storedConfig(t, store) != before || mustState(t, store).ConsentSource != db.TelemetryConsentDefault {
		t.Fatal("a refused save must change nothing")
	}
}

func TestApplyConsent_ExtendedOnCreatesIDAndOpensSchedule(t *testing.T) {
	store := freshStore(t)
	if err := apply(t, store, bundledDest, true, true, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("apply: %v", err)
	}
	st := mustState(t, store)
	if st.ConsentSource != db.TelemetryConsentAdmin || !uuidV4RE.MatchString(st.InstallID) {
		t.Fatalf("state = %+v, want source admin and a UUIDv4 install id", st)
	}
	if st.NextDueAt == "" {
		t.Fatal("an admin choice opens the schedule")
	}
	if secret, err := store.EnsureSigningSecret(t.Context()); err != nil || len(secret) != 32 {
		t.Fatalf("signing secret = %d bytes, %v; want it created with the ID", len(secret), err)
	}
	if got := storedConfig(t, store); got != `{"sendMetrics":true,"extended":true}` {
		t.Fatalf("config = %s", got)
	}
}

func TestApplyConsent_BasicOffForcesExtendedOffAndClosesSchedule(t *testing.T) {
	store := freshStore(t)
	if err := apply(t, store, bundledDest, true, true, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("apply on: %v", err)
	}
	if err := apply(t, store, bundledDest, false, true, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("apply off: %v", err)
	}
	st := mustState(t, store)
	if st.InstallID != "" || st.NextDueAt != "" {
		t.Fatalf("state = %+v, want the ID deleted and the schedule closed", st)
	}
	if got := storedConfig(t, store); got != `{"sendMetrics":false,"extended":false}` {
		t.Fatalf("config = %s, want extended stored off with basic", got)
	}
}

func TestApplyConsent_ExtendedOffThenOnMakesANewID(t *testing.T) {
	store := freshStore(t)
	if err := apply(t, store, bundledDest, true, true, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("apply: %v", err)
	}
	first := mustState(t, store).InstallID
	secretBefore, err := store.EnsureSigningSecret(t.Context())
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if err := apply(t, store, bundledDest, true, false, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("extended off: %v", err)
	}
	if st := mustState(t, store); st.InstallID != "" || st.NextDueAt == "" {
		t.Fatalf("state = %+v, want no ID but the basic schedule kept", st)
	}
	if err := apply(t, store, bundledDest, true, true, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("extended on again: %v", err)
	}
	second := mustState(t, store).InstallID
	if second == "" || second == first {
		t.Fatalf("install id after off/on = %q (was %q), want a new one", second, first)
	}
	secretAfter, err := store.EnsureSigningSecret(t.Context())
	if err != nil || string(secretAfter) != string(secretBefore) {
		t.Fatalf("the signing secret must survive an off/on cycle (err %v)", err)
	}
}

func TestApplyConsent_DefaultSourceKeepsGateClosedUntilNoticeShown(t *testing.T) {
	store := freshStore(t)
	if err := apply(t, store, bundledDest, true, true, db.TelemetryConsentDefault); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if st := mustState(t, store); st.NextDueAt != "" {
		t.Fatalf("slot = %q, want none while source is default and the notice is unseen", st.NextDueAt)
	}
	if err := store.MarkNoticeShown(t.Context(), "2026-10-06T09:00:00Z"); err != nil {
		t.Fatalf("mark shown: %v", err)
	}
	if err := apply(t, store, bundledDest, true, true, db.TelemetryConsentDefault); err != nil {
		t.Fatalf("apply after notice: %v", err)
	}
	if st := mustState(t, store); st.NextDueAt == "" {
		t.Fatal("once the notice was shown the schedule opens")
	}
}

func TestApplyConsent_KeepsAnExistingSlotAndSkipsNoDestination(t *testing.T) {
	store := freshStore(t)
	if err := apply(t, store, bundledDest, true, false, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("apply: %v", err)
	}
	slot := mustState(t, store).NextDueAt
	if err := apply(t, store, bundledDest, true, true, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("apply again: %v", err)
	}
	if got := mustState(t, store).NextDueAt; got != slot {
		t.Fatalf("slot = %q, want the existing %q kept", got, slot)
	}

	none := freshStore(t)
	if err := apply(t, none, Destination{Kind: KindNone}, true, true, db.TelemetryConsentAdmin); err != nil {
		t.Fatalf("apply with no destination: %v", err)
	}
	if st := mustState(t, none); st.NextDueAt != "" {
		t.Fatalf("slot = %q, want none without a destination", st.NextDueAt)
	}
}

func TestApplyConsent_ZeroIntervalMeansDaily(t *testing.T) {
	store := freshStore(t)
	err := inTx(t, store, func(ctx context.Context, tx *sql.Tx) error {
		return ApplyConsent(ctx, tx, bundledDest, 0, true, false, db.TelemetryConsentAdmin)
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	due := mustParse(t, mustState(t, store).NextDueAt)
	if wait := time.Until(due); wait > 15*time.Minute {
		t.Fatalf("first slot is %v away, want within the 15m window of a 24h interval", wait)
	}
}

func TestApplyConsent_MissingStateRowIsAnError(t *testing.T) {
	store := freshStore(t)
	if _, err := store.DB.ExecContext(t.Context(), `DELETE FROM telemetry_state`); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	if err := apply(t, store, bundledDest, true, true, db.TelemetryConsentAdmin); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestMarkNoticeSeen_SetsOnceCreatesIDAndOpensSchedule(t *testing.T) {
	store := freshStore(t) // fresh install: default source, extended on, no ID yet
	see := func() error {
		return inTx(t, store, func(ctx context.Context, tx *sql.Tx) error {
			return MarkNoticeSeen(ctx, tx, bundledDest, 24*time.Hour)
		})
	}
	if err := see(); err != nil {
		t.Fatalf("seen: %v", err)
	}
	st := mustState(t, store)
	if st.NoticeShownAt == "" || st.NextDueAt == "" || !uuidV4RE.MatchString(st.InstallID) {
		t.Fatalf("state = %+v, want the notice stamped, a slot and an install id", st)
	}
	if st.ConsentSource != db.TelemetryConsentDefault {
		t.Fatalf("consent source = %q, seen must not change it", st.ConsentSource)
	}
	if wait := mustParse(t, st.NextDueAt).Sub(mustParse(t, st.NoticeShownAt)); wait < 0 || wait > 15*time.Minute {
		t.Fatalf("first slot is %v after the notice, want within 15m", wait)
	}
	if err := see(); err != nil {
		t.Fatalf("seen again: %v", err)
	}
	if again := mustState(t, store); again != st {
		t.Fatalf("a second seen changed the state: %+v then %+v", st, again)
	}
}

func TestMarkNoticeSeen_BasicOffOrNoDestinationOpensNothing(t *testing.T) {
	off := freshStore(t)
	if _, err := off.DB.ExecContext(t.Context(),
		`UPDATE config SET value = '{"sendMetrics":false,"extended":false}' WHERE key = 'telemetry'`); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	none := freshStore(t)
	for name, tc := range map[string]struct {
		store *db.Store
		dest  Destination
	}{"basic off": {off, bundledDest}, "no destination": {none, Destination{Kind: KindNone}}} {
		err := inTx(t, tc.store, func(ctx context.Context, tx *sql.Tx) error {
			return MarkNoticeSeen(ctx, tx, tc.dest, 0)
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		st := mustState(t, tc.store)
		if st.NoticeShownAt == "" || st.NextDueAt != "" {
			t.Fatalf("%s: state = %+v, want the notice stamped and no slot", name, st)
		}
	}
}

func TestMarkNoticeSeen_MissingStateRowIsAnError(t *testing.T) {
	store := freshStore(t)
	if _, err := store.DB.ExecContext(t.Context(), `DELETE FROM telemetry_state`); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	err := inTx(t, store, func(ctx context.Context, tx *sql.Tx) error {
		return MarkNoticeSeen(ctx, tx, bundledDest, time.Hour)
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestNewInstallID_IsRandomUUIDv4(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		id, err := NewInstallID()
		if err != nil || !uuidV4RE.MatchString(id) {
			t.Fatalf("NewInstallID = %q, %v; want a lowercase UUIDv4", id, err)
		}
		if seen[id] {
			t.Fatalf("NewInstallID repeated %q", id)
		}
		seen[id] = true
	}
}

func TestFirstDueAndRandN(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	if got := firstDue(now, 24*time.Hour, func(n int64) int64 { return n - 1 }); got.Sub(now) >= 15*time.Minute {
		t.Errorf("a 24h interval allows up to 15m, got %v", got.Sub(now))
	}
	if got := firstDue(now, time.Minute, func(n int64) int64 { return n - 1 }); got.Sub(now) >= 15*time.Second {
		t.Errorf("a 1m interval allows up to interval/4 = 15s, got %v", got.Sub(now))
	}
	if got := randN(0); got != 0 {
		t.Errorf("randN(0) = %d, want 0", got)
	}
	for range 50 {
		if v := randN(10); v < 0 || v >= 10 {
			t.Fatalf("randN(10) = %d, out of [0,10)", v)
		}
	}
}

func TestReadConsent_ClosedStoreIsAnError(t *testing.T) {
	store := freshStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := ReadConsent(t.Context(), store); err == nil {
		t.Fatal("want an error from a closed database")
	}
}
