//go:build postgres

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/handlers"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

// Each instance has its own pool and auth middleware, like two API processes.
func runtimeReplicas(t *testing.T) [2]*db.Store {
	t.Helper()
	dsn := os.Getenv("GAMEPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GAMEPLANE_TEST_POSTGRES_DSN not set")
	}
	open := func(dsn string) *db.Store {
		s, err := db.Open(t.Context(), "postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	admin := open(dsn)
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "gp_runtime_" + hex.EncodeToString(suffix[:])
	if _, err := admin.DB.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.DB.ExecContext(context.WithoutCancel(t.Context()), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(dsn, "://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "search_path=" + schema
	} else {
		dsn += " search_path=" + schema
	}
	stores := [2]*db.Store{open(dsn), open(dsn)}
	if err := stores[0].Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return stores
}

func TestPostgresWaitingDemotionCannotRemoveLastManager(t *testing.T) {
	stores := runtimeReplicas(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for _, store := range stores {
		// Keep the session default across calls, as with a role's isolation setting.
		store.DB.SetMaxOpenConns(1)
		if _, err := store.DB.ExecContext(ctx, "SET default_transaction_isolation = 'repeatable read'"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stores[0].DB.ExecContext(ctx, `INSERT INTO users(id, username, display_name, email, role) VALUES
		(101, 'manager-a', '', '', 'admin'), (102, 'manager-b', '', '', 'admin')`); err != nil {
		t.Fatal(err)
	}
	var waiterPID int
	if err := stores[1].DB.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&waiterPID); err != nil {
		t.Fatal(err)
	}
	first, err := stores[0].BeginUserManagement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback() }()
	if _, err := first.ExecContext(ctx, "UPDATE users SET role='viewer' WHERE id=101"); err != nil {
		t.Fatal(err)
	}

	// This request was authenticated before the other replica's demotion commits.
	router := chi.NewRouter()
	handlers.MountUsers(router, stores[1], auth.NewSessionStore(stores[1]), nil)
	caller := auth.WithUser(ctx, &auth.User{ID: 101, Role: "admin"})
	req := httptest.NewRequestWithContext(caller, http.MethodPatch, "/users/102", strings.NewReader(`{"role":"viewer"}`))
	req.Header.Set("Accept", "application/problem+json")
	resp := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		router.ServeHTTP(resp, req)
	}()
	defer func() { cancel(); <-finished }()

	// Observe the exact two-key advisory lock before releasing the first writer.
	for {
		var waiting bool
		err := first.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM pg_locks WHERE pid = ?
			AND locktype = 'advisory' AND NOT granted AND objsubid = 2
			AND classid = hashtext(current_schema()::text)::oid
			AND objid = hashtext('gameplane.user-management')::oid)`, waiterPID).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-finished:
			t.Fatalf("peer returned before waiting on the lock: HTTP %d", resp.Code)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	<-finished
	count, err := stores[0].UserManagerCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "server.users.cannot_demote_last_manager") || count != 1 {
		t.Fatalf("waiting demotion: HTTP %d, body %s, remaining managers %d", resp.Code, resp.Body.String(), count)
	}
}

func TestPostgresReadOnlyFence(t *testing.T) {
	ctx := t.Context()
	fixture := runtimeReplicas(t)[0]
	var schema string
	if err := fixture.DB.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("GAMEPLANE_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal("invalid PostgreSQL fixture configuration")
	}
	cfg.RuntimeParams["search_path"] = schema
	var fenced atomic.Bool
	cfg.AfterConnect = func(ctx context.Context, conn *pgconn.PgConn) error {
		if !fenced.Load() {
			return nil
		}
		_, err := conn.Exec(ctx, "SET default_transaction_read_only=on").ReadAll()
		return err
	}
	dsn := stdlib.RegisterConnConfig(cfg)
	t.Cleanup(func() { stdlib.UnregisterConnConfig(dsn) })
	store, err := db.Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.DB.SetMaxOpenConns(1)
	var id int64
	if err := store.DB.QueryRowContext(ctx, "INSERT INTO users(username,display_name,email,role) VALUES ('fence-viewer','','','viewer') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionStore(store)
	token, _, err := sessions.Create(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	mountHealthRoutes(r, store)
	r.With(sessions.Authenticate).Get("/protected", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	check := func(path string, want int) {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "gameplane_session", Value: token})
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		if resp.Code != want {
			t.Fatalf("%s: got HTTP %d, want %d", path, resp.Code, want)
		}
	}
	check("/protected", http.StatusOK)
	check("/readyz/postgres-ha", http.StatusOK)
	// Fence the already pooled connection and all future dials without
	// changing a global setting on the shared test database.
	fenced.Store(true)
	if _, err := store.DB.ExecContext(ctx, "SET default_transaction_read_only=on"); err != nil {
		t.Fatal(err)
	}
	check("/protected", http.StatusServiceUnavailable)
	check("/readyz", http.StatusServiceUnavailable)
	check("/readyz/postgres-ha", http.StatusServiceUnavailable)
	check("/healthz", http.StatusOK)
	fenced.Store(false)
	check("/readyz/postgres-ha", http.StatusOK)
	check("/protected", http.StatusOK)
}

func TestPostgresRuntimeAcrossReplicas(t *testing.T) {
	stores := runtimeReplicas(t)
	ctx := t.Context()
	hash, err := auth.HashPassword(ctx, "replica-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stores[0].DB.ExecContext(ctx, `INSERT INTO users(username,display_name,email,pw_hash) VALUES ('qa','','',?)`, hash); err != nil {
		t.Fatal(err)
	}
	var muxes [2]http.Handler
	started, ended := make(chan struct{}), make(chan struct{})
	for i, s := range stores {
		runtime, err := auth.PostgresRuntime(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		sessions := auth.NewSessionStore(s)
		mux := http.NewServeMux()
		login := auth.NewLocal(s).HandleLogin(sessions, auth.NewRegistry(s, nil, nil, "", nil))
		mux.Handle("POST /login", auth.LoginLimiter.Middleware(login))
		mux.Handle("POST /logout", sessions.Authenticate(sessions.HandleLogout()))
		mux.Handle("GET /me", sessions.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })))
		revalidate := sessions.RevalidateStreams(func(*http.Request) bool { return true }, 20*time.Millisecond, rbac.StreamGrantValidator(nil))
		stream := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(ended)
		})
		mux.Handle("GET /stream", sessions.Authenticate(revalidate(stream)))
		mux.Handle("GET /hit", auth.LoginLimiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })))
		muxes[i] = runtime(mux)
	}
	call := func(replica int, method, path, ip, password string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"username":"qa","password":%q}`, password)
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			req.AddCookie(c)
			if c.Name == "gameplane_csrf" {
				req.Header.Set("X-Gameplane-CSRF", c.Value)
			}
		}
		rec := httptest.NewRecorder()
		muxes[replica].ServeHTTP(rec, req)
		return rec
	}
	// 32 simultaneous requests admitted to the same IP must share exactly 10 tokens.
	var group sync.WaitGroup
	var admitted, limited atomic.Int32
	for i := range 32 {
		group.Go(func() {
			switch call(i%2, "GET", "/hit", "192.0.2.1", "").Code {
			case http.StatusNoContent:
				admitted.Add(1)
			case http.StatusTooManyRequests:
				limited.Add(1)
			}
		})
	}
	group.Wait()
	if admitted.Load() != 10 || limited.Load() != 22 {
		t.Fatalf("race admitted=%d limited=%d", admitted.Load(), limited.Load())
	}
	login := call(0, "POST", "/login", "192.0.2.2", "replica-test")
	if login.Code != 200 {
		t.Fatalf("login status %d", login.Code)
	}
	response := login.Result()
	defer func() { _ = response.Body.Close() }()
	cookies := response.Cookies()
	for i := range 5 {
		if got := call(i%2, "POST", "/login", fmt.Sprintf("192.0.2.%d", 10+i), "wrong").Code; got != 401 {
			t.Fatalf("wrong password %d: %d", i, got)
		}
	}
	if got := call(1, "POST", "/login", "192.0.2.20", "replica-test").Code; got != 429 {
		t.Fatalf("exhausted user budget: %d", got)
	}
	if got := call(1, "POST", "/login", "192.0.2.21", "replica-test", &http.Cookie{Name: "gameplane_login_device", Value: "forged"}).Code; got != 429 {
		t.Fatalf("forged known-device cookie: %d", got)
	}
	if got := call(1, "POST", "/login", "192.0.2.22", "replica-test", cookies...).Code; got != 200 {
		t.Fatalf("recognized device on peer: %d", got)
	}
	if got := call(1, "GET", "/me", "192.0.2.23", "", cookies...).Code; got != 200 {
		t.Fatalf("cross-replica session: %d", got)
	}
	go call(0, "GET", "/stream", "192.0.2.23", "", cookies...)
	waitSignal(t, started)
	if got := call(1, "POST", "/logout", "192.0.2.23", "", cookies...).Code; got != 204 {
		t.Fatalf("logout on peer: %d", got)
	}
	waitSignal(t, ended)
	if got := call(0, "GET", "/me", "192.0.2.23", "", cookies...).Code; got != 401 {
		t.Fatalf("revoked session on original replica: %d", got)
	}
	if _, err := stores[1].DB.ExecContext(ctx, `INSERT INTO config(key,value) VALUES('auth','{"providers":[{"name":"local","kind":"local","enabled":false}]}') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	if got := call(0, "POST", "/login", "192.0.2.24", "replica-test", cookies...).Code; got != 403 {
		t.Fatalf("provider disabled on peer: %d", got)
	}
	if err := stores[0].Close(); err != nil {
		t.Fatal(err)
	}
	if got := call(0, "GET", "/hit", "192.0.2.40", "").Code; got != 503 {
		t.Fatalf("lost database created local budget: %d", got)
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replica")
	}
}

func TestPostgresWorkersRecoverWhenLeaderBackendDies(t *testing.T) {
	stores := runtimeReplicas(t)
	first, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	second, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()
	leader, stopped, standby, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	go runPanelWorkers(first, stores[0], 100*time.Millisecond, slog.Default(), func(ctx context.Context) {
		close(leader)
		<-ctx.Done()
		cancelFirst()
		close(stopped)
	})
	waitSignal(t, leader)
	go func() {
		defer close(finished)
		runPanelWorkers(second, stores[1], 100*time.Millisecond, slog.Default(), func(ctx context.Context) {
			close(standby)
			<-ctx.Done()
		})
	}()
	select {
	case <-standby:
		t.Fatal("both replicas became leader while original lock was held")
	case <-time.After(300 * time.Millisecond):
	}
	var killed bool
	err := stores[1].DB.QueryRowContext(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
		AND classid=1196447563 AND objid=hashtext(current_schema())::oid AND objsubid=2 AND granted`).Scan(&killed)
	if err != nil || !killed {
		t.Fatalf("terminate original leader connection: %v, %v", killed, err)
	}
	waitSignal(t, stopped)
	waitSignal(t, standby)
	// Killing only the lock's backend leaves the former replica's other
	// database connections and already-queued observations alive. Both
	// replicas may attempt the same sink after ownership changes.
	var sent atomic.Int32
	var attempts sync.WaitGroup
	for _, replica := range stores {
		attempts.Go(func() {
			won, err := replica.ClaimNotification(t.Context(), "", "server-uid", "271", "server.unhealthy", "ops")
			if err != nil {
				t.Error(err)
				return
			}
			if won {
				sent.Add(1)
			}
		})
	}
	attempts.Wait()
	if sent.Load() != 1 {
		t.Fatalf("dispatched one transition %d times after backend loss", sent.Load())
	}
	won, err := stores[1].ClaimNotification(t.Context(), "", "server-uid", "272", "server.unhealthy", "ops")
	if err != nil || !won {
		t.Fatalf("distinct object update lost after failover: %v", err)
	}
	cancelSecond()
	waitSignal(t, finished)
}
