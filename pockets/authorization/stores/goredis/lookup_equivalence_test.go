package goredis

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	tursostore "github.com/gopernicus/gopernicus/pockets/authorization/stores/turso"
	_ "modernc.org/sqlite"
)

// TestTupleCacheLookupEquivalence repeats the short-circuit equivalence proof
// with Redis in front of a local SQLite source. It skips without redis-server.
func TestTupleCacheLookupEquivalence(t *testing.T) {
	client, _ := startRedis(t, "", false)
	namespaces := 0
	storetest.RunLookupEquivalenceTupleCache(t, func(t *testing.T) storetest.Repositories {
		ctx := context.Background()
		db, err := tursodb.Open(ctx, tursodb.Config{URL: "file:" + filepath.Join(t.TempDir(), "authority.sqlite"), MaxOpenConns: 4, MaxIdleConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			t.Fatal(err)
		}
		if err := tursodb.RunMigrations(ctx, db, tursostore.MigrationsFS, tursostore.MigrationsDir); err != nil {
			t.Fatal(err)
		}
		cache, err := tursostore.TupleCacheMigrationsFS.ReadFile(tursostore.TupleCacheMigrationsDir + "/0002_iam_tuple_cache.sql")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(ctx, func(tx *tursodb.Tx) error { _, err := tx.Exec(ctx, string(cache)); return err }); err != nil {
			t.Fatal(err)
		}
		repos, err := tursostore.Repositories(ctx, db, tursostore.WithTupleCache())
		if err != nil {
			t.Fatal(err)
		}
		return storetest.Repositories{Repositories: repos}
	}, func(t *testing.T) tuplecache.Backend {
		namespaces++
		c, err := NewTupleCache(client, WithNamespace(fmt.Sprintf("test:equivalence:%d", namespaces)))
		if err != nil {
			t.Fatal(err)
		}
		return c
	})
}
