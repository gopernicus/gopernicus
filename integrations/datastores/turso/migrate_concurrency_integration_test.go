//go:build integration

package turso

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestRunMigrations_ConcurrentRunnersSerialize: two runners on one database,
// each on its own connection, both succeed and each file is recorded once
// (#298). BEGIN IMMEDIATE takes the database write lock first, so the second
// runner waits, then finds the stream applied.
func TestRunMigrations_ConcurrentRunnersSerialize(t *testing.T) {
	url, token := os.Getenv("TURSO_DATABASE_URL"), os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — concurrent migration runners NOT verified")
	}
	ctx := context.Background()
	open := func() *DB {
		db, err := Open(ctx, Config{URL: sdk.Secret(url), AuthToken: sdk.Secret(token)})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	a, b := open(), open()
	stamp := time.Now().UnixNano()
	tables := []string{fmt.Sprintf("mig_lock_one_%d", stamp), fmt.Sprintf("mig_lock_two_%d", stamp)}
	versions := []string{fmt.Sprintf("%d_0001_lock.sql", stamp), fmt.Sprintf("%d_0002_lock.sql", stamp)}
	fsys := fstest.MapFS{
		"migrations/" + versions[0]: {Data: []byte("CREATE TABLE " + tables[0] + " (id TEXT PRIMARY KEY);")},
		"migrations/" + versions[1]: {Data: []byte("CREATE TABLE " + tables[1] + " (id TEXT PRIMARY KEY);")},
	}
	t.Cleanup(func() {
		for _, tbl := range tables {
			_, _ = a.Exec(context.Background(), "DROP TABLE IF EXISTS "+tbl)
		}
		for _, v := range versions {
			_, _ = a.Exec(context.Background(), "DELETE FROM schema_migrations WHERE version = ?", v)
		}
	})

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	for i, db := range []*DB{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = RunMigrations(ctx, db, fsys, "migrations")
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("runner %d: %v", i, err)
		}
	}
	for _, v := range versions {
		var n int
		if err := a.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version = ?", v).Scan(&n); err != nil || n != 1 {
			t.Fatalf("ledger rows for %s = %d (%v), want 1", v, n, err)
		}
	}
}
