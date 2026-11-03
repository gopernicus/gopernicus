package pgxdb

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// lockStream is a two-file stream whose first file sleeps, so a concurrent
// runner reliably overlaps the first one's transaction.
func lockStream(sleep time.Duration) fstest.MapFS {
	return fstest.MapFS{
		"migrations/0001_lock_probe.sql": {Data: []byte(fmt.Sprintf("SELECT pg_catalog.pg_sleep(%f); CREATE TABLE lock_probe (id TEXT PRIMARY KEY);", sleep.Seconds()))},
		"migrations/0002_lock_probe.sql": {Data: []byte("CREATE TABLE lock_probe_two (id TEXT PRIMARY KEY);")},
	}
}

func ledgerCount(t *testing.T, db *DB, schema Schema) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM "+schema.Table("schema_migrations")).Scan(&n); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	return n
}

// runConcurrently starts each runner behind one barrier and returns their errors.
func runConcurrently(runners ...func() error) []error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, len(runners))
	for i, run := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = run()
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// TestLive_RunMigrations_ConcurrentRunnersSerialize: two runners on one fresh
// schema both succeed and every file is applied exactly once (#298).
func TestLive_RunMigrations_ConcurrentRunnersSerialize(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	db := openLive(t, dsn)
	s := disposableSchema(t, db, "pgxdb_lock_serial")
	other := openLive(t, dsn)
	fsys := lockStream(500 * time.Millisecond)
	errs := runConcurrently(
		func() error { return RunMigrations(ctx, db, fsys, "migrations", WithSchema(s)) },
		func() error { return RunMigrations(ctx, other, fsys, "migrations", WithSchema(s)) },
	)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("runner %d: %v", i, err)
		}
	}
	if n := ledgerCount(t, db, s); n != 2 {
		t.Fatalf("ledger rows = %d, want 2 (each file once)", n)
	}
}

// TestLive_RunMigrations_WaitIgnoresPoolLockTimeout: with a 50ms pool
// lock_timeout, the second runner waits out the first's slow file instead of
// failing, and logs that it waited.
func TestLive_RunMigrations_WaitIgnoresPoolLockTimeout(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	db := openLive(t, dsn)
	s := disposableSchema(t, db, "pgxdb_lock_timeout")
	strict, err := Open(ctx, Config{DSN: sdk.Secret(dsn), LockTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer strict.Close()
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	fsys := lockStream(time.Second)
	first := make(chan error, 1)
	go func() { first <- RunMigrations(ctx, db, fsys, "migrations", WithSchema(s)) }()
	time.Sleep(200 * time.Millisecond) // the first runner holds the lock, sleeping in 0001
	if err := RunMigrations(ctx, strict, fsys, "migrations", WithSchema(s)); err != nil {
		t.Fatalf("second runner with a 50ms lock_timeout: %v", err)
	}
	if err := <-first; err != nil {
		t.Fatalf("first runner: %v", err)
	}
	if !strings.Contains(logs.String(), "waited for a concurrent runner") {
		t.Fatalf("no wait line logged:\n%s", logs.String())
	}
	if n := ledgerCount(t, db, s); n != 2 {
		t.Fatalf("ledger rows = %d, want 2", n)
	}
}

// TestLive_RunMigrations_SchemasDoNotWaitOnEachOther: runners on two schemas
// overlap instead of queueing.
func TestLive_RunMigrations_SchemasDoNotWaitOnEachOther(t *testing.T) {
	dsn := schemaLiveDSN(t)
	ctx := context.Background()
	db := openLive(t, dsn)
	a := disposableSchema(t, db, "pgxdb_lock_a")
	b := disposableSchema(t, db, "pgxdb_lock_b")
	other := openLive(t, dsn)
	fsys := lockStream(time.Second)
	start := time.Now()
	errs := runConcurrently(
		func() error { return RunMigrations(ctx, db, fsys, "migrations", WithSchema(a)) },
		func() error { return RunMigrations(ctx, other, fsys, "migrations", WithSchema(b)) },
	)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("runner %d: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 1800*time.Millisecond {
		t.Fatalf("two schemas took %v; they queued on one lock", elapsed)
	}
}

func TestMigrationLockKey(t *testing.T) {
	a, _ := NewSchema("tenant_a")
	if migrationLockKey(Schema{}) != "gopernicus.migrations/(default)" || migrationLockKey(a) != "gopernicus.migrations/tenant_a" {
		t.Fatalf("keys: %q %q", migrationLockKey(Schema{}), migrationLockKey(a))
	}
}
