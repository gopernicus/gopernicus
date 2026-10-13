package turso

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func freshIAMTables(t *testing.T, db *tursodb.DB) []string {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT name FROM main.sqlite_schema WHERE type='table' AND name LIKE 'iam_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

func assertFreshIndexes(t *testing.T, db *tursodb.DB) {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT name FROM main.sqlite_schema WHERE type='index' AND tbl_name IN ('iam_tuples','iam_audit') AND name LIKE 'idx_iam_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(expectedIndexes)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("applied indexes: got %v, want %v", got, want)
	}
}

func TestFreshCanonicalSchema(t *testing.T) {
	db := canonicalFixture(t, false)
	assertFreshIndexes(t, db)
	if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_operations", "iam_tuples"}) {
		t.Fatalf("primary tables: %v", got)
	}
	repos, err := testRepositories(t.Context(), db, WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	if repos.TupleSource != nil {
		t.Fatal("primary installation enabled optional capture")
	}
	ctx := audit.WithSource(t.Context(), audit.Source{System: "fresh-schema-test"})
	owner := tuples.Tuple{Scope: tuples.On("organization", "o"), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "u"}}
	member := owner
	member.Relation = "member"
	global := owner
	global.Scope = tuples.Global()
	userset := global
	userset.Subject = tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}
	facts := []tuples.Tuple{owner, member, global, userset}
	if err := repos.Tuples.ApplyTuples(ctx, tuples.Changes{Add: facts}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Tuples.ApplyTuples(ctx, tuples.Changes{Add: facts}); err != nil {
		t.Fatal(err)
	}
	records, err := repos.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 20})
	if err != nil || len(records.Items) != 4 {
		t.Fatalf("canonical audit: %+v/%v", records, err)
	}
	for _, row := range records.Items {
		if row.Encoding != audit.EncodingTuple {
			t.Fatalf("audit encoding: %s", row.Encoding)
		}
	}
	for _, statement := range []string{
		`INSERT INTO iam_tuples VALUES(0,'','','owner','user','bad','')`,
		`INSERT INTO iam_tuples VALUES(1,'organization','o','owner','user','bad','')`,
		`INSERT INTO iam_tuples VALUES(2,'organization','','owner','user','bad','')`,
		`INSERT INTO iam_tuples VALUES(2,'organization','o','','user','bad','')`,
		`INSERT INTO iam_tuples VALUES(2,'organization','o','owner','','bad','')`,
		`INSERT INTO iam_tuples VALUES(2,'organization','o','owner','user','', '')`,
		`UPDATE iam_audit SET encoding='unsupported'`,
		`UPDATE iam_audit SET actor_type='user'`,
	} {
		if _, err := db.Exec(t.Context(), statement); err == nil {
			t.Fatalf("schema accepted invalid state: %s", statement)
		}
	}
	// Optional capture can be installed later without inventing historic deltas.
	if err := tursodb.RunMigrations(t.Context(), db, TupleCacheMigrationsFS, TupleCacheMigrationsDir); err != nil {
		t.Fatal(err)
	}
	cached, err := testRepositories(t.Context(), db, WithAudit(), WithTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := cached.TupleSource.Snapshot(t.Context(), "")
	slices.SortFunc(facts, tuples.Compare)
	slices.SortFunc(snapshot.Tuples, tuples.Compare)
	if err != nil || !snapshot.Full || !slices.Equal(snapshot.Tuples, facts) || len(snapshot.Changes) != 0 {
		t.Fatalf("cache-later bootstrap: %+v/%v", snapshot, err)
	}
	after, err := cached.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 20})
	if err != nil || !slices.Equal(after.Items, records.Items) {
		t.Fatalf("cache installation changed audit: %+v/%v", after, err)
	}
	if err := cached.TupleSource.Acknowledge(t.Context(), "", "ready", nil); err != nil {
		t.Fatal(err)
	}
	if err := cached.Tuples.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{owner}}); err != nil {
		t.Fatal(err)
	}
	delta, err := cached.TupleSource.Snapshot(t.Context(), "ready")
	if err != nil || delta.Full || len(delta.Changes) != 1 || delta.Changes[0].Before == nil || *delta.Changes[0].Before != owner {
		t.Fatalf("capture after late enable: %+v/%v", delta, err)
	}
	// Reapplying the exported stream uses its ledger and preserves cache identity.
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if err := tursodb.RunMigrations(t.Context(), db, TupleCacheMigrationsFS, TupleCacheMigrationsDir); err != nil {
		t.Fatal(err)
	}
	again, err := testRepositories(t.Context(), db, WithTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	if again.TupleSource.Binding() != cached.TupleSource.Binding() {
		t.Fatal("idempotent installation changed cache binding")
	}
}

func TestFreshCacheInstallationOrderAndRollback(t *testing.T) {
	db := emptyCanonicalFixture(t)
	if err := tursodb.RunMigrations(t.Context(), db, TupleCacheMigrationsFS, TupleCacheMigrationsDir); err == nil {
		t.Fatal("cache installed before its authority")
	}
	if got := freshIAMTables(t, db); len(got) != 0 {
		t.Fatalf("wrong-order partial schema: %v", got)
	}
	primary, err := MigrationsFS.ReadFile(MigrationsDir + "/0001_iam_tuples.sql")
	if err != nil {
		t.Fatal(err)
	}
	broken := fstest.MapFS{MigrationsDir + "/0001_iam_tuples.sql": &fstest.MapFile{Data: append(slices.Clone(primary), []byte("\nINVALID SQL;")...)}}
	if err := tursodb.RunMigrations(t.Context(), db, broken, MigrationsDir); err == nil {
		t.Fatal("broken primary migration succeeded")
	}
	if got := freshIAMTables(t, db); len(got) != 0 {
		t.Fatalf("primary DDL escaped rollback: %v", got)
	}
	// A merged host export must apply the primary before optional cache capture.
	merged := fstest.MapFS{MigrationsDir + "/0001_iam_tuples.sql": &fstest.MapFile{Data: primary}}
	cache, err := TupleCacheMigrationsFS.ReadFile(TupleCacheMigrationsDir + "/0002_iam_tuple_cache.sql")
	if err != nil {
		t.Fatal(err)
	}
	operations, err := MigrationsFS.ReadFile(MigrationsDir + "/0003_iam_operations.sql")
	if err != nil {
		t.Fatal(err)
	}
	merged[MigrationsDir+"/0003_iam_operations.sql"] = &fstest.MapFile{Data: operations}
	fenced, err := MigrationsFS.ReadFile(MigrationsDir + "/0004_iam_operations_fenced.sql")
	if err != nil {
		t.Fatal(err)
	}
	merged[MigrationsDir+"/0004_iam_operations_fenced.sql"] = &fstest.MapFile{Data: fenced}
	merged[MigrationsDir+"/0002_iam_tuple_cache.sql"] = &fstest.MapFile{Data: cache}
	if err := tursodb.RunMigrations(t.Context(), db, merged, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_operations", "iam_tuple_cache", "iam_tuple_outbox", "iam_tuples"}) {
		t.Fatalf("merged schema: %v", got)
	}
	if _, err := testRepositories(t.Context(), db, WithAudit(), WithTupleCache()); err != nil {
		t.Fatal(err)
	}
	t.Run("LateCacheFailurePreservesAuthority", func(t *testing.T) {
		db := canonicalFixture(t, false)
		repos, err := testRepositories(t.Context(), db, WithAudit())
		if err != nil {
			t.Fatal(err)
		}
		ctx := audit.WithSource(t.Context(), audit.Source{System: "cache-install-rollback"})
		fact := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
		if err := repos.Tuples.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{fact}}); err != nil {
			t.Fatal(err)
		}
		before, err := repos.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		broken := fstest.MapFS{TupleCacheMigrationsDir + "/0002_iam_tuple_cache.sql": &fstest.MapFile{Data: append(slices.Clone(cache), []byte("\nINVALID SQL;")...)}}
		if err := tursodb.RunMigrations(ctx, db, broken, TupleCacheMigrationsDir); err == nil {
			t.Fatal("broken optional migration succeeded")
		}
		if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_operations", "iam_tuples"}) {
			t.Fatalf("cache DDL escaped rollback: %v", got)
		}
		found, err := repos.Tuples.Contains(ctx, fact)
		if err != nil || !found {
			t.Fatalf("authority changed during failed install: %v/%v", found, err)
		}
		after, err := repos.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 20})
		if err != nil || !slices.Equal(after.Items, before.Items) {
			t.Fatalf("audit changed during failed install: %+v/%v", after, err)
		}
		if err := tursodb.RunMigrations(ctx, db, TupleCacheMigrationsFS, TupleCacheMigrationsDir); err != nil {
			t.Fatal(err)
		}
		if _, err := testRepositories(ctx, db, WithTupleCache()); err != nil {
			t.Fatal(err)
		}
	})

}

// canonicalOperationsDDL is iam_operations exactly as SQLite stores it after
// 0004's rename: the header is rewritten, the body is 0004's CREATE body.
const canonicalOperationsDDL = `CREATE TABLE "iam_operations" (
    operation_id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    encoding TEXT COLLATE BINARY NOT NULL,
    fingerprint TEXT COLLATE BINARY NOT NULL,
    outcome TEXT COLLATE BINARY NOT NULL,
    committed_at TEXT NOT NULL,
    CONSTRAINT ck_iam_operations_id CHECK (operation_id <> ''),
    CONSTRAINT ck_iam_operations_encoding CHECK (encoding = 'operation/v1'),
    CONSTRAINT ck_iam_operations_fingerprint CHECK (length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
    CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found', 'fenced'))
)`

func operationsDDL(t *testing.T, db *tursodb.DB) string {
	t.Helper()
	var ddl string
	if err := db.QueryRow(t.Context(), `SELECT sql FROM main.sqlite_schema WHERE type='table' AND name='iam_operations'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	return ddl
}

func baseOperationsMigrations(t *testing.T) fstest.MapFS {
	t.Helper()
	old := fstest.MapFS{}
	for _, name := range []string{"0001_iam_tuples.sql", "0003_iam_operations.sql"} {
		data, err := MigrationsFS.ReadFile(MigrationsDir + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		old[MigrationsDir+"/"+name] = &fstest.MapFile{Data: data}
	}
	return old
}

func TestFreshOperationsSchemaMatchesFixture(t *testing.T) {
	db := canonicalFixture(t, false)
	if got := operationsDDL(t, db); got != canonicalOperationsDDL {
		t.Fatalf("fresh iam_operations:\n%s\nwant:\n%s", got, canonicalOperationsDDL)
	}
	if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_operations", "iam_tuples"}) {
		t.Fatalf("tables after 0004: %v", got)
	}
}

// TestOperationsFencedUpgrade applies 0001+0003, seeds every committed outcome,
// then applies 0004: the bundle probe refuses the old check, the rebuild keeps
// every record byte-for-byte and the stored DDL matches the fixture.
func TestOperationsFencedUpgrade(t *testing.T) {
	db := emptyCanonicalFixture(t)
	if err := tursodb.RunMigrations(t.Context(), db, baseOperationsMigrations(t), MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := testRepositories(t.Context(), db); err == nil {
		t.Fatal("bundle accepted the 0003-only outcome check")
	}
	if _, err := RelationshipRepository(t.Context(), db); err != nil {
		t.Fatalf("facade requires 0004: %v", err)
	}
	seeded := []mutations.Outcome{mutations.OutcomeApplied, mutations.OutcomeNoChange, mutations.OutcomeNotFound}
	for i, outcome := range seeded {
		if _, err := db.Exec(t.Context(), `INSERT INTO main.iam_operations VALUES (?, 'operation/v1', ?, ?, ?)`, "seed:"+string(outcome), strings.Repeat(string(rune('a'+i)), 64), string(outcome), "2026-10-13T00:00:0"+string(rune('0'+i))+"Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO main.iam_operations VALUES ('seed:fenced', 'operation/v1', ?, 'fenced', '2026-10-13T00:00:00Z')`, mutations.FenceFingerprint); err == nil {
		t.Fatal("0003 check accepted a fenced outcome")
	}
	before := operationRows(t, db)
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if got := operationsDDL(t, db); got != canonicalOperationsDDL {
		t.Fatalf("upgraded iam_operations:\n%s\nwant:\n%s", got, canonicalOperationsDDL)
	}
	if after := operationRows(t, db); !slices.Equal(after, before) || len(after) != len(seeded) {
		t.Fatalf("rebuild changed records: before=%v after=%v", before, after)
	}
	if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_operations", "iam_tuples"}) {
		t.Fatalf("tables after 0004: %v", got)
	}
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatalf("bundle after 0004: %v", err)
	}
	for _, outcome := range seeded {
		if got, ok, err := repos.Operations.Recorded(t.Context(), "seed:"+string(outcome)); err != nil || !ok || got != outcome {
			t.Fatalf("seeded %s after upgrade: %q %v %v", outcome, got, ok, err)
		}
		if got, err := repos.Operations.Fence(t.Context(), "seed:"+string(outcome)); err != nil || got.Outcome != outcome || !got.Recorded {
			t.Fatalf("fence over seeded %s: %+v %v", outcome, got, err)
		}
	}
	if got, err := repos.Operations.Fence(t.Context(), "fresh"); err != nil || got.Outcome != mutations.OutcomeFenced || got.Recorded {
		t.Fatalf("fence after upgrade: %+v %v", got, err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO main.iam_operations VALUES ('bad', 'operation/v1', ?, 'refused', '2026-10-13T00:00:00Z')`, mutations.FenceFingerprint); err == nil {
		t.Fatal("0004 check accepted an unknown outcome")
	}
}

// TestOperationsFencedUpgradeFailsOnHostView pins the documented caveat: a host
// view naming iam_operations makes 0004 fail and roll back whole.
func TestOperationsFencedUpgradeFailsOnHostView(t *testing.T) {
	db := emptyCanonicalFixture(t)
	if err := tursodb.RunMigrations(t.Context(), db, baseOperationsMigrations(t), MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `CREATE VIEW main.host_operations AS SELECT operation_id FROM main.iam_operations`); err != nil {
		t.Fatal(err)
	}
	before := operationsDDL(t, db)
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err == nil {
		t.Fatal("0004 applied over a host view")
	}
	if got := operationsDDL(t, db); got != before {
		t.Fatalf("failed 0004 left a partial rebuild:\n%s", got)
	}
	if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_operations", "iam_tuples"}) {
		t.Fatalf("failed 0004 left tables: %v", got)
	}
}

// TestOperationsFencedUpgradeDropsHostTriggersAndIndexes pins the documented
// caveat: the rebuild succeeds and silently drops host triggers and indexes on
// iam_operations, so hosts must save and recreate them.
func TestOperationsFencedUpgradeDropsHostTriggersAndIndexes(t *testing.T) {
	db := emptyCanonicalFixture(t)
	if err := tursodb.RunMigrations(t.Context(), db, baseOperationsMigrations(t), MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `CREATE TABLE main.host_operation_log (operation_id TEXT);
CREATE TRIGGER main.host_operations_log AFTER INSERT ON iam_operations BEGIN INSERT INTO host_operation_log VALUES (NEW.operation_id); END;
CREATE INDEX main.host_operations_outcome ON iam_operations(outcome);`); err != nil {
		t.Fatal(err)
	}
	preflight := `SELECT type || ':' || name FROM main.sqlite_master WHERE tbl_name='iam_operations' AND name NOT LIKE 'sqlite_autoindex%' AND type<>'table' ORDER BY name`
	objects := func() []string {
		rows, err := db.Query(t.Context(), preflight)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var object string
			if err := rows.Scan(&object); err != nil {
				t.Fatal(err)
			}
			out = append(out, object)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := objects(); !slices.Equal(got, []string{"trigger:host_operations_log", "index:host_operations_outcome"}) {
		t.Fatalf("preflight before 0004: %v", got)
	}
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("0004 must not fail on host triggers or indexes: %v", err)
	}
	if got := objects(); len(got) != 0 {
		t.Fatalf("0004 kept host objects (update the UPGRADE caveat): %v", got)
	}
	if got := operationsDDL(t, db); got != canonicalOperationsDDL {
		t.Fatalf("upgraded iam_operations:\n%s", got)
	}
	if _, err := testRepositories(t.Context(), db); err != nil {
		t.Fatalf("boot after 0004: %v", err)
	}
}

// TestRecordedJoinsAmbientSingleConnection proves Recorded reads through the
// ambient transaction: with one pooled connection held by Transact, a pool read
// would block until the deadline.
func TestRecordedJoinsAmbientSingleConnection(t *testing.T) {
	db, err := tursodb.Open(t.Context(), tursodb.Config{URL: "file:" + filepath.Join(t.TempDir(), "single.sqlite"), MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	cmd := mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d"}, Operation: mutations.OpBatch, Tuples: tuples.Changes{Add: []tuples.Tuple{{Scope: tuples.On("doc", "d"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "u"}}}}, OperationID: "ambient:single"}
	if _, err := repos.Mutations.Apply(t.Context(), cmd, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = db.Transact(ctx, func(ctx context.Context) error {
		outcome, ok, err := repos.Operations.Recorded(ctx, cmd.OperationID)
		if err != nil || !ok || outcome != mutations.OutcomeApplied {
			t.Fatalf("Recorded inside Transact: %q %v %v", outcome, ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func operationRows(t *testing.T, db *tursodb.DB) []string {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT operation_id || '|' || encoding || '|' || fingerprint || '|' || outcome || '|' || committed_at FROM main.iam_operations ORDER BY operation_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
