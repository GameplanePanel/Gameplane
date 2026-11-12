package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var errChainFull = errors.New("audit writer queue is full")

var chainWrites = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "gameplane_audit_store_operations_total",
	Help: "Audit database operations by operation and result (committed, rejected, timeout, canceled, failed).",
}, []string{"operation", "result"})

// chainTransaction serializes append and retention in this API process. Only
// the current writer can request a connection. Waiters run in their caller's
// goroutine and leave on cancellation. Failed attempts are never replayed.
func (a *Auditor) chainTransaction(ctx context.Context, operation string, budget time.Duration, write func(context.Context, *sql.Tx) error) (err error) {
	defer func() {
		result := "committed"
		switch {
		case errors.Is(err, errChainFull):
			result = "rejected"
		case errors.Is(err, context.DeadlineExceeded):
			result = "timeout"
		case errors.Is(err, context.Canceled):
			result = "canceled"
		case err != nil:
			result = "failed"
		}
		chainWrites.WithLabelValues(operation, result).Inc()
		if err != nil {
			// Driver errors can contain credentials, SQL values or event details.
			slog.Warn("audit database operation failed", "operation", operation, "result", result)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.admitted <- struct{}{}:
		defer func() { <-a.admitted }()
	default:
		return errChainFull
	}
	waitCtx, stopWaiting := context.WithTimeout(ctx, 250*time.Millisecond)
	select {
	case a.chain <- struct{}{}:
		defer func() { <-a.chain }()
		err = waitCtx.Err()
	case <-waitCtx.Done():
		err = waitCtx.Err()
	}
	stopWaiting()
	if err != nil {
		return err
	}

	txCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	// Some drivers report a socket timeout or sql.ErrTxDone when the
	// transaction context expires. Preserve the cause for callers and metrics.
	defer func() {
		if err != nil && txCtx.Err() != nil {
			err = txCtx.Err()
		}
	}()
	tx, err := a.db.DB.BeginTx(txCtx, nil)
	if err != nil {
		return fmt.Errorf("begin audit %s: %w", operation, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := write(txCtx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit %s: %w", operation, err)
	}
	return nil
}
