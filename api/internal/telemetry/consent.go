package telemetry

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// ErrOperatorDisabled is returned by the consent writers when the operator
// disabled telemetry at install time (destination kind "disabled"): the
// admin toggles are refused, whatever they say (FR-005).
var ErrOperatorDisabled = errors.New("telemetry is disabled by the operator")

// firstReportWindow caps the random delay before the first report once the
// gate opens (research R12).
const firstReportWindow = 15 * time.Minute

// configKey is the config row that holds the admin's choice.
const configKey = "telemetry"

// Consent is the admin's stored choice, config key "telemetry".
type Consent struct {
	// Basic is sendMetrics.
	Basic bool
	// Extended is true only while Basic is true.
	Extended bool
}

// storedConsent is Consent as stored in the config row.
type storedConsent struct {
	SendMetrics bool `json:"sendMetrics"`
	Extended    bool `json:"extended"`
}

// parseConsent decodes the raw config value. An absent, empty or malformed
// value means both tiers off, and Extended is forced off while Basic is.
func parseConsent(raw string, present bool) Consent {
	if !present {
		return Consent{}
	}
	var c storedConsent
	if json.Unmarshal([]byte(raw), &c) != nil {
		return Consent{}
	}
	return Consent{Basic: c.SendMetrics, Extended: c.SendMetrics && c.Extended}
}

// ReadConsent reads the stored choice through the store.
func ReadConsent(ctx context.Context, store *db.Store) (Consent, error) {
	raw, ok, err := store.ConfigValue(ctx, configKey)
	if err != nil {
		return Consent{}, fmt.Errorf("read telemetry consent: %w", err)
	}
	return parseConsent(raw, ok), nil
}

// ReadConsentTx reads the stored choice inside tx.
func ReadConsentTx(ctx context.Context, tx *sql.Tx) (Consent, error) {
	raw, ok, err := db.ConfigValueTx(ctx, tx, configKey)
	if err != nil {
		return Consent{}, fmt.Errorf("read telemetry consent: %w", err)
	}
	return parseConsent(raw, ok), nil
}

// destActive reports whether the destination can receive reports at all.
func destActive(d Destination) bool {
	return d.Kind != KindDisabled && d.Kind != KindNone && d.URL != ""
}

// gateOpen is the report gate (data-model "Gate open"): the basic tier is
// effectively on, and either an admin made a choice or the notice was shown.
func gateOpen(basicOn bool, st db.TelemetryState) bool {
	return basicOn && (st.ConsentSource != db.TelemetryConsentDefault || st.NoticeShownAt != "")
}

// stamp formats t as the RFC 3339 UTC text telemetry_state stores.
func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// randN returns a uniform value in [0, n); n <= 0 gives 0. It reads
// crypto/rand, which keeps the jitter unpredictable across replicas.
func randN(n int64) int64 {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return 0
	}
	return v.Int64()
}

// firstDue is when the first report is due once the gate opens:
// now + U(0, min(15m, interval/4)).
func firstDue(now time.Time, interval time.Duration, rnd func(int64) int64) time.Time {
	window := min(firstReportWindow, interval/4)
	return now.Add(time.Duration(rnd(int64(window))))
}

// NewInstallID returns a random lowercase UUIDv4. It is not derived from any
// cluster, host, network or user attribute (FR-012).
func NewInstallID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate install id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

// ApplyConsent records an admin choice inside tx. It is shared by the config
// hook (source "admin") and the notice actions. In one transaction it:
//   - writes config key telemetry (extended is forced off while basic is off)
//   - sets consent_source to source
//   - creates the install ID (and the signing secret) when extended is on and
//     there is none, or clears the ID when extended is off
//   - opens the schedule when the gate is open (keeping an existing slot), or
//     closes it
//
// It returns ErrOperatorDisabled, writing nothing, when dest is disabled. A
// non-positive interval means 24h.
func ApplyConsent(ctx context.Context, tx *sql.Tx, dest Destination, interval time.Duration, basic, extended bool, source string) error {
	if dest.Kind == KindDisabled {
		return ErrOperatorDisabled
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	extended = extended && basic
	raw, err := json.Marshal(storedConsent{SendMetrics: basic, Extended: extended})
	if err != nil {
		return fmt.Errorf("apply consent: %w", err)
	}
	if err := db.UpsertConfigTx(ctx, tx, configKey, string(raw)); err != nil {
		return fmt.Errorf("apply consent: %w", err)
	}
	st, err := db.GetTelemetryStateTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("apply consent: %w", err)
	}
	installID := st.InstallID
	if extended {
		if installID == "" {
			if installID, err = NewInstallID(); err != nil {
				return fmt.Errorf("apply consent: %w", err)
			}
		}
		if err := db.EnsureSigningSecretTx(ctx, tx); err != nil {
			return fmt.Errorf("apply consent: %w", err)
		}
	} else {
		installID = ""
	}
	next := ""
	if gateOpen(destActive(dest) && basic, db.TelemetryState{ConsentSource: source, NoticeShownAt: st.NoticeShownAt}) {
		next = st.NextDueAt
		if next == "" {
			next = stamp(firstDue(time.Now(), interval, randN))
		}
	}
	if err := db.UpdateTelemetryConsentTx(ctx, tx, source, installID, next); err != nil {
		return fmt.Errorf("apply consent: %w", err)
	}
	return nil
}

// MarkNoticeSeen records, inside tx, that the notice was rendered: it sets
// notice_shown_at once, creates the install ID (and secret) when the stored
// choice has extended on and there is none, and opens the schedule when the
// gate is open and no slot is set. consent_source is unchanged. A non-positive
// interval means 24h.
func MarkNoticeSeen(ctx context.Context, tx *sql.Tx, dest Destination, interval time.Duration) error {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	st, err := db.GetTelemetryStateTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("mark notice seen: %w", err)
	}
	now := time.Now()
	if st.NoticeShownAt == "" {
		st.NoticeShownAt = stamp(now)
		if err := db.MarkNoticeShownTx(ctx, tx, st.NoticeShownAt); err != nil {
			return fmt.Errorf("mark notice seen: %w", err)
		}
	}
	c, err := ReadConsentTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("mark notice seen: %w", err)
	}
	installID := st.InstallID
	if c.Extended && installID == "" {
		if installID, err = NewInstallID(); err != nil {
			return fmt.Errorf("mark notice seen: %w", err)
		}
		if err := db.EnsureSigningSecretTx(ctx, tx); err != nil {
			return fmt.Errorf("mark notice seen: %w", err)
		}
	}
	next := st.NextDueAt
	if next == "" && gateOpen(destActive(dest) && c.Basic, st) {
		next = stamp(firstDue(now, interval, randN))
	}
	if next == st.NextDueAt && installID == st.InstallID {
		return nil
	}
	if err := db.UpdateTelemetryConsentTx(ctx, tx, st.ConsentSource, installID, next); err != nil {
		return fmt.Errorf("mark notice seen: %w", err)
	}
	return nil
}
