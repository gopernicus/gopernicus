package turso

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

func runTransaction(t *testing.T, db *DB, mode string, ctx context.Context, fn func(*Tx) error) error {
	t.Helper()
	if mode == "InTx" {
		return db.InTx(ctx, fn)
	}
	return db.Transact(ctx, func(txctx context.Context) error {
		tx, ok := TxFromContext(txctx)
		if !ok || db.QuerierFrom(txctx) != tx {
			t.Fatal("transaction not available through callback context")
		}
		return fn(tx)
	})
}

func TestTransactionSQLiteLifecycle(t *testing.T) {
	refused := errors.New("business rule refused")
	panicValue := new(int)
	for _, mode := range []string{"InTx", "Transact"} {
		for _, scenario := range []string{"commit", "error", "panic", "canceled commit", "canceled error"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				db := newMemDB(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if _, err := db.Exec(ctx, "CREATE TABLE probe (id INTEGER)"); err != nil {
					t.Fatal(err)
				}
				var err error
				var panicked any
				func() {
					defer func() { panicked = recover() }()
					err = runTransaction(t, db, mode, ctx, func(tx *Tx) error {
						if _, err := tx.Exec(ctx, "INSERT INTO probe VALUES (1)"); err != nil {
							return err
						}
						switch scenario {
						case "error":
							return refused
						case "panic":
							panic(panicValue)
						case "canceled commit":
							cancel()
						case "canceled error":
							cancel()
							return refused
						}
						return nil
					})
				}()
				switch scenario {
				case "commit":
					if err != nil {
						t.Fatalf("commit: %v", err)
					}
				case "error", "canceled error":
					if err != refused {
						t.Fatalf("callback error = %v, want identical original", err)
					}
				case "panic":
					if panicked != panicValue {
						t.Fatalf("panic = %v, want original value", panicked)
					}
				case "canceled commit":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("commit error = %v, want context.Canceled", err)
					}
				}
				if scenario != "panic" && panicked != nil {
					t.Fatalf("unexpected panic: %v", panicked)
				}
				if db.db.Stats().InUse != 0 {
					t.Fatal("transaction kept its pooled connection")
				}
				checkCtx, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				var count int
				if err := db.QueryRow(checkCtx, "SELECT count(*) FROM probe").Scan(&count); err != nil {
					t.Fatal(err)
				}
				want := 0
				if scenario == "commit" {
					want = 1
				}
				if count != want {
					t.Fatalf("visible rows = %d, want %d", count, want)
				}
				next, err := db.Begin(checkCtx)
				if err != nil {
					t.Fatalf("Begin after finalization: %v", err)
				}
				if err := next.Rollback(); err != nil {
					t.Fatalf("rollback next transaction: %v", err)
				}
			})
		}
	}
}

func TestTransactionSQLiteFailedCommit(t *testing.T) {
	for _, mode := range []string{"InTx", "Transact", "Commit"} {
		t.Run(mode, func(t *testing.T) {
			db := newMemDB(t)
			ctx := context.Background()
			for _, query := range []string{
				"PRAGMA foreign_keys=ON",
				"CREATE TABLE parent (id INTEGER PRIMARY KEY)",
				"CREATE TABLE child (parent_id INTEGER REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED)",
			} {
				if _, err := db.Exec(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			insert := func(tx *Tx) error {
				_, err := tx.Exec(ctx, "INSERT INTO child VALUES (999)")
				return err
			}
			var err error
			if mode == "Commit" {
				var tx *Tx
				tx, err = db.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := insert(tx); err != nil {
					t.Fatal(err)
				}
				err = tx.Commit()
			} else {
				err = runTransaction(t, db, mode, ctx, insert)
			}
			if !errors.Is(err, sdk.ErrInvalidReference) {
				t.Fatalf("deferred constraint error = %v, want ErrInvalidReference", err)
			}
			if db.db.Stats().InUse != 0 {
				t.Fatal("failed commit kept its pooled connection")
			}
			var count int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM child").Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed commit left visible uncommitted rows: %d, %v", count, err)
			}
			next, err := db.Begin(ctx)
			if err != nil {
				t.Fatalf("Begin after failed commit: %v", err)
			}
			if err := next.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
