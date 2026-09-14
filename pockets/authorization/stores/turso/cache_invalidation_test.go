package turso

import (
	"context"
	"errors"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
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
			err := db.InTx(ctx, func(tx *tursodb.Tx) error {
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

func TestCacheUpdateTriggerTamper(t *testing.T) {
	ctx := context.Background()
	for _, table := range []string{"iam_relationships", "iam_roles"} {
		t.Run(table, func(t *testing.T) {
			db, cfg := cacheFixture(t, true)
			trigger := table + "_cache_update"
			if _, err := db.Exec(ctx, "DROP TRIGGER "+trigger+"; CREATE TRIGGER "+trigger+" AFTER UPDATE OF subject_id ON "+table+" BEGIN UPDATE iam_cache_invalidation SET generation=generation+1; END"); err != nil {
				t.Fatal(err)
			}
			if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err == nil {
				t.Fatal("partial UPDATE OF coverage accepted")
			}
		})
	}
}

func TestCacheTempShadowCannotGrant(t *testing.T) {
	ctx := context.Background()
	// One physical connection makes the TEMP schema deterministic for all reads.
	db, cfg := cacheFixture(t, true, 1)
	if _, err := db.Exec(ctx, `CREATE TEMP TABLE iam_roles AS SELECT * FROM main.iam_roles;
 INSERT INTO temp.iam_roles(subject_type,subject_id,role,resource_type,resource_id) VALUES ('user','shadow','admin','','');
 CREATE TEMP TABLE iam_cache_invalidation AS SELECT * FROM main.iam_cache_invalidation;
 UPDATE temp.iam_cache_invalidation SET generation=1000;
 CREATE TEMP TABLE iam_relationships AS SELECT * FROM main.iam_relationships;
 INSERT INTO temp.iam_relationships(resource_type,resource_id,relation,subject_type,subject_id,subject_relation) VALUES ('document','d1','viewer','user','shadow','');`); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(ctx, db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	version, err := repos.CacheSource.Observe(ctx)
	if err != nil || version.Generation != 0 {
		t.Fatalf("TEMP head selected: %v/%v", version, err)
	}
	err = repos.CacheSource.ReadSnapshot(ctx, func(ctx context.Context, _ decisions.CacheVersion, reads decisions.CheckReads) error {
		allowed, err := reads.HasExactRole(ctx, "user", "shadow", "admin", "", "")
		if err != nil {
			return err
		}
		if allowed {
			return errors.New("TEMP role granted")
		}
		model := relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "document", Relation: "viewer", SubjectType: "user"}})
		allowed, err = reads.ForChecks(model).CheckRelationWithGroupExpansion(ctx, "document", "d1", "viewer", "user", "shadow", 100)
		if err != nil {
			return err
		}
		if allowed {
			return errors.New("TEMP relationship granted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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
