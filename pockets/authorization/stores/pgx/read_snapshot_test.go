package pgx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func cacheFixture(t testing.TB, install bool) (*pgxdb.DB, config) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set: cache snapshots NOT verified")
	}
	db, err := pgxdb.Open(ctx, pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := pgxdb.NewSchema(fmt.Sprintf("cache_test_%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+schema.Table("")[:len(schema.Table(""))-1]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(context.Background(), "DROP SCHEMA "+schema.Table("")[:len(schema.Table(""))-1]+" CASCADE")
		db.Close()
	})
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	cfg := config{schema: schema}

	if install {
		data, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/0001_iam_cache_invalidation.sql")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(ctx, func(tx *pgxdb.Tx) error {
			if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+cfg.schema.Table("")[:len(cfg.schema.Table(""))-1]); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, string(data))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	return db, cfg
}
func cacheOptions(cfg config) []Option {
	return []Option{func(c *config) { *c = cfg }, WithCacheReads()}
}
func cacheTable(cfg config, name string) string { return cfg.schema.Table(name) }
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
	if _, err := db.Exec(ctx, "CREATE OR REPLACE FUNCTION "+cfg.schema.Table("iam_advance_cache_generation")+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$"); err != nil {
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
