package turso

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestBeginReadSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target := url.URL{Scheme: "file", Path: filepath.Join(t.TempDir(), "snapshot.sqlite")}
	db, err := Open(ctx, Config{URL: target.String(), MaxOpenConns: 2, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE snapshot_probe (value INTEGER)", "INSERT INTO snapshot_probe VALUES (1)"} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var value int
	if err := tx.QueryRow(ctx, "SELECT value FROM snapshot_probe").Scan(&value); err != nil || value != 1 {
		t.Fatalf("first read: %d, %v", value, err)
	}
	// A writer must commit while this snapshot remains open.
	writer, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.Exec(ctx, "UPDATE snapshot_probe SET value=2"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT value FROM snapshot_probe").Scan(&value); err != nil || value != 1 {
		t.Fatalf("snapshot changed: %d, %v", value, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT value FROM snapshot_probe").Scan(&value); err != nil || value != 2 {
		t.Fatalf("committed read: %d, %v", value, err)
	}
}

func TestBeginReadCancellationAndDiscard(t *testing.T) {
	for _, failBegin := range []bool{false, true} {
		d := &txDriver{}
		if failBegin {
			d.beginErr = errors.New("unknown begin outcome")
		}
		db := newDriverDB(t, d)
		ctx, cancel := context.WithCancel(context.Background())
		tx, err := db.BeginRead(ctx)
		if failBegin {
			cancel()
			if !errors.Is(err, d.beginErr) {
				t.Fatalf("begin error: %v", err)
			}
			assertConnectionState(t, db, d, true)
			continue
		}
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		if err := tx.Commit(); !errors.Is(err, context.Canceled) {
			t.Fatalf("commit: %v", err)
		}
		assertConnectionState(t, db, d, false)
	}
}
