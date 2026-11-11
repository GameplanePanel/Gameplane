package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// runPanelWorkers keeps the install's periodic jobs on one replica. A read-
// committed transaction pins its PostgreSQL backend even with transaction
// pooling. The transaction-scoped lock is released on rollback or backend
// disconnect. Keeping workers out of this transaction leaves their writes
// short-lived and avoids holding a repeatable-read snapshot through a tenure.
func runPanelWorkers(ctx context.Context, store *db.Store, interval time.Duration, logger *slog.Logger, workers func(context.Context)) {
	if store.Driver != "postgres" {
		workers(ctx)
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := workerTenure(ctx, store.DB, interval, workers); err != nil && ctx.Err() == nil {
			logger.Warn("panel worker election", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func workerTenure(ctx context.Context, database *sql.DB, interval time.Duration, workers func(context.Context)) error {
	// The transaction's context outlives the workers on an orderly shutdown.
	// Otherwise database/sql could release the lock while they were draining.
	txCtx, cancelTx := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTx()
	// Bound pool checkout and the initial BEGIN even during failover.
	deadline := time.AfterFunc(interval, cancelTx)
	tx, err := database.BeginTx(txCtx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: true})
	deadline.Stop()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	probe, cancelProbe := context.WithTimeout(ctx, interval)
	var elected bool
	// The first key reserves Gameplane worker election ('GPWK'); the second
	// partitions installations that use distinct schemas in one database.
	err = tx.QueryRowContext(probe, `SELECT pg_try_advisory_xact_lock(1196447563,hashtext(current_schema()))`).Scan(&elected)
	cancelProbe()
	if err != nil || !elected {
		return err
	}
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); workers(workerCtx) }()
	defer func() { cancelWorkers(); <-done }()
	heartbeat := time.NewTicker(interval)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return errors.New("panel workers stopped before their tenure ended")
		case <-heartbeat.C:
			ping, cancel := context.WithTimeout(ctx, interval)
			_, err := tx.ExecContext(ping, "SELECT 1")
			cancel()
			if err != nil {
				return err
			}
		}
	}
}
