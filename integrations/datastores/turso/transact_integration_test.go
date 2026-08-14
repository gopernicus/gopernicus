//go:build integration

// Live Transact semantics against a real Turso/libSQL database. Run with:
//
//	go test -tags=integration ./...
//
// Requires TURSO_DATABASE_URL and TURSO_AUTH_TOKEN (the store modules'
// integration convention); absent those the tests skip loudly.
package turso

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// transactLiveDB opens a live connection (skipping loudly without the TURSO_*
// env) and creates a throwaway table dropped at cleanup.
func transactLiveDB(t *testing.T) (*DB, string) {
	t.Helper()
	url, token := os.Getenv("TURSO_DATABASE_URL"), os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — Transact live semantics NOT verified")
	}
	db, err := Open(Config{URL: url, AuthToken: token})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	table := fmt.Sprintf("transact_probe_%d", time.Now().UnixNano())
	if _, err := db.Exec(context.Background(), "CREATE TABLE "+table+" (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	return db, table
}

func probeCount(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	return n
}

func TestTransact_CommitOnNil(t *testing.T) {
	db, table := transactLiveDB(t)
	ctx := context.Background()

	err := db.Transact(ctx, func(ctx context.Context) error {
		tx, ok := TxFromContext(ctx)
		if !ok {
			t.Fatal("TxFromContext inside Transact returned false")
		}
		_, err := tx.Exec(ctx, "INSERT INTO "+table+" (id) VALUES ('committed')")
		return err
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if n := probeCount(t, db, table); n != 1 {
		t.Fatalf("after commit: %d rows, want 1", n)
	}
}

func TestTransact_RollbackOnErrorUnwrapped(t *testing.T) {
	db, table := transactLiveDB(t)
	ctx := context.Background()
	sentinel := errors.New("business rule refused")

	err := db.Transact(ctx, func(ctx context.Context) error {
		if _, err := db.QuerierFrom(ctx).Exec(ctx, "INSERT INTO "+table+" (id) VALUES ('doomed')"); err != nil {
			return err
		}
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("Transact returned %v, want the identical sentinel (unwrapped)", err)
	}
	if n := probeCount(t, db, table); n != 0 {
		t.Fatalf("after rollback: %d rows, want 0", n)
	}
}

func TestTransact_PanicRollsBackAndRepanics(t *testing.T) {
	db, table := transactLiveDB(t)
	ctx := context.Background()

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = db.Transact(ctx, func(ctx context.Context) error {
			if _, err := db.QuerierFrom(ctx).Exec(ctx, "INSERT INTO "+table+" (id) VALUES ('panicked')"); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if !panicked {
		t.Fatal("panic inside fn did not propagate out of Transact")
	}
	if n := probeCount(t, db, table); n != 0 {
		t.Fatalf("after panic rollback: %d rows, want 0", n)
	}
}

func TestTransact_NestedFailsLoud(t *testing.T) {
	db, _ := transactLiveDB(t)
	ctx := context.Background()

	err := db.Transact(ctx, func(ctx context.Context) error {
		return db.Transact(ctx, func(ctx context.Context) error { return nil })
	})
	if !errors.Is(err, ErrNestedTransact) {
		t.Fatalf("nested Transact returned %v, want ErrNestedTransact", err)
	}
}

func TestTransact_QuerierFrom(t *testing.T) {
	db, table := transactLiveDB(t)
	ctx := context.Background()

	if q := db.QuerierFrom(ctx); q != Querier(db) {
		t.Fatalf("QuerierFrom outside Transact returned %T, want the *DB pool", q)
	}
	err := db.Transact(ctx, func(txCtx context.Context) error {
		q := db.QuerierFrom(txCtx)
		if _, isTx := q.(*Tx); !isTx {
			t.Fatalf("QuerierFrom inside Transact returned %T, want *Tx", q)
		}
		_, err := q.Exec(txCtx, "INSERT INTO "+table+" (id) VALUES ('ambient')")
		return err
	})
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	if n := probeCount(t, db, table); n != 1 {
		t.Fatalf("after commit: %d rows, want 1", n)
	}
}
