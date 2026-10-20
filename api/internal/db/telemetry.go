package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// telemetrySingletonID is the primary key of the one telemetry_state row.
const telemetrySingletonID = "singleton"

// Consent sources recorded in telemetry_state.consent_source.
const (
	// TelemetryConsentDefault marks a fresh install seeded with the project
	// default (basic and extended on, reports gated on the notice).
	TelemetryConsentDefault = "default"
	// TelemetryConsentLegacy marks an install that existed before the
	// extended tier; its saved choice is kept and extended stays off.
	TelemetryConsentLegacy = "legacy"
	// TelemetryConsentAdmin marks an install whose admin made an explicit
	// choice.
	TelemetryConsentAdmin = "admin"
)

// TelemetryState is the persisted telemetry machine state. Timestamp fields
// hold RFC 3339 UTC text, and the empty string stands for SQL NULL. The
// signing secret is deliberately not a field: it is reachable only through
// EnsureSigningSecret, so no handler can serialise it by accident.
type TelemetryState struct {
	ConsentSource          string
	InstallID              string
	NoticeShownAt          string
	NextDueAt              string
	LastAttemptAt          string
	LastSuccessAt          string
	LastOutcome            string
	ConsecutiveFailures    int
	ExtUnsupportedUntil    string
	ExtUnsupportedEndpoint string
	LastIDRotationAt       string
}

// seedTelemetryState inserts the singleton telemetry_state row once and, only
// when this call inserted it, applies the consent seeding. fresh reports that
// schema_migrations held no rows when Migrate started. Running it again, for
// example bootstrap-admin followed by serve, finds the row and changes
// nothing, so a fresh install never flips to legacy.
func (s *Store) seedTelemetryState(ctx context.Context, fresh bool) error {
	source := TelemetryConsentLegacy
	if fresh {
		source = TelemetryConsentDefault
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("seed telemetry state: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO telemetry_state(id, consent_source) VALUES (?, ?) ON CONFLICT (id) DO NOTHING`,
		telemetrySingletonID, source)
	if err != nil {
		return fmt.Errorf("seed telemetry state: insert: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("seed telemetry state: rows affected: %w", err)
	}
	if inserted == 0 {
		return nil
	}

	if fresh {
		if err := upsertConfigTx(ctx, tx, "telemetry", `{"sendMetrics":true,"extended":true}`); err != nil {
			return fmt.Errorf("seed telemetry state: %w", err)
		}
	} else {
		raw, ok, err := ConfigValueTx(ctx, tx, "telemetry")
		if err != nil {
			return fmt.Errorf("seed telemetry state: read config: %w", err)
		}
		if ok {
			if rewritten, changed := withExtendedOff(raw); changed {
				if err := upsertConfigTx(ctx, tx, "telemetry", rewritten); err != nil {
					return fmt.Errorf("seed telemetry state: %w", err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("seed telemetry state: commit: %w", err)
	}
	return nil
}

// withExtendedOff returns raw, a JSON object, with "extended":false added.
// A value that is not a JSON object is left alone (changed is false) rather
// than overwritten.
func withExtendedOff(raw string) (string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return raw, false
	}
	m["extended"] = json.RawMessage("false")
	out, err := json.Marshal(m)
	if err != nil {
		return raw, false
	}
	return string(out), true
}

// upsertConfigTx writes one config row inside tx, stamping updated_at in the
// layout the other config writers use.
func upsertConfigTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO config(key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, NowTimestamp()); err != nil {
		return fmt.Errorf("write config %q: %w", key, err)
	}
	return nil
}

// GetTelemetryState reads the singleton row. NULL columns come back as the
// empty string. A missing row (Migrate not run) wraps sql.ErrNoRows.
func (s *Store) GetTelemetryState(ctx context.Context) (TelemetryState, error) {
	var st TelemetryState
	err := s.DB.QueryRowContext(ctx,
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

// UpdateTelemetryState writes every field of st back to the singleton row,
// storing empty strings as NULL. It never touches the signing secret.
func (s *Store) UpdateTelemetryState(ctx context.Context, st TelemetryState) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET
		     consent_source = ?, install_id = NULLIF(?, ''), notice_shown_at = NULLIF(?, ''),
		     next_due_at = NULLIF(?, ''), last_attempt_at = NULLIF(?, ''), last_success_at = NULLIF(?, ''),
		     last_outcome = ?, consecutive_failures = ?, ext_unsupported_until = NULLIF(?, ''),
		     ext_unsupported_endpoint = NULLIF(?, ''), last_id_rotation_at = NULLIF(?, '')
		 WHERE id = ?`,
		st.ConsentSource, st.InstallID, st.NoticeShownAt,
		st.NextDueAt, st.LastAttemptAt, st.LastSuccessAt,
		st.LastOutcome, st.ConsecutiveFailures, st.ExtUnsupportedUntil,
		st.ExtUnsupportedEndpoint, st.LastIDRotationAt,
		telemetrySingletonID)
	if err != nil {
		return fmt.Errorf("update telemetry state: %w", err)
	}
	return requireOneRow(res, "update telemetry state")
}

// ClaimTelemetryDue claims the report slot whose next_due_at equals expected
// by moving it to next and stamping last_attempt_at with now (all RFC 3339
// UTC text). It reports true only when this call changed the row, so of
// several replicas racing for one slot exactly one proceeds. A NULL
// next_due_at never matches, because the gate-open step sets the first slot.
func (s *Store) ClaimTelemetryDue(ctx context.Context, expected, next, now string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET next_due_at = ?, last_attempt_at = ?
		  WHERE id = ? AND next_due_at = ?`,
		next, now, telemetrySingletonID, expected)
	if err != nil {
		return false, fmt.Errorf("claim telemetry slot: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim telemetry slot: rows affected: %w", err)
	}
	return n == 1, nil
}

// SetInstallID stores the extended-tier install ID.
func (s *Store) SetInstallID(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET install_id = ? WHERE id = ?`, id, telemetrySingletonID)
	if err != nil {
		return fmt.Errorf("set install id: %w", err)
	}
	return requireOneRow(res, "set install id")
}

// ClearInstallID removes the install ID (the extended tier went off).
func (s *Store) ClearInstallID(ctx context.Context) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET install_id = NULL WHERE id = ?`, telemetrySingletonID)
	if err != nil {
		return fmt.Errorf("clear install id: %w", err)
	}
	return requireOneRow(res, "clear install id")
}

// EnsureSigningSecret returns the install's 32-byte signing secret, creating
// it on first use. Concurrent first calls agree on one value because the
// write only lands while the column is NULL and the loser re-reads. The
// secret is for api/internal/telemetry key derivation only: it is never part
// of TelemetryState, an HTTP response, a log line or an audit event.
func (s *Store) EnsureSigningSecret(ctx context.Context) ([]byte, error) {
	if secret, err := s.readSigningSecret(ctx); err != nil || secret != nil {
		return secret, err
	}
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("generate signing secret: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET signing_secret = ? WHERE id = ? AND signing_secret IS NULL`,
		base64.StdEncoding.EncodeToString(fresh), telemetrySingletonID); err != nil {
		return nil, fmt.Errorf("store signing secret: %w", err)
	}
	secret, err := s.readSigningSecret(ctx)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, errors.New("store signing secret: no telemetry_state row")
	}
	return secret, nil
}

// readSigningSecret returns the stored secret, or nil when none is set yet or
// the row is absent.
func (s *Store) readSigningSecret(ctx context.Context) ([]byte, error) {
	var enc sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT signing_secret FROM telemetry_state WHERE id = ?`, telemetrySingletonID).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read signing secret: %w", err)
	}
	if !enc.Valid || enc.String == "" {
		return nil, nil
	}
	secret, err := base64.StdEncoding.DecodeString(enc.String)
	if err != nil {
		return nil, fmt.Errorf("decode signing secret: %w", err)
	}
	return secret, nil
}

// MarkNoticeShown records when the consent notice was first rendered. The
// value is set once: later calls leave the first timestamp in place.
func (s *Store) MarkNoticeShown(ctx context.Context, now string) error {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE telemetry_state SET notice_shown_at = ? WHERE id = ? AND notice_shown_at IS NULL`,
		now, telemetrySingletonID); err != nil {
		return fmt.Errorf("mark notice shown: %w", err)
	}
	return nil
}

// InsertNoticeAck records that userID dismissed the notice with action
// (keep, extended-off or all-off) at ackedAt (RFC 3339 UTC). The first
// dismissal stands; repeats are ignored.
func (s *Store) InsertNoticeAck(ctx context.Context, userID int64, action string, ackedAt string) error {
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO telemetry_notice_acks(user_id, acked_at, action) VALUES (?, ?, ?)
		 ON CONFLICT (user_id) DO NOTHING`,
		userID, ackedAt, action); err != nil {
		return fmt.Errorf("insert notice ack: %w", err)
	}
	return nil
}

// HasNoticeAck reports whether userID has dismissed the notice.
func (s *Store) HasNoticeAck(ctx context.Context, userID int64) (bool, error) {
	var one int
	err := s.DB.QueryRowContext(ctx,
		`SELECT 1 FROM telemetry_notice_acks WHERE user_id = ?`, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("has notice ack: %w", err)
	}
	return true, nil
}

// DeleteNoticeAcksForUser removes a user's notice ack. ex is the open
// transaction of the account removal, or nil to run on the store directly.
// The telemetry_notice_acks table has no foreign key, so removal is explicit.
func (s *Store) DeleteNoticeAcksForUser(ctx context.Context, ex Execer, userID int64) error {
	if ex == nil {
		ex = s.DB
	}
	if _, err := ex.ExecContext(ctx,
		`DELETE FROM telemetry_notice_acks WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete notice acks: %w", err)
	}
	return nil
}

// requireOneRow fails when an UPDATE of the singleton matched no row, which
// means Migrate has not seeded telemetry_state.
func requireOneRow(res sql.Result, op string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: rows affected: %w", op, err)
	}
	if n != 1 {
		return fmt.Errorf("%s: %w", op, sql.ErrNoRows)
	}
	return nil
}
