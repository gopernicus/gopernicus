package pgxdb

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBeginReadSnapshot(t *testing.T) {
	db := transactLiveDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := db.BeginRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	query := "SELECT count(*) FROM " + probeTable(t)
	if err := tx.QueryRow(ctx, query).Scan(&count); err != nil || count != 0 {
		t.Fatalf("first read: %d, %v", count, err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO "+probeTable(t)+" VALUES ('new')"); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, query).Scan(&count); err != nil || count != 0 {
		t.Fatalf("snapshot changed: %d, %v", count, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := probeCount(t, db); got != 1 {
		t.Fatalf("committed count: %d", got)
	}

	tx, err = db.BeginRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM "+probeTable(t)); err == nil {
		t.Fatal("read transaction accepted a write")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := probeCount(t, db); got != 1 {
		t.Fatalf("read-only violation: %d", got)
	}

	acquired := db.pool.Stat().AcquiredConns()
	canceled, stop := context.WithCancel(ctx)
	tx, err = db.BeginRead(canceled)
	if err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	if err := tx.Commit(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled commit: %v", err)
	}
	if got := db.pool.Stat().AcquiredConns(); got != acquired {
		t.Fatalf("canceled transaction retained a connection: %d, want %d", got, acquired)
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("pool after cancellation: %v", err)
	}
}
