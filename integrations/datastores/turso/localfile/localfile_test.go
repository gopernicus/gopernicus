package localfile_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	_ "github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile"
)

// TestOpenAppliesTheLocalFileProfileOnEveryConnection proves the profile is a
// per-connection guarantee, not a one-off PRAGMA on whichever connection ran
// first: four goroutines each pin a distinct pooled connection and every one
// reports WAL journal mode, foreign keys on and the configured busy timeout. A
// second Open of the same file (the shape two independently booted processes
// need) works and re-applies the profile to its own pool.
func TestOpenAppliesTheLocalFileProfileOnEveryConnection(t *testing.T) {
	ctx := context.Background()
	cfg := tursodb.Config{
		// A nested, not-yet-existing directory: Open must create it.
		URL:          "file:" + filepath.Join(t.TempDir(), "nested", "auth.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 4,
		BusyTimeout:  5 * time.Second,
	}
	db, err := tursodb.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	assertPragmasOnFourConcurrentConnections(t, db)

	db2, err := tursodb.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("second Open (same file): %v", err)
	}
	defer db2.Close()
	assertPragmasOnFourConcurrentConnections(t, db2)
	if err := tursodb.StatusCheck(ctx, db2); err != nil {
		t.Fatalf("status check: %v", err)
	}
}

func assertPragmasOnFourConcurrentConnections(t *testing.T, db *tursodb.DB) {
	t.Helper()
	const n = 4
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Go(func() { errs <- checkConnectionPragmas(context.Background(), db) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

// checkConnectionPragmas pins one physical connection through a read
// transaction and reads the three pragmas off it before releasing it.
func checkConnectionPragmas(ctx context.Context, db *tursodb.DB) error {
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return fmt.Errorf("BeginRead: %w", err)
	}
	defer tx.Rollback()

	var journalMode string
	if err := tx.QueryRow(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("PRAGMA journal_mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("journal_mode = %q, want wal", journalMode)
	}
	var foreignKeys int
	if err := tx.QueryRow(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("PRAGMA foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
	var busyTimeout int
	if err := tx.QueryRow(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		return fmt.Errorf("PRAGMA busy_timeout: %w", err)
	}
	if busyTimeout != 5000 {
		return fmt.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}
	return nil
}
