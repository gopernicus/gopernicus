package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5/pgconn"
)

// instanceTimeout bounds every two-instance callback: generous, so a commit is
// never turned into a rollback by an expiring deadline, and finite, so a
// misrouted lock wait fails instead of hanging.
const instanceTimeout = 30 * time.Second

// openLiveInstance opens one *DB on POSTGRES_TEST_DSN with the given lock
// timeout, skipping loudly without the DSN.
func openLiveInstance(t *testing.T, lockTimeout time.Duration) *DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — two-instance Transact semantics NOT verified")
	}
	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn), LockTimeout: lockTimeout})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// liveInstance opens one *DB and creates its own probe table, returned
// explicitly so two instances in one test never share a table name.
func liveInstance(t *testing.T) (*DB, string) {
	t.Helper()
	db := openLiveInstance(t, 5*time.Second)
	table := fmt.Sprintf("transact_instance_%d", time.Now().UnixNano())
	if _, err := db.Exec(context.Background(), "CREATE TABLE "+table+" (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	return db, table
}

func count(t *testing.T, ctx context.Context, q Querier, table string) int {
	t.Helper()
	var n int
	if err := q.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func insert(ctx context.Context, q Querier, table, id string) error {
	_, err := q.Exec(ctx, "INSERT INTO "+table+" (id) VALUES ($1)", id)
	return err
}

func isolation(t *testing.T, ctx context.Context, q Querier) string {
	t.Helper()
	var level string
	if err := q.QueryRow(ctx, "SHOW transaction_isolation").Scan(&level); err != nil {
		t.Fatalf("SHOW transaction_isolation: %v", err)
	}
	return level
}

// TestTransact_TwoInstancesRouteAndNest: two Open calls on one DSN are two
// instances. Each routes to its own transaction or pool, B nests inside A
// (either method on either side), each transaction keeps its own isolation,
// A's writes inside B's callback still join A, the same instance still refuses
// nesting, and an outer error after the inner committed keeps B's row and
// drops every A row — no cross-instance atomicity.
func TestTransact_TwoInstancesRouteAndNest(t *testing.T) {
	const snapshotIsolation = "repeatable read"
	for _, arm := range []struct{ outer, inner string }{
		{"Transact", "Transact"},
		{"TransactSnapshot", "Transact"},
		{"Transact", "TransactSnapshot"},
	} {
		for _, outcome := range []string{"commit", "outer error"} {
			t.Run(arm.outer+"/"+arm.inner+"/"+outcome, func(t *testing.T) {
				a, t1 := liveInstance(t)
				b, t2 := liveInstance(t)
				ctx, cancel := context.WithTimeout(t.Context(), instanceTimeout)
				defer cancel()
				var defaultIsolation string
				if err := a.QueryRow(ctx, "SHOW default_transaction_isolation").Scan(&defaultIsolation); err != nil {
					t.Fatal(err)
				}
				outerTransact, wantA := a.Transact, defaultIsolation
				if arm.outer == "TransactSnapshot" {
					outerTransact, wantA = a.TransactSnapshot, snapshotIsolation
				}
				innerTransact, wantB := b.Transact, defaultIsolation
				if arm.inner == "TransactSnapshot" {
					innerTransact, wantB = b.TransactSnapshot, snapshotIsolation
				}
				refused := errors.New("outer refused after inner commit")

				err := outerTransact(ctx, func(ctx context.Context) error {
					if _, ok := b.TxFrom(ctx); ok {
						t.Fatal("b.TxFrom found a transaction inside a.Transact")
					}
					if err := insert(ctx, a.QuerierFrom(ctx), t1, "outer"); err != nil {
						return err
					}
					if n := count(t, ctx, a.QuerierFrom(ctx), t1); n != 1 {
						t.Fatalf("A's own transaction sees %d rows in T1, want 1", n)
					}
					if n := count(t, ctx, b.QuerierFrom(ctx), t1); n != 0 {
						t.Fatalf("B sees %d uncommitted T1 rows, want 0 (B ran on A's transaction)", n)
					}

					if err := innerTransact(ctx, func(ctx context.Context) error {
						txA, okA := a.TxFrom(ctx)
						txB, okB := b.TxFrom(ctx)
						if !okA || !okB || txA == txB {
							t.Fatalf("inner callback: a.TxFrom=%t b.TxFrom=%t distinct=%t, want both and distinct", okA, okB, txA != txB)
						}
						if got := isolation(t, ctx, b.QuerierFrom(ctx)); got != wantB {
							t.Fatalf("B's isolation inside the nest = %q, want %q", got, wantB)
						}
						if got := isolation(t, ctx, a.QuerierFrom(ctx)); got != wantA {
							t.Fatalf("A's isolation inside the nest = %q, want %q", got, wantA)
						}
						if err := insert(ctx, b.QuerierFrom(ctx), t2, "inner"); err != nil {
							return err
						}
						if err := insert(ctx, a.QuerierFrom(ctx), t1, "inside-b"); err != nil {
							return err
						}
						if err := b.Transact(ctx, func(context.Context) error { return nil }); !errors.Is(err, ErrNestedTransact) {
							t.Fatalf("b.Transact inside b = %v, want ErrNestedTransact", err)
						}
						if err := a.Transact(ctx, func(context.Context) error { return nil }); !errors.Is(err, ErrNestedTransact) {
							t.Fatalf("a.Transact inside a = %v, want ErrNestedTransact", err)
						}
						return nil
					}); err != nil {
						return fmt.Errorf("b.%s inside a: %w", arm.inner, err)
					}

					if n := count(t, ctx, b, t1); n != 0 {
						t.Fatalf("after B committed, B's pool sees %d T1 rows, want 0 (A's write joined B)", n)
					}
					if outcome == "outer error" {
						return refused
					}
					return nil
				})

				switch outcome {
				case "commit":
					if err != nil {
						t.Fatalf("outer: %v", err)
					}
					if n := count(t, ctx, a, t1); n != 2 {
						t.Fatalf("T1 after commit = %d rows, want 2", n)
					}
				default:
					if err != refused {
						t.Fatalf("outer = %v, want the identical refusal", err)
					}
					if n := count(t, ctx, a, t1); n != 0 {
						t.Fatalf("T1 after outer error = %d rows, want 0 (A's write inside B escaped A)", n)
					}
				}
				if n := count(t, ctx, b, t2); n != 1 {
					t.Fatalf("T2 = %d rows, want 1 (B commits on its own)", n)
				}
			})
		}
	}
}

// TestTransact_SameDatabaseInstancesLockWait pins the documented hazard: two
// instances over one database do not share a transaction, so B waits on a
// lock A holds — b.Transact nests inside a.Transact on its own connection —
// and, with lock_timeout set, fails fast instead of hanging. ACCESS EXCLUSIVE
// stands in for the authorization store's runWrite, whose LOCK TABLE iam_tuples
// IN SHARE ROW EXCLUSIVE MODE conflicts with the outer's ROW EXCLUSIVE the same
// way.
func TestTransact_SameDatabaseInstancesLockWait(t *testing.T) {
	a, t1 := liveInstance(t)
	b := openLiveInstance(t, 200*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), instanceTimeout)
	defer cancel()

	err := a.Transact(ctx, func(ctx context.Context) error {
		if err := insert(ctx, a.QuerierFrom(ctx), t1, "held"); err != nil {
			return err
		}
		err := b.Transact(ctx, func(ctx context.Context) error {
			_, err := b.QuerierFrom(ctx).Exec(ctx, "LOCK TABLE "+t1+" IN ACCESS EXCLUSIVE MODE")
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			t.Fatalf("b.Transact LOCK TABLE under A's held lock = %v, want SQLSTATE 55P03 (lock_timeout)", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("a.Transact: %v", err)
	}
}
