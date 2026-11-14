//go:build postgres

package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func openPostgres(ctx context.Context, dsn string) (*Store, error) {
	connector, err := newRebindConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db := sql.OpenDB(connector)
	// Shared by API traffic, probes and the serialized audit writer.
	db.SetMaxOpenConns(16)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{DB: db, Driver: "postgres"}, nil
}

// pgxConn is the set of database/sql driver interfaces pgx's stdlib *Conn
// implements. rebindConn embeds it so database/sql still sees every one of
// them (context-aware exec/query, transactions with options, named-value
// checking, session reset) on the wrapper.
type pgxConn interface {
	Conn() *pgx.Conn
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.NamedValueChecker
	driver.SessionResetter
}

// rebindConnector wraps pgx's connector so every connection it hands to
// database/sql rewrites `?` placeholders to `$n` (see Rebind). This is the
// one place the Postgres backend rebinds: every query issued through the
// Store's *sql.DB, including those on a *sql.Tx, passes through it.
type rebindConnector struct {
	base driver.Connector
}

func newRebindConnector(dsn string) (driver.Connector, error) {
	dc, ok := stdlib.GetDefaultDriver().(driver.DriverContext)
	if !ok {
		return nil, errors.New("pgx stdlib driver does not implement driver.DriverContext")
	}
	base, err := dc.OpenConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgx connector: %w", err)
	}
	return rebindConnector{base: base}, nil
}

func (c rebindConnector) Connect(ctx context.Context) (driver.Conn, error) {
	// database/sql can open a replacement using the pool's lifetime context.
	// Bound that handshake even when the originating request has returned.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	pc, ok := conn.(pgxConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("pgx connection %T lacks a required database/sql driver interface", conn)
	}
	if err := pgconn.ValidateConnectTargetSessionAttrsReadWrite(ctx, pc.Conn().PgConn()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("check writable PostgreSQL connection: %w", err)
	}
	return rebindConn{pgxConn: pc}, nil
}

func (c rebindConnector) Driver() driver.Driver { return c.base.Driver() }

// rebindConn translates query placeholders and checks the database role
// when database/sql lends an idle connection to a new caller.
type rebindConn struct {
	pgxConn
}

// An existing connection can remain reachable after the database has been
// fenced read-only. Stale sessions and permissions must not authorize direct
// requests or streams while Kubernetes withdraws an unready API endpoint.
func (c rebindConn) ResetSession(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.pgxConn.ResetSession(ctx); err != nil {
		return err
	}
	if err := pgconn.ValidateConnectTargetSessionAttrsReadWrite(ctx, c.Conn().PgConn()); err != nil {
		// database/sql only discards a borrowed connection for ErrBadConn.
		// A replacement must pass the same check in Connect.
		return errors.Join(driver.ErrBadConn, err)
	}
	return nil
}

func (c rebindConn) Prepare(query string) (driver.Stmt, error) {
	return c.pgxConn.PrepareContext(context.Background(), Rebind("postgres", query))
}

func (c rebindConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.pgxConn.PrepareContext(ctx, Rebind("postgres", query))
}

func (c rebindConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.pgxConn.ExecContext(ctx, Rebind("postgres", query), args)
}

func (c rebindConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.pgxConn.QueryContext(ctx, Rebind("postgres", query), args)
}
