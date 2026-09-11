package turso

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

const rollbackTimeout = 5 * time.Second

// Tx represents a database transaction pinned to a single connection.
type Tx struct {
	conn *sql.Conn
	ctx  context.Context
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
		// A transport error may leave BEGIN's outcome unknown.
		if discardErr := discardConn(conn); discardErr != nil {
			return nil, fmt.Errorf("discard failed: %w (begin error: %w)", discardErr, MapError(err))
		}
		return nil, fmt.Errorf("beginning transaction: %w", MapError(err))
	}
	return &Tx{conn: conn, ctx: ctx, tracer: d.tracer}, nil
}

// Commit uses the Begin context. A failed commit is rolled back before releasing
// the pinned connection; failed cleanup discards the physical connection.
func (t *Tx) Commit() error {
	err := t.ctx.Err()
	if err == nil {
		_, err = t.conn.ExecContext(t.ctx, "COMMIT")
	}
	if err != nil {
		if rbErr := t.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrConnDone) {
			return fmt.Errorf("rollback failed: %w (commit error: %w)", rbErr, MapError(err))
		}
		return MapError(err)
	}
	return t.conn.Close()
}

// Rollback uses an independent five-second context, then releases the pinned
// connection. A failed rollback discards the physical connection from the pool.
func (t *Tx) Rollback() error {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	_, err := t.conn.ExecContext(ctx, "ROLLBACK")
	if err != nil {
		if discardErr := discardConn(t.conn); discardErr != nil {
			return fmt.Errorf("discard failed: %w (rollback error: %w)", discardErr, MapError(err))
		}
		return MapError(err)
	}
	return t.conn.Close()
}

func discardConn(conn *sql.Conn) error {
	// Returning ErrBadConn through Raw tells database/sql to discard the driver
	// connection. Close alone would return a potentially open transaction to it.
	err := conn.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return nil
	}
	return err
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

// InTx commits when fn returns nil and rolls back on errors or panics. Callback
// errors remain unchanged unless cleanup also fails; panic values are preserved.
func (d *DB) InTx(ctx context.Context, fn func(tx *Tx) error) error {
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}

	return tx.run(fn)
}

func (t *Tx) run(fn func(*Tx) error) (err error) {
	defer func() {
		if rbErr := t.Rollback(); err != nil && rbErr != nil && !errors.Is(rbErr, sql.ErrConnDone) {
			err = fmt.Errorf("rollback failed: %w (original error: %w)", rbErr, err)
		}
	}()
	if err = fn(t); err != nil {
		return err
	}
	if err = t.Commit(); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}
	return nil
}
