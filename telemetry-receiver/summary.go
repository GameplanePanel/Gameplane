package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// summaryTTL is how long a computed snapshot is served before it is
	// recomputed (FR-029: at most every 5 minutes).
	summaryTTL = 5 * time.Minute
	// summaryPerMinute and summaryBurst are the per-source token bucket of
	// GET /v1/summary (research R9).
	summaryPerMinute = 60
	summaryBurst     = 10
	// summaryWindowDays is the span of the reports30d figure.
	summaryWindowDays = 30
	// summaryCacheControl lets clients and CDNs cache a response for an hour.
	summaryCacheControl = "public, max-age=3600"
)

// summaryResponse is the whole body of GET /v1/summary: exactly these five
// keys (FR-029, FR-030).
type summaryResponse struct {
	AsOf              string `json:"asOf"`
	ReportsLatestDay  int64  `json:"reportsLatestDay"`
	Reports30d        int64  `json:"reports30d"`
	ReportsTotal      int64  `json:"reportsTotal"`
	ActiveInstalls30d int64  `json:"activeInstalls30d"`
}

// summarySnapshot is one encoded response with its entity tag.
type summarySnapshot struct {
	body []byte
	etag string
	at   time.Time
}

// summaryCache holds the latest snapshot. Computing it takes the lock, so
// heavy polling costs one computation per summaryTTL, not one per request.
type summaryCache struct {
	mu   sync.Mutex
	snap *summarySnapshot
}

// get returns the cached snapshot, recomputing it when it is older than
// summaryTTL. When the recomputation fails and an older snapshot exists, the
// older one is served.
func (c *summaryCache) get(ctx context.Context, st *store, now time.Time) (*summarySnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && now.Sub(c.snap.at) < summaryTTL {
		return c.snap, nil
	}
	resp, err := computeSummary(ctx, st, now)
	if err != nil {
		if c.snap != nil {
			return c.snap, nil
		}
		return nil, err
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode summary: %w", err)
	}
	sum := sha256.Sum256(body)
	c.snap = &summarySnapshot{body: body, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, at: now}
	return c.snap, nil
}

// computeSummary reads the five headline values. asOf is the latest complete
// UTC day. With no data every count is 0 (the summary is a counter, not a
// chart). reportsTotal comes from meta.reports_total, which retention sweeps
// never change.
func computeSummary(ctx context.Context, st *store, now time.Time) (summaryResponse, error) {
	asOf := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	resp := summaryResponse{AsOf: dayString(asOf)}
	if err := st.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(CASE WHEN day = ? THEN reports END), 0), COALESCE(SUM(reports), 0)
		 FROM daily_basic WHERE day >= ? AND day <= ?`,
		resp.AsOf, dayString(asOf.AddDate(0, 0, 1-summaryWindowDays)), resp.AsOf).
		Scan(&resp.ReportsLatestDay, &resp.Reports30d); err != nil {
		return summaryResponse{}, fmt.Errorf("query summary reports: %w", err)
	}
	total, ok, err := st.metaGet(ctx, metaReportsTotal)
	if err != nil {
		return summaryResponse{}, err
	}
	if ok {
		if resp.ReportsTotal, err = strconv.ParseInt(total, 10, 64); err != nil {
			return summaryResponse{}, fmt.Errorf("parse meta %s: %w", metaReportsTotal, err)
		}
	}
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM activity WHERE last_seen >= ?`,
		dayString(asOf.AddDate(0, 0, 1-summaryWindowDays))).Scan(&resp.ActiveInstalls30d); err != nil {
		return summaryResponse{}, fmt.Errorf("query summary installs: %w", err)
	}
	return resp, nil
}

// summary serves GET /v1/summary on the public listener.
func (s *server) summary(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.publicSummary {
		http.NotFound(w, r)
		return
	}
	now := s.now()
	if !s.summaryLimiter.allow(sourceIP(r, s.cfg.trustedProxyCIDRs), now) {
		s.rateLimited.WithLabelValues(routeSummary).Inc()
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	snap, err := s.summaries.get(r.Context(), s.store, now)
	if err != nil {
		slog.Error("compute summary", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", summaryCacheControl)
	h.Set("ETag", snap.etag)
	h.Set("Access-Control-Allow-Origin", "*")
	if etagMatches(r.Header.Get("If-None-Match"), snap.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	_, _ = w.Write(snap.body)
}

// etagMatches reports whether an If-None-Match header names etag (weak
// validators and "*" included).
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if p == "*" || p == etag {
			return true
		}
	}
	return false
}
