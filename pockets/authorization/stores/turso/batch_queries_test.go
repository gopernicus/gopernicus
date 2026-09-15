package turso

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

// Count physical permission statements, after model scoping. Delivery metadata
// reads are outside this counter. The source and all iterators run sequentially.
type batchQueryCounter struct {
	tursodb.Querier
	count *int
}

func (q batchQueryCounter) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	*q.count++
	return q.Querier.Query(ctx, query, args...)
}
func (q batchQueryCounter) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	*q.count++
	return q.Querier.QueryRow(ctx, query, args...)
}

type sequentialBatchStore struct{ relationships.Storer }
type sequentialBatchReader struct{ relationships.Reader }

func (s sequentialBatchStore) ForModel(model relationships.ReadModel) relationships.Reader {
	return sequentialBatchReader{s.Storer.ForModel(model)}
}

type countedBatchSource struct {
	tuplecache.Source
	count *int
}

func (s countedBatchSource) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	return s.Source.ReadSnapshot(ctx, func(ctx context.Context, reads tuplecache.CheckReads) error {
		snapshot := reads.(*cacheSnapshot)
		snapshot.rel.readQuerier = batchQueryCounter{snapshot.rel.readQuerier, s.count}
		return fn(ctx, reads)
	})
}

func batchQuerySchema() relationships.Schema {
	return relationships.Schema{ResourceTypes: map[string]relationships.ResourceTypeDef{
		"group": {Relations: map[string]relationships.RelationDef{
			"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			"admin":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
		}},
		"tenant": {
			Relations:   map[string]relationships.RelationDef{"owner": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "admin"}}}},
			Permissions: map[string]relationships.PermissionRule{"manage": relationships.AnyOf(relationships.Direct("owner"))},
		},
		"space": {
			Relations: map[string]relationships.RelationDef{
				"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}},
				"tenant": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "tenant"}}},
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"), relationships.Through("parent", "view"), relationships.Through("tenant", "manage"))},
		},
		"dashboard": {
			Relations:   map[string]relationships.RelationDef{"container": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}}},
			Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Through("container", "view"))},
		},
	}}
}

func batchQueryFixture(t testing.TB, n int) (authorization.Repositories, []string, *int) {
	t.Helper()
	db, cfg := cacheFixture(t, true)
	repos, err := Repositories(context.Background(), db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	count := new(int)
	rel := repos.Relationships.(*relationshipStore)
	rel.readQuerier = batchQueryCounter{db, count}
	repos.TupleSource = countedBatchSource{repos.TupleSource, count}
	var tuples []relationships.CreateRelationship
	add := func(rt, id, relation, st, sid, sr string) {
		tuples = append(tuples, relationships.CreateRelationship{ResourceType: rt, ResourceID: id, Relation: relation, SubjectType: st, SubjectID: sid, SubjectRelation: sr})
	}
	add("group", "members", "member", "user", "alice", "")
	add("group", "owners", "admin", "group", "members", "member")
	// Bob is only a member of owners; that must not satisfy group:owners#admin.
	add("group", "owners", "member", "user", "bob", "")
	for i := range 4 {
		add("tenant", fmt.Sprintf("t%d", i), "owner", "group", "owners", "admin")
	}
	ids := make([]string, n)
	for i := range n {
		ids[i] = fmt.Sprintf("s%04d", i)
		add("space", ids[i], "tenant", "tenant", fmt.Sprintf("t%d", i%4), "")
		if i%10 == 9 {
			add("space", ids[i], "parent", "space", ids[i-1], "")
		}
		add("dashboard", ids[i], "container", "space", ids[i], "")
	}
	if err := repos.Relationships.CreateRelationships(context.Background(), tuples); err != nil {
		t.Fatal(err)
	}
	*count = 0
	return repos, ids, count
}

func TestThroughBatchSQLiteQueryCounts(t *testing.T) {
	for _, n := range []int{50, 128, 366, 600} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			repos, ids, count := batchQueryFixture(t, n)
			fast, err := relationships.NewService(repos.Relationships, batchQuerySchema())
			if err != nil {
				t.Fatal(err)
			}
			before, err := relationships.NewService(sequentialBatchStore{repos.Relationships}, batchQuerySchema())
			if err != nil {
				t.Fatal(err)
			}
			alice := authmodel.PrincipalRef{Type: "user", ID: "alice"}
			operations := []struct {
				name string
				run  func(*relationships.Service) ([]string, error)
			}{
				{"lookup", func(s *relationships.Service) ([]string, error) {
					r, e := s.LookupResources(t.Context(), alice, "view", "space")
					return r.IDs, e
				}},
				{"page50", func(s *relationships.Service) ([]string, error) {
					r, e := s.LookupResourcesPage(t.Context(), alice, "view", "space", "", 50)
					if e == nil && r.HasMore != (n > 50) {
						t.Fatalf("HasMore=%v", r.HasMore)
					}
					return r.IDs, e
				}},
				{"filter", func(s *relationships.Service) ([]string, error) {
					return s.FilterAuthorized(t.Context(), alice, "view", "dashboard", ids)
				}},
			}
			for _, op := range operations {
				*count = 0
				want, err := op.run(before.Service)
				oldCount := *count
				if err != nil {
					t.Fatal(err)
				}
				*count = 0
				got, err := op.run(fast.Service)
				newCount := *count
				if err != nil || !slices.Equal(got, want) || len(got) == 0 {
					t.Fatalf("%s: %v/%v", op.name, got, err)
				}
				if newCount > 25 || newCount*3 >= oldCount {
					t.Fatalf("%s N+1 regression: %d queries, baseline %d", op.name, newCount, oldCount)
				}
				t.Logf("%s: %d -> %d permission SQL queries (%d results)", op.name, oldCount, newCount, len(got))
			}
			// Real SQL group expansion keeps the exact userset relation.
			denied, err := fast.Service.FilterAuthorized(t.Context(), authmodel.PrincipalRef{Type: "user", ID: "bob"}, "view", "dashboard", ids)
			if err != nil || len(denied) != 0 {
				t.Fatalf("member treated as admin: %v/%v", denied, err)
			}
		})
	}
}

func TestThroughBatchSQLiteTupleCache(t *testing.T) {
	repos, ids, count := batchQueryFixture(t, 128)
	backend := memory.NewTupleCache()
	components, err := authorization.New(repos, authorization.WithRelationshipModel(batchQuerySchema()), authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: time.Minute}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = components.TupleCache.Close() })
	requests := make([]authmodel.CheckRequest, len(ids))
	for i, id := range ids {
		requests[i] = authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "alice"}, Permission: "view", Resource: authmodel.Resource{Type: "dashboard", ID: id}}
	}
	// Unsorted candidates, a duplicate and an absent resource exercise the
	// public filter's projection as well as its TupleCache routing.
	candidates := append([]string{ids[127], "missing", ids[0]}, ids...)
	allowedIDs := append([]string{ids[127], ids[0]}, ids...)
	check := func(want bool, wantSQL int) {
		t.Helper()
		*count = 0
		results, err := components.Decisions.CheckBatch(t.Context(), requests)
		if err != nil || len(results) != len(requests) {
			t.Fatalf("batch: %v/%v", results, err)
		}
		for _, result := range results {
			if result.Allowed != want {
				t.Fatalf("want allowed=%v: %+v", want, result)
			}
		}
		if *count != wantSQL {
			t.Fatalf("permission SQL: got %d want %d", *count, wantSQL)
		}
		*count = 0
		before := components.TupleCache.Stats()
		filtered, err := components.Decisions.FilterAuthorized(t.Context(), requests[0].Principal, "view", "dashboard", candidates)
		wantIDs := []string{}
		if want {
			wantIDs = allowedIDs
		}
		if err != nil || filtered == nil || !slices.Equal(filtered, wantIDs) {
			t.Fatalf("filter: %v/%v, want %v", filtered, err, wantIDs)
		}
		after := components.TupleCache.Stats()
		if *count != wantSQL {
			t.Fatalf("filter permission SQL: got %d want %d", *count, wantSQL)
		}
		if wantSQL == 0 && (after.Hits != before.Hits+1 || after.Fallbacks != before.Fallbacks) {
			t.Fatalf("filter did not use cache: %+v -> %+v", before, after)
		}
		if wantSQL != 0 && (after.Fallbacks != before.Fallbacks+1 || after.Hits != before.Hits) {
			t.Fatalf("filter did not use durable snapshot: %+v -> %+v", before, after)
		}
		t.Logf("filter: %d permission SQL queries, %d cache hits, %d durable fallbacks", *count, after.Hits-before.Hits, after.Fallbacks-before.Fallbacks)
	}
	check(true, 4) // Initial requests use one durable snapshot; they never fill Redis.
	if err := components.TupleCache.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(true, 0)
	if stats := components.TupleCache.Stats(); stats.Hits != 2 || stats.Rebuilds != 1 {
		t.Fatalf("cache not serving: %+v", stats)
	}
	first, err := backend.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	untouched := tuplecache.SetKey{Ref: relationships.SubjectRef{Type: "space", ID: ids[0], Relation: "tenant"}}
	before, err := backend.Read(t.Context(), first, []tuplecache.SetKey{untouched})
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.Relationships.DeleteRelationshipTarget(t.Context(), "group", "members", "member", relationships.SubjectRef{Type: "user", ID: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := components.TupleCache.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(false, 0)
	if err := components.TupleCache.Close(); err != nil {
		t.Fatal(err)
	}
	check(false, 7) // Revoked grants require exploring the remaining Through branches.
	second, err := backend.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	after, err := backend.Read(t.Context(), second, []tuplecache.SetKey{untouched})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("membership mutation changed unrelated set: %v -> %v", before, after)
	}
	pending := mustTupleSnapshot(t, repos.TupleSource, second.Receipt)
	if pending.Full || len(pending.Changes) != 0 {
		t.Fatalf("delivered outbox not disposed: %+v", pending)
	}
	// A fresh Redis-equivalent mirror reconstructs from current SQL, after all
	// original events and the revocation event have been disposed.
	restored, err := authorization.New(repos, authorization.WithRelationshipModel(batchQuerySchema()), authorization.WithTupleCache(memory.NewTupleCache(), tuplecache.Policy{MaxStaleness: time.Minute}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.TupleCache.Close() })
	if err := restored.TupleCache.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	*count = 0
	results, err := restored.Decisions.CheckBatch(t.Context(), requests)
	if err != nil || len(results) != len(requests) {
		t.Fatalf("rebuilt: %v/%v", results, err)
	}
	for _, result := range results {
		if result.Allowed {
			t.Fatal("rebuild resurrected revoked membership")
		}
	}
	if *count != 0 || restored.TupleCache.Stats().Rebuilds != 1 {
		t.Fatalf("rebuilt check used durable permission reads: %d/%+v", *count, restored.TupleCache.Stats())
	}
}

func BenchmarkThroughBatchSQLite(b *testing.B) {
	repos, ids, count := batchQueryFixture(b, 366)
	for _, mode := range []string{"sequential", "batched"} {
		b.Run(mode, func(b *testing.B) {
			store := repos.Relationships
			if mode == "sequential" {
				store = sequentialBatchStore{store}
			}
			parts, err := relationships.NewService(store, batchQuerySchema())
			if err != nil {
				b.Fatal(err)
			}
			principal := authmodel.PrincipalRef{Type: "user", ID: "alice"}
			b.ReportAllocs()
			b.ResetTimer()
			queries := 0
			for b.Loop() {
				*count = 0
				got, err := parts.Service.FilterAuthorized(context.Background(), principal, "view", "dashboard", ids)
				if err != nil || len(got) != len(ids) {
					b.Fatalf("filter: %d/%v", len(got), err)
				}
				queries = *count
			}
			b.ReportMetric(float64(queries), "queries/op")
		})
	}
}
