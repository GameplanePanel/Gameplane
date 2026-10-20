package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// Dimension names stored in daily_dim.dim (data-model.md, Receiver).
const (
	dimK8s        = "k8s"
	dimDistro     = "distro"
	dimArch       = "arch"
	dimNodes      = "nodes"
	dimDeployment = "deployment"
	dimTunnel     = "tunnel"
	dimClusters   = "clusters"
	dimDB         = "db"
	dimLanguage   = "language"
	dimFeature    = "feature"
)

// featureNames are the boolean features stored under dimFeature, in display
// order. A daily_dim row exists only when the feature is on.
var featureNames = []string{"wakeOnConnect", "capture", "backups", "sso", "auditForwarding"}

// gameCustom is the daily_game.module of every non-catalog game server.
const gameCustom = "custom"

// Refusal reasons of the identity checks (contracts/report-schema.md,
// Signature and claim). They are the JSON error codes and the label values
// of gameplane_telemetry_refused_total.
const (
	refuseBadSignature = "bad_signature"
	refuseStale        = "stale"
	refuseIDClaimed    = "id_claimed"
	refuseReplay       = "replay"
)

// errIDClaimed and errReplay are returned by checkClaim and, through the
// write transaction, by recordExtended.
var (
	errIDClaimed = errors.New(refuseIDClaimed)
	errReplay    = errors.New(refuseReplay)
)

// refusalStatus maps a refusal reason to its HTTP status.
var refusalStatus = map[string]int{
	refuseBadSignature: http.StatusForbidden,
	refuseStale:        http.StatusForbidden,
	refuseReplay:       http.StatusForbidden,
	refuseIDClaimed:    http.StatusConflict,
}

// refuse answers a refused report with a fixed JSON body and counts it. A
// refused report changes no stored data (FR-036).
func (s *server) refuse(w http.ResponseWriter, reason string) {
	s.refused.WithLabelValues(reason).Inc()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(refusalStatus[reason])
	_, _ = w.Write([]byte(`{"error":"` + reason + `"}`))
}

// installHMAC returns HMAC-SHA256(pepper, installID), hex-encoded: the only
// form of an install ID the receiver stores (research R5).
func (s *store) installHMAC(installID string) string {
	m := hmac.New(sha256.New, s.pepper)
	m.Write([]byte(installID))
	return hex.EncodeToString(m.Sum(nil))
}

// ingestExtended handles a report that carries a valid ext part. The identity
// checks (verifyExtSignature, precheckClaim) run first, before the limiter
// counts the report and before any write.
func (s *server) ingestExtended(w http.ResponseWriter, req *http.Request, body []byte, rep telemetryschema.Report) {
	ext := rep.Ext
	if reason := s.verifyExtSignature(req.Header.Get(telemetryschema.SignatureHeader), body, ext); reason != "" {
		s.refuse(w, reason)
		return
	}
	idHMAC := s.store.installHMAC(ext.InstallID)
	if reason := s.precheckClaim(req.Context(), idHMAC, ext); reason != "" {
		s.refuse(w, reason)
		return
	}
	if !s.daily.take(sourceIP(req, s.cfg.trustedProxyCIDRs), s.now()) {
		s.rateLimited.WithLabelValues(routeIngest).Inc()
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	version := s.versionLabel(rep.Version)
	outcome, err := s.store.recordExtended(req.Context(), extRecord{
		Day: dayString(s.now()), Version: version, Servers: rep.Servers, Templates: rep.Templates,
		IDHMAC: idHMAC, Ext: ext,
	})
	switch {
	case errors.Is(err, errIDClaimed):
		s.refuse(w, refuseIDClaimed)
		return
	case errors.Is(err, errReplay):
		s.refuse(w, refuseReplay)
		return
	case err != nil:
		slog.Error("telemetry report not stored", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if outcome == extDuplicate {
		s.duplicates.Inc()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	slog.Info("telemetry report",
		"version", version, "servers", rep.Servers, "templates", rep.Templates, "extended", true)
	s.reports.WithLabelValues(version).Inc()
	s.servers.Observe(float64(rep.Servers))
	s.templates.Observe(float64(rep.Templates))
	s.extReports.Inc()
	w.WriteHeader(http.StatusNoContent)
}

// extRecord is one extended report that passed the identity checks.
type extRecord struct {
	Day       string
	Version   string // a bounded label from versionLabel
	Servers   int
	Templates int
	IDHMAC    string
	Ext       *telemetryschema.Extended
}

// extOutcome is how recordExtended counted a report.
type extOutcome int

const (
	// extNew: the first report of an install ID (new_installs++).
	extNew extOutcome = iota
	// extReturning: a known install's first report of the UTC day.
	extReturning
	// extDuplicate: a repeat within the UTC day; only duplicates changed.
	extDuplicate
)

// activityRow is one stored activity record, as far as the checks need it.
type activityRow struct {
	KeyFP       string
	LastSeen    string
	LastSentAt  string // RFC 3339 UTC
	LastVersion string // the last reported version label
}

// rowQuerier is satisfied by *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// lookupActivity reads the activity record of idHMAC.
func lookupActivity(ctx context.Context, q rowQuerier, idHMAC string) (activityRow, bool, error) {
	var r activityRow
	err := q.QueryRowContext(ctx,
		`SELECT key_fp, last_seen, last_sent_at, last_version FROM activity WHERE id_hmac = ?`, idHMAC).
		Scan(&r.KeyFP, &r.LastSeen, &r.LastSentAt, &r.LastVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return activityRow{}, false, nil
	}
	if err != nil {
		return activityRow{}, false, fmt.Errorf("read activity: %w", err)
	}
	return r, true, nil
}

// recordExtended counts one extended report in a single transaction (research
// R5): insert, update or duplicate, then the aggregates. A claim or replay
// failure from checkClaim rolls the transaction back and is returned as
// errIDClaimed or errReplay.
func (s *store) recordExtended(ctx context.Context, r extRecord) (extOutcome, error) {
	outcome := extNew
	prevVersion := ""
	err := s.writeTx(ctx, func(tx *sql.Tx) error {
		keyFP := telemetryschema.KeyFingerprint(r.Ext.Key)
		// Full precision: a report sent at a fractional second must not look
		// later than its own stored send time when replayed.
		sentAt := r.Ext.SentAt.UTC().Format(time.RFC3339Nano)
		row, found, err := lookupActivity(ctx, tx, r.IDHMAC)
		if err != nil {
			return err
		}
		switch {
		case !found:
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO activity (id_hmac, key_fp, first_seen, last_seen, last_sent_at, last_version)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				r.IDHMAC, keyFP, r.Day, r.Day, sentAt, r.Version); err != nil {
				return fmt.Errorf("insert activity: %w", err)
			}
		default:
			if err := checkClaim(row, keyFP, r.Ext.SentAt); err != nil {
				return err
			}
			if row.LastSeen >= r.Day {
				outcome = extDuplicate
				// A duplicate changes no aggregate, but its send time still
				// becomes the latest accepted one; otherwise a captured
				// duplicate could be replayed later the same day, or the next
				// day as that day's report.
				if _, err := tx.ExecContext(ctx,
					`UPDATE activity SET last_sent_at = ? WHERE id_hmac = ?`, sentAt, r.IDHMAC); err != nil {
					return fmt.Errorf("update activity send time: %w", err)
				}
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO daily_basic (day, duplicates) VALUES (?, 1)
					 ON CONFLICT (day) DO UPDATE SET duplicates = duplicates + 1`, r.Day); err != nil {
					return fmt.Errorf("record duplicate: %w", err)
				}
				return nil
			}
			outcome = extReturning
			prevVersion = row.LastVersion
			if _, err := tx.ExecContext(ctx,
				`UPDATE activity SET last_seen = ?, last_sent_at = ?, last_version = ? WHERE id_hmac = ?`,
				r.Day, sentAt, r.Version, r.IDHMAC); err != nil {
				return fmt.Errorf("update activity: %w", err)
			}
		}
		if err := writeBasic(ctx, tx, r.Day, r.Version, r.Servers, r.Templates); err != nil {
			return err
		}
		if err := writeExtendedAggregates(ctx, tx, r, outcome == extNew); err != nil {
			return err
		}
		return writeVersionTrend(ctx, tx, r.Day, prevVersion, r.Version, outcome == extNew)
	})
	return outcome, err
}

// writeExtendedAggregates writes daily_ext, daily_dim and daily_game for one
// non-duplicate extended report. No attribute is stored next to the install ID.
func writeExtendedAggregates(ctx context.Context, tx *sql.Tx, r extRecord, isNew bool) error {
	newInstalls := 0
	if isNew {
		newInstalls = 1
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_ext (day, ext_reports, active_installs, new_installs) VALUES (?, 1, 1, ?)
		 ON CONFLICT (day) DO UPDATE SET
		   ext_reports = ext_reports + 1,
		   active_installs = active_installs + 1,
		   new_installs = new_installs + excluded.new_installs`,
		r.Day, newInstalls); err != nil {
		return fmt.Errorf("record daily_ext: %w", err)
	}
	e := r.Ext
	dims := []struct{ dim, value string }{
		{dimNodes, e.Env.Nodes}, {dimDeployment, e.Env.Deployment},
		{dimClusters, e.Features.Clusters}, {dimDB, e.Features.DB}, {dimLanguage, e.Features.Language},
	}
	// k8s, distro and arch are sets (OD-15): one row per value present.
	for _, set := range []struct {
		dim    string
		values []string
	}{{dimK8s, e.Env.K8s}, {dimDistro, e.Env.Distro}, {dimArch, e.Env.Arch}} {
		for _, v := range set.values {
			dims = append(dims, struct{ dim, value string }{set.dim, v})
		}
	}
	for _, t := range e.Features.Tunnels {
		dims = append(dims, struct{ dim, value string }{dimTunnel, t})
	}
	on := map[string]bool{
		"wakeOnConnect": e.Features.WakeOnConnect, "capture": e.Features.Capture,
		"backups": e.Features.Backups, "sso": e.Features.SSO, "auditForwarding": e.Features.AuditForwarding,
	}
	for _, f := range featureNames {
		if on[f] {
			dims = append(dims, struct{ dim, value string }{dimFeature, f})
		}
	}
	for _, d := range dims {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO daily_dim (day, dim, value, installs) VALUES (?, ?, ?, 1)
			 ON CONFLICT (day, dim, value) DO UPDATE SET installs = installs + 1`,
			r.Day, d.dim, d.value); err != nil {
			return fmt.Errorf("record daily_dim %s: %w", d.dim, err)
		}
	}
	games := make(map[string]int, len(e.Games.Official)+1)
	for module, n := range e.Games.Official {
		games[module] = n
	}
	if e.Games.Custom > 0 {
		games[gameCustom] += e.Games.Custom
	}
	for module, n := range games {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO daily_game (day, module, installs, servers) VALUES (?, ?, 1, ?)
			 ON CONFLICT (day, module) DO UPDATE SET
			   installs = installs + 1,
			   servers = servers + excluded.servers`,
			r.Day, module, min(n, maxSummedCount)); err != nil {
			return fmt.Errorf("record daily_game %s: %w", module, err)
		}
	}
	return nil
}

// writeVersionTrend records one non-duplicate extended report in the version
// trend tables (FR-042): the install counts on its version for the day, and a
// version change against its previous report is counted by kind. A new
// install is never a change. Nothing is linked to the install ID.
func writeVersionTrend(ctx context.Context, tx *sql.Tx, day, prev, next string, isNew bool) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_install_version (day, version, installs) VALUES (?, ?, 1)
		 ON CONFLICT (day, version) DO UPDATE SET installs = installs + 1`,
		day, next); err != nil {
		return fmt.Errorf("record daily_install_version: %w", err)
	}
	if isNew {
		return nil
	}
	kind, changed := classifyChange(prev, next)
	if !changed {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_version_change (day, kind, installs) VALUES (?, ?, 1)
		 ON CONFLICT (day, kind) DO UPDATE SET installs = installs + 1`,
		day, kind); err != nil {
		return fmt.Errorf("record daily_version_change: %w", err)
	}
	return nil
}

// The send-time window of an extended report: sentAt must lie in
// [now - extWindowPast, now + extWindowFuture] (FR-036).
const (
	extWindowPast   = 36 * time.Hour
	extWindowFuture = time.Hour
)

// verifyExtSignature is the first two identity checks, in contract order: the
// signature over the exact body bytes under ext.key (bad_signature), then the
// send-time window (stale). It returns a refusal reason, or "" when the
// report may continue.
func (s *server) verifyExtSignature(header string, body []byte, ext *telemetryschema.Extended) string {
	if err := telemetryschema.Verify(header, ext.Key, body); err != nil {
		return refuseBadSignature
	}
	now := s.now()
	if ext.SentAt.Before(now.Add(-extWindowPast)) || ext.SentAt.After(now.Add(extWindowFuture)) {
		return refuseStale
	}
	return ""
}

// precheckClaim is the read-only claim and replay check that runs before the
// limiter counts the report, so a refused report never uses up a source's
// budget. It returns a refusal reason, or "". recordExtended repeats the
// check inside its transaction, which is the authoritative one. A read failure
// here is not a refusal: the transaction reads again.
func (s *server) precheckClaim(ctx context.Context, idHMAC string, ext *telemetryschema.Extended) string {
	row, found, err := lookupActivity(ctx, s.store.db, idHMAC)
	if err != nil || !found {
		return ""
	}
	switch claimErr := checkClaim(row, telemetryschema.KeyFingerprint(ext.Key), ext.SentAt); {
	case errors.Is(claimErr, errIDClaimed):
		return refuseIDClaimed
	case errors.Is(claimErr, errReplay):
		return refuseReplay
	}
	return ""
}

// checkClaim decides whether a report may use an existing activity record, in
// contract order: errIDClaimed when the record belongs to another key
// (key_fp differs), then errReplay when sentAt is not later than the last
// accepted sentAt. An unreadable stored send time is an error, never an
// acceptance.
func checkClaim(row activityRow, keyFP string, sentAt time.Time) error {
	if row.KeyFP != keyFP {
		return errIDClaimed
	}
	last, err := time.Parse(time.RFC3339Nano, row.LastSentAt)
	if err != nil {
		// Fail closed: an unreadable send time must not switch the replay
		// check off for this ID.
		return fmt.Errorf("activity last_sent_at %q: %w", row.LastSentAt, err)
	}
	if !sentAt.After(last) {
		return errReplay
	}
	return nil
}
