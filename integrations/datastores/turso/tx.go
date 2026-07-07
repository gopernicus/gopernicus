package turso

import (
	"context"
	"database/sql"
	"fmt"
)

// Tx represents a database transaction.
type Tx struct {
	tx *sql.Tx
}

// Begin starts a new transaction.
func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	return &Tx{tx: tx}, nil
}

// Commit commits the transaction.
func (t *Tx) Commit() error { return t.tx.Commit() }

// Rollback aborts the transaction.
func (t *Tx) Rollback() error { return t.tx.Rollback() }

// Exec executes a query within the transaction.
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, MapError(err)
	}
	return result, nil
}

// Query executes a query within the transaction.
func (t *Tx) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, MapError(err)
	}
	return rows, nil
}

// QueryRow executes a query that returns at most one row within the transaction.
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
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
