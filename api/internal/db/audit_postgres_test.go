//go:build postgres

package db_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// postgresBlackhole drops server replies on real pgx sockets. The client can
// still send requests, and its next read must wait for a real socket deadline.
type postgresBlackhole struct {
	enabled atomic.Bool
	started chan struct{}
	once    sync.Once
}

type replyReader struct {
	io.Reader
	fault *postgresBlackhole
}

func (r replyReader) Read(p []byte) (int, error) {
	for {
		n, err := r.Reader.Read(p)
		if err != nil || !r.fault.enabled.Load() {
			return n, err
		}
		r.fault.once.Do(func() { close(r.fault.started) })
	}
}

func TestPostgresAuditBlackholeRecovery(t *testing.T) {
	ctx := t.Context()
	schemaStore := db.NewPostgresTestStore(t)
	var schema string
	if err := schemaStore.DB.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("GAMEPLANE_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal("parse PostgreSQL fixture configuration")
	}
	fault := &postgresBlackhole{started: make(chan struct{})}
	cfg.RuntimeParams["search_path"] = schema
	cfg.BuildFrontend = func(r io.Reader, w io.Writer) *pgproto3.Frontend {
		return pgproto3.NewFrontend(replyReader{r, fault}, w)
	}
	dsn := stdlib.RegisterConnConfig(cfg)
	t.Cleanup(func() { stdlib.UnregisterConnConfig(dsn) })
	store, err := db.Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	a := audit.New(store)
	write := func(ctx context.Context) error {
		return a.WriteSync(ctx, http.MethodPost, "/servers", "pending", "", http.StatusCreated)
	}
	verify := func(want int64) {
		t.Helper()
		result, err := a.Verify(ctx)
		if err != nil || !result.OK || result.Checked != want {
			t.Fatalf("verify: %+v, %v (want %d chained rows)", result, err, want)
		}
	}
	if err := write(ctx); err != nil {
		t.Fatal(err)
	}
	verify(1)

	fault.enabled.Store(true)
	defer fault.enabled.Store(false)
	first := make(chan error, 1)
	go func() { first <- write(ctx) }() // Exercise the auditor's own transaction timeout.
	select {
	case <-fault.started:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not reach the blackholed socket")
	}
	// The middleware has already run the mutation. Its audit work may wait
	// only until the HTTP deadline or queue rejection, preserving the 204.
	h := audit.Middleware(a)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	var requests sync.WaitGroup
	for range 64 {
		requests.Go(func() {
			reqCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequestWithContext(reqCtx, http.MethodPost, "/servers", nil))
			if w.Code != http.StatusNoContent {
				t.Errorf("post-mutation status = %d", w.Code)
			}
		})
	}
	pruneCtx, cancelPrune := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelPrune()
	if _, err := a.Prune(pruneCtx, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("retention succeeded during the outage")
	}
	drained := make(chan struct{})
	go func() {
		requests.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("mutations remained blocked past their HTTP deadlines")
	}
	if stats := store.DB.Stats(); stats.MaxOpenConnections != 16 || stats.OpenConnections > 1 {
		t.Fatalf("unbounded pool or more than one audit connection: %+v", stats)
	}
	select {
	case err := <-first:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blackholed write = %v, want deadline exceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer remained blocked after transaction budget expired")
	}

	fault.enabled.Store(false)
	verify(1)
	// Let retention write its checkpoint, then stall its DELETE. Both
	// changes must roll back together when the deadline expires.
	blocker, err := schemaStore.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err := blocker.ExecContext(ctx, "LOCK TABLE audit_events IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	lockedCtx, cancelLocked := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancelLocked()
	if n, err := a.Prune(lockedCtx, time.Now().Add(time.Hour)); n != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked prune = %d, %v", n, err)
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	verify(1)
	if n, err := a.Prune(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("prune after recovery = %d, %v", n, err)
	}
	if err := write(ctx); err != nil {
		t.Fatal(err)
	}
	verify(1) // New row chains off the checkpoint. Lost attempts were not replayed.
}

func TestPostgresAuditConcurrentReplicas(t *testing.T) {
	ctx := t.Context()
	store := db.NewPostgresTestStore(t)
	first, second := audit.New(store), audit.New(store)
	write := func(writer *audit.Auditor) error {
		return writer.WriteSync(ctx, http.MethodPost, "/servers", "test", "", http.StatusCreated)
	}
	if err := write(first); err != nil {
		t.Fatal(err)
	}
	// A relation lock held by another server connection must fence both
	// append and prune, while verification can still read the previous state.
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "LOCK TABLE audit_events IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	err = second.WriteSync(short, http.MethodPost, "/servers", "", "", 200)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfenced append: %v", err)
	}
	short, stop = context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = first.Prune(short, time.Now().Add(time.Hour))
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfenced prune: %v", err)
	}
	result, err := second.Verify(ctx)
	if err != nil || !result.OK || result.Checked != 1 {
		t.Fatalf("verify during lock: %+v, %v", result, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	for _, writer := range []*audit.Auditor{first, second} {
		writers.Go(func() {
			for range 20 {
				if err := write(writer); err != nil {
					t.Error(err)
					return
				}
				if state, err := writer.Verify(ctx); err != nil || !state.OK {
					t.Errorf("verify during append: %+v, %v", state, err)
					return
				}
			}
		})
	}
	writers.Wait()
	result, err = first.Verify(ctx)
	if err != nil || !result.OK || result.Checked != 41 {
		t.Fatalf("concurrent chain: %+v, %v", result, err)
	}
}
