package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const rollbackTimeout = 5 * time.Second

// Tx represents a database transaction. Commit uses the Begin context; Rollback
// uses an independent context bounded to five seconds so cancellation can clean up.
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

// Commit commits using the Begin context and cleans up a failed commit.
func (t *Tx) Commit() error {
	err := t.ctx.Err()
	if err == nil {
		err = t.tx.Commit(t.ctx)
	}
	if err != nil {
		if rbErr := t.Rollback(); rbErr != nil && !errors.Is(rbErr, jackpgx.ErrTxClosed) {
			return fmt.Errorf("rollback failed: %w (commit error: %w)", rbErr, err)
		}
	}
	return err
}

// Rollback aborts the transaction even when the Begin context has been canceled.
func (t *Tx) Rollback() error {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	return t.tx.Rollback(ctx)
}

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
		if rbErr := t.Rollback(); err != nil && rbErr != nil && !errors.Is(rbErr, jackpgx.ErrTxClosed) {
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
