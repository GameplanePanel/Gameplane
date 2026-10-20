// Command telemetry-receiver is the collection endpoint for Gameplane's
// anonymous usage telemetry. The API's reporter (api/internal/telemetry)
// POSTs a tiny JSON payload — {version, servers, templates}, nothing
// identifying — once a day when the admin has opted in; this receiver
// validates it, logs it structurally, and exposes aggregate Prometheus
// metrics so an operator can chart adoption without storing raw reports.
//
// It is deliberately standalone (own module, stdlib + client_golang +
// modernc.org/sqlite, no cgo) so it can run anywhere: in-cluster via the
// Helm chart's api.telemetry.receiver.enabled, or on a public host
// collecting reports from many installs. Aggregates live in a SQLite file
// under DATA_DIR (see store.go); raw reports are never stored.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/GameplanePanel/gameplane/telemetryschema"
	"github.com/prometheus/client_golang/prometheus"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

// maxBody bounds the inbound POST body. Real payloads are well under a
// kilobyte; this just stops a misdirected client from streaming at us.
const maxBody = 16 << 10

// HTTP server timeouts: the whole-body read is bounded so a client can't
// hold /ingest open after sending a report.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 15 * time.Second
	idleTimeout       = 120 * time.Second
)

// Route label values of gameplane_telemetry_rate_limited_total.
const (
	routeIngest  = "ingest"
	routeLogin   = "login"
	routeSummary = "summary"
)

// maxVersionLabels bounds how many different valid version labels are
// retained; telemetryschema.VersionRE limits each label's syntax.
const maxVersionLabels = 128

// Configuration defaults and the enforced minimums for the two retention
// settings (spec 022 OD-3, OD-4).
const (
	defaultDashboardListen        = ":8081"
	defaultIngestSourceDailyLimit = 20
	defaultRetentionDays          = 730
	defaultActivityExpiryDays     = 90
	minRetentionDays              = 365
	minActivityExpiryDays         = 31
	// minDashboardTokenLen is the shortest DASHBOARD_TOKEN run accepts
	// (spec 022 FR-041, research R8).
	minDashboardTokenLen = 32
)

// config holds the receiver's settings. The zero value is valid and means
// "no per-source limits, in-memory store, no dashboard"; loadConfig applies
// the production defaults.
type config struct {
	listen    string
	authToken string

	// dashboardListen is the dashboard listener address; the listener only
	// starts when dashboardToken is set.
	dashboardListen string
	dashboardToken  string
	// dataDir is the SQLite directory. Empty means an in-memory store.
	dataDir       string
	publicSummary bool
	// trustedProxyCIDRs are the peers whose X-Forwarded-For is trusted.
	trustedProxyCIDRs []netip.Prefix
	// ingestSourceDailyLimit is the accepted reports per source per UTC day;
	// 0 means unlimited.
	ingestSourceDailyLimit int
	retentionDays          int
	activityExpiryDays     int
	// idPepper is the HMAC pepper for install IDs; empty means the store
	// generates one and keeps it in meta.
	idPepper string
	// ingestPoW requires proof-of-work on /ingest and serves /v1/challenge
	// (spec 022 R21). powTargetPerMin, powMinBits and powMaxBits are the
	// normal challenge rate and the lowest and highest difficulty.
	ingestPoW       bool
	powTargetPerMin int
	powMinBits      int
	powMaxBits      int

	// loadErrs collects environment values that could not be parsed, so that
	// validate can report them as startup errors.
	loadErrs []error
}

// loadConfig reads the environment. It never fails: unparsable values are
// recorded in loadErrs and reported by validate.
func loadConfig() config {
	cfg := config{
		listen:          envOr("LISTEN_ADDR", ":8080"),
		authToken:       envOr("AUTH_TOKEN", ""),
		dashboardListen: envOr("DASHBOARD_LISTEN_ADDR", defaultDashboardListen),
		dashboardToken:  envOr("DASHBOARD_TOKEN", ""),
		dataDir:         envOr("DATA_DIR", ""),
		publicSummary:   strings.EqualFold(strings.TrimSpace(envOr("PUBLIC_SUMMARY", "")), "true"),
		idPepper:        envOr("ID_PEPPER", ""),
		ingestPoW:       strings.EqualFold(strings.TrimSpace(envOr("INGEST_POW", "")), "true"),
	}
	cfg.ingestSourceDailyLimit = cfg.envInt("INGEST_SOURCE_DAILY_LIMIT", defaultIngestSourceDailyLimit)
	cfg.retentionDays = cfg.envInt("RETENTION_DAYS", defaultRetentionDays)
	cfg.activityExpiryDays = cfg.envInt("ACTIVITY_EXPIRY_DAYS", defaultActivityExpiryDays)
	cfg.powTargetPerMin = cfg.envInt("INGEST_POW_TARGET_PER_MIN", defaultPoWTargetPerMin)
	cfg.powMinBits = cfg.envInt("INGEST_POW_MIN_BITS", defaultPoWMinBits)
	cfg.powMaxBits = cfg.envInt("INGEST_POW_MAX_BITS", defaultPoWMaxBits)
	for _, raw := range strings.Split(envOr("TRUSTED_PROXY_CIDRS", ""), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			cfg.loadErrs = append(cfg.loadErrs, fmt.Errorf("TRUSTED_PROXY_CIDRS: %w", err))
			continue
		}
		cfg.trustedProxyCIDRs = append(cfg.trustedProxyCIDRs, p)
	}
	return cfg
}

// envInt reads an integer variable. Unset or blank gives fallback; a value
// that is not an integer records an error and gives fallback.
func (c *config) envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		c.loadErrs = append(c.loadErrs, fmt.Errorf("%s: %w", key, err))
		return fallback
	}
	return n
}

// validate rejects settings the receiver must not start with. The zero
// config is deliberately not validated: tests and newServer use it to mean
// "no limits"; only run validates the configuration read from the
// environment.
func (c config) validate() error {
	errs := append([]error(nil), c.loadErrs...)
	if c.retentionDays < minRetentionDays {
		errs = append(errs, fmt.Errorf("RETENTION_DAYS must be at least %d, got %d", minRetentionDays, c.retentionDays))
	}
	if c.activityExpiryDays < minActivityExpiryDays {
		errs = append(errs, fmt.Errorf("ACTIVITY_EXPIRY_DAYS must be at least %d, got %d", minActivityExpiryDays, c.activityExpiryDays))
	}
	if c.ingestSourceDailyLimit < 0 {
		errs = append(errs, fmt.Errorf("INGEST_SOURCE_DAILY_LIMIT must not be negative, got %d", c.ingestSourceDailyLimit))
	}
	if c.powTargetPerMin < 1 {
		errs = append(errs, fmt.Errorf("INGEST_POW_TARGET_PER_MIN must be at least 1, got %d", c.powTargetPerMin))
	}
	if c.powMinBits < 0 {
		errs = append(errs, fmt.Errorf("INGEST_POW_MIN_BITS must not be negative, got %d", c.powMinBits))
	}
	if c.powMaxBits < c.powMinBits || c.powMaxBits > telemetryschema.MaxPoWBits {
		errs = append(errs, fmt.Errorf("INGEST_POW_MAX_BITS must be between INGEST_POW_MIN_BITS (%d) and %d, got %d",
			c.powMinBits, telemetryschema.MaxPoWBits, c.powMaxBits))
	}
	return errors.Join(errs...)
}

// validateDashboardToken refuses a dashboard credential that is set but
// shorter than minDashboardTokenLen characters (spec 022 FR-041). run calls
// it next to validate; it is separate so that validate, which tests call on
// configs with placeholder tokens, keeps its behavior.
func (c config) validateDashboardToken() error {
	if n := utf8.RuneCountInString(c.dashboardToken); c.dashboardToken != "" && n < minDashboardTokenLen {
		return fmt.Errorf("DASHBOARD_TOKEN must be at least %d characters, got %d (generate one from 32 random bytes)",
			minDashboardTokenLen, n)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// payload mirrors api/internal/telemetry's report shape.
type payload struct {
	Version   string
	Servers   int
	Templates int
}

// errInvalidPayload is returned by decodePayload for any body that
// telemetryschema.Decode rejects, and for any report that carries the
// extended tier. The ingest handler no longer calls decodePayload: it decodes
// with telemetryschema.Decode directly and accepts ext (T072).
var errInvalidPayload = errors.New("invalid payload")

// decodePayload parses a body that has already been read in full. It is a
// thin wrapper over telemetryschema.Decode, which owns the strict parsing
// rules (exactly one object, exact case-sensitive keys, no duplicates or
// nulls, nothing trailing). Every failure, including an unsupported
// schema, is reported as errInvalidPayload with the cause kept in the
// chain. Reports carrying ext are rejected too: this wrapper keeps the
// basic-only parse that main_test.go covers.
func decodePayload(body []byte) (payload, error) {
	rep, info, err := telemetryschema.Decode(body)
	if err != nil {
		return payload{}, fmt.Errorf("%w: %w", errInvalidPayload, err)
	}
	if rep.Ext != nil || info.ExtDropped {
		return payload{}, fmt.Errorf("%w: extended reports are not accepted", errInvalidPayload)
	}
	return payload{Version: rep.Version, Servers: rep.Servers, Templates: rep.Templates}, nil
}

type server struct {
	cfg   config
	reg   *prometheus.Registry
	store *store

	versionMu sync.Mutex
	versions  map[string]struct{}

	// now is the clock for day bucketing and limiter windows; tests replace it.
	now func() time.Time
	// daily is the per-source accepted-reports-per-UTC-day counter (T062).
	daily *dailyLimiter
	// summaryLimiter and summaryCache serve GET /v1/summary (T079).
	summaryLimiter *bucketLimiter
	summaries      summaryCache

	reports     *prometheus.CounterVec
	servers     prometheus.Histogram
	templates   prometheus.Histogram
	rateLimited *prometheus.CounterVec
	// duplicates counts same-day repeat extended reports (T072).
	duplicates prometheus.Counter
	// extReports counts extended reports that changed the aggregates (T072).
	extReports prometheus.Counter
	// refused counts reports refused by the signature, window, claim or
	// replay checks, by reason (T083).
	refused *prometheus.CounterVec

	// pow, powLimiter and powChallenges are the proof-of-work state (T102).
	// pow and powLimiter are nil unless INGEST_POW is on (initPoW).
	pow           *powState
	powLimiter    *bucketLimiter
	powChallenges prometheus.Counter
}

// newServer builds a server backed by a private in-memory store, whatever
// cfg.dataDir says. Tests use it; main goes through newServerWithStore.
func newServer(cfg config) *server {
	memCfg := cfg
	memCfg.dataDir = ""
	st, err := openStore(context.Background(), memCfg)
	if err != nil {
		// Opening a private in-memory database only fails if the SQLite
		// driver itself is broken, which is not recoverable.
		panic(fmt.Sprintf("telemetry-receiver: in-memory store: %v", err))
	}
	return newServerWithStore(cfg, st)
}

// newServerWithStore builds a server over a store the caller opened and
// owns.
func newServerWithStore(cfg config, st *store) *server {
	reg := prometheus.NewRegistry()
	s := &server{
		cfg:            cfg,
		reg:            reg,
		store:          st,
		versions:       make(map[string]struct{}),
		now:            time.Now,
		daily:          newDailyLimiter(cfg.ingestSourceDailyLimit),
		summaryLimiter: newBucketLimiter(summaryPerMinute, summaryBurst),
		reports: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gameplane_telemetry_reports_total",
			Help: "Telemetry reports accepted, by reported Gameplane version.",
		}, []string{"version"}),
		// Fleet-size distributions: the buckets skew small because a
		// typical install is a homelab; the top bucket catches big ones.
		servers: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "gameplane_telemetry_servers",
			Help:    "Distribution of GameServer counts across reports.",
			Buckets: []float64{0, 1, 2, 5, 10, 25, 50, 100, 250},
		}),
		templates: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "gameplane_telemetry_templates",
			Help:    "Distribution of GameTemplate counts across reports.",
			Buckets: []float64{0, 1, 2, 5, 10, 25, 50, 100, 250},
		}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gameplane_telemetry_rate_limited_total",
			Help: "Requests refused by a per-source limit, by route.",
		}, []string{"route"}),
		duplicates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gameplane_telemetry_duplicates_total",
			Help: "Extended reports that repeated an install's report of the same UTC day.",
		}),
		extReports: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gameplane_telemetry_extended_reports_total",
			Help: "Extended reports that were counted in the aggregates.",
		}),
		refused: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gameplane_telemetry_refused_total",
			Help: "Extended reports refused by the signature, window, claim or replay checks, by reason.",
		}, []string{"reason"}),
	}
	reg.MustRegister(s.reports, s.servers, s.templates, s.rateLimited, s.duplicates, s.extReports, s.refused)
	s.initPoW()
	for _, reason := range []string{refuseBadSignature, refuseStale, refuseIDClaimed, refuseReplay} {
		s.refused.WithLabelValues(reason)
	}
	// Make the series visible from the start, so a scrape shows 0 rather
	// than nothing.
	s.rateLimited.WithLabelValues(routeIngest)
	s.rateLimited.WithLabelValues(routeLogin)
	s.rateLimited.WithLabelValues(routeSummary)
	return s
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("POST /ingest", s.ingest)
	mux.HandleFunc("GET /v1/summary", s.summary)
	mux.HandleFunc("GET /v1/challenge", s.challenge)
	return mux
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func run(cfg config) error {
	if err := errors.Join(cfg.validate(), cfg.validateDashboardToken()); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serve(ctx, cfg)
}

func serve(ctx context.Context, cfg config) error {
	st, err := openStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if cerr := st.close(); cerr != nil {
			slog.Error("close store", "err", cerr)
		}
	}()
	s := newServerWithStore(cfg, st)

	// The hourly lifecycle job (retention sweep, day rollover) stops before
	// the store is closed: defers run last-in first.
	lifeCtx, stopLife := context.WithCancel(ctx)
	lifeDone := make(chan struct{})
	go func() {
		defer close(lifeDone)
		st.runLifecycle(lifeCtx, cfg.retentionDays, time.Now)
	}()
	defer func() {
		stopLife()
		<-lifeDone
	}()

	srv := newHTTPServer(cfg.listen, s.routes())
	servers := []*http.Server{srv}
	errCh := make(chan error, 2)
	go func() {
		slog.Info("telemetry-receiver listening", "addr", cfg.listen, "version", Version, "auth", cfg.authToken != "")
		errCh <- fmt.Errorf("listen on %s: %w", cfg.listen, srv.ListenAndServe())
	}()
	// The dashboard listener only exists when a token is configured, so an
	// unconfigured receiver never exposes it.
	if cfg.dashboardToken != "" {
		dsrv := newHTTPServer(cfg.dashboardListen, s.dashboardRoutes())
		servers = append(servers, dsrv)
		go func() {
			slog.Info("telemetry-receiver dashboard listening", "addr", cfg.dashboardListen)
			errCh <- fmt.Errorf("dashboard listen on %s: %w", cfg.dashboardListen, dsrv.ListenAndServe())
		}()
	}
	select {
	case err := <-errCh:
		for _, h := range servers {
			_ = h.Close()
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var errs []error
		for _, h := range servers {
			errs = append(errs, h.Shutdown(shutdownCtx))
		}
		return errors.Join(errs...)
	}
}

func main() {
	if err := run(loadConfig()); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("telemetry-receiver failed", "err", err)
		os.Exit(1)
	}
}
