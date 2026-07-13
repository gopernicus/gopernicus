package turso

import (
	"context"
	"database/sql"
	"fmt"
)

// Tx represents a database transaction pinned to a single connection.
type Tx struct {
	conn *sql.Conn
	// tracer is inherited from the DB that began the transaction so opted-in
	// query logging covers transaction-path statements too.
	tracer *loggingQueryTracer
}

// Begin starts a new write-intent transaction.
//
// It issues BEGIN IMMEDIATE rather than the driver's default BEGIN (DEFERRED).
// A DEFERRED transaction starts as a reader and only tries to upgrade to the
// write lock at its first write; under a concurrent read-then-write CAS
// (SELECT auth_revision … then UPDATE), both losers' lock upgrades fail and
// libSQL/sqld surfaces a raw "database is locked" (SQLITE_BUSY) instead of
// serializing. BEGIN IMMEDIATE takes the write intent up front so sqld
// serializes contending transactions: the loser then reads the winner's
// committed state and the store's own CAS returns sdk.ErrConflict — matching
// the pgx SELECT … FOR UPDATE behavior. The libsql driver hardcodes plain
// BEGIN and rejects non-default isolation via sql.TxOptions, so the mode is
// driven explicitly over a pinned *sql.Conn.
func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("beginning transaction: %w", MapError(err))
	}
	return &Tx{conn: conn, tracer: d.tracer}, nil
}

// Commit commits the transaction and releases the pinned connection.
func (t *Tx) Commit() error {
	_, err := t.conn.ExecContext(context.Background(), "COMMIT")
	closeErr := t.conn.Close()
	if err != nil {
		return MapError(err)
	}
	return closeErr
}

// Rollback aborts the transaction and releases the pinned connection.
func (t *Tx) Rollback() error {
	_, err := t.conn.ExecContext(context.Background(), "ROLLBACK")
	closeErr := t.conn.Close()
	if err != nil {
		return MapError(err)
	}
	return closeErr
}

// Exec executes a query within the transaction.
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if t.tracer != nil {
		t.tracer.traceQuery(query, args)
	}
	result, err := t.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, MapError(err)
	}
	return result, nil
}

// Query executes a query within the transaction.
func (t *Tx) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if t.tracer != nil {
		t.tracer.traceQuery(query, args)
	}
	rows, err := t.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, MapError(err)
	}
	return rows, nil
}

// QueryRow executes a query that returns at most one row within the transaction.
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if t.tracer != nil {
		t.tracer.traceQuery(query, args)
	}
	return t.conn.QueryRowContext(ctx, query, args...)
}

// InTx runs fn within a transaction, rolling back on error and committing
// otherwise.
func (d *DB) InTx(ctx context.Context, fn func(tx *Tx) error) error {
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("rollback failed: %v (original error: %w)", rbErr, err)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}
	return nil
}
