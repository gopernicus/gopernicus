package turso

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	_ "modernc.org/sqlite"
)

func cacheFixture(t *testing.T, install bool, poolSize ...int) (*tursodb.DB, config) {
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
		data, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/0001_iam_cache_invalidation.sql")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(ctx, func(tx *tursodb.Tx) error { ; _, err := tx.Exec(ctx, string(data)); return err }); err != nil {
			t.Fatal(err)
		}
	}
	return db, cfg
}
func cacheOptions(cfg config) []Option {
	return []Option{func(c *config) { *c = cfg }, WithCacheReads()}
}
func cacheTable(cfg config, name string) string { return "main." + name }
func TestCacheSnapshots(t *testing.T) {
	storetest.RunReadSnapshots(t, func(t *testing.T) authorization.Repositories {
		db, cfg := cacheFixture(t, true)
		repos, err := Repositories(context.Background(), db, cacheOptions(cfg)...)
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}
func TestCacheInstallationAndHeadFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("optional", func(t *testing.T) {
		db, cfg := cacheFixture(t, false)
		repos, err := Repositories(ctx, db, func(c *config) { *c = cfg })
		if err != nil || repos.CacheSource != nil {
			t.Fatalf("direct construction: %v/%v", repos.CacheSource, err)
		}
		if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err == nil {
			t.Fatal("missing migration accepted")
		}
	})
	for _, kind := range []string{"missing", "overflow", "changed epoch"} {
		t.Run(kind, func(t *testing.T) {
			db, cfg := cacheFixture(t, true)
			repos, err := Repositories(ctx, db, cacheOptions(cfg)...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RelationshipRepository(ctx, db, cacheOptions(cfg)...); err == nil {
				t.Fatal("partial constructor accepted")
			}
			table := cacheTable(cfg, "iam_cache_invalidation")
			query := "DELETE FROM " + table
			if kind == "overflow" {
				query = "UPDATE " + table + " SET generation=9223372036854775807"
			}
			if kind == "changed epoch" {
				query = "UPDATE " + table + " SET epoch='ffffffffffffffffffffffffffffffff'"
			}
			if _, err := db.Exec(ctx, query); err != nil {
				t.Fatal(err)
			}
			if kind == "changed epoch" {
				if _, err := repos.CacheSource.Observe(ctx); !errors.Is(err, decisions.ErrCacheVersion) {
					t.Fatalf("epoch: %v", err)
				}
				return
			}
			// These are ordinary writers with no caching option; triggers still protect facts.
			direct := newRoleStore(db, cfg)
			if err := direct.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "viewer"}); err == nil {
				t.Fatal("invalid head allowed fact commit")
			}
			var n int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM "+cacheTable(cfg, "iam_roles")).Scan(&n); err != nil || n != 0 {
				t.Fatalf("partial commit: %d/%v", n, err)
			}
		})
	}
}
func TestCacheTriggerTamper(t *testing.T) {
	ctx := context.Background()
	db, cfg := cacheFixture(t, true)
	if _, err := db.Exec(ctx, "DROP TRIGGER iam_roles_cache_insert; CREATE TRIGGER iam_roles_cache_insert AFTER INSERT ON iam_roles BEGIN SELECT 1; END;"); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err == nil {
		t.Fatal("same-name no-op trigger accepted")
	}
}
func TestCacheMigrationExport(t *testing.T) {
	dst := t.TempDir()
	if err := ExportCacheMigrations(dst); err != nil {
		t.Fatal(err)
	}
	files, err := CacheMigrationsFS.ReadDir(CacheMigrationsDir)
	if err != nil || len(files) != 1 {
		t.Fatalf("inventory %v/%v", files, err)
	}
	data, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/" + files[0].Name())
	if err != nil || !strings.Contains(string(data), "iam_cache_invalidation") {
		t.Fatal("missing cache migration")
	}
	exported, err := os.ReadFile(filepath.Join(dst, files[0].Name()))
	if err != nil || !bytes.Equal(data, exported) {
		t.Fatalf("cache migration export differs: %v", err)
	}
}
