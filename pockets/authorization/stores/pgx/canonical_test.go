package pgx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func emptyCanonicalFixture(t testing.TB) *pgxdb.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set: canonical SQL NOT verified")
	}
	admin, err := pgxdb.Open(t.Context(), pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := pgxdb.NewSchema(fmt.Sprintf("canonical_%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	quoted := schema.Table("")
	quoted = quoted[:len(quoted)-1]
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema.String())
	parsed.RawQuery = query.Encode()
	db, err := pgxdb.Open(t.Context(), pgxdb.Config{DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); admin.Close() })
	return db
}

func canonicalFixture(t testing.TB, cache bool) *pgxdb.DB {
	t.Helper()
	db := emptyCanonicalFixture(t)
	if err := pgxdb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	if cache {
		data, err := TupleCacheMigrationsFS.ReadFile(TupleCacheMigrationsDir + "/0002_iam_tuple_cache.sql")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(t.Context(), func(tx *pgxdb.Tx) error { _, err := tx.Exec(t.Context(), string(data)); return err }); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func TestCanonicalSQLFactsAndCache(t *testing.T) {
	db := canonicalFixture(t, true)
	repos, err := testRepositories(t.Context(), db, WithTupleCache(), WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithSource(t.Context(), audit.Source{System: "canonical-test"})
	fact := tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "editor", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	other := fact
	other.Relation = "owner"
	global := fact
	global.Scope = tuples.Global()
	userset := global
	userset.Subject.Relation = "member"
	if err := repos.Tuples.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{fact, other, global, userset, fact}}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "doc", ResourceID: "d", Relation: "editor", SubjectType: "user", SubjectID: "alice"}}); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Tuples.ContainsMany(ctx, []tuples.Tuple{fact, global, other, fact})
	if err != nil || !slices.Equal(got, []bool{true, true, true, true}) {
		t.Fatalf("contains: %v %v", got, err)
	}
	rows, err := repos.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 20})
	if err != nil || len(rows.Items) != 4 {
		t.Fatalf("actual delta audit: %+v %v", rows, err)
	}
	for _, r := range rows.Items {
		if r.Encoding != "tuple/v2" {
			t.Fatalf("encoding: %+v", r)
		}
	}
	snap, err := repos.TupleSource.Snapshot(ctx, "")
	if err != nil || len(snap.Tuples) != 4 || !snap.Full {
		t.Fatalf("cache snapshot: %+v %v", snap, err)
	}
	ids := make([]string, len(snap.Changes))
	for i, c := range snap.Changes {
		ids[i] = c.ID
	}
	if err := repos.TupleSource.Acknowledge(ctx, snap.Receipt, "baseline", ids); err != nil {
		t.Fatal(err)
	}
	if err := repos.Tuples.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{global}}); err != nil {
		t.Fatal(err)
	}
	delta, err := repos.TupleSource.Snapshot(ctx, "baseline")
	if err != nil || delta.Full || len(delta.Changes) != 1 || delta.Changes[0].Before == nil || *delta.Changes[0].Before != global {
		t.Fatalf("role removal capture: %+v %v", delta, err)
	}
	var escaped tuples.Reader
	err = repos.Tuples.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error { escaped = r; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.Contains(ctx, fact); !errors.Is(err, tuples.ErrSnapshotClosed) {
		t.Fatalf("escaped: %v", err)
	}
	oldCursor, _ := list.EncodeCursor("tuple_key", "doc\x01d\x01editor\x01user\x01alice\x01", "doc\x01d\x01editor\x01user\x01alice\x01")
	if _, err := repos.Tuples.ListTuples(ctx, tuples.Query{}, list.Request{Cursor: oldCursor}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("obsolete cursor: %v", err)
	}
}
func TestCanonicalSQLBulkAndSnapshots(t *testing.T) {
	db := canonicalFixture(t, false)
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	facts := make([]tuples.Tuple, 205)
	keys := make([]tuples.SetKey, 205)
	for i := range facts {
		scope := tuples.On("doc", fmt.Sprintf("%03d", i))
		facts[i] = tuples.Tuple{Scope: scope, Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
		keys[i] = tuples.SetKey{Scope: scope, Relation: "viewer"}
	}
	if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Tuples.ContainsMany(t.Context(), facts)
	if err != nil || len(got) != len(facts) {
		t.Fatalf("bulk: %v %v", got, err)
	}
	for _, held := range got {
		if !held {
			t.Fatal("missing bulk fact")
		}
	}
	probes := make([]tuples.Tuple, 307)
	want := make([]bool, len(probes))
	for i := range probes {
		probes[i] = facts[(i*17)%len(facts)]
		want[i] = i%3 != 0
		if !want[i] {
			probes[i].Relation = "absent"
		}
	}
	matches, err := repos.Tuples.ContainsMany(t.Context(), probes)
	if err != nil || !slices.Equal(matches, want) {
		t.Fatalf("transport order/duplicates: %v %v", matches, err)
	}
	repeated := []tuples.SetKey{keys[101], keys[0], keys[101]}
	repeatedSets, err := repos.Tuples.ReadSets(t.Context(), repeated, 3)
	if err != nil || len(repeatedSets) != 3 || len(repeatedSets[0]) != 1 || repeatedSets[0][0] != facts[101] || repeatedSets[1][0] != facts[0] || repeatedSets[2][0] != facts[101] {
		t.Fatalf("repeated sets: %+v %v", repeatedSets, err)
	}
	if partial, err := repos.Tuples.ReadSets(t.Context(), repeated, 2); partial != nil || !errors.Is(err, tuples.ErrReadLimit) {
		t.Fatalf("duplicate bound: %+v %v", partial, err)
	}
	sets, err := repos.Tuples.ReadSets(t.Context(), keys, 205)
	if err != nil || len(sets) != len(keys) {
		t.Fatalf("sets: %v %v", len(sets), err)
	}
	if partial, err := repos.Tuples.ReadSets(t.Context(), keys, 204); !errors.Is(err, tuples.ErrReadLimit) || partial != nil {
		t.Fatalf("overflow leaked: %v %v", partial, err)
	}
	var walked []tuples.Tuple
	req := list.Request{Limit: 7}
	for {
		page, err := repos.Tuples.ListTuples(t.Context(), tuples.Query{}, req)
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, page.Items...)
		if page.NextCursor == "" {
			break
		}
		req.Cursor = page.NextCursor
	}
	if !slices.Equal(walked, facts) {
		t.Fatalf("canonical keyset: %d", len(walked))
	}
	rollback := errors.New("rollback")
	err = db.TransactSnapshot(t.Context(), func(ctx context.Context) error {
		if err := repos.Tuples.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{facts[0]}}); err != nil {
			return err
		}
		return repos.Tuples.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
			held, err := r.Contains(ctx, facts[0])
			if err != nil {
				return err
			}
			if held {
				return errors.New("snapshot lost pending delete")
			}
			return rollback
		})
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if held, err := repos.Tuples.Contains(t.Context(), facts[0]); err != nil || !held {
		t.Fatalf("rollback: %v %v", held, err)
	}
}
func TestCanonicalSQLIntegrityAcrossRoleWrites(t *testing.T) {
	db := canonicalFixture(t, false)
	policy := mutations.IntegrityPolicy{Rules: []mutations.IntegrityRule{{ResourceType: "doc", Relation: "owner", MinSubjects: 1}}}
	repos, err := testRepositories(t.Context(), db, WithIntegrityPolicy(policy))
	if err != nil {
		t.Fatal(err)
	}
	owner := tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{owner}}); err != nil {
		t.Fatal(err)
	}
	cmd := mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d"}, Operation: mutations.OpRoleUnassign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "alice", Role: "owner"}}}
	if result, err := repos.Mutations.Apply(t.Context(), cmd, nil); !errors.Is(err, mutations.ErrInvariantBlocked) || result != nil {
		t.Fatalf("integrity bypass: %+v %v", result, err)
	}
	if held, err := repos.Tuples.Contains(t.Context(), owner); err != nil || !held {
		t.Fatalf("lost integrity: %v %v", held, err)
	}
}

func TestCanonicalSQLRejectsWeakAmbientSnapshot(t *testing.T) {
	db := canonicalFixture(t, true)
	repos, err := testRepositories(t.Context(), db, WithTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	entered := false
	err = db.Transact(t.Context(), func(ctx context.Context) error {
		return repos.TupleSource.ReadSnapshot(ctx, func(context.Context, tuples.Reader) error { entered = true; return nil })
	})
	if entered || !errors.Is(err, tuples.ErrSnapshotIsolation) {
		t.Fatalf("weak ambient entered=%v err=%v", entered, err)
	}
	comps, err := authorization.New(repos.Repositories)
	if err != nil {
		t.Fatal(err)
	}
	fact := tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{fact}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Transact(t.Context(), func(ctx context.Context) error {
		bySubject, err := comps.Relationships.ListRelationshipsBySubject(ctx, "user", "alice", relationships.SubjectRelationshipFilter{}, list.Request{WithCount: true})
		if !errors.Is(err, tuples.ErrSnapshotIsolation) || len(bySubject.Items) != 0 || bySubject.Total != nil {
			t.Fatalf("subject listing in weak ambient: %+v/%v", bySubject, err)
		}
		byResource, err := comps.Relationships.ListRelationshipsByResource(ctx, "doc", "d", relationships.ResourceRelationshipFilter{}, list.Request{WithCount: true})
		if !errors.Is(err, tuples.ErrSnapshotIsolation) || len(byResource.Items) != 0 || byResource.Total != nil {
			t.Fatalf("resource listing in weak ambient: %+v/%v", byResource, err)
		}
		count, err := comps.Relationships.CountByResourceAndRelation(ctx, "doc", "d", "viewer")
		if !errors.Is(err, tuples.ErrSnapshotIsolation) || count != 0 {
			t.Fatalf("count in weak ambient: %d/%v", count, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assignRole(store tuples.Storer, ctx context.Context, a roles.Assignment) error {
	return store.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{a.Tuple()}})
}
func roleFact(st, sid, role, rt, rid string) tuples.Tuple {
	scope := tuples.Global()
	if rt != "" || rid != "" {
		scope = tuples.On(rt, rid)
	}
	return tuples.Tuple{Scope: scope, Relation: role, Subject: tuples.SubjectRef{Type: st, ID: sid}}
}
func hasRoleTuple(store tuples.Reader, ctx context.Context, st, sid, role, rt, rid string) (bool, error) {
	return store.Contains(ctx, roleFact(st, sid, role, rt, rid))
}
func removeRole(store tuples.Storer, ctx context.Context, st, sid, role, rt, rid string) error {
	return store.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{roleFact(st, sid, role, rt, rid)}})
}
func listRoleAssignments(store tuples.Storer, ctx context.Context, st, sid string, req list.Request) (list.Page[roles.Assignment], error) {
	service, err := roles.NewService(store)
	if err != nil {
		return list.Page[roles.Assignment]{}, err
	}
	return service.ListRoleAssignmentsBySubject(ctx, authmodel.PrincipalRef{Type: st, ID: sid}, req)
}

func TestCanonicalReconcileRetryRetainsDesiredState(t *testing.T) {
	db := canonicalFixture(t, false)
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	scope := tuples.On("doc", "retry")
	alice := tuples.SubjectRef{Type: "user", ID: "alice"}
	bob := tuples.SubjectRef{Type: "user", ID: "bob"}
	carol := tuples.SubjectRef{Type: "user", ID: "carol"}
	if err := repos.Tuples.ReconcileTuples(t.Context(), scope, "viewer", []tuples.SubjectRef{alice, bob}); err != nil {
		t.Fatal(err)
	}
	// Sequence values survive rollback, making exactly the first attempt fail.
	if _, err := db.Exec(t.Context(), `CREATE SEQUENCE reconcile_attempt;
 CREATE FUNCTION fail_reconcile_once() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF nextval('reconcile_attempt')=1 THEN RAISE EXCEPTION 'retry fixture' USING ERRCODE='40001'; END IF; RETURN OLD; END; $$;
 CREATE TRIGGER retry_delete BEFORE DELETE ON iam_tuples FOR EACH ROW EXECUTE FUNCTION fail_reconcile_once()`); err != nil {
		t.Fatal(err)
	}
	if err := repos.Tuples.ReconcileTuples(t.Context(), scope, "viewer", []tuples.SubjectRef{alice, carol}); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Tuples.Lookup(t.Context(), tuples.Query{Scope: &scope})
	if err != nil || len(got) != 2 || got[0].Subject != alice || got[1].Subject != carol {
		t.Fatalf("retry changed desired set: %+v %v", got, err)
	}
}

func TestCanonicalSubjectMutationReadsOnlyItsTarget(t *testing.T) {
	db := canonicalFixture(t, false)
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	concrete := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	userset := concrete
	userset.Subject.Relation = "member"
	if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{concrete, userset}}); err != nil {
		t.Fatal(err)
	}
	// Direct SQL can contain invalid external data. An unrelated subject must
	// neither enter this command's read set nor prevent a targeted revocation.
	if _, err := db.Exec(t.Context(), "INSERT INTO iam_tuples ("+tupleColumns+") VALUES (1,'','','viewer','user',$1,'')", strings.Repeat("x", 257)); err != nil {
		t.Fatal(err)
	}
	result, err := repos.Mutations.Apply(t.Context(), mutations.Command{
		Target:    mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "alice"},
		Operation: mutations.OpBatch, Tuples: tuples.Changes{Remove: []tuples.Tuple{userset}},
	}, nil)
	if err != nil || result == nil {
		t.Fatalf("targeted userset mutation: %+v %v", result, err)
	}
	got, err := repos.Tuples.ContainsMany(t.Context(), []tuples.Tuple{concrete, userset})
	if err != nil || !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("concrete/userset isolation: %v %v", got, err)
	}
}
