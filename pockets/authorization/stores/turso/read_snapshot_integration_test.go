//go:build integration

package turso

import (
	"context"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func TestCacheLiveSnapshots(t *testing.T) {
	url, token := requireTursoEnv(t)
	db := openAndMigrate(t, url, token)
	ctx := context.Background()
	data, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/0001_iam_cache_invalidation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InTx(ctx, func(tx *tursodb.Tx) error { _, err := tx.Exec(ctx, string(data)); return err }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"iam_relationships", "iam_roles"} {
			for _, op := range []string{"insert", "delete", "update"} {
				if _, err := db.Exec(ctx, "DROP TRIGGER "+table+"_cache_"+op); err != nil {
					t.Error(err)
				}
			}
		}
		if _, err := db.Exec(ctx, "DROP TABLE iam_cache_invalidation"); err != nil {
			t.Error(err)
		}
	})
	storetest.RunReadSnapshots(t, func(t *testing.T) authorization.Repositories {
		truncate(t, db)
		repos, err := Repositories(ctx, db, WithCacheReads())
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}
