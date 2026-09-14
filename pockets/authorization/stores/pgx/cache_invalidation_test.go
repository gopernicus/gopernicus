package pgx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
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

func TestCacheFactTriggers(t *testing.T) {
	ctx := context.Background()
	db, cfg := cacheFixture(t, true)
	generation := func() int64 {
		t.Helper()
		var value int64
		if err := db.QueryRow(ctx, "SELECT generation FROM "+cacheTable(cfg, "iam_cache_invalidation")).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	tables := []struct {
		name, insert string
		columns      []string
	}{
		{"iam_relationships", "(resource_type,resource_id,relation,subject_type,subject_id,subject_relation) VALUES ('document','doc','viewer','user','alice','')", []string{"resource_type", "resource_id", "relation", "subject_type", "subject_id", "subject_relation"}},
		{"iam_roles", "(subject_type,subject_id,role,resource_type,resource_id) VALUES ('user','alice','viewer','document','doc')", []string{"subject_type", "subject_id", "role", "resource_type", "resource_id"}},
	}
	for _, table := range tables {
		t.Run(table.name, func(t *testing.T) {
			name := cacheTable(cfg, table.name)
			previous := generation()
			exec("INSERT INTO " + name + table.insert)
			if generation() <= previous {
				t.Fatal("ordinary INSERT did not advance head")
			}
			previous = generation()
			exec("INSERT INTO " + name + table.insert + " ON CONFLICT DO NOTHING")
			if generation() != previous {
				t.Fatal("conflict no-op advanced head")
			}
			for _, column := range table.columns {
				previous = generation()
				exec("UPDATE " + name + " SET " + column + "=" + column)
				if generation() != previous {
					t.Fatalf("unchanged %s advanced head", column)
				}
				exec("UPDATE " + name + " SET " + column + "='changed'")
				if generation() <= previous {
					t.Fatalf("changed %s failed to advance head", column)
				}
			}
			previous = generation()
			injected := errors.New("abort fixture")
			err := db.InTx(ctx, func(tx *pgxdb.Tx) error {
				if _, err := tx.Exec(ctx, "DELETE FROM "+name); err != nil {
					return err
				}
				var inside int64
				if err := tx.QueryRow(ctx, "SELECT generation FROM "+cacheTable(cfg, "iam_cache_invalidation")).Scan(&inside); err != nil {
					return err
				}
				if inside <= previous {
					return errors.New("DELETE did not advance transaction head")
				}
				return injected
			})
			if !errors.Is(err, injected) {
				t.Fatal(err)
			}
			if generation() != previous {
				t.Fatal("rollback leaked generation")
			}
			var count int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM "+name).Scan(&count); err != nil || count != 1 {
				t.Fatalf("rollback lost fact: %d/%v", count, err)
			}
			exec("DELETE FROM " + name)
			if generation() <= previous {
				t.Fatal("DELETE failed to advance head")
			}
		})
	}
}

func TestCacheNilWriterParticipation(t *testing.T) {
	ctx := context.Background()
	db, cfg := cacheFixture(t, true)
	cached, err := Repositories(ctx, db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := Repositories(ctx, db, func(c *config) { *c = cfg })
	if err != nil || direct.CacheSource != nil {
		t.Fatalf("direct constructor: %v", err)
	}
	before, err := cached.CacheSource.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := direct.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "writer", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	after, err := cached.CacheSource.Observe(ctx)
	if err != nil || before.Epoch != after.Epoch || after.Generation <= before.Generation {
		t.Fatalf("nil writer did not invalidate: %v -> %v: %v", before, after, err)
	}
}

func TestCacheTruncateAndDisabledTrigger(t *testing.T) {
	ctx := context.Background()
	for _, table := range []string{"iam_relationships", "iam_roles"} {
		t.Run(table, func(t *testing.T) {
			db, cfg := cacheFixture(t, true)
			repos, err := Repositories(ctx, db, cacheOptions(cfg)...)
			if err != nil {
				t.Fatal(err)
			}
			before, err := repos.CacheSource.Observe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "TRUNCATE "+cacheTable(cfg, table)); err != nil {
				t.Fatal(err)
			}
			after, err := repos.CacheSource.Observe(ctx)
			if err != nil || after.Generation <= before.Generation {
				t.Fatalf("TRUNCATE not observed: %v", err)
			}
			if _, err := db.Exec(ctx, "ALTER TABLE "+cacheTable(cfg, table)+" DISABLE TRIGGER "+table+"_cache_update"); err != nil {
				t.Fatal(err)
			}
			if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err == nil {
				t.Fatal("disabled UPDATE trigger accepted")
			}
		})
	}
}

func TestCacheRejectsRLS(t *testing.T) {
	ctx := context.Background()
	for _, table := range []string{"iam_relationships", "iam_roles", "iam_cache_invalidation"} {
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
	repos, err := Repositories(ctx, db, WithCacheReads())
	if err != nil {
		t.Fatal(err)
	}
	before, err := repos.CacheSource.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "SET search_path TO "+cacheSchemaName(second)); err != nil {
		t.Fatal(err)
	}
	if err := repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "frozen", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	after, err := repos.CacheSource.Observe(ctx)
	if err != nil || after.Epoch != before.Epoch || after.Generation <= before.Generation {
		t.Fatalf("authority changed after SET search_path: %v/%v", after, err)
	}
	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM "+cacheTable(second, "iam_roles")).Scan(&count); err != nil || count != 0 {
		t.Fatalf("write escaped frozen schema: %d/%v", count, err)
	}
	// Resolving facts and the head from different schemas cannot certify one authority.
	if _, err := db.Exec(ctx, "DROP TABLE "+cacheTable(first, "iam_cache_invalidation")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "SET search_path TO "+cacheSchemaName(first)+","+cacheSchemaName(second)); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db, WithCacheReads()); err == nil {
		t.Fatal("mixed-schema authority accepted")
	}
}

func TestCacheInvokerPrivileges(t *testing.T) {
	ctx := context.Background()
	admin, cfg := cacheFixture(t, true)
	name := "cache_invoker_" + fmt.Sprint(time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE ROLE "+name+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP OWNED BY "+name+"; DROP ROLE "+name); err != nil {
			t.Error(err)
		}
	})
	schema := cacheSchemaName(cfg)
	for _, query := range []string{
		"GRANT USAGE ON SCHEMA " + schema + " TO " + name,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON " + cacheTable(cfg, "iam_roles") + "," + cacheTable(cfg, "iam_relationships") + " TO " + name,
		"GRANT SELECT ON " + cacheTable(cfg, "iam_audit") + " TO " + name,
	} {
		if _, err := admin.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	db := cacheConnection(t, schema)
	if _, err := db.Exec(ctx, "SET ROLE "+name); err != nil {
		t.Fatal(err)
	}
	assignment := roles.Assignment{SubjectType: "user", SubjectID: "invoker", Role: "viewer"}
	writer := newRoleStore(db, cfg)
	for _, privilege := range []string{"", "SELECT", "UPDATE"} {
		if privilege != "" {
			if _, err := admin.Exec(ctx, "GRANT "+privilege+" ON "+cacheTable(cfg, "iam_cache_invalidation")+" TO "+name); err != nil {
				t.Fatal(err)
			}
		}
		err := writer.Assign(ctx, assignment)
		if privilege == "UPDATE" {
			if err != nil {
				t.Fatalf("SELECT+UPDATE writer failed: %v", err)
			}
		} else {
			if err == nil {
				t.Fatalf("writer succeeded with head privilege %q", privilege)
			}
			var count int
			if err := admin.QueryRow(ctx, "SELECT count(*) FROM "+cacheTable(cfg, "iam_roles")).Scan(&count); err != nil || count != 0 {
				t.Fatalf("permission failure partially committed: %d/%v", count, err)
			}
		}
	}
	if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err != nil {
		t.Fatalf("least-privilege cache constructor: %v", err)
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
