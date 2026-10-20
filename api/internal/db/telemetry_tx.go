package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
)

// This file holds the telemetry_state helpers that need an open transaction
// (consent changes must commit with the config row, and a SQLite store has a
// single connection, so a second query on the store would deadlock) and the
// targeted schedule and result writes the reporter uses. Targeted writes
// change only the columns they name, so a reporter tick can't overwrite a
// concurrent consent change with a stale copy of the whole row.

// TelemetryResult is the outcome of one report attempt.
type TelemetryResult struct {
	// Outcome is "ok" or "failed".
	Outcome string
	// SuccessAt (RFC 3339 UTC) becomes last_success_at; empty keeps the old value.
	SuccessAt string
	// Failures becomes consecutive_failures.
	Failures int
	// ExpectedNext is the next_due_at value this attempt claimed. NextDue
	// replaces next_due_at only while the column still holds ExpectedNext, so
	// a gate that closed during the send (NULL) stays closed.
	ExpectedNext string
	NextDue      string
}

// UpsertConfigTx writes one config row inside tx.
func UpsertConfigTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	return upsertConfigTx(ctx, tx, key, value)
}

// GetTelemetryStateTx is GetTelemetryState inside tx.
func GetTelemetryStateTx(ctx context.Context, tx *sql.Tx) (TelemetryState, error) {
	var st TelemetryState
	err := tx.QueryRowContext(ctx,
		`SELECT consent_source, COALESCE(install_id, ''), COALESCE(notice_shown_at, ''),
		        COALESCE(next_due_at, ''), COALESCE(last_attempt_at, ''), COALESCE(last_success_at, ''),
		        last_outcome, consecutive_failures, COALESCE(ext_unsupported_until, ''),
		        COALESCE(ext_unsupported_endpoint, ''), COALESCE(last_id_rotation_at, '')
		   FROM telemetry_state WHERE id = ?`, telemetrySingletonID).Scan(
		&st.ConsentSource, &st.InstallID, &st.NoticeShownAt,
		&st.NextDueAt, &st.LastAttemptAt, &st.LastSuccessAt,
		&st.LastOutcome, &st.ConsecutiveFailures, &st.ExtUnsupportedUntil,
		&st.ExtUnsupportedEndpoint, &st.LastIDRotationAt)
	if err != nil {
		return TelemetryState{}, fmt.Errorf("get telemetry state: %w", err)
	}
	return st, nil
}

// UpdateTelemetryConsentTx sets consent_source, install_id and next_due_at
// inside tx. Empty installID and nextDueAt store NULL.
func UpdateTelemetryConsentTx(ctx context.Context, tx *sql.Tx, source, installID, nextDueAt string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE telemetry_state
		    SET consent_source = ?, install_id = NULLIF(?, ''), next_due_at = NULLIF(?, '')
		  WHERE id = ?`,
		source, installID, nextDueAt, telemetrySingletonID)
	if err != nil {
		return fmt.Errorf("update telemetry consent: %w", err)
	}
	return requireOneRow(res, "update telemetry consent")
}

// EnsureSigningSecretTx creates the install's signing secret inside tx when
// none exists yet. It never reads the secret back.
func EnsureSigningSecretTx(ctx context.Context, tx *sql.Tx) error {
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return fmt.Errorf("generate signing secret: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE telemetry_state SET signing_secret = ? WHERE id = ? AND signing_secret IS NULL`,
		base64.StdEncoding.EncodeToString(fresh), telemetrySingletonID); err != nil {
		return fmt.Errorf("store signing secret: %w", err)
	}
	return nil
}

// MarkNoticeShownTx is MarkNoticeShown inside tx.
func MarkNoticeShownTx(ctx context.Context, tx *sql.Tx, now string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE telemetry_state SET notice_shown_at = ? WHERE id = ? AND notice_shown_at IS NULL`,
		now, telemetrySingletonID); err != nil {
		return fmt.Errorf("mark notice shown: %w", err)
	}
	return nil
}

// InsertNoticeAckTx is InsertNoticeAck inside tx.
func InsertNoticeAckTx(ctx context.Context, tx *sql.Tx, userID int64, action, ackedAt string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO telemetry_notice_acks(user_id, acked_at, action) VALUES (?, ?, ?)
		 ON CONFLICT (user_id) DO NOTHING`,
		userID, ackedAt, action); err != nil {
		return fmt.Errorf("insert notice ack: %w", err)
	}
	return nil
}

// OpenTelemetrySchedule sets next_due_at to next (RFC 3339 UTC) only while it
// is NULL, and reports whether it changed the row.
func (s *Store) OpenTelemetrySchedule(ctx context.Context, next string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET next_due_at = ? WHERE id = ? AND next_due_at IS NULL`,
		next, telemetrySingletonID)
	if err != nil {
		return false, fmt.Errorf("open telemetry schedule: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("open telemetry schedule: rows affected: %w", err)
	}
	return n == 1, nil
}

// CloseTelemetrySchedule sets next_due_at to NULL (the gate closed).
func (s *Store) CloseTelemetrySchedule(ctx context.Context) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET next_due_at = NULL WHERE id = ?`, telemetrySingletonID)
	if err != nil {
		return fmt.Errorf("close telemetry schedule: %w", err)
	}
	return requireOneRow(res, "close telemetry schedule")
}

// RecordTelemetryResult stores the outcome of one attempt (see TelemetryResult).
func (s *Store) RecordTelemetryResult(ctx context.Context, r TelemetryResult) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET
		     last_outcome = ?, consecutive_failures = ?,
		     last_success_at = COALESCE(NULLIF(?, ''), last_success_at),
		     next_due_at = CASE WHEN next_due_at = ? THEN ? ELSE next_due_at END
		 WHERE id = ?`,
		r.Outcome, r.Failures, r.SuccessAt, r.ExpectedNext, r.NextDue, telemetrySingletonID)
	if err != nil {
		return fmt.Errorf("record telemetry result: %w", err)
	}
	return requireOneRow(res, "record telemetry result")
}

// SetExtUnsupported records (or, with empty arguments, clears) the marker that
// the endpoint rejected the extended part: until is RFC 3339 UTC.
func (s *Store) SetExtUnsupported(ctx context.Context, until, endpoint string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state
		    SET ext_unsupported_until = NULLIF(?, ''), ext_unsupported_endpoint = NULLIF(?, '')
		  WHERE id = ?`,
		until, endpoint, telemetrySingletonID)
	if err != nil {
		return fmt.Errorf("set ext unsupported: %w", err)
	}
	return requireOneRow(res, "set ext unsupported")
}

// RotateInstallID replaces the install ID with id and stamps
// last_id_rotation_at with at (RFC 3339 UTC), after the receiver reported the
// old ID as claimed by another key (FR-037). The signing secret is untouched.
// It changes nothing, and wraps sql.ErrNoRows, when there is no install ID to
// replace, for example because the extended tier was turned off meanwhile.
func (s *Store) RotateInstallID(ctx context.Context, id, at string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET install_id = ?, last_id_rotation_at = ?
		  WHERE id = ? AND install_id IS NOT NULL`,
		id, at, telemetrySingletonID)
	if err != nil {
		return fmt.Errorf("rotate install id: %w", err)
	}
	return requireOneRow(res, "rotate install id")
}
