package pgxdb

import (
	"context"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a *pgxpool.Pool connected to a PostgreSQL database. Driver errors
// from Exec/Query are mapped to sdk/errs sentinels via MapError.
type DB struct {
	pool *pgxpool.Pool
}

// Close closes the connection pool. It returns nil to mirror the turso
// connector's Close signature (pgxpool.Pool.Close is itself infallible).
func (d *DB) Close() error {
	d.pool.Close()
	return nil
}

// Ping checks if the database is reachable.
func (d *DB) Ping(ctx context.Context) error {
	return d.pool.Ping(ctx)
}

// Underlying returns the underlying *pgxpool.Pool for advanced operations.
func (d *DB) Underlying() *pgxpool.Pool {
	return d.pool
}

// StatusCheck returns nil if it can successfully talk to the database.
func StatusCheck(ctx context.Context, db *DB) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	if err := db.Ping(ctx); err != nil {
		return err
	}
	// A readiness check runs a real statement, not just a pool ping — kept
	// symmetric with the turso twin, whose lazy remote driver requires it.
	var one int
	return db.QueryRow(ctx, "SELECT 1").Scan(&one)
}

// Exec executes a query that doesn't return rows.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	tag, err := d.pool.Exec(ctx, query, args...)
	if err != nil {
		return pgconn.CommandTag{}, MapError(err)
	}
	return tag, nil
}

// Query executes a query that returns rows.
func (d *DB) Query(ctx context.Context, query string, args ...any) (jackpgx.Rows, error) {
	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, MapError(err)
	}
	return rows, nil
}

// QueryRow executes a query that returns at most one row. Map the Scan error
// with MapError to translate jackpgx.ErrNoRows into errs.ErrNotFound.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) jackpgx.Row {
	return d.pool.QueryRow(ctx, query, args...)
}
