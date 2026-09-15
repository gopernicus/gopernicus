package turso

import (
	"context"
	"path/filepath"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	_ "modernc.org/sqlite"
)

func cacheFixture(t testing.TB, install bool, poolSize ...int) (*tursodb.DB, config) {
	t.Helper()
	ctx := context.Background()
	connections := 4
	if len(poolSize) > 0 {
		connections = poolSize[0]
	}
	db, err := tursodb.Open(ctx, tursodb.Config{URL: "file:" + filepath.Join(t.TempDir(), "cache.sqlite"), MaxOpenConns: connections, MaxIdleConns: connections})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	cfg := config{}

	if install {
		data, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/0002_iam_tuple_cache.sql")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(ctx, func(tx *tursodb.Tx) error { _, err := tx.Exec(ctx, string(data)); return err }); err != nil {
			t.Fatal(err)
		}
	}
	return db, cfg
}
func cacheOptions(cfg config) []Option {
	return []Option{func(c *config) { *c = cfg }, WithTupleCache()}
}
func TestTupleCacheSnapshots(t *testing.T) {
	storetest.RunReadSnapshots(t, func(t *testing.T) authorization.Repositories {
		db, cfg := cacheFixture(t, true)
		repos, err := Repositories(context.Background(), db, cacheOptions(cfg)...)
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}
