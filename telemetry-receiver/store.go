package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// storeFileName is the SQLite file created inside DATA_DIR.
const storeFileName = "telemetry.db"

// schemaVersion is written to meta.schema_version on first open.
const schemaVersion = "1"

// Keys of the meta table (data-model.md, Receiver).
const (
	metaSchemaVersion   = "schema_version"
	metaPepper          = "pepper"
	metaCollectionStart = "collection_started"
	metaReportsTotal    = "reports_total"
	// metaRolloverThrough is the last completed UTC day the lifecycle job
	// has finalised (data-model.md, Lifecycle jobs).
	metaRolloverThrough = "rollover_through"
)

const (
	// pepperBytes is the size of a generated install-ID pepper.
	pepperBytes = 32
	// storeBusyTimeoutMsec bounds how long a connection waits on a locked
	// database before failing.
	storeBusyTimeoutMsec = 5000
)

// storeSchema creates every receiver table. All statements are idempotent
// so reopening an existing DATA_DIR is a no-op. Days are UTC calendar days
// written YYYY-MM-DD; other timestamps are RFC 3339 UTC strings bound from
// Go. Nothing here stores a raw report, a source address or a raw install
// ID.
var storeSchema = []string{
	`CREATE TABLE IF NOT EXISTS meta (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS daily_basic (
		day           TEXT PRIMARY KEY,
		reports       INTEGER NOT NULL DEFAULT 0,
		duplicates    INTEGER NOT NULL DEFAULT 0,
		servers_sum   INTEGER NOT NULL DEFAULT 0,
		templates_sum INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS daily_version (
		day     TEXT NOT NULL,
		version TEXT NOT NULL,
		reports INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, version)
	)`,
	`CREATE TABLE IF NOT EXISTS daily_fleet (
		day     TEXT NOT NULL,
		metric  TEXT NOT NULL,
		value   INTEGER NOT NULL,
		reports INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, metric, value)
	)`,
	`CREATE TABLE IF NOT EXISTS daily_ext (
		day             TEXT PRIMARY KEY,
		ext_reports     INTEGER NOT NULL DEFAULT 0,
		active_installs INTEGER NOT NULL DEFAULT 0,
		new_installs    INTEGER NOT NULL DEFAULT 0,
		lapsed_installs INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS daily_dim (
		day      TEXT NOT NULL,
		dim      TEXT NOT NULL,
		value    TEXT NOT NULL,
		installs INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, dim, value)
	)`,
	`CREATE TABLE IF NOT EXISTS daily_game (
		day      TEXT NOT NULL,
		module   TEXT NOT NULL,
		installs INTEGER NOT NULL DEFAULT 0,
		servers  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, module)
	)`,
	`CREATE TABLE IF NOT EXISTS daily_install_version (
		day      TEXT NOT NULL,
		version  TEXT NOT NULL,
		installs INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, version)
	)`,
	`CREATE TABLE IF NOT EXISTS daily_version_change (
		day      TEXT NOT NULL,
		kind     TEXT NOT NULL,
		installs INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, kind)
	)`,
	`CREATE TABLE IF NOT EXISTS activity (
		id_hmac      TEXT PRIMARY KEY,
		key_fp       TEXT NOT NULL,
		first_seen   TEXT NOT NULL,
		last_seen    TEXT NOT NULL,
		last_sent_at TEXT NOT NULL,
		last_version TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS activity_last_seen ON activity (last_seen)`,
}

// store is the receiver's SQLite-backed aggregate store. A single writer
// is enforced with mu: every write goes through exec, so WAL readers never
// contend with more than one writer.
type store struct {
	db     *sql.DB
	mu     sync.Mutex // serializes writers
	pepper []byte
	// mem reports whether the store is a private in-memory database.
	mem bool
	// activityExpiryDays is ACTIVITY_EXPIRY_DAYS; zero (the zero config)
	// disables the activity-record expiry (T073).
	activityExpiryDays int
}

// storeDSN builds the modernc.org/sqlite DSN for the on-disk database: WAL
// journaling, a busy timeout and enforced foreign keys.
func storeDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", storeBusyTimeoutMsec))
	q.Add("_pragma", "foreign_keys(1)")
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}).String()
}

// openStore opens (creating if needed) the store described by cfg. With
// cfg.dataDir empty the database is private to this store and lives in
// memory (a warning is logged); otherwise it is $DATA_DIR/telemetry.db in
// WAL mode. The pepper is cfg.idPepper when set, else a random value that
// is generated once and kept in meta so it survives restarts.
func openStore(ctx context.Context, cfg config) (*store, error) {
	var db *sql.DB
	mem := cfg.dataDir == ""
	if mem {
		slog.Warn("DATA_DIR is empty: telemetry aggregates are kept in memory and lost on restart")
		// ":memory:" without a shared cache gives every connection its own
		// database, so the pool is pinned to one connection that is never
		// recycled. Each store therefore owns a private database.
		var err error
		db, err = sql.Open("sqlite", ":memory:")
		if err != nil {
			return nil, fmt.Errorf("open in-memory store: %w", err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		db.SetConnMaxIdleTime(0)
	} else {
		if err := os.MkdirAll(cfg.dataDir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir %s: %w", cfg.dataDir, err)
		}
		var err error
		db, err = sql.Open("sqlite", storeDSN(filepath.Join(cfg.dataDir, storeFileName)))
		if err != nil {
			return nil, fmt.Errorf("open store in %s: %w", cfg.dataDir, err)
		}
	}
	st := &store{db: db, mem: mem, activityExpiryDays: cfg.activityExpiryDays}
	if err := st.init(ctx, cfg.idPepper); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return st, nil
}

// init creates the schema and seeds the meta keys. It is safe to run on an
// existing database: existing meta values are never overwritten.
func (s *store) init(ctx context.Context, envPepper string) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping store: %w", err)
	}
	for _, stmt := range storeSchema {
		if err := s.exec(ctx, stmt); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}
	if err := s.metaInit(ctx, metaSchemaVersion, schemaVersion); err != nil {
		return err
	}
	if err := s.metaInit(ctx, metaCollectionStart, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if err := s.metaInit(ctx, metaReportsTotal, "0"); err != nil {
		return err
	}
	if envPepper != "" {
		s.pepper = []byte(envPepper)
		return nil
	}
	raw := make([]byte, pepperBytes)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("generate pepper: %w", err)
	}
	if err := s.metaInit(ctx, metaPepper, hex.EncodeToString(raw)); err != nil {
		return err
	}
	stored, ok, err := s.metaGet(ctx, metaPepper)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("pepper missing from meta after initialization")
	}
	s.pepper = []byte(stored)
	return nil
}

// exec runs one write statement under the single-writer lock.
func (s *store) exec(ctx context.Context, query string, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

// metaInit inserts a meta key only when it does not exist yet.
func (s *store) metaInit(ctx context.Context, key, value string) error {
	if err := s.exec(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`,
		key, value); err != nil {
		return fmt.Errorf("init meta %s: %w", key, err)
	}
	return nil
}

// metaGet returns the value for a meta key and whether it exists.
func (s *store) metaGet(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read meta %s: %w", key, err)
	}
	return v, true, nil
}

// close releases the database.
func (s *store) close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

// writeTx runs fn in one transaction under the single-writer lock. The
// transaction commits when fn returns nil and rolls back otherwise. fn must
// use only tx: the in-memory store has a single connection, so reading
// through s.db inside fn would deadlock.
func (s *store) writeTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, rollbackErr(tx.Rollback()))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// rollbackErr hides sql.ErrTxDone, which only means the transaction had
// already ended.
func rollbackErr(err error) error {
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

const (
	// lifecycleInterval is how often the lifecycle job runs.
	lifecycleInterval = time.Hour
	// maxRolloverCatchup bounds how many missed days one run finalises after
	// a long outage.
	maxRolloverCatchup = 400
)

// retentionSweeps delete the daily_* rows older than the cutoff day (the one
// bound parameter). The statements are constants, never built from input.
var retentionSweeps = []string{
	`DELETE FROM daily_basic WHERE day < ?`,
	`DELETE FROM daily_version WHERE day < ?`,
	`DELETE FROM daily_fleet WHERE day < ?`,
	`DELETE FROM daily_ext WHERE day < ?`,
	`DELETE FROM daily_dim WHERE day < ?`,
	`DELETE FROM daily_game WHERE day < ?`,
	`DELETE FROM daily_install_version WHERE day < ?`,
	`DELETE FROM daily_version_change WHERE day < ?`,
}

// pendingDays returns the completed UTC days after through, up to and
// including yesterday (relative to now), oldest first. An empty through
// yields just yesterday. At most maxRolloverCatchup days are returned.
func pendingDays(through string, now time.Time) []string {
	yesterday := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	last, err := time.Parse(time.DateOnly, through)
	if through == "" || err != nil {
		return []string{dayString(yesterday)}
	}
	first := last.AddDate(0, 0, 1)
	if earliest := yesterday.AddDate(0, 0, 1-maxRolloverCatchup); first.Before(earliest) {
		first = earliest
	}
	var days []string
	for d := first; !d.After(yesterday); d = d.AddDate(0, 0, 1) {
		days = append(days, dayString(d))
	}
	return days
}

// lifecycleOnce runs one pass of the lifecycle job. It is idempotent: it
// finalises the completed days after meta.rollover_through, then deletes
// daily_* rows older than retentionDays. A retentionDays of zero or less
// (the zero config) deletes nothing. For every finalised day D it records
// lapsed_installs[D] (activity records last seen on D - 30), then it expires
// activity records older than activityExpiryDays (T073).
func (s *store) lifecycleOnce(ctx context.Context, now time.Time, retentionDays int) error {
	through, _, err := s.metaGet(ctx, metaRolloverThrough)
	if err != nil {
		return err
	}
	now = now.UTC()
	days := pendingDays(through, now)
	cutoff := dayString(now.AddDate(0, 0, -retentionDays))
	return s.writeTx(ctx, func(tx *sql.Tx) error {
		if len(days) > 0 {
			// Never move the marker backwards.
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO meta (key, value) VALUES (?, ?)
				 ON CONFLICT (key) DO UPDATE SET value = excluded.value WHERE excluded.value > meta.value`,
				metaRolloverThrough, days[len(days)-1]); err != nil {
				return fmt.Errorf("advance rollover_through: %w", err)
			}
		}
		for _, day := range days {
			if err := recordLapsed(ctx, tx, day); err != nil {
				return err
			}
		}
		if s.activityExpiryDays > 0 {
			expiry := dayString(now.AddDate(0, 0, -s.activityExpiryDays))
			if _, err := tx.ExecContext(ctx, `DELETE FROM activity WHERE last_seen < ?`, expiry); err != nil {
				return fmt.Errorf("expire activity records: %w", err)
			}
		}
		if retentionDays <= 0 {
			return nil
		}
		for _, sweep := range retentionSweeps {
			if _, err := tx.ExecContext(ctx, sweep, cutoff); err != nil {
				return fmt.Errorf("retention sweep: %w", err)
			}
		}
		return nil
	})
}

// lapsedWindowDays is how long an install must stay silent to count as lapsed.
const lapsedWindowDays = 30

// recordLapsed sets daily_ext[day].lapsed_installs to the number of activity
// records whose last report was lapsedWindowDays before day. It writes nothing
// when that number is 0, so quiet days leave no row, and it is idempotent.
func recordLapsed(ctx context.Context, tx *sql.Tx, day string) error {
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return fmt.Errorf("lapsed installs: bad day %q: %w", day, err)
	}
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM activity WHERE last_seen = ?`,
		dayString(d.AddDate(0, 0, -lapsedWindowDays))).Scan(&n); err != nil {
		return fmt.Errorf("count lapsed installs: %w", err)
	}
	if n == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_ext (day, lapsed_installs) VALUES (?, ?)
		 ON CONFLICT (day) DO UPDATE SET lapsed_installs = excluded.lapsed_installs`,
		day, n); err != nil {
		return fmt.Errorf("record lapsed installs: %w", err)
	}
	return nil
}

// runLifecycle runs lifecycleOnce immediately and then every
// lifecycleInterval until ctx is cancelled. Failures are logged and retried
// on the next tick.
func (s *store) runLifecycle(ctx context.Context, retentionDays int, now func() time.Time) {
	run := func() {
		if err := s.lifecycleOnce(ctx, now(), retentionDays); err != nil && ctx.Err() == nil {
			slog.Error("telemetry lifecycle job failed", "err", err)
		}
	}
	run()
	t := time.NewTicker(lifecycleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
