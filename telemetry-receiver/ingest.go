package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

const (
	// fleetCap is the stored fleet-size value for "more than 1000": exact
	// counts 0-1000 are kept, anything larger shares this row (research R4).
	fleetCap = 1001
	// maxSummedCount clamps one report's servers/templates contribution to
	// the daily sums, so a hostile count cannot overflow the integer column.
	maxSummedCount = 1_000_000
)

// Fleet histogram metric names stored in daily_fleet.metric.
const (
	fleetMetricServers   = "servers"
	fleetMetricTemplates = "templates"
)

func (s *server) ingest(w http.ResponseWriter, req *http.Request) {
	// The request budget comes first and counts every request, so refused
	// and malformed reports (which never reach the daily limit) cannot be
	// sent at an unbounded rate to burn signature checks, store reads or
	// AUTH_TOKEN guesses.
	if s.ingestLimiter != nil && !s.ingestLimiter.allow(sourceIP(req, s.cfg.trustedProxyCIDRs), s.now()) {
		s.rateLimited.WithLabelValues(routeIngest).Inc()
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	if s.cfg.authToken != "" {
		// tokenEqual hashes both sides, so the comparison does not reveal
		// the token's length the way ConstantTimeCompare on raw bytes does.
		if !tokenEqual(req.Header.Get("Authorization"), s.cfg.authToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	// Proof-of-work comes after AUTH_TOKEN and before the body is read, so a
	// refused report costs one HMAC and one hash (spec 022 R21).
	powOK, powBits := s.requirePoW(w, req)
	if !powOK {
		return
	}
	// Read the whole body first so the size limit applies to all of it, not
	// just to the part a JSON decoder happens to consume.
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	rep, _, err := telemetryschema.Decode(body)
	if err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	if rep.Servers < 0 || rep.Templates < 0 {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	// A report whose ext part was dropped by Decode (bad install ID) comes
	// back with Ext == nil and is counted as basic.
	if rep.Ext != nil {
		s.ingestExtended(w, req, body, rep, powBits)
		return
	}
	p := payload{Version: rep.Version, Servers: rep.Servers, Templates: rep.Templates}
	// The per-source daily limit counts accepted reports only, so it sits
	// after validation: a malformed request never uses up a source's budget.
	// The address is used for the counter and then dropped (FR-014).
	if !s.daily.take(sourceIP(req, s.cfg.trustedProxyCIDRs), s.now()) {
		s.rateLimited.WithLabelValues(routeIngest).Inc()
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	version := s.versionLabel(p.Version)
	if err := s.store.recordBasic(req.Context(), dayString(s.now()), version, p.Servers, p.Templates); err != nil {
		slog.Error("telemetry report not stored", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	slog.Info("telemetry report",
		"version", version, "servers", p.Servers, "templates", p.Templates)
	s.countReport(version)
	s.servers.Observe(float64(p.Servers))
	s.templates.Observe(float64(p.Templates))
	w.WriteHeader(http.StatusNoContent)
}

// recordBasic writes one accepted basic report into the aggregates for day
// in a single transaction: daily_basic, daily_version, daily_fleet (two
// rows) and meta.reports_total. version must already be a bounded label
// (versionLabel). Nothing identifying the report or its source is stored.
func (s *store) recordBasic(ctx context.Context, day, version string, servers, templates int) error {
	return s.writeTx(ctx, func(tx *sql.Tx) error {
		return writeBasic(ctx, tx, day, version, servers, templates)
	})
}

// writeBasic is the transaction body of recordBasic, shared with the
// extended path, which also counts the basic part of its report.
func writeBasic(ctx context.Context, tx *sql.Tx, day, version string, servers, templates int) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_basic (day, reports, servers_sum, templates_sum) VALUES (?, 1, ?, ?)
		 ON CONFLICT (day) DO UPDATE SET
		   reports = reports + 1,
		   servers_sum = servers_sum + excluded.servers_sum,
		   templates_sum = templates_sum + excluded.templates_sum`,
		day, min(servers, maxSummedCount), min(templates, maxSummedCount)); err != nil {
		return fmt.Errorf("record daily_basic: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_version (day, version, reports) VALUES (?, ?, 1)
		 ON CONFLICT (day, version) DO UPDATE SET reports = reports + 1`,
		day, version); err != nil {
		return fmt.Errorf("record daily_version: %w", err)
	}
	for _, f := range []struct {
		metric string
		n      int
	}{{fleetMetricServers, servers}, {fleetMetricTemplates, templates}} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO daily_fleet (day, metric, value, reports) VALUES (?, ?, ?, 1)
			 ON CONFLICT (day, metric, value) DO UPDATE SET reports = reports + 1`,
			day, f.metric, min(f.n, fleetCap)); err != nil {
			return fmt.Errorf("record daily_fleet %s: %w", f.metric, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE meta SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT) WHERE key = ?`,
		metaReportsTotal); err != nil {
		return fmt.Errorf("record reports_total: %w", err)
	}
	return nil
}
