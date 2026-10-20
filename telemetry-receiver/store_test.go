package main

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// wantTables lists every table data-model.md § Receiver requires.
var wantTables = []string{
	"meta", "daily_basic", "daily_version", "daily_fleet",
	"daily_ext", "daily_dim", "daily_game", "activity",
}

func openTestStore(ctx context.Context, t *testing.T, cfg config) *store {
	t.Helper()
	st, err := openStore(ctx, cfg)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { _ = st.close() })
	return st
}

func mustMeta(t *testing.T, st *store, key string) string {
	t.Helper()
	v, ok, err := st.metaGet(context.Background(), key)
	if err != nil {
		t.Fatalf("metaGet(%s): %v", key, err)
	}
	if !ok {
		t.Fatalf("meta key %q missing", key)
	}
	return v
}

func countRows(ctx context.Context, t *testing.T, st *store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count (%s): %v", query, err)
	}
	return n
}

func TestStoreCreatesSchemaAndMeta(t *testing.T) {
	st := openTestStore(t.Context(), t, config{dataDir: t.TempDir()})
	for _, name := range wantTables {
		if n := countRows(t.Context(), t, st, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name); n != 1 {
			t.Errorf("table %s: found %d, want 1", name, n)
		}
	}
	// The activity claim columns the spec calls out by name.
	for _, col := range []string{"key_fp", "last_sent_at"} {
		if n := countRows(t.Context(), t, st, `SELECT count(*) FROM pragma_table_info('activity') WHERE name = ?`, col); n != 1 {
			t.Errorf("activity.%s: found %d, want 1", col, n)
		}
	}
	if got := mustMeta(t, st, "schema_version"); got != "1" {
		t.Errorf("schema_version = %q, want 1", got)
	}
	if got := mustMeta(t, st, "reports_total"); got != "0" {
		t.Errorf("reports_total = %q, want 0", got)
	}
	if _, err := time.Parse(time.RFC3339, mustMeta(t, st, "collection_started")); err != nil {
		t.Errorf("collection_started is not RFC 3339: %v", err)
	}
	// A generated pepper is 32 random bytes, hex encoded, and kept in meta.
	pepper := mustMeta(t, st, "pepper")
	if len(pepper) != 64 || string(st.pepper) != pepper {
		t.Errorf("pepper = %q (store holds %q), want 64 hex chars matching the store", pepper, st.pepper)
	}
	if st.mem {
		t.Error("an on-disk store reported itself as in-memory")
	}
}

func TestStoreUsesWALOnDisk(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t.Context(), t, config{dataDir: dir})
	var mode string
	if err := st.db.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	if _, err := os.Stat(filepath.Join(dir, "telemetry.db")); err != nil {
		t.Errorf("telemetry.db not created in DATA_DIR: %v", err)
	}
}

func TestStoreCreatesMissingDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	st := openTestStore(t.Context(), t, config{dataDir: dir})
	if st.mem {
		t.Error("store with DATA_DIR set must not be in-memory")
	}
	if _, err := os.Stat(filepath.Join(dir, "telemetry.db")); err != nil {
		t.Errorf("telemetry.db not created in new directory: %v", err)
	}
}

// SC-009: aggregates and the pepper survive a restart on the same DATA_DIR.
func TestStoreReopenKeepsAggregatesAndPepper(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	first, err := openStore(context.Background(), config{dataDir: dir})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	pepper := string(first.pepper)
	started := mustMeta(t, first, "collection_started")
	if err := first.exec(ctx,
		`INSERT INTO daily_basic (day, reports, servers_sum, templates_sum) VALUES (?, ?, ?, ?)`,
		"2026-10-05", 7, 21, 14); err != nil {
		t.Fatalf("insert daily_basic: %v", err)
	}
	if err := first.exec(ctx,
		`INSERT INTO activity (id_hmac, key_fp, first_seen, last_seen, last_sent_at, last_version) VALUES (?, ?, ?, ?, ?, ?)`,
		"abc123", "fp", "2026-10-01", "2026-10-05", "2026-10-05T10:00:00Z", "0.3.0"); err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if err := first.exec(ctx, `UPDATE meta SET value = ? WHERE key = 'reports_total'`, "42"); err != nil {
		t.Fatalf("update reports_total: %v", err)
	}
	if err := first.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := openTestStore(t.Context(), t, config{dataDir: dir})
	if got := string(second.pepper); got != pepper {
		t.Errorf("pepper changed across restart: %q -> %q", pepper, got)
	}
	if got := mustMeta(t, second, "collection_started"); got != started {
		t.Errorf("collection_started changed across restart: %q -> %q", started, got)
	}
	if got := mustMeta(t, second, "reports_total"); got != "42" {
		t.Errorf("reports_total = %q after restart, want 42 (initialisation must not reset it)", got)
	}
	if n := countRows(t.Context(), t, second, `SELECT reports FROM daily_basic WHERE day = ?`, "2026-10-05"); n != 7 {
		t.Errorf("daily_basic reports = %d after restart, want 7", n)
	}
	if n := countRows(t.Context(), t, second, `SELECT count(*) FROM activity WHERE id_hmac = ?`, "abc123"); n != 1 {
		t.Errorf("activity rows = %d after restart, want 1", n)
	}
}

func TestStoreEnvPepperIsUsedAndNotStored(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t.Context(), t, config{dataDir: dir, idPepper: "from-secret"})
	if got := string(st.pepper); got != "from-secret" {
		t.Errorf("pepper = %q, want the configured value", got)
	}
	if n := countRows(t.Context(), t, st, `SELECT count(*) FROM meta WHERE key = 'pepper'`); n != 0 {
		t.Errorf("meta holds %d pepper rows, want 0 when ID_PEPPER is set", n)
	}
}

func TestStoreInMemoryIsPrivatePerStore(t *testing.T) {
	ctx := context.Background()
	a := openTestStore(t.Context(), t, config{})
	b := openTestStore(t.Context(), t, config{})
	if !a.mem || !b.mem {
		t.Fatal("DATA_DIR empty must give in-memory stores")
	}
	if err := a.exec(ctx, `INSERT INTO daily_basic (day, reports) VALUES (?, ?)`, "2026-10-05", 1); err != nil {
		t.Fatalf("insert into a: %v", err)
	}
	if n := countRows(t.Context(), t, a, `SELECT count(*) FROM daily_basic`); n != 1 {
		t.Errorf("store a has %d rows, want 1", n)
	}
	if n := countRows(t.Context(), t, b, `SELECT count(*) FROM daily_basic`); n != 0 {
		t.Errorf("store b sees %d rows from store a, want 0 (no shared cache)", n)
	}
	if string(a.pepper) == string(b.pepper) {
		t.Error("two in-memory stores generated the same pepper")
	}
}

// newServer keeps its signature and always uses a private in-memory store,
// even when the config names a DATA_DIR.
func TestNewServerOpensPrivateMemoryStore(t *testing.T) {
	dir := t.TempDir()
	s1 := newServer(config{dataDir: dir})
	s2 := newServer(config{})
	t.Cleanup(func() { _ = s1.store.close(); _ = s2.store.close() })
	if !s1.store.mem || !s2.store.mem {
		t.Fatal("newServer must open an in-memory store")
	}
	if _, err := os.Stat(filepath.Join(dir, "telemetry.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("newServer created a file in DATA_DIR (stat err = %v)", err)
	}
	if s1.store == s2.store {
		t.Error("two servers share one store")
	}
}

func TestStoreSerialisesConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t.Context(), t, config{dataDir: t.TempDir()})
	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.exec(ctx, `UPDATE meta SET value = CAST(value AS INTEGER) + 1 WHERE key = 'reports_total'`)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent write: %v", err)
		}
	}
	if got := mustMeta(t, st, "reports_total"); got != "20" {
		t.Errorf("reports_total = %q after %d increments, want 20", got, writers)
	}
}

func TestStoreMetaGetMissingKey(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	v, ok, err := st.metaGet(context.Background(), "no-such-key")
	if err != nil || ok || v != "" {
		t.Errorf("metaGet(missing) = %q, %v, %v; want empty, false, nil", v, ok, err)
	}
}

func TestOpenStoreErrors(t *testing.T) {
	// DATA_DIR that cannot be created because a regular file is in the way.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := openStore(context.Background(), config{dataDir: filepath.Join(blocker, "sub")}); err == nil {
		t.Error("expected an error when DATA_DIR cannot be created")
	}

	// A DATA_DIR whose telemetry.db is not a SQLite database.
	dir := t.TempDir()
	junk := strings.Repeat("this is not a sqlite database\n", 20)
	if err := os.WriteFile(filepath.Join(dir, "telemetry.db"), []byte(junk), 0o600); err != nil {
		t.Fatalf("write junk db: %v", err)
	}
	if _, err := openStore(context.Background(), config{dataDir: dir}); err == nil {
		t.Error("expected an error opening a corrupt telemetry.db")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"LISTEN_ADDR", "AUTH_TOKEN", "DASHBOARD_LISTEN_ADDR", "DASHBOARD_TOKEN", "DATA_DIR",
		"PUBLIC_SUMMARY", "TRUSTED_PROXY_CIDRS", "INGEST_SOURCE_DAILY_LIMIT",
		"RETENTION_DAYS", "ACTIVITY_EXPIRY_DAYS", "ID_PEPPER",
	} {
		// t.Setenv registers the restore; the variable must then be truly
		// unset so loadConfig sees "not set", not "set to empty".
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
}

func TestLoadConfigNewDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg := loadConfig()
	if cfg.dashboardListen != ":8081" || cfg.dashboardToken != "" || cfg.dataDir != "" ||
		cfg.publicSummary || len(cfg.trustedProxyCIDRs) != 0 || cfg.idPepper != "" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.ingestSourceDailyLimit != 20 || cfg.retentionDays != 730 || cfg.activityExpiryDays != 90 {
		t.Fatalf("numeric defaults = %+v", cfg)
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestLoadConfigNewEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DASHBOARD_LISTEN_ADDR", ":9091")
	t.Setenv("DASHBOARD_TOKEN", "dash")
	t.Setenv("DATA_DIR", "/data")
	t.Setenv("PUBLIC_SUMMARY", "true")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.1.0/24,")
	t.Setenv("INGEST_SOURCE_DAILY_LIMIT", "0")
	t.Setenv("RETENTION_DAYS", "365")
	t.Setenv("ACTIVITY_EXPIRY_DAYS", "31")
	t.Setenv("ID_PEPPER", "pep")
	cfg := loadConfig()
	if cfg.dashboardListen != ":9091" || cfg.dashboardToken != "dash" || cfg.dataDir != "/data" ||
		!cfg.publicSummary || cfg.idPepper != "pep" {
		t.Fatalf("env override = %+v", cfg)
	}
	if cfg.ingestSourceDailyLimit != 0 || cfg.retentionDays != 365 || cfg.activityExpiryDays != 31 {
		t.Fatalf("numeric override = %+v", cfg)
	}
	if len(cfg.trustedProxyCIDRs) != 2 || cfg.trustedProxyCIDRs[0].String() != "10.0.0.0/8" ||
		cfg.trustedProxyCIDRs[1].String() != "192.168.1.0/24" {
		t.Fatalf("trustedProxyCIDRs = %v", cfg.trustedProxyCIDRs)
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("minimum values must validate: %v", err)
	}
	t.Setenv("PUBLIC_SUMMARY", "yes")
	if loadConfig().publicSummary {
		t.Error("PUBLIC_SUMMARY other than true must stay off")
	}
}

func TestConfigValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"retention below minimum", map[string]string{"RETENTION_DAYS": "364"}, "RETENTION_DAYS"},
		{"activity expiry below minimum", map[string]string{"ACTIVITY_EXPIRY_DAYS": "30"}, "ACTIVITY_EXPIRY_DAYS"},
		{"negative source limit", map[string]string{"INGEST_SOURCE_DAILY_LIMIT": "-1"}, "INGEST_SOURCE_DAILY_LIMIT"},
		{"non-integer retention", map[string]string{"RETENTION_DAYS": "forever"}, "RETENTION_DAYS"},
		{"non-integer limit", map[string]string{"INGEST_SOURCE_DAILY_LIMIT": "many"}, "INGEST_SOURCE_DAILY_LIMIT"},
		{"bad proxy CIDR", map[string]string{"TRUSTED_PROXY_CIDRS": "10.0.0.0/8,nonsense"}, "TRUSTED_PROXY_CIDRS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			err := loadConfig().validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate() = %v, want an error mentioning %s", err, tc.want)
			}
		})
	}

	// Every problem is reported, not just the first.
	clearConfigEnv(t)
	t.Setenv("RETENTION_DAYS", "1")
	t.Setenv("ACTIVITY_EXPIRY_DAYS", "1")
	err := loadConfig().validate()
	if err == nil || !strings.Contains(err.Error(), "RETENTION_DAYS") || !strings.Contains(err.Error(), "ACTIVITY_EXPIRY_DAYS") {
		t.Fatalf("validate() = %v, want both settings reported", err)
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("RETENTION_DAYS", "30")
	err := run(loadConfig())
	if err == nil || !strings.Contains(err.Error(), "RETENTION_DAYS") {
		t.Fatalf("run() = %v, want a configuration error", err)
	}
}

func TestDashboardRoutesRedirectUnauthenticatedToLogin(t *testing.T) {
	s := newServer(config{})
	t.Cleanup(func() { _ = s.store.close() })
	rec := httptest.NewRecorder()
	s.dashboardRoutes().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("dashboard / = %d with Location %q, want 303 to /login", rec.Code, rec.Header().Get("Location"))
	}
}

// freeAddr returns a loopback address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return addr
}

func TestServeStartsDashboardOnlyWithToken(t *testing.T) {
	dashAddr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, config{
			listen:          "127.0.0.1:0",
			dashboardListen: dashAddr,
			dashboardToken:  "tok",
		})
	}()

	// Live dashboard listener serves the login page after redirecting unauthenticated requests.
	var status int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+dashAddr+"/", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != http.StatusOK {
		t.Fatalf("dashboard listener status = %d, want 200 (login page after redirect)", status)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not shut down")
	}
}

func TestServeNoDashboardWithoutToken(t *testing.T) {
	dashAddr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, config{listen: "127.0.0.1:0", dashboardListen: dashAddr}) }()
	time.Sleep(200 * time.Millisecond)
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", dashAddr)
	if err == nil {
		_ = conn.Close()
		t.Error("dashboard listener is up although DASHBOARD_TOKEN is unset")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve returned %v after cancel, want nil", err)
	}
}

func TestServeBadDashboardAddr(t *testing.T) {
	err := serve(context.Background(), config{
		listen:          "127.0.0.1:0",
		dashboardListen: "not-an-addr",
		dashboardToken:  "tok",
	})
	if err == nil || !strings.Contains(err.Error(), "dashboard listen") {
		t.Fatalf("serve() = %v, want a dashboard listen error", err)
	}
}

func TestServeFailsWhenStoreCannotOpen(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	err := serve(context.Background(), config{listen: "127.0.0.1:0", dataDir: filepath.Join(blocker, "sub")})
	if err == nil || !strings.Contains(err.Error(), "open store") {
		t.Fatalf("serve() = %v, want an open store error", err)
	}
}
