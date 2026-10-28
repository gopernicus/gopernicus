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
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
)

func cacheFixture(t testing.TB, install bool) (*pgxdb.DB, config) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set: cache snapshots NOT verified")
	}
	db, err := pgxdb.Open(ctx, pgxdb.Config{DSN: sdk.Secret(dsn)})
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
		installTupleCache(t, db, schema)
	}

	return db, cfg
}

// installTupleCache applies the tuple cache migration inside schema, as a host
// would after the primary authorization migrations.
func installTupleCache(t testing.TB, db *pgxdb.DB, schema pgxdb.Schema) {
	t.Helper()
	ctx := context.Background()
	files, err := TupleCacheMigrationsFS.ReadDir(TupleCacheMigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := TupleCacheMigrationsFS.ReadFile(TupleCacheMigrationsDir + "/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(ctx, func(tx *pgxdb.Tx) error {
			if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema.Table("")[:len(schema.Table(""))-1]); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, string(data))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}
func cacheOptions(cfg config) []Option {
	return []Option{func(c *config) { *c = cfg }, WithTupleCache()}
}
func cacheTable(cfg config, name string) string { return cfg.schema.Table(name) }
func TestCacheSnapshots(t *testing.T) {
	storetest.RunReadSnapshots(t, func(t *testing.T) storetest.Repositories {
		db, cfg := cacheFixture(t, true)
		repos, err := testRepositories(context.Background(), db, cacheOptions(cfg)...)
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}
func TestTupleCacheInstallationAndIdentityFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("optional", func(t *testing.T) {
		db, cfg := cacheFixture(t, false)
		repos, err := testRepositories(ctx, db, func(c *config) { *c = cfg })
		if err != nil || repos.TupleSource != nil {
			t.Fatalf("direct construction: %v/%v", repos.TupleSource, err)
		}
		if _, err := testRepositories(ctx, db, cacheOptions(cfg)...); err == nil {
			t.Fatal("missing migration accepted")
		}
	})
	cases := []struct {
		kind, query, message string
		identity             bool
	}{
		{kind: "missing", query: "DELETE FROM %s", message: "apply the tuple cache migration once"},
		{kind: "wrong protocol", query: "ALTER TABLE %[1]s DROP CONSTRAINT iam_tuple_cache_protocol_check; UPDATE %[1]s SET protocol=3", message: "slot 1 protocol 3, want 1/2"},
		{kind: "malformed identity", query: "ALTER TABLE %[1]s DROP CONSTRAINT iam_tuple_cache_identity_check; UPDATE %[1]s SET identity='NOT-HEX-NOT-HEX-NOT-HEX-NOT-HEX!'", message: "not 32 lowercase hex"},
		{kind: "changed identity", query: "UPDATE %s SET identity='ffffffffffffffffffffffffffffffff'", message: "restart the process", identity: true},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			db, cfg := cacheFixture(t, true)
			repos, err := testRepositories(ctx, db, cacheOptions(cfg)...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RelationshipRepository(ctx, db, cacheOptions(cfg)...); err == nil {
				t.Fatal("partial constructor accepted")
			}
			before, err := repos.TupleSource.Snapshot(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, fmt.Sprintf(tc.query, cacheTable(cfg, "iam_tuple_cache"))); err != nil {
				t.Fatal(err)
			}
			_, snapshotErr := repos.TupleSource.Snapshot(ctx, "")
			acknowledgeErr := repos.TupleSource.Acknowledge(ctx, before.Receipt, "next", nil)
			for stage, err := range map[string]error{"snapshot": snapshotErr, "acknowledgement": acknowledgeErr} {
				if !errors.Is(err, tuplecache.ErrUnavailable) || errors.Is(err, tuplecache.ErrSourceIdentity) != tc.identity || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("%s: %v", stage, err)
				}
				if strings.Contains(err.Error(), "ffffffff") || strings.Contains(err.Error(), "NOT-HEX") {
					t.Fatalf("%s echoes the identity: %v", stage, err)
				}
			}
		})
	}
}

func TestTupleCacheTriggerTamper(t *testing.T) {
	ctx := context.Background()
	db, cfg := cacheFixture(t, true)
	if _, err := db.Exec(ctx, "CREATE OR REPLACE FUNCTION "+cfg.schema.Table("iam_capture_tuple_change")+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$"); err != nil {
		t.Fatal(err)
	}
	if _, err := testRepositories(ctx, db, cacheOptions(cfg)...); err == nil {
		t.Fatal("same-name no-op trigger accepted")
	}
}
func TestCacheMigrationExport(t *testing.T) {
	dst := t.TempDir()
	if err := ExportTupleCacheMigrations(dst); err != nil {
		t.Fatal(err)
	}
	files, err := TupleCacheMigrationsFS.ReadDir(TupleCacheMigrationsDir)
	if err != nil || len(files) != 1 {
		t.Fatalf("inventory %v/%v", files, err)
	}
	data, err := TupleCacheMigrationsFS.ReadFile(TupleCacheMigrationsDir + "/" + files[0].Name())
	if err != nil || !strings.Contains(string(data), "iam_tuple_cache") {
		t.Fatal("missing cache migration")
	}
	exported, err := os.ReadFile(filepath.Join(dst, files[0].Name()))
	if err != nil || !bytes.Equal(data, exported) {
		t.Fatalf("cache migration export differs: %v", err)
	}
}
