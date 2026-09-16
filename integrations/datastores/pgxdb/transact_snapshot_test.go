package pgxdb

import (
	"context"
	"errors"
	"testing"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
)

func TestTransactSnapshotCoherentReadsAndPendingWrites(t *testing.T) {
	db := transactLiveDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var escaped *Tx
	err := db.TransactSnapshot(ctx, func(ctx context.Context) error {
		tx, ok := TxFromContext(ctx)
		if !ok || db.QuerierFrom(ctx) != Querier(tx) {
			t.Fatal("snapshot transaction did not bind its ambient querier")
		}
		escaped = tx
		if suitable, err := tx.SnapshotIsolation(ctx); !suitable || err != nil {
			t.Fatalf("snapshot isolation: %t, %v", suitable, err)
		}
		query := "SELECT count(*) FROM " + probeTable(t)
		var count int
		if err := tx.QueryRow(ctx, query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("initial read: %d, %v", count, err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO "+probeTable(t)+" VALUES ('concurrent')"); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("snapshot observed a later commit: %d, %v", count, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO "+probeTable(t)+" VALUES ('own')"); err != nil {
			return err
		}
		if err := db.QuerierFrom(ctx).QueryRow(ctx, query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("pending write visibility: %d, %v", count, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("TransactSnapshot: %v", err)
	}
	if count := probeCount(t, db); count != 2 {
		t.Fatalf("committed rows = %d, want 2", count)
	}
	if suitable, err := escaped.SnapshotIsolation(ctx); suitable || !errors.Is(err, jackpgx.ErrTxClosed) {
		t.Fatalf("escaped transaction remained usable: %t, %v", suitable, err)
	}
}

func TestSnapshotIsolationActualTransaction(t *testing.T) {
	db := transactLiveDB(t)
	ctx := t.Context()
	var defaultIsolation string
	if err := db.QueryRow(ctx, "SHOW default_transaction_isolation").Scan(&defaultIsolation); err != nil {
		t.Fatal(err)
	}
	if err := db.Transact(ctx, func(ctx context.Context) error {
		tx, _ := TxFromContext(ctx)
		var actual string
		if err := tx.QueryRow(ctx, "SHOW transaction_isolation").Scan(&actual); err != nil {
			return err
		}
		if actual != defaultIsolation {
			t.Fatalf("ordinary Transact changed default isolation: %q, want %q", actual, defaultIsolation)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, level := range []jackpgx.TxIsoLevel{jackpgx.ReadUncommitted, jackpgx.ReadCommitted, jackpgx.RepeatableRead, jackpgx.Serializable} {
		t.Run(string(level), func(t *testing.T) {
			driver, err := db.pool.BeginTx(ctx, jackpgx.TxOptions{IsoLevel: level})
			if err != nil {
				t.Fatal(err)
			}
			tx := &Tx{tx: driver, ctx: ctx}
			defer tx.Rollback()
			got, err := tx.SnapshotIsolation(ctx)
			want := level == jackpgx.RepeatableRead || level == jackpgx.Serializable
			if err != nil || got != want {
				t.Fatalf("SnapshotIsolation = %t, %v; want %t, nil", got, err, want)
			}
		})
	}
}

func TestTransactSnapshotRollback(t *testing.T) {
	for _, scenario := range []string{"error", "panic", "canceled commit"} {
		t.Run(scenario, func(t *testing.T) {
			db := transactLiveDB(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("callback refused")
			panicValue := new(int)
			var err error
			var caught any
			func() {
				defer func() { caught = recover() }()
				err = db.TransactSnapshot(ctx, func(ctx context.Context) error {
					if _, err := db.QuerierFrom(ctx).Exec(ctx, "INSERT INTO "+probeTable(t)+" VALUES ('doomed')"); err != nil {
						return err
					}
					switch scenario {
					case "error":
						return cause
					case "panic":
						panic(panicValue)
					default:
						cancel()
						return nil
					}
				})
			}()
			switch scenario {
			case "error":
				if err != cause {
					t.Fatalf("callback error = %v, want original %v", err, cause)
				}
			case "panic":
				if caught != panicValue {
					t.Fatalf("panic = %v, want original %v", caught, panicValue)
				}
			case "canceled commit":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled commit = %v", err)
				}
			}
			if count := probeCount(t, db); count != 0 {
				t.Fatalf("rollback retained %d rows", count)
			}
			if got := db.pool.Stat().AcquiredConns(); got != 0 {
				t.Fatalf("rollback retained %d connections", got)
			}
		})
	}
}
