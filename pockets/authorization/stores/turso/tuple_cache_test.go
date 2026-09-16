package turso

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestTupleCacheEnabledConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T, policy mutations.GuardianPolicy) authorization.Repositories {
		db, cfg := cacheFixture(t, true)
		repos, err := Repositories(context.Background(), db, append(cacheOptions(cfg), WithGuardianPolicy(policy))...)
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}

func TestTupleCacheEnabledAuditConformance(t *testing.T) {
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

func tupleTestSource(t *testing.T) (*tursodb.DB, tuplecache.Source) {
	t.Helper()
	db, cfg := cacheFixture(t, true)
	repos, err := Repositories(t.Context(), db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	return db, repos.TupleSource
}

func mustTupleSnapshot(t *testing.T, source tuplecache.Source, receipt string) tuplecache.Snapshot {
	t.Helper()
	snapshot, err := source.Snapshot(t.Context(), receipt)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func mustTupleExec(t *testing.T, db *tursodb.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func tupleChangeIDs(snapshot tuplecache.Snapshot) []string {
	ids := make([]string, len(snapshot.Changes))
	for i, change := range snapshot.Changes {
		ids[i] = change.ID
	}
	return ids
}

func TestTupleCacheRawMutations(t *testing.T) {
	db, source := tupleTestSource(t)
	if err := source.Acknowledge(t.Context(), "", "initial", nil); err != nil {
		t.Fatal(err)
	}
	original := relationships.CreateRelationship{ResourceType: "document", ResourceID: "doc:\"#@", Relation: "viewer", SubjectType: "group", SubjectID: "team:\"#@", SubjectRelation: "member"}
	mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES (?, ?, ?, ?, ?, ?)`, original.ResourceType, original.ResourceID, original.Relation, original.SubjectType, original.SubjectID, original.SubjectRelation)
	first := mustTupleSnapshot(t, source, "initial")
	if first.Full || len(first.Changes) != 1 || first.Changes[0].Before != nil || *first.Changes[0].After != original {
		t.Fatalf("raw create: %+v", first)
	}
	mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, original.ResourceType, original.ResourceID, original.Relation, original.SubjectType, original.SubjectID, original.SubjectRelation)
	mustTupleExec(t, db, `UPDATE iam_relationships SET resource_id = resource_id`)
	if got := mustTupleSnapshot(t, source, "initial"); len(got.Changes) != 1 {
		t.Fatalf("no-op appended work: %+v", got)
	}
	columns := []string{"resource_type", "resource_id", "relation", "subject_type", "subject_id", "subject_relation"}
	before := original
	for i, column := range columns {
		next := before
		fields := []*string{&next.ResourceType, &next.ResourceID, &next.Relation, &next.SubjectType, &next.SubjectID, &next.SubjectRelation}
		*fields[i] = "changed-" + column
		mustTupleExec(t, db, "UPDATE iam_relationships SET "+column+"=?", *fields[i])
		got := mustTupleSnapshot(t, source, "initial")
		change := got.Changes[len(got.Changes)-1]
		if change.Before == nil || change.After == nil || *change.Before != before || *change.After != next {
			t.Fatalf("%s: %+v", column, got)
		}
		if full := mustTupleSnapshot(t, source, ""); len(full.Tuples) != 1 || full.Tuples[0] != next {
			t.Fatalf("current %s: %+v", column, full)
		}
		before = next
	}
	pending := mustTupleSnapshot(t, source, "initial")
	injected := errors.New("rollback")
	if err := db.InTx(t.Context(), func(tx *tursodb.Tx) error {
		if _, err := tx.Exec(t.Context(), "DELETE FROM iam_relationships"); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM iam_tuple_outbox").Scan(&count); err != nil {
			return err
		}
		if count != len(pending.Changes)+1 {
			return errors.New("transaction failed to append delete")
		}
		return injected
	}); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if got := mustTupleSnapshot(t, source, "initial"); !reflect.DeepEqual(got, pending) {
		t.Fatalf("rollback changed facts or events: %+v", got)
	}
	mustTupleExec(t, db, "DELETE FROM iam_relationships")
	got := mustTupleSnapshot(t, source, "initial")
	change := got.Changes[len(got.Changes)-1]
	if len(got.Tuples) != 0 || change.Before == nil || *change.Before != before || change.After != nil {
		t.Fatalf("delete: %+v", got)
	}
	// Roles remain durable and do not produce raw relationship work.
	mustTupleExec(t, db, `INSERT INTO iam_roles VALUES ('user','alice','admin','','')`)
	if next := mustTupleSnapshot(t, source, "initial"); !reflect.DeepEqual(got, next) {
		t.Fatalf("role appended relationship event: %+v", next)
	}
}

func TestTupleCacheAcknowledgeAndRebuild(t *testing.T) {
	db, source := tupleTestSource(t)
	mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES ('document','a','viewer','user','alice','')`)
	first := mustTupleSnapshot(t, source, "")
	// A commit after capture must survive acknowledgement of the captured IDs.
	mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES ('document','b','viewer','user','alice','')`)
	if err := source.Acknowledge(t.Context(), "", "r1", tupleChangeIDs(first)); err != nil {
		t.Fatal(err)
	}
	next := mustTupleSnapshot(t, source, "r1")
	if next.Full || len(next.Tuples) != 0 || len(next.Changes) != 1 || next.Changes[0].After.ResourceID != "b" {
		t.Fatalf("exact ack: %+v", next)
	}
	if err := source.Acknowledge(t.Context(), "", "loser", tupleChangeIDs(next)); !errors.Is(err, tuplecache.ErrConflict) {
		t.Fatalf("receipt conflict: %v", err)
	}
	if got := mustTupleSnapshot(t, source, "r1"); !reflect.DeepEqual(got, next) {
		t.Fatalf("failed CAS deleted work: %+v", got)
	}
	if err := source.Acknowledge(t.Context(), "r1", "r2", tupleChangeIDs(next)); err != nil {
		t.Fatal(err)
	}
	steady := mustTupleSnapshot(t, source, "r2")
	if steady.Full || len(steady.Changes) != 0 {
		t.Fatalf("work not disposed: %+v", steady)
	}
	for _, receipt := range []string{"", "lost-redis-receipt"} {
		rebuilt := mustTupleSnapshot(t, source, receipt)
		if !rebuilt.Full || len(rebuilt.Tuples) != 2 || len(rebuilt.Changes) != 0 {
			t.Fatalf("rebuild depended on disposed events: %+v", rebuilt)
		}
		if rebuilt.Tuples[0].ResourceID != "a" || rebuilt.Tuples[1].ResourceID != "b" {
			t.Fatalf("nondeterministic rebuild: %+v", rebuilt.Tuples)
		}
	}
	mustTupleExec(t, db, `DELETE FROM iam_relationships WHERE resource_id='a'`)
	mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES ('document','a','editor','user','alice','')`)
	recreated := mustTupleSnapshot(t, source, "r2")
	if len(recreated.Changes) != 2 || recreated.Changes[0].Before.Relation != "viewer" || recreated.Changes[0].After != nil || recreated.Changes[1].Before != nil || recreated.Changes[1].After.Relation != "editor" {
		t.Fatalf("delete/recreate ordering: %+v", recreated)
	}
	if recreated.Changes[0].ID == first.Changes[0].ID || recreated.Changes[1].ID == first.Changes[0].ID {
		t.Fatal("disposed event ID reused")
	}
}

func TestTupleCacheOrdinaryWriterReconciliation(t *testing.T) {
	db, source := tupleTestSource(t)
	direct, err := Repositories(t.Context(), db)
	if err != nil || direct.TupleSource != nil {
		t.Fatalf("ordinary writer: %v", err)
	}
	alice := relationships.CreateRelationship{ResourceType: "document", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}
	bob := alice
	bob.SubjectID = "bob"
	carol := alice
	carol.SubjectID = "carol"
	if err := direct.Relationships.CreateRelationships(t.Context(), []relationships.CreateRelationship{alice, bob}); err != nil {
		t.Fatal(err)
	}
	created := mustTupleSnapshot(t, source, "")
	if len(created.Changes) != 2 {
		t.Fatalf("batch missing events: %+v", created)
	}
	if err := source.Acknowledge(t.Context(), "", "created", tupleChangeIDs(created)); err != nil {
		t.Fatal(err)
	}
	if err := direct.Relationships.SetRelationTargets(t.Context(), "document", "d1", "viewer", []relationships.CreateRelationship{bob, carol}); err != nil {
		t.Fatal(err)
	}
	changed := mustTupleSnapshot(t, source, "created")
	if len(changed.Changes) != 2 || changed.Changes[0].Before == nil || *changed.Changes[0].Before != alice || changed.Changes[0].After != nil || changed.Changes[1].Before != nil || changed.Changes[1].After == nil || *changed.Changes[1].After != carol {
		t.Fatalf("replace events: %+v", changed)
	}
	if err := direct.Relationships.SetRelationTargets(t.Context(), "document", "d1", "viewer", []relationships.CreateRelationship{bob, carol, bob}); err != nil {
		t.Fatal(err)
	}
	if got := mustTupleSnapshot(t, source, "created"); !reflect.DeepEqual(got, changed) {
		t.Fatalf("unchanged replace appended work: %+v", got)
	}
}

func TestTupleCacheSourcePreconditions(t *testing.T) {
	t.Run("optional", func(t *testing.T) {
		db, cfg := cacheFixture(t, false)
		repos, err := Repositories(t.Context(), db)
		if err != nil || repos.TupleSource != nil {
			t.Fatalf("direct: %+v/%v", repos, err)
		}
		if _, err := Repositories(t.Context(), db, cacheOptions(cfg)...); err == nil {
			t.Fatal("missing migration accepted")
		}
	})
	for _, mutation := range []string{"DELETE FROM iam_tuple_cache", "UPDATE iam_tuple_cache SET binding='ffffffffffffffffffffffffffffffff'"} {
		t.Run(mutation, func(t *testing.T) {
			db, source := tupleTestSource(t)
			mustTupleExec(t, db, mutation)
			if _, err := source.Snapshot(t.Context(), ""); err == nil {
				t.Fatal("invalid metadata accepted")
			}
			if err := source.Acknowledge(t.Context(), "", "next", nil); err == nil {
				t.Fatal("invalid metadata acknowledged")
			}
			if err := source.ReadSnapshot(t.Context(), func(context.Context, tuplecache.CheckReads) error { return nil }); err == nil {
				t.Fatal("restored binding accepted")
			}
		})
	}
	t.Run("missing metadata rejects write", func(t *testing.T) {
		db, _ := tupleTestSource(t)
		mustTupleExec(t, db, "DELETE FROM iam_tuple_cache")
		if _, err := db.Exec(t.Context(), `INSERT INTO iam_relationships VALUES ('document','a','viewer','user','alice','')`); err == nil {
			t.Fatal("untracked commit accepted")
		}
	})
	for _, trigger := range []string{"insert", "delete", "update"} {
		t.Run("tamper "+trigger, func(t *testing.T) {
			db, _ := tupleTestSource(t)
			mustTupleExec(t, db, "DROP TRIGGER iam_relationships_tuple_"+trigger+"; CREATE TRIGGER iam_relationships_tuple_"+trigger+" AFTER UPDATE ON iam_relationships BEGIN SELECT 1; END;")
			if _, err := Repositories(t.Context(), db, WithTupleCache()); err == nil {
				t.Fatal("altered trigger accepted")
			}
		})
	}
	t.Run("invalid ack", func(t *testing.T) {
		_, source := tupleTestSource(t)
		for _, ids := range [][]string{{"0"}, {"-1"}, {"01"}, {"1); DELETE FROM iam_relationships;"}} {
			if err := source.Acknowledge(t.Context(), "", "next", ids); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("invalid id %v: %v", ids, err)
			}
		}
		if err := source.Acknowledge(t.Context(), "", "", nil); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("empty receipt: %v", err)
		}
	})
}

func TestTupleCacheAmbientAndAcknowledgementRollback(t *testing.T) {
	db, source := tupleTestSource(t)
	err := db.Transact(t.Context(), func(ctx context.Context) error {
		if source.CacheableContext(ctx) {
			t.Fatal("ambient context cacheable")
		}
		if _, err := source.Snapshot(ctx, ""); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ambient snapshot: %v", err)
		}
		if err := source.Acknowledge(ctx, "", "next", nil); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ambient acknowledgement: %v", err)
		}
		if err := source.ReadSnapshot(ctx, func(context.Context, tuplecache.CheckReads) error { return nil }); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ambient reader: %v", err)
		}
		if _, err := Repositories(ctx, db, WithTupleCache()); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ambient construction: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RelationshipRepository(t.Context(), db, WithTupleCache()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("partial constructor: %v", err)
	}
	mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES ('document','d1','viewer','user','alice','')`)
	captured := mustTupleSnapshot(t, source, "")
	mustTupleExec(t, db, `CREATE TRIGGER reject_ack BEFORE DELETE ON iam_tuple_outbox BEGIN SELECT RAISE(ABORT, 'injected ack failure'); END;`)
	if err := source.Acknowledge(t.Context(), "", "next", tupleChangeIDs(captured)); err == nil {
		t.Fatal("ack failure ignored")
	}
	if got := mustTupleSnapshot(t, source, ""); !reflect.DeepEqual(got, captured) {
		t.Fatalf("ack failure committed receipt: %+v", got)
	}
	mustTupleExec(t, db, "DROP TRIGGER reject_ack")
	if err := source.Acknowledge(t.Context(), "", "next", tupleChangeIDs(captured)); err != nil {
		t.Fatal(err)
	}
}

func TestTupleCacheMalformedOutbox(t *testing.T) {
	for _, payload := range []string{`["document","d1","viewer","user","alice",null]`, `["document","d1","viewer","user","alice",3]`, `{"resource_type":"document"}`} {
		t.Run(payload, func(t *testing.T) {
			db, source := tupleTestSource(t)
			if err := source.Acknowledge(t.Context(), "", "initial", nil); err != nil {
				t.Fatal(err)
			}
			mustTupleExec(t, db, "INSERT INTO iam_tuple_outbox(after_tuple) VALUES (?)", payload)
			if _, err := source.Snapshot(t.Context(), "initial"); !errors.Is(err, tuplecache.ErrUnavailable) {
				t.Fatalf("malformed event: %v", err)
			}
			full := mustTupleSnapshot(t, source, "")
			if !full.Full || len(full.Tuples) != 0 || len(full.Changes) != 1 || full.Changes[0].Before != nil || full.Changes[0].After != nil {
				t.Fatalf("full recovery decoded obsolete event: %+v", full)
			}
		})
	}
}

func TestTupleCacheRejectsMalformedCurrentFacts(t *testing.T) {
	for _, id := range []string{"control\n", strings.Repeat("x", 257), string([]byte{0xff})} {
		t.Run(id, func(t *testing.T) {
			db, source := tupleTestSource(t)
			if err := source.Acknowledge(t.Context(), "", "initial", nil); err != nil {
				t.Fatal(err)
			}
			mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES ('document',?,'viewer','user','alice','')`, id)
			for _, receipt := range []string{"initial", ""} {
				if _, err := source.Snapshot(t.Context(), receipt); !errors.Is(err, tuplecache.ErrUnavailable) {
					t.Fatalf("malformed fact with receipt %q: %v", receipt, err)
				}
			}
		})
	}
}

func TestTupleCacheFullRecoveryPreservesLateWork(t *testing.T) {
	db, source := tupleTestSource(t)
	if err := source.Acknowledge(t.Context(), "", "initial", nil); err != nil {
		t.Fatal(err)
	}
	mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES ('document','current','viewer','user','alice','')`)
	mustTupleExec(t, db, `INSERT INTO iam_tuple_outbox(after_tuple) VALUES ('{}')`)
	if _, err := source.Snapshot(t.Context(), "initial"); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("delta accepted malformed payload: %v", err)
	}
	full := mustTupleSnapshot(t, source, "")
	if !full.Full || len(full.Tuples) != 1 || full.Tuples[0].ResourceID != "current" || len(full.Changes) != 2 {
		t.Fatalf("full recovery: %+v", full)
	}
	for _, change := range full.Changes {
		if change.Before != nil || change.After != nil {
			t.Fatalf("full recovery retained payload: %+v", change)
		}
	}
	mustTupleExec(t, db, `INSERT INTO iam_relationships VALUES ('document','late','viewer','user','alice','')`)
	if err := source.Acknowledge(t.Context(), full.Receipt, "rebuilt", tupleChangeIDs(full)); err != nil {
		t.Fatal(err)
	}
	remaining := mustTupleSnapshot(t, source, "rebuilt")
	if remaining.Full || len(remaining.Changes) != 1 || remaining.Changes[0].After.ResourceID != "late" {
		t.Fatalf("full acknowledgement lost later commit: %+v", remaining)
	}
}

func TestTupleCacheTempShadowCannotGrant(t *testing.T) {
	db, cfg := cacheFixture(t, true, 1)
	mustTupleExec(t, db, `CREATE TEMP TABLE iam_roles AS SELECT * FROM main.iam_roles;
INSERT INTO temp.iam_roles VALUES ('user','shadow','admin','','');
CREATE TEMP TABLE iam_tuple_cache AS SELECT * FROM main.iam_tuple_cache;
UPDATE temp.iam_tuple_cache SET receipt='shadow';
CREATE TEMP TABLE iam_relationships AS SELECT * FROM main.iam_relationships;
INSERT INTO temp.iam_relationships VALUES ('document','d1','viewer','user','shadow','');`)
	repos, err := Repositories(t.Context(), db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	got := mustTupleSnapshot(t, repos.TupleSource, "")
	if got.Receipt != "" || len(got.Tuples) != 0 {
		t.Fatalf("TEMP tuple source selected: %+v", got)
	}
	err = repos.TupleSource.ReadSnapshot(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		if allowed, err := reads.HasExactRole(ctx, "user", "shadow", "admin", "", ""); err != nil || allowed {
			t.Fatalf("TEMP role: %v/%v", allowed, err)
		}
		model := relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "document", Relation: "viewer", SubjectType: "user"}})
		if allowed, err := reads.ForChecks(model).CheckRelationWithGroupExpansion(ctx, "document", "d1", "viewer", "user", "shadow", 100); err != nil || allowed {
			t.Fatalf("TEMP relation: %v/%v", allowed, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTupleCacheMigrationUpgradeAndExport(t *testing.T) {
	db, _ := cacheFixture(t, false)
	for _, name := range []string{"0001_iam_cache_invalidation.sql", "0002_iam_tuple_cache.sql"} {
		data, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(t.Context(), func(tx *tursodb.Tx) error { _, err := tx.Exec(t.Context(), string(data)); return err }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Repositories(t.Context(), db, WithTupleCache()); err != nil {
		t.Fatal(err)
	}
	var old int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM main.sqlite_schema WHERE name='iam_cache_invalidation' OR name LIKE '%_cache_insert' OR name LIKE '%_cache_update' OR name LIKE '%_cache_delete'`).Scan(&old); err != nil || old != 0 {
		t.Fatalf("old objects remain: %d/%v", old, err)
	}
	dst := t.TempDir()
	if err := ExportCacheMigrations(dst); err != nil {
		t.Fatal(err)
	}
	files, err := CacheMigrationsFS.ReadDir(CacheMigrationsDir)
	if err != nil || len(files) != 2 {
		t.Fatalf("inventory %v/%v", files, err)
	}
	for _, file := range files {
		canonical, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		exported, err := os.ReadFile(filepath.Join(dst, file.Name()))
		if err != nil || !bytes.Equal(canonical, exported) {
			t.Fatalf("migration differs: %s/%v", file.Name(), err)
		}
	}
}

func TestTupleCachePayloadRejectsIdentityNormalization(t *testing.T) {
	for _, payload := range []string{
		"[\"space\",\"s\",\"viewer\",\"user\",\"\xff\",\"\"]",
	} {
		if tuple, err := decodeTuple(sql.NullString{String: payload, Valid: true}); !errors.Is(err, tuplecache.ErrUnavailable) || tuple != nil {
			t.Fatalf("malformed identity became a cache tuple: %+v/%v", tuple, err)
		}
	}
	// A real replacement character is a distinct valid ID; retain it exactly.
	tuple, err := decodeTuple(sql.NullString{String: `["space","s","viewer","user","�",""]`, Valid: true})
	if err != nil || tuple.SubjectID != "�" {
		t.Fatalf("valid opaque identity: %+v/%v", tuple, err)
	}
}
