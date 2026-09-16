package pgx

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func freshIAMTables(t *testing.T, db *pgxdb.DB) []string {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE 'iam_%' ORDER BY tablename`)
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

func assertFreshIndexes(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT indexname FROM pg_indexes WHERE schemaname=current_schema() AND tablename IN ('iam_tuples','iam_audit') AND indexname LIKE 'idx_iam_%' ORDER BY indexname`)
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
	if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_tuples"}) {
		t.Fatalf("primary tables: %v", got)
	}
	for _, table := range []string{"iam_tuples", "iam_audit"} {
		var dataType string
		err := db.QueryRow(t.Context(), `SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name='scope_kind'`, table).Scan(&dataType)
		if err != nil {
			t.Fatal(err)
		}
		if dataType != "smallint" {
			t.Fatalf("%s.scope_kind type: got %q, want smallint", table, dataType)
		}
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
	if err := pgxdb.RunMigrations(t.Context(), db, TupleCacheMigrationsFS, TupleCacheMigrationsDir); err != nil {
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
	if err := pgxdb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if err := pgxdb.RunMigrations(t.Context(), db, TupleCacheMigrationsFS, TupleCacheMigrationsDir); err != nil {
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
	if err := pgxdb.RunMigrations(t.Context(), db, TupleCacheMigrationsFS, TupleCacheMigrationsDir); err == nil {
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
	if err := pgxdb.RunMigrations(t.Context(), db, broken, MigrationsDir); err == nil {
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
	merged[MigrationsDir+"/0002_iam_tuple_cache.sql"] = &fstest.MapFile{Data: cache}
	if err := pgxdb.RunMigrations(t.Context(), db, merged, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_tuple_cache", "iam_tuple_outbox", "iam_tuples"}) {
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
		if err := pgxdb.RunMigrations(ctx, db, broken, TupleCacheMigrationsDir); err == nil {
			t.Fatal("broken optional migration succeeded")
		}
		if got := freshIAMTables(t, db); !slices.Equal(got, []string{"iam_audit", "iam_tuples"}) {
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
		if err := pgxdb.RunMigrations(ctx, db, TupleCacheMigrationsFS, TupleCacheMigrationsDir); err != nil {
			t.Fatal(err)
		}
		if _, err := testRepositories(ctx, db, WithTupleCache()); err != nil {
			t.Fatal(err)
		}
	})

}
