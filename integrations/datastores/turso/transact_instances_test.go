package turso

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// instanceTimeout bounds every two-instance callback: generous, because
// Tx.Commit uses the Begin context and an expired deadline would turn a commit
// into a rollback, and finite, so a misrouted wait fails instead of hanging.
const instanceTimeout = 30 * time.Second

// instanceProbe is created IDENTICALLY in both databases so a misroute shows
// as "B sees A's row" instead of a "no such table" error that proves nothing.
const instanceProbe = "CREATE TABLE probe (id TEXT PRIMARY KEY)"

func probeRows(t *testing.T, ctx context.Context, q Querier) int {
	t.Helper()
	var n int
	if err := q.QueryRow(ctx, "SELECT count(*) FROM probe").Scan(&n); err != nil {
		t.Fatalf("count probe: %v", err)
	}
	return n
}

func probeInsert(ctx context.Context, q Querier, id string) error {
	_, err := q.Exec(ctx, "INSERT INTO probe (id) VALUES (?)", id)
	return err
}

// TestTxFrom_PerInstance needs no database: each *DB reads only the slot it
// stashed, so two instances' transactions layer on one context.
func TestTxFrom_PerInstance(t *testing.T) {
	a, b := &DB{}, &DB{}
	txA, txB := &Tx{}, &Tx{}
	onlyA := context.WithValue(t.Context(), txKey{a}, txA)
	both := context.WithValue(onlyA, txKey{b}, txB)

	if tx, ok := a.TxFrom(both); !ok || tx != txA {
		t.Fatalf("a.TxFrom(both) = (%p, %t), want (%p, true)", tx, ok, txA)
	}
	if tx, ok := b.TxFrom(both); !ok || tx != txB {
		t.Fatalf("b.TxFrom(both) = (%p, %t), want (%p, true)", tx, ok, txB)
	}
	if tx, ok := b.TxFrom(onlyA); ok || tx != nil {
		t.Fatalf("b.TxFrom(onlyA) = (%v, %t), want (nil, false)", tx, ok)
	}
	if tx, ok := a.TxFrom(context.Background()); ok || tx != nil {
		t.Fatalf("a.TxFrom(bare) = (%v, %t), want (nil, false)", tx, ok)
	}
	if q := a.QuerierFrom(onlyA); q != Querier(txA) {
		t.Fatalf("a.QuerierFrom(onlyA) = %T %p, want A's transaction", q, q)
	}
	if q := b.QuerierFrom(onlyA); q != Querier(b) {
		t.Fatalf("b.QuerierFrom(onlyA) = %T %p, want B's pool", q, q)
	}
	if q := b.QuerierFrom(both); q != Querier(txB) {
		t.Fatalf("b.QuerierFrom(both) = %T %p, want B's transaction", q, q)
	}
	for name, ctx := range map[string]context.Context{"onlyA": onlyA, "both": both} {
		err := a.Transact(ctx, func(context.Context) error {
			t.Fatalf("%s: nested Transact invoked callback", name)
			return nil
		})
		if !errors.Is(err, ErrNestedTransact) {
			t.Fatalf("%s: a.Transact on A's own ctx = %v, want ErrNestedTransact", name, err)
		}
	}
}

// TestTransact_TwoInstancesRouteAndNest: two separate :memory: databases with
// the same table. Each instance routes to its own transaction or pool, B nests
// inside A, A's write inside B's callback still joins A, same-instance nesting
// still refuses, and an outer error after the inner committed keeps B's row and
// drops A's — no cross-instance atomicity. Never touch a's pool while A's
// transaction is open: MaxOpenConns(1) would block it until the deadline.
func TestTransact_TwoInstancesRouteAndNest(t *testing.T) {
	for _, outcome := range []string{"commit", "outer error"} {
		t.Run(outcome, func(t *testing.T) {
			a, b := newMemDB(t), newMemDB(t)
			ctx, cancel := context.WithTimeout(t.Context(), instanceTimeout)
			defer cancel()
			for _, db := range []*DB{a, b} {
				if _, err := db.Exec(ctx, instanceProbe); err != nil {
					t.Fatal(err)
				}
			}
			refused := errors.New("outer refused after inner commit")

			err := a.Transact(ctx, func(ctx context.Context) error {
				if _, ok := b.TxFrom(ctx); ok {
					t.Fatal("b.TxFrom found a transaction inside a.Transact")
				}
				if err := probeInsert(ctx, a.QuerierFrom(ctx), "outer"); err != nil {
					return err
				}
				if n := probeRows(t, ctx, a.QuerierFrom(ctx)); n != 1 {
					t.Fatalf("A's own transaction sees %d rows, want 1", n)
				}
				if n := probeRows(t, ctx, b.QuerierFrom(ctx)); n != 0 {
					t.Fatalf("B sees %d rows, want 0 (B ran on A's transaction)", n)
				}

				if err := b.Transact(ctx, func(ctx context.Context) error {
					txA, okA := a.TxFrom(ctx)
					txB, okB := b.TxFrom(ctx)
					if !okA || !okB || txA == txB {
						t.Fatalf("inner callback: a.TxFrom=%t b.TxFrom=%t distinct=%t, want both and distinct", okA, okB, txA != txB)
					}
					if err := probeInsert(ctx, b.QuerierFrom(ctx), "inner"); err != nil {
						return err
					}
					if err := probeInsert(ctx, a.QuerierFrom(ctx), "inside-b"); err != nil {
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
					return fmt.Errorf("b.Transact inside a: %w", err)
				}

				if n := probeRows(t, ctx, b); n != 1 {
					t.Fatalf("after B committed, B holds %d rows, want 1 (only its own)", n)
				}
				if n := probeRows(t, ctx, a.QuerierFrom(ctx)); n != 2 {
					t.Fatalf("A's transaction holds %d rows, want 2 (A's write inside B joined B)", n)
				}
				if outcome == "outer error" {
					return refused
				}
				return nil
			})

			wantA := 2
			if outcome == "commit" {
				if err != nil {
					t.Fatalf("outer: %v", err)
				}
			} else {
				if err != refused {
					t.Fatalf("outer = %v, want the identical refusal", err)
				}
				wantA = 0
			}
			if n := probeRows(t, ctx, a); n != wantA {
				t.Fatalf("A after %s = %d rows, want %d", outcome, n, wantA)
			}
			if n := probeRows(t, ctx, b); n != 1 {
				t.Fatalf("B after %s = %d rows, want 1 (B commits on its own)", outcome, n)
			}
		})
	}
}

// TestTransact_SameFileInstancesBusy pins the documented hazard: two Open
// calls on one SQLite file are two instances, so B's BEGIN IMMEDIATE inside
// A's transaction waits busy_timeout on A's RESERVED lock and fails busy.
func TestTransact_SameFileInstancesBusy(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), instanceTimeout)
	defer cancel()
	target := url.URL{Scheme: "file", Path: filepath.Join(t.TempDir(), "shared.sqlite")}
	open := func() *DB {
		db, err := Open(ctx, Config{URL: sdk.Secret(target.String()), BusyTimeout: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	a, b := open(), open()
	if _, err := a.Exec(ctx, instanceProbe); err != nil {
		t.Fatal(err)
	}

	err := a.Transact(ctx, func(ctx context.Context) error {
		if err := probeInsert(ctx, a.QuerierFrom(ctx), "held"); err != nil {
			return err
		}
		err := b.Transact(ctx, func(ctx context.Context) error {
			return probeInsert(ctx, b.QuerierFrom(ctx), "blocked")
		})
		var sqliteError interface{ Code() int }
		if !errors.As(err, &sqliteError) || sqliteError.Code()&0xff != 5 {
			t.Fatalf("b.Transact inside a.Transact on one file = %v, want SQLITE_BUSY", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("a.Transact: %v", err)
	}
}
