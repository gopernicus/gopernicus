package pgxdb

import (
	"context"
	"errors"
	"testing"

	jackpgx "github.com/jackc/pgx/v5"
)

type isolationTx struct {
	jackpgx.Tx
	value string
	err   error
	ctx   context.Context
	query string
}

func (t *isolationTx) QueryRow(ctx context.Context, query string, _ ...any) jackpgx.Row {
	t.ctx, t.query = ctx, query
	return isolationRow{value: t.value, err: t.err}
}

type isolationRow struct {
	value string
	err   error
}

func (r isolationRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = r.value
	return nil
}

func TestSnapshotIsolation(t *testing.T) {
	for _, level := range []string{"read uncommitted", "read committed", "repeatable read", "serializable", "unknown"} {
		t.Run(level, func(t *testing.T) {
			driver := &isolationTx{value: level}
			tx := &Tx{tx: driver}
			ctx := t.Context()
			got, err := tx.SnapshotIsolation(ctx)
			want := level == "repeatable read" || level == "serializable"
			if err != nil || got != want {
				t.Fatalf("SnapshotIsolation = %t, %v; want %t, nil", got, err, want)
			}
			if driver.ctx != ctx || driver.query != "SHOW transaction_isolation" {
				t.Fatalf("isolation was not queried on the bound transaction: ctx=%v query=%q", driver.ctx, driver.query)
			}
			// A later call must read the current transaction setting again.
			driver.value = "read committed"
			if got, err := tx.SnapshotIsolation(ctx); got || err != nil {
				t.Fatalf("isolation was cached: %t, %v", got, err)
			}
		})
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, jackpgx.ErrTxClosed, errors.New("read failed")} {
		t.Run(cause.Error(), func(t *testing.T) {
			tx := &Tx{tx: &isolationTx{err: cause}}
			got, err := tx.SnapshotIsolation(t.Context())
			if got || !errors.Is(err, cause) {
				t.Fatalf("SnapshotIsolation = %t, %v; want false, %v", got, err, cause)
			}
		})
	}
}

func TestTransactSnapshotNestingRejectedBeforeBegin(t *testing.T) {
	db := &DB{}
	ctx := context.WithValue(t.Context(), txCtxKey{}, &Tx{})
	for _, transact := range []func(context.Context, func(context.Context) error) error{db.Transact, db.TransactSnapshot} {
		err := transact(ctx, func(context.Context) error {
			t.Fatal("nested transaction invoked callback")
			return nil
		})
		if !errors.Is(err, ErrNestedTransact) {
			t.Fatalf("nested transaction = %v, want ErrNestedTransact", err)
		}
	}
}
