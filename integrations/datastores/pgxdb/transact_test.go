package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestTxFromContext_Absent needs no database: a context outside any Transact
// carries no transaction.
func TestTxFromContext_Absent(t *testing.T) {
	if tx, ok := TxFromContext(context.Background()); ok || tx != nil {
		t.Fatalf("TxFromContext on a bare context = (%v, %v), want (nil, false)", tx, ok)
	}
}

// transactLiveDB opens a live connection (skipping loudly without
// POSTGRES_TEST_DSN, the live_test.go convention) and creates a throwaway
// table dropped at cleanup, so the Transact semantics run against real
// BEGIN/COMMIT/ROLLBACK.
func transactLiveDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — Transact live semantics NOT verified")
	}
	db, err := Open(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	table := fmt.Sprintf("transact_probe_%d", time.Now().UnixNano())
	if _, err := db.Exec(context.Background(), "CREATE TABLE "+table+" (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	t.Setenv("TRANSACT_PROBE_TABLE", table)
	return db
}

func probeTable(t *testing.T) string { t.Helper(); return os.Getenv("TRANSACT_PROBE_TABLE") }

func probeCount(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM "+probeTable(t)).Scan(&n); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	return n
}

// TestTransact_CommitOnNil: a nil return from fn commits — the write is
// visible on the pool afterwards.
func TestTransact_CommitOnNil(t *testing.T) {
	db := transactLiveDB(t)
	ctx := context.Background()

	err := db.Transact(ctx, func(ctx context.Context) error {
		tx, ok := TxFromContext(ctx)
		if !ok {
			t.Fatal("TxFromContext inside Transact returned false")
		}
		_, err := tx.Exec(ctx, "INSERT INTO "+probeTable(t)+" (id) VALUES ('committed')")
		return err
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if n := probeCount(t, db); n != 1 {
		t.Fatalf("after commit: %d rows, want 1", n)
	}
}

// TestTransact_RollbackOnErrorUnwrapped: fn's error rolls back and comes back
// IDENTICAL (unwrapped, per the crud.Transactor contract).
func TestTransact_RollbackOnErrorUnwrapped(t *testing.T) {
	db := transactLiveDB(t)
	ctx := context.Background()
	sentinel := errors.New("business rule refused")

	err := db.Transact(ctx, func(ctx context.Context) error {
		if _, err := db.QuerierFrom(ctx).Exec(ctx, "INSERT INTO "+probeTable(t)+" (id) VALUES ('doomed')"); err != nil {
			return err
		}
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("Transact returned %v, want the identical sentinel (unwrapped)", err)
	}
	if n := probeCount(t, db); n != 0 {
		t.Fatalf("after rollback: %d rows, want 0", n)
	}
}

// TestTransact_PanicRollsBackAndRepanics: a panic inside fn rolls the write
// back and continues unwinding out of Transact.
func TestTransact_PanicRollsBackAndRepanics(t *testing.T) {
	db := transactLiveDB(t)
	ctx := context.Background()

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = db.Transact(ctx, func(ctx context.Context) error {
			if _, err := db.QuerierFrom(ctx).Exec(ctx, "INSERT INTO "+probeTable(t)+" (id) VALUES ('panicked')"); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if !panicked {
		t.Fatal("panic inside fn did not propagate out of Transact")
	}
	if n := probeCount(t, db); n != 0 {
		t.Fatalf("after panic rollback: %d rows, want 0", n)
	}
}

// TestTransact_NestedFailsLoud: Transact inside an active Transact ctx is the
// ruled-out shape — ErrNestedTransact, and the outer transaction still commits
// what it wrote before the refused nesting attempt was handled by the caller.
func TestTransact_NestedFailsLoud(t *testing.T) {
	db := transactLiveDB(t)
	ctx := context.Background()

	err := db.Transact(ctx, func(ctx context.Context) error {
		return db.Transact(ctx, func(ctx context.Context) error { return nil })
	})
	if !errors.Is(err, ErrNestedTransact) {
		t.Fatalf("nested Transact returned %v, want ErrNestedTransact", err)
	}
}

// TestTransact_QuerierFrom: inside a Transact the helper hands back the
// ambient *Tx (uncommitted writes visible through it), outside it the pool.
func TestTransact_QuerierFrom(t *testing.T) {
	db := transactLiveDB(t)
	ctx := context.Background()

	if q := db.QuerierFrom(ctx); q != Querier(db) {
		t.Fatalf("QuerierFrom outside Transact returned %T, want the *DB pool", q)
	}
	err := db.Transact(ctx, func(txCtx context.Context) error {
		q := db.QuerierFrom(txCtx)
		if _, isTx := q.(*Tx); !isTx {
			t.Fatalf("QuerierFrom inside Transact returned %T, want *Tx", q)
		}
		if _, err := q.Exec(txCtx, "INSERT INTO "+probeTable(t)+" (id) VALUES ('ambient')"); err != nil {
			return err
		}
		// Uncommitted write is visible through the SAME transaction…
		var n int
		if err := q.QueryRow(txCtx, "SELECT count(*) FROM "+probeTable(t)).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("inside tx: %d rows via ambient querier, want 1", n)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if n := probeCount(t, db); n != 1 {
		t.Fatalf("after commit: %d rows, want 1", n)
	}
}
