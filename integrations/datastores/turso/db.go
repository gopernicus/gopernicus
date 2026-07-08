package turso

import (
	"context"
	"database/sql"
	"time"
)

// Scanner abstracts *sql.Row and *sql.Rows for shared scan helpers, so a store
// can scan a single-row QueryRow result and a Rows element through the same
// function.
type Scanner interface {
	Scan(dest ...any) error
}

// DB wraps a *sql.DB connected to a remote libSQL database. Driver errors from
// Exec/Query are mapped to sdk/errs sentinels via MapError.
type DB struct {
	db *sql.DB
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// Ping checks if the database is reachable.
func (d *DB) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// Underlying returns the underlying *sql.DB for advanced operations.
func (d *DB) Underlying() *sql.DB {
	return d.db
}

// StatusCheck returns nil if it can successfully talk to the database.
func StatusCheck(ctx context.Context, db *DB) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	return db.Ping(ctx)
}

// Exec executes a query that doesn't return rows.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, MapError(err)
	}
	return result, nil
}

// Query executes a query that returns rows.
func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, MapError(err)
	}
	return rows, nil
}

// QueryRow executes a query that returns at most one row. Map the Scan error
// with MapError to translate sql.ErrNoRows into errs.ErrNotFound.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}
