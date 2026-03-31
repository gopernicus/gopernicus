package pgxdb

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---------------------------------------------------------------------------
// Mock types for InTx tests
// ---------------------------------------------------------------------------

type mockTx struct {
	committed  bool
	rolledBack bool
	commitErr  error
}

func (m *mockTx) Begin(ctx context.Context) (pgx.Tx, error)                          { return m, nil }
func (m *mockTx) Commit(ctx context.Context) error                                    { m.committed = true; return m.commitErr }
func (m *mockTx) Rollback(ctx context.Context) error                                  { m.rolledBack = true; return nil }
func (m *mockTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (m *mockTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (m *mockTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
func (m *mockTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults  { return nil }
func (m *mockTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (m *mockTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (m *mockTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (m *mockTx) Conn() *pgx.Conn { return nil }

type mockQuerier struct {
	tx       *mockTx
	beginErr error
}

func (m *mockQuerier) Begin(ctx context.Context) (pgx.Tx, error) {
	if m.beginErr != nil {
		return nil, m.beginErr
	}
	return m.tx, nil
}
func (m *mockQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (m *mockQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (m *mockQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
func (m *mockQuerier) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults  { return nil }

// ---------------------------------------------------------------------------
// InTx tests
// ---------------------------------------------------------------------------

func TestInTx_CommitsOnSuccess(t *testing.T) {
	tx := &mockTx{}
	q := &mockQuerier{tx: tx}

	err := InTx(context.Background(), q, func(pgx.Tx) error {
		return nil
	})

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !tx.committed {
		t.Error("transaction was not committed")
	}
	if tx.rolledBack {
		t.Error("transaction was rolled back unexpectedly")
	}
}

func TestInTx_RollsBackOnError(t *testing.T) {
	tx := &mockTx{}
	q := &mockQuerier{tx: tx}
	fnErr := errors.New("business logic failed")

	err := InTx(context.Background(), q, func(pgx.Tx) error {
		return fnErr
	})

	if !errors.Is(err, fnErr) {
		t.Fatalf("err = %v, want %v", err, fnErr)
	}
	if tx.committed {
		t.Error("transaction was committed unexpectedly")
	}
	if !tx.rolledBack {
		t.Error("transaction was not rolled back")
	}
}

func TestInTx_ReturnsBeginError(t *testing.T) {
	beginErr := errors.New("connection refused")
	q := &mockQuerier{beginErr: beginErr}

	err := InTx(context.Background(), q, func(pgx.Tx) error {
		t.Fatal("fn should not be called when Begin fails")
		return nil
	})

	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !errors.Is(err, beginErr) {
		t.Errorf("err = %v, want wrapping %v", err, beginErr)
	}
}

func TestInTx_ReturnsCommitError(t *testing.T) {
	commitErr := errors.New("serialization failure")
	tx := &mockTx{commitErr: commitErr}
	q := &mockQuerier{tx: tx}

	err := InTx(context.Background(), q, func(pgx.Tx) error {
		return nil
	})

	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !errors.Is(err, commitErr) {
		t.Errorf("err = %v, want wrapping %v", err, commitErr)
	}
}
