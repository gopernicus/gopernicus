//go:build integration

package turso

import (
	"context"
	"testing"
	"time"
)

func TestBeginReadLiveSnapshot(t *testing.T) {
	db, table := transactLiveDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := db.BeginRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	query := "SELECT count(*) FROM " + table
	if err := tx.QueryRow(ctx, query).Scan(&count); err != nil || count != 0 {
		t.Fatalf("initial snapshot: %d/%v", count, err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO "+table+" VALUES ('committed')"); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, query).Scan(&count); err != nil || count != 0 {
		t.Fatalf("snapshot changed: %d/%v", count, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := probeCount(t, db, table); got != 1 {
		t.Fatalf("committed count: %d", got)
	}
}
