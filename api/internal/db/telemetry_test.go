package db

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// telemetryConfig returns the raw telemetry config value and whether the key
// exists.
func telemetryConfig(t *testing.T, s *Store) (string, bool) {
	t.Helper()
	v, ok, err := s.ConfigValue(t.Context(), "telemetry")
	if err != nil {
		t.Fatalf("read telemetry config: %v", err)
	}
	return v, ok
}

// existingInstallStore returns a migrated store rewound to the state of an
// install that ran before migration 015: no telemetry tables, 015 unrecorded,
// and telemetryValue (when non-empty) saved as config key telemetry. The next
// Migrate sees recorded migrations, so it treats the database as existing.
func existingInstallStore(t *testing.T, telemetryValue string) *Store {
	t.Helper()
	s := newRBACStore(t)
	ctx := t.Context()
	for _, stmt := range []string{
		`DROP TABLE telemetry_state`,
		`DROP TABLE telemetry_notice_acks`,
		`DELETE FROM schema_migrations WHERE version = '015_telemetry_state.sql'`,
		`DELETE FROM config WHERE key = 'telemetry'`,
	} {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if telemetryValue != "" {
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO config(key, value, updated_at) VALUES ('telemetry', ?, ?)`,
			telemetryValue, "2026-01-01 00:00:00"); err != nil {
			t.Fatalf("seed telemetry config: %v", err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate existing install: %v", err)
	}
	return s
}

func TestMigrate_FreshInstallSeedsTelemetryDefault(t *testing.T) {
	s := newRBACStore(t)
	st, err := s.GetTelemetryState(t.Context())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st.ConsentSource != TelemetryConsentDefault {
		t.Errorf("consent source = %q, want default", st.ConsentSource)
	}
	if st.LastOutcome != "never" || st.ConsecutiveFailures != 0 {
		t.Errorf("outcome/failures = %q/%d, want never/0", st.LastOutcome, st.ConsecutiveFailures)
	}
	if st.InstallID != "" || st.NoticeShownAt != "" || st.NextDueAt != "" {
		t.Errorf("a fresh seed must leave id, notice and schedule empty, got %+v", st)
	}
	v, ok := telemetryConfig(t, s)
	if !ok || v != `{"sendMetrics":true,"extended":true}` {
		t.Errorf("config telemetry = %q (present=%v), want both tiers on", v, ok)
	}
}

// bootstrap-admin migrates before the first serve; the second Migrate must
// not turn a fresh install into a legacy one or undo an admin's choice.
func TestMigrate_TwiceKeepsFreshInstallDefault(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	st, err := s.GetTelemetryState(ctx)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st.ConsentSource != TelemetryConsentDefault {
		t.Fatalf("consent source after a second migrate = %q, want default", st.ConsentSource)
	}
	if v, _ := telemetryConfig(t, s); v != `{"sendMetrics":true,"extended":true}` {
		t.Fatalf("config after a second migrate = %q", v)
	}

	// An admin choice made between the two runs survives a later Migrate.
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE config SET value = '{"sendMetrics":false,"extended":false}' WHERE key = 'telemetry'`); err != nil {
		t.Fatalf("admin save: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("third migrate: %v", err)
	}
	if v, _ := telemetryConfig(t, s); v != `{"sendMetrics":false,"extended":false}` {
		t.Fatalf("admin choice overwritten: %q", v)
	}
}

func TestMigrate_ExistingInstallWithSendMetricsIsLegacy(t *testing.T) {
	s := existingInstallStore(t, `{"sendMetrics":true}`)
	st, err := s.GetTelemetryState(t.Context())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st.ConsentSource != TelemetryConsentLegacy {
		t.Fatalf("consent source = %q, want legacy", st.ConsentSource)
	}
	v, ok := telemetryConfig(t, s)
	if !ok {
		t.Fatal("the saved telemetry config must be kept")
	}
	var got map[string]bool
	if err := json.Unmarshal([]byte(v), &got); err != nil {
		t.Fatalf("config %q is not a JSON object of booleans: %v", v, err)
	}
	if len(got) != 2 || !got["sendMetrics"] || got["extended"] {
		t.Fatalf("config = %v, want sendMetrics kept true and extended false", got)
	}
}

func TestMigrate_ExistingInstallWithoutTelemetryRowStaysOff(t *testing.T) {
	s := existingInstallStore(t, "")
	st, err := s.GetTelemetryState(t.Context())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st.ConsentSource != TelemetryConsentLegacy {
		t.Fatalf("consent source = %q, want legacy", st.ConsentSource)
	}
	if v, ok := telemetryConfig(t, s); ok {
		t.Fatalf("no telemetry config row may be created for a legacy install, got %q", v)
	}
}

func TestMigrate_ExistingInstallLeavesUnusableConfigAlone(t *testing.T) {
	for _, raw := range []string{`{bad`, `null`, `[1]`} {
		s := existingInstallStore(t, raw)
		if v, _ := telemetryConfig(t, s); v != raw {
			t.Errorf("config %q was rewritten to %q", raw, v)
		}
		st, err := s.GetTelemetryState(t.Context())
		if err != nil || st.ConsentSource != TelemetryConsentLegacy {
			t.Errorf("config %q: state = %+v, err = %v, want a legacy row", raw, st, err)
		}
	}
}

func TestMigrate_ExistingInstallTwiceDoesNotRewriteAgain(t *testing.T) {
	s := existingInstallStore(t, `{"sendMetrics":true}`)
	ctx := t.Context()
	// An admin turns the extended tier on after the upgrade.
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE config SET value = '{"sendMetrics":true,"extended":true}' WHERE key = 'telemetry'`); err != nil {
		t.Fatalf("admin save: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	if v, _ := telemetryConfig(t, s); v != `{"sendMetrics":true,"extended":true}` {
		t.Fatalf("a later migrate rewrote the admin's config: %q", v)
	}
}

func TestTelemetryState_UpdateRoundTripAndNulls(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	want := TelemetryState{
		ConsentSource:          TelemetryConsentAdmin,
		InstallID:              "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10",
		NoticeShownAt:          "2026-10-06T09:00:00Z",
		NextDueAt:              "2026-10-07T09:05:00Z",
		LastAttemptAt:          "2026-10-06T09:05:00Z",
		LastSuccessAt:          "2026-10-06T09:05:01Z",
		LastOutcome:            "ok",
		ConsecutiveFailures:    3,
		ExtUnsupportedUntil:    "2026-10-13T09:05:00Z",
		ExtUnsupportedEndpoint: "https://telemetry.example/ingest",
		LastIDRotationAt:       "2026-10-05T00:00:00Z",
	}
	if err := s.UpdateTelemetryState(ctx, want); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.GetTelemetryState(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != want {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}

	// Empty strings are stored as NULL, not as empty text.
	cleared := TelemetryState{ConsentSource: TelemetryConsentAdmin, LastOutcome: "never"}
	if err := s.UpdateTelemetryState(ctx, cleared); err != nil {
		t.Fatalf("update cleared: %v", err)
	}
	var nulls int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM telemetry_state
		  WHERE install_id IS NULL AND notice_shown_at IS NULL AND next_due_at IS NULL
		    AND last_attempt_at IS NULL AND last_success_at IS NULL AND ext_unsupported_until IS NULL
		    AND ext_unsupported_endpoint IS NULL AND last_id_rotation_at IS NULL`).Scan(&nulls); err != nil {
		t.Fatalf("count nulls: %v", err)
	}
	if nulls != 1 {
		t.Fatal("empty fields must be stored as NULL")
	}
}

func TestTelemetryState_UpdateKeepsSigningSecret(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	secret, err := s.EnsureSigningSecret(ctx)
	if err != nil {
		t.Fatalf("ensure secret: %v", err)
	}
	if err := s.UpdateTelemetryState(ctx, TelemetryState{ConsentSource: TelemetryConsentAdmin, LastOutcome: "ok"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := s.EnsureSigningSecret(ctx)
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if string(again) != string(secret) {
		t.Fatal("UpdateTelemetryState must not change the signing secret")
	}
}

func TestClaimTelemetryDue_MovesSlotOnce(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	const due, next, now = "2026-10-06T10:00:00Z", "2026-10-07T10:03:00Z", "2026-10-06T10:00:05Z"
	if err := s.UpdateTelemetryState(ctx, TelemetryState{
		ConsentSource: TelemetryConsentAdmin, LastOutcome: "never", NextDueAt: due,
	}); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}

	if won, err := s.ClaimTelemetryDue(ctx, "2026-01-01T00:00:00Z", next, now); err != nil || won {
		t.Fatalf("claim with a stale slot = %v, %v; want false, nil", won, err)
	}
	if won, err := s.ClaimTelemetryDue(ctx, due, next, now); err != nil || !won {
		t.Fatalf("claim of the due slot = %v, %v; want true, nil", won, err)
	}
	st, err := s.GetTelemetryState(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if st.NextDueAt != next || st.LastAttemptAt != now {
		t.Fatalf("after the claim next/attempt = %q/%q, want %q/%q", st.NextDueAt, st.LastAttemptAt, next, now)
	}
	if won, err := s.ClaimTelemetryDue(ctx, due, next, now); err != nil || won {
		t.Fatalf("second claim of the same slot = %v, %v; want false, nil", won, err)
	}
}

func TestClaimTelemetryDue_NullSlotNeverMatches(t *testing.T) {
	s := newRBACStore(t)
	if won, err := s.ClaimTelemetryDue(t.Context(), "", "2026-10-07T00:00:00Z", "2026-10-06T00:00:00Z"); err != nil || won {
		t.Fatalf("claim while the gate is closed = %v, %v; want false, nil", won, err)
	}
}

func TestClaimTelemetryDue_ConcurrentCallersOneWins(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	const due = "2026-10-06T10:00:00Z"
	if err := s.UpdateTelemetryState(ctx, TelemetryState{
		ConsentSource: TelemetryConsentAdmin, LastOutcome: "never", NextDueAt: due,
	}); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}

	const callers = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
		errs []error
	)
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			won, err := s.ClaimTelemetryDue(ctx, due, "2026-10-07T10:00:00Z", "2026-10-06T10:00:01Z")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			}
			if won {
				wins++
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("claim errors: %v", errs)
	}
	if wins != 1 {
		t.Fatalf("%d concurrent claims won, want exactly 1", wins)
	}
}

func TestInstallID_SetAndClear(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	const id = "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10"
	if err := s.SetInstallID(ctx, id); err != nil {
		t.Fatalf("set: %v", err)
	}
	if st, _ := s.GetTelemetryState(ctx); st.InstallID != id {
		t.Fatalf("install id = %q, want %q", st.InstallID, id)
	}
	if err := s.ClearInstallID(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if st, _ := s.GetTelemetryState(ctx); st.InstallID != "" {
		t.Fatalf("install id after clear = %q, want empty", st.InstallID)
	}
}

func TestEnsureSigningSecret_CreatedOnceAndStable(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	first, err := s.EnsureSigningSecret(ctx)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("secret length = %d, want 32", len(first))
	}
	second, err := s.EnsureSigningSecret(ctx)
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("the secret changed between calls")
	}
	// Stored as base64 text, and the same value after ID changes.
	var enc string
	if err := s.DB.QueryRowContext(ctx, `SELECT signing_secret FROM telemetry_state`).Scan(&enc); err != nil {
		t.Fatalf("read column: %v", err)
	}
	if raw, err := base64.StdEncoding.DecodeString(enc); err != nil || string(raw) != string(first) {
		t.Fatalf("stored secret %q does not decode to the returned secret (err=%v)", enc, err)
	}
	if err := s.SetInstallID(ctx, "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10"); err != nil {
		t.Fatalf("set id: %v", err)
	}
	if err := s.ClearInstallID(ctx); err != nil {
		t.Fatalf("clear id: %v", err)
	}
	third, err := s.EnsureSigningSecret(ctx)
	if err != nil || string(third) != string(first) {
		t.Fatalf("the secret must survive ID changes, got err=%v", err)
	}
}

func TestEnsureSigningSecret_ConcurrentFirstCallsAgree(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	const callers = 6
	results := make([][]byte, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			secret, err := s.EnsureSigningSecret(ctx)
			if err != nil {
				t.Errorf("ensure: %v", err)
				return
			}
			results[i] = secret
		}()
	}
	wg.Wait()
	for i := 1; i < callers; i++ {
		if string(results[i]) != string(results[0]) {
			t.Fatalf("caller %d got a different secret", i)
		}
	}
}

func TestEnsureSigningSecret_Errors(t *testing.T) {
	ctx := t.Context()

	s := newRBACStore(t)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM telemetry_state`); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	if _, err := s.EnsureSigningSecret(ctx); err == nil {
		t.Error("want an error when there is no telemetry_state row to hold the secret")
	}

	s = newRBACStore(t)
	if _, err := s.DB.ExecContext(ctx, `UPDATE telemetry_state SET signing_secret = '!!not base64!!'`); err != nil {
		t.Fatalf("corrupt secret: %v", err)
	}
	if _, err := s.EnsureSigningSecret(ctx); err == nil {
		t.Error("want an error for a stored secret that is not base64")
	}
}

func TestMarkNoticeShown_SetsOnce(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	if err := s.MarkNoticeShown(ctx, "2026-10-06T09:00:00Z"); err != nil {
		t.Fatalf("first mark: %v", err)
	}
	if err := s.MarkNoticeShown(ctx, "2026-10-06T12:00:00Z"); err != nil {
		t.Fatalf("second mark: %v", err)
	}
	st, err := s.GetTelemetryState(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if st.NoticeShownAt != "2026-10-06T09:00:00Z" {
		t.Fatalf("notice_shown_at = %q, want the first timestamp", st.NoticeShownAt)
	}
}

func TestNoticeAck_Lifecycle(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	admin := insertUser(t, s, "notice-admin", "admin")
	other := insertUser(t, s, "notice-other", "admin")

	if has, err := s.HasNoticeAck(ctx, admin); err != nil || has {
		t.Fatalf("before any ack: has=%v err=%v", has, err)
	}
	if err := s.InsertNoticeAck(ctx, admin, "keep", "2026-10-06T09:00:00Z"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if has, err := s.HasNoticeAck(ctx, admin); err != nil || !has {
		t.Fatalf("after ack: has=%v err=%v", has, err)
	}
	if has, _ := s.HasNoticeAck(ctx, other); has {
		t.Fatal("an ack must belong to one user only")
	}

	// The first dismissal stands.
	if err := s.InsertNoticeAck(ctx, admin, "all-off", "2026-10-06T10:00:00Z"); err != nil {
		t.Fatalf("repeat insert: %v", err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_notice_acks WHERE action = 'keep'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("the first action must be kept: count=%d err=%v", count, err)
	}

	if err := s.DeleteNoticeAcksForUser(ctx, nil, admin); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if has, _ := s.HasNoticeAck(ctx, admin); has {
		t.Fatal("ack still present after DeleteNoticeAcksForUser")
	}
}

func TestDeleteUser_RemovesNoticeAck(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	gone := insertUser(t, s, "ack-removed", "admin")
	kept := insertUser(t, s, "ack-kept", "admin")
	for _, id := range []int64{gone, kept} {
		if err := s.InsertNoticeAck(ctx, id, "keep", "2026-10-06T09:00:00Z"); err != nil {
			t.Fatalf("insert ack: %v", err)
		}
	}
	if err := s.DeleteUser(ctx, gone); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if has, _ := s.HasNoticeAck(ctx, gone); has {
		t.Error("the removed user's ack must be deleted with the account")
	}
	if has, _ := s.HasNoticeAck(ctx, kept); !has {
		t.Error("another user's ack must survive")
	}
}

func TestTelemetryState_MissingRowIsAnError(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM telemetry_state`); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	if _, err := s.GetTelemetryState(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get on a missing row = %v, want sql.ErrNoRows", err)
	}
	if err := s.UpdateTelemetryState(ctx, TelemetryState{ConsentSource: "admin", LastOutcome: "never"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Update on a missing row = %v, want sql.ErrNoRows", err)
	}
	if err := s.SetInstallID(ctx, "x"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("SetInstallID on a missing row = %v, want sql.ErrNoRows", err)
	}
	if err := s.ClearInstallID(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ClearInstallID on a missing row = %v, want sql.ErrNoRows", err)
	}
}

func TestTelemetryHelpers_ClosedDatabaseErrors(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	checks := map[string]func() error{
		"migrate":    func() error { return s.Migrate(ctx) },
		"seed":       func() error { return s.seedTelemetryState(ctx, true) },
		"get":        func() error { _, err := s.GetTelemetryState(ctx); return err },
		"update":     func() error { return s.UpdateTelemetryState(ctx, TelemetryState{}) },
		"claim":      func() error { _, err := s.ClaimTelemetryDue(ctx, "a", "b", "c"); return err },
		"setID":      func() error { return s.SetInstallID(ctx, "x") },
		"clearID":    func() error { return s.ClearInstallID(ctx) },
		"secret":     func() error { _, err := s.EnsureSigningSecret(ctx); return err },
		"markNotice": func() error { return s.MarkNoticeShown(ctx, "t") },
		"insertAck":  func() error { return s.InsertNoticeAck(ctx, 1, "keep", "t") },
		"hasAck":     func() error { _, err := s.HasNoticeAck(ctx, 1); return err },
		"deleteAcks": func() error { return s.DeleteNoticeAcksForUser(ctx, nil, 1) },
		"deleteUser": func() error { return s.DeleteUser(ctx, 1) },
	}
	for name, fn := range checks {
		if err := fn(); err == nil {
			t.Errorf("%s: want an error from a closed database", name)
		}
	}
}

func TestWithExtendedOff(t *testing.T) {
	got, changed := withExtendedOff(`{"sendMetrics":true,"extra":1}`)
	if !changed || got != `{"extended":false,"extra":1,"sendMetrics":true}` {
		t.Errorf("object: got %q changed=%v", got, changed)
	}
	for _, raw := range []string{``, `{bad`, `null`, `"str"`} {
		if out, changed := withExtendedOff(raw); changed || out != raw {
			t.Errorf("%q: got %q changed=%v, want it left alone", raw, out, changed)
		}
	}
}
