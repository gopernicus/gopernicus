package pgxdb

import (
	"context"
	"fmt"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tx represents a database transaction. The context captured at Begin is reused
// by Commit/Rollback so their signatures mirror the turso connector's no-arg
// forms (database/sql captures the begin context the same way internally).
type Tx struct {
	tx  jackpgx.Tx
	ctx context.Context
}

// Begin starts a new transaction.
func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	return &Tx{tx: tx, ctx: ctx}, nil
}

// Commit commits the transaction.
func (t *Tx) Commit() error { return t.tx.Commit(t.ctx) }

// Rollback aborts the transaction.
func (t *Tx) Rollback() error { return t.tx.Rollback(t.ctx) }

// Exec executes a query within the transaction.
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	tag, err := t.tx.Exec(ctx, query, args...)
	if err != nil {
		return pgconn.CommandTag{}, MapError(err)
	}
	return tag, nil
}

// Query executes a query within the transaction.
func (t *Tx) Query(ctx context.Context, query string, args ...any) (jackpgx.Rows, error) {
	rows, err := t.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, MapError(err)
	}
	return rows, nil
}

// QueryRow executes a query that returns at most one row within the transaction.
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) jackpgx.Row {
	return t.tx.QueryRow(ctx, query, args...)
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
