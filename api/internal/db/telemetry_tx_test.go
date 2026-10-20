package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// txTestInstallID is a lowercase UUIDv4 for the install ID column.
const txTestInstallID = "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10"

// inTestTx runs fn in a transaction on s and commits it.
func inTestTx(t *testing.T, s *Store, fn func(ctx context.Context, tx *sql.Tx)) {
	t.Helper()
	ctx := t.Context()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	fn(ctx, tx)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestTelemetryTx_ConsentRoundTrip(t *testing.T) {
	s := newRBACStore(t)
	inTestTx(t, s, func(ctx context.Context, tx *sql.Tx) {
		if err := UpsertConfigTx(ctx, tx, "telemetry", `{"sendMetrics":true,"extended":false}`); err != nil {
			t.Fatalf("upsert config: %v", err)
		}
		if err := EnsureSigningSecretTx(ctx, tx); err != nil {
			t.Fatalf("ensure secret: %v", err)
		}
		if err := MarkNoticeShownTx(ctx, tx, "2026-10-06T09:00:00Z"); err != nil {
			t.Fatalf("mark shown: %v", err)
		}
		if err := MarkNoticeShownTx(ctx, tx, "2026-10-06T10:00:00Z"); err != nil {
			t.Fatalf("mark shown again: %v", err)
		}
		if err := UpdateTelemetryConsentTx(ctx, tx, TelemetryConsentAdmin, txTestInstallID, "2026-10-06T09:05:00Z"); err != nil {
			t.Fatalf("update consent: %v", err)
		}
		st, err := GetTelemetryStateTx(ctx, tx)
		if err != nil {
			t.Fatalf("get in tx: %v", err)
		}
		if st.ConsentSource != TelemetryConsentAdmin || st.InstallID != txTestInstallID ||
			st.NextDueAt != "2026-10-06T09:05:00Z" || st.NoticeShownAt != "2026-10-06T09:00:00Z" {
			t.Fatalf("state in tx = %+v", st)
		}
		if err := InsertNoticeAckTx(ctx, tx, 7, "keep", "2026-10-06T09:01:00Z"); err != nil {
			t.Fatalf("ack: %v", err)
		}
		if err := InsertNoticeAckTx(ctx, tx, 7, "all-off", "2026-10-06T09:02:00Z"); err != nil {
			t.Fatalf("second ack: %v", err)
		}
	})
	ctx := t.Context()
	if v, ok := telemetryConfig(t, s); !ok || v != `{"sendMetrics":true,"extended":false}` {
		t.Fatalf("config = %q, %v", v, ok)
	}
	if secret, err := s.EnsureSigningSecret(ctx); err != nil || len(secret) != 32 {
		t.Fatalf("secret = %d bytes, %v; want 32, nil", len(secret), err)
	}
	if ok, err := s.HasNoticeAck(ctx, 7); err != nil || !ok {
		t.Fatalf("HasNoticeAck = %v, %v; want true, nil", ok, err)
	}
	var action string
	if err := s.DB.QueryRowContext(ctx, `SELECT action FROM telemetry_notice_acks WHERE user_id = 7`).Scan(&action); err != nil || action != "keep" {
		t.Fatalf("ack action = %q, %v; want the first ack (keep)", action, err)
	}

	// Clearing the install ID and the slot stores NULL.
	inTestTx(t, s, func(ctx context.Context, tx *sql.Tx) {
		if err := UpdateTelemetryConsentTx(ctx, tx, TelemetryConsentAdmin, "", ""); err != nil {
			t.Fatalf("clear: %v", err)
		}
	})
	st, err := s.GetTelemetryState(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if st.InstallID != "" || st.NextDueAt != "" {
		t.Fatalf("after clearing, install id/slot = %q/%q, want empty", st.InstallID, st.NextDueAt)
	}
}

func TestTelemetrySchedule_OpenAndClose(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	if ok, err := s.OpenTelemetrySchedule(ctx, "2026-10-06T10:00:00Z"); err != nil || !ok {
		t.Fatalf("open on a NULL slot = %v, %v; want true, nil", ok, err)
	}
	if ok, err := s.OpenTelemetrySchedule(ctx, "2026-10-06T11:00:00Z"); err != nil || ok {
		t.Fatalf("open on a set slot = %v, %v; want false, nil", ok, err)
	}
	st, err := s.GetTelemetryState(ctx)
	if err != nil || st.NextDueAt != "2026-10-06T10:00:00Z" {
		t.Fatalf("slot = %q, %v; want the first value kept", st.NextDueAt, err)
	}
	if err := s.CloseTelemetrySchedule(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if st, _ = s.GetTelemetryState(ctx); st.NextDueAt != "" {
		t.Fatalf("slot after close = %q, want empty", st.NextDueAt)
	}
}

func TestRecordTelemetryResult(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	const claimed = "2026-10-07T10:03:00Z"
	if err := s.UpdateTelemetryState(ctx, TelemetryState{
		ConsentSource: TelemetryConsentAdmin, LastOutcome: "never", NextDueAt: claimed,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Success moves the slot while it still holds the claimed value.
	if err := s.RecordTelemetryResult(ctx, TelemetryResult{
		Outcome: "ok", SuccessAt: "2026-10-06T10:00:09Z", ExpectedNext: claimed, NextDue: "2026-10-07T10:09:00Z",
	}); err != nil {
		t.Fatalf("record ok: %v", err)
	}
	st, _ := s.GetTelemetryState(ctx)
	if st.LastOutcome != "ok" || st.LastSuccessAt != "2026-10-06T10:00:09Z" ||
		st.NextDueAt != "2026-10-07T10:09:00Z" || st.ConsecutiveFailures != 0 {
		t.Fatalf("after success: %+v", st)
	}

	// A failure keeps last_success_at (empty SuccessAt) and counts up.
	if err := s.RecordTelemetryResult(ctx, TelemetryResult{
		Outcome: "failed", Failures: 2, ExpectedNext: "2026-10-07T10:09:00Z", NextDue: "2026-10-06T12:00:00Z",
	}); err != nil {
		t.Fatalf("record failed: %v", err)
	}
	st, _ = s.GetTelemetryState(ctx)
	if st.LastOutcome != "failed" || st.ConsecutiveFailures != 2 ||
		st.LastSuccessAt != "2026-10-06T10:00:09Z" || st.NextDueAt != "2026-10-06T12:00:00Z" {
		t.Fatalf("after failure: %+v", st)
	}

	// A slot that changed since the claim (the gate closed, or reopened) is
	// left alone, while the outcome is still recorded.
	if err := s.CloseTelemetrySchedule(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := s.RecordTelemetryResult(ctx, TelemetryResult{
		Outcome: "ok", SuccessAt: "2026-10-06T13:00:00Z", ExpectedNext: "2026-10-06T12:00:00Z", NextDue: "2026-10-07T13:00:00Z",
	}); err != nil {
		t.Fatalf("record after close: %v", err)
	}
	st, _ = s.GetTelemetryState(ctx)
	if st.NextDueAt != "" || st.LastOutcome != "ok" {
		t.Fatalf("after a closed gate: slot %q outcome %q, want empty and ok", st.NextDueAt, st.LastOutcome)
	}
}

func TestSetExtUnsupported_SetAndClear(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	if err := s.SetExtUnsupported(ctx, "2026-10-13T10:00:00Z", "https://old.example/ingest"); err != nil {
		t.Fatalf("set: %v", err)
	}
	st, _ := s.GetTelemetryState(ctx)
	if st.ExtUnsupportedUntil != "2026-10-13T10:00:00Z" || st.ExtUnsupportedEndpoint != "https://old.example/ingest" {
		t.Fatalf("marker = %q/%q", st.ExtUnsupportedUntil, st.ExtUnsupportedEndpoint)
	}
	if err := s.SetExtUnsupported(ctx, "", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	st, _ = s.GetTelemetryState(ctx)
	if st.ExtUnsupportedUntil != "" || st.ExtUnsupportedEndpoint != "" {
		t.Fatalf("marker after clear = %q/%q, want empty", st.ExtUnsupportedUntil, st.ExtUnsupportedEndpoint)
	}
}

func TestTelemetryTx_MissingRowAndClosedDatabase(t *testing.T) {
	s := newRBACStore(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM telemetry_state`); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	inTestTx(t, s, func(ctx context.Context, tx *sql.Tx) {
		if _, err := GetTelemetryStateTx(ctx, tx); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetTelemetryStateTx on a missing row = %v, want sql.ErrNoRows", err)
		}
		if err := UpdateTelemetryConsentTx(ctx, tx, "admin", "", ""); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("UpdateTelemetryConsentTx on a missing row = %v, want sql.ErrNoRows", err)
		}
	})
	for name, fn := range map[string]func() error{
		"close":  func() error { return s.CloseTelemetrySchedule(ctx) },
		"record": func() error { return s.RecordTelemetryResult(ctx, TelemetryResult{Outcome: "ok"}) },
		"marker": func() error { return s.SetExtUnsupported(ctx, "", "") },
	} {
		if err := fn(); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("%s on a missing row = %v, want sql.ErrNoRows", name, err)
		}
	}

	closed := newRBACStore(t)
	if err := closed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := closed.OpenTelemetrySchedule(ctx, "x"); err == nil {
		t.Error("OpenTelemetrySchedule: want an error from a closed database")
	}
	if err := closed.CloseTelemetrySchedule(ctx); err == nil {
		t.Error("CloseTelemetrySchedule: want an error from a closed database")
	}
	if err := closed.RecordTelemetryResult(ctx, TelemetryResult{}); err == nil {
		t.Error("RecordTelemetryResult: want an error from a closed database")
	}
	if err := closed.SetExtUnsupported(ctx, "", ""); err == nil {
		t.Error("SetExtUnsupported: want an error from a closed database")
	}
}
