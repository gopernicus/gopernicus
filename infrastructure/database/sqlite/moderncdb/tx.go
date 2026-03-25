package moderncdb

import (
	"context"
	"database/sql"
	"fmt"
)

// Tx represents a database transaction with optional tracing.
type Tx struct {
	tx     *sql.Tx
	tracer *OTelTracer
}

// Begin starts a new transaction.
func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	return &Tx{tx: tx, tracer: d.tracer}, nil
}

// BeginImmediate starts a new transaction with IMMEDIATE lock.
// This is recommended for write transactions to avoid deadlocks.
func (d *DB) BeginImmediate(ctx context.Context) (*Tx, error) {
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("beginning immediate transaction: %w", err)
	}
	// SQLite requires explicit IMMEDIATE for write transactions.
	if _, err := tx.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("setting immediate mode: %w", err)
	}
	return &Tx{tx: tx, tracer: d.tracer}, nil
}

// Commit commits the transaction.
func (t *Tx) Commit() error {
	return t.tx.Commit()
}

// Rollback aborts the transaction.
func (t *Tx) Rollback() error {
	return t.tx.Rollback()
}

// Exec executes a query within the transaction.
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if t.tracer != nil && t.tracer.tracer != nil {
		operation := extractSQLOperation(query)
		var span spanHandle
		ctx, span = t.tracer.startSpan(ctx, operation, query)
		defer span.End()

		result, err := t.tx.ExecContext(ctx, query, args...)
		if err != nil {
			span.RecordError(err)
			return nil, handleSQLiteError(err)
		}

		if rowsAffected, raErr := result.RowsAffected(); raErr == nil && rowsAffected > 0 {
			span.SetRowsAffected(rowsAffected)
		}

		return result, nil
	}

	result, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, handleSQLiteError(err)
	}
	return result, nil
}

// Query executes a query within the transaction.
func (t *Tx) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if t.tracer != nil && t.tracer.tracer != nil {
		operation := extractSQLOperation(query)
		var span spanHandle
		ctx, span = t.tracer.startSpan(ctx, operation, query)
		defer span.End()

		rows, err := t.tx.QueryContext(ctx, query, args...)
		if err != nil {
			span.RecordError(err)
			return nil, handleSQLiteError(err)
		}
		return rows, nil
	}

	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, handleSQLiteError(err)
	}
	return rows, nil
}

// QueryRow executes a query that returns at most one row within the transaction.
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if t.tracer != nil && t.tracer.tracer != nil {
		operation := extractSQLOperation(query)
		var span spanHandle
		ctx, span = t.tracer.startSpan(ctx, operation, query)
		defer span.End()

		return t.tx.QueryRowContext(ctx, query, args...)
	}

	return t.tx.QueryRowContext(ctx, query, args...)
}

// InTx executes a function within a transaction.
// If the function returns an error, the transaction is rolled back.
// Otherwise, the transaction is committed.
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
