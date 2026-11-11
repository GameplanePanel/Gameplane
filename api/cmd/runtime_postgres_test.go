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

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
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
		revalidate := sessions.RevalidateStreams(func(*http.Request) bool { return true }, 20*time.Millisecond)
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
	cancelSecond()
	waitSignal(t, finished)
}
