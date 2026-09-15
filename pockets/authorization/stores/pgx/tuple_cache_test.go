package pgx

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

func TestCacheEnabledConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T, policy mutations.GuardianPolicy) authorization.Repositories {
		db, cfg := cacheFixture(t, true)
		repos, err := Repositories(context.Background(), db, append(cacheOptions(cfg), WithGuardianPolicy(policy))...)
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}

func TestCacheRejectsRLS(t *testing.T) {
	ctx := context.Background()
	for _, table := range []string{"iam_relationships", "iam_roles", "iam_tuple_cache", "iam_tuple_outbox"} {
		for _, mode := range []string{"ENABLE", "FORCE"} {
			t.Run(table+mode, func(t *testing.T) {
				db, cfg := cacheFixture(t, true)
				if _, err := db.Exec(ctx, "ALTER TABLE "+cacheTable(cfg, table)+" "+mode+" ROW LEVEL SECURITY"); err != nil {
					t.Fatal(err)
				}
				if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err == nil {
					t.Fatal("RLS-enabled authority accepted")
				}
			})
		}
	}
}

func TestCacheRejectsInheritedAuthority(t *testing.T) {
	ctx := context.Background()
	db, cfg := cacheFixture(t, true)
	if _, err := db.Exec(ctx, "CREATE TABLE "+cacheTable(cfg, "child_roles")+" () INHERITS ("+cacheTable(cfg, "iam_roles")+")"); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err == nil {
		t.Fatal("inherited facts accepted")
	}
}

func cacheConnection(t *testing.T, searchPath string) *pgxdb.DB {
	t.Helper()
	parsed, err := url.Parse(os.Getenv("POSTGRES_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", searchPath)
	parsed.RawQuery = query.Encode()
	db, err := pgxdb.Open(context.Background(), pgxdb.Config{DSN: parsed.String(), MaxConns: 1, MinConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func cacheSchemaName(cfg config) string {
	name := cfg.schema.Table("")
	return name[:len(name)-1]
}

func TestCacheSchemaFrozenAndMixed(t *testing.T) {
	ctx := context.Background()
	_, first := cacheFixture(t, true)
	_, second := cacheFixture(t, true)
	db := cacheConnection(t, cacheSchemaName(first))
	// The zero-schema constructor resolves the connection search path once.
	repos, err := Repositories(ctx, db, WithTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	before, err := repos.TupleSource.Snapshot(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "SET search_path TO "+cacheSchemaName(second)); err != nil {
		t.Fatal(err)
	}
	if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "document", ResourceID: "frozen", Relation: "viewer", SubjectType: "user", SubjectID: "u"}}); err != nil {
		t.Fatal(err)
	}
	after, err := repos.TupleSource.Snapshot(ctx, "")
	if err != nil || len(after.Changes) != len(before.Changes)+1 {
		t.Fatalf("authority changed after SET search_path: %v/%v", after, err)
	}
	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM "+cacheTable(second, "iam_relationships")).Scan(&count); err != nil || count != 0 {
		t.Fatalf("write escaped frozen schema: %d/%v", count, err)
	}
	// Resolving facts and the head from different schemas cannot certify one authority.
	if _, err := db.Exec(ctx, "DROP TABLE "+cacheTable(first, "iam_tuple_cache")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "SET search_path TO "+cacheSchemaName(first)+","+cacheSchemaName(second)); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db, WithTupleCache()); err == nil {
		t.Fatal("mixed-schema authority accepted")
	}
}

func TestCacheEnabledAuditConformance(t *testing.T) {
	storetest.RunAudit(t, func(t *testing.T, enabled bool) authorization.Repositories {
		db, cfg := cacheFixture(t, true)
		cfg.audit = enabled
		repos, err := Repositories(context.Background(), db, cacheOptions(cfg)...)
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}

func TestTupleCacheRejectsDisabledCapture(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{"insert", "update", "delete", "truncate"} {
		t.Run(op, func(t *testing.T) {
			db, cfg := cacheFixture(t, true)
			if _, err := db.Exec(ctx, "ALTER TABLE "+cacheTable(cfg, "iam_relationships")+" DISABLE TRIGGER iam_relationships_tuple_"+op); err != nil {
				t.Fatal(err)
			}
			if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err == nil {
				t.Fatal("disabled capture accepted")
			}
		})
	}
}

func TestTupleCacheRejectsOutboxIdentityTamper(t *testing.T) {
	ctx := context.Background()
	db, cfg := cacheFixture(t, true)
	if _, err := db.Exec(ctx, "ALTER TABLE "+cacheTable(cfg, "iam_tuple_outbox")+" ALTER COLUMN id DROP IDENTITY"); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err == nil {
		t.Fatal("missing event identity accepted")
	}
}
