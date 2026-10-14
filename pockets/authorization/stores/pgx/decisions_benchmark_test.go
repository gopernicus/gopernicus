package pgx

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

// BenchmarkDecisionsPostgres needs POSTGRES_TEST_DSN for a disposable database.
// The graph has 366 documents sharing one userset-backed parent grant.
func BenchmarkDecisionsPostgres(b *testing.B) {
	db, cfg := cacheFixture(b, false)
	repos, err := testRepositories(b.Context(), db, WithSchema(cfg.schema))
	if err != nil {
		b.Fatal(err)
	}
	schema := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"group": {Relations: map[string]decisions.RelationDef{"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}},
		"space": {
			Relations:   map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}},
			Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("viewer"))},
		},
		"document": {
			Relations:   map[string]decisions.RelationDef{"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "space"}}}},
			Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Through("parent", "view"))},
		},
	}}
	tuples := []relationships.CreateRelationship{
		{ResourceType: "group", ResourceID: "members", Relation: "member", SubjectType: "user", SubjectID: "alice"},
		{ResourceType: "space", ResourceID: "main", Relation: "viewer", SubjectType: "group", SubjectID: "members", SubjectRelation: "member"},
		{ResourceType: "space", ResourceID: "direct", Relation: "viewer", SubjectType: "user", SubjectID: "alice"},
	}
	principal := authmodel.PrincipalRef{Type: "user", ID: "alice"}
	ids := make([]string, 366)
	requests := make([]authmodel.CheckRequest, len(ids))
	for i := range ids {
		ids[i] = fmt.Sprintf("d%04d", i)
		tuples = append(tuples, relationships.CreateRelationship{ResourceType: "document", ResourceID: ids[i], Relation: "parent", SubjectType: "space", SubjectID: "main"})
		requests[i] = authmodel.CheckRequest{Principal: principal, Permission: "view", Resource: authmodel.Resource{Type: "document", ID: ids[i]}}
	}
	if err := repos.Relationships.CreateRelationships(b.Context(), tuples); err != nil {
		b.Fatal(err)
	}
	components, err := authorization.New(repos.Repositories, authorization.WithModel(schema))
	if err != nil {
		b.Fatal(err)
	}
	for _, operation := range []string{"direct", "through", "batch", "filter"} {
		b.Run(operation, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				switch operation {
				case "direct", "through":
					req := requests[0]
					if operation == "direct" {
						req.Resource = authmodel.Resource{Type: "space", ID: "direct"}
					}
					result, err := components.Decisions.Check(b.Context(), req)
					if err != nil || !result.Allowed {
						b.Fatalf("check: %+v/%v", result, err)
					}
				case "batch":
					results, err := components.Decisions.CheckBatch(b.Context(), requests)
					if err != nil || len(results) != len(requests) {
						b.Fatalf("batch: %d/%v", len(results), err)
					}
					for _, result := range results {
						if !result.Allowed {
							b.Fatal("expected allowed batch")
						}
					}
				case "filter":
					got, err := components.Decisions.FilterAuthorized(b.Context(), principal, "view", "document", ids)
					if err != nil || len(got) != len(ids) {
						b.Fatalf("filter: %d/%v", len(got), err)
					}
				}
			}
		})
	}
}

// Distinct tenant IDs remove row contention; authorization's table lock still
// serializes integrity validation and actual alternating grant/revoke on every call.
func BenchmarkTupleWriterContentionPostgres(b *testing.B) {
	db, cfg := cacheFixture(b, false)
	repos, err := testRepositories(b.Context(), db, WithSchema(cfg.schema))
	if err != nil {
		b.Fatal(err)
	}
	for _, workers := range []int{1, 8} {
		b.Run(fmt.Sprintf("tenants=%d", workers), func(b *testing.B) {
			for i := range workers {
				if err := repos.Relationships.DeleteResourceRelationships(b.Context(), "tenant", fmt.Sprintf("tenant-%d", i)); err != nil {
					b.Fatal(err)
				}
			}
			var next atomic.Int64
			var wg sync.WaitGroup
			errors := make(chan error, workers)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range workers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cmd := mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "tenant", ID: fmt.Sprintf("tenant-%d", i)}, Operation: mutations.OpGrant, Relationships: []mutations.RelationshipRow{{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "alice"}}}}
					for next.Add(1) <= int64(b.N) {
						if _, err := repos.Mutations.Apply(b.Context(), cmd, nil); err != nil {
							errors <- err
							return
						}
						if cmd.Operation == mutations.OpGrant {
							cmd.Operation = mutations.OpRevoke
						} else {
							cmd.Operation = mutations.OpGrant
						}
					}
				}()
			}
			wg.Wait()
			b.StopTimer()
			close(errors)
			for err := range errors {
				b.Fatal(err)
			}
		})
	}
}

// BenchmarkDirectLookupPostgres needs POSTGRES_TEST_DSN for a disposable database.
// It measures Direct checks whose cost depends on which side of a tuple is large:
// alice holds 5,000 direct grants, and space "wide" has 10,001 grantees. Every
// case runs over durable PostgreSQL reads and over a polled memory tuple cache.
func BenchmarkDirectLookupPostgres(b *testing.B) {
	db, cfg := cacheFixture(b, true)
	repos, err := testRepositories(b.Context(), db, cacheOptions(cfg)...)
	if err != nil {
		b.Fatal(err)
	}
	schema := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"group": {Relations: map[string]decisions.RelationDef{"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}}},
		"space": {
			Relations:   map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}},
			Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("viewer"))},
		},
	}}
	table := cacheTable(cfg, "iam_tuples")
	bulk := []string{
		"INSERT INTO " + table + " SELECT 2,'space','s'||lpad(i::text,5,'0'),'viewer','user','alice','' FROM generate_series(1,5000) i",
		"INSERT INTO " + table + " SELECT 2,'space','wide','viewer','user','w'||lpad(i::text,5,'0'),'' FROM generate_series(1,10000) i",
	}
	for _, statement := range bulk {
		if _, err := db.Exec(b.Context(), statement); err != nil {
			b.Fatal(err)
		}
	}
	facts := []relationships.CreateRelationship{
		{ResourceType: "space", ResourceID: "target", Relation: "viewer", SubjectType: "user", SubjectID: "alice"},
		{ResourceType: "space", ResourceID: "target", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "space", ResourceID: "target", Relation: "viewer", SubjectType: "user", SubjectID: "u2"},
		{ResourceType: "group", ResourceID: "g1", Relation: "member", SubjectType: "user", SubjectID: "carol"},
		{ResourceType: "group", ResourceID: "g2", Relation: "member", SubjectType: "user", SubjectID: "carol"},
		{ResourceType: "group", ResourceID: "g3", Relation: "member", SubjectType: "user", SubjectID: "carol"},
		{ResourceType: "group", ResourceID: "team", Relation: "member", SubjectType: "user", SubjectID: "dave"},
		{ResourceType: "space", ResourceID: "via-group", Relation: "viewer", SubjectType: "group", SubjectID: "team", SubjectRelation: "member"},
		{ResourceType: "space", ResourceID: "wide", Relation: "viewer", SubjectType: "user", SubjectID: "erin"},
		{ResourceType: "space", ResourceID: "e1", Relation: "viewer", SubjectType: "user", SubjectID: "erin"},
		{ResourceType: "space", ResourceID: "e2", Relation: "viewer", SubjectType: "user", SubjectID: "erin"},
	}
	if err := repos.Relationships.CreateRelationships(b.Context(), facts); err != nil {
		b.Fatal(err)
	}
	cases := []struct {
		name      string
		principal string
		space     string
		want      bool
	}{
		{"hit_principal_5000_grants", "alice", "target", true},
		{"miss_userset_relation", "carol", "target", false},
		{"grant_via_userset", "dave", "via-group", true},
		{"hit_resource_10000_grantees", "erin", "wide", true},
	}
	run := func(b *testing.B, mode string, only string) {
		opts := []authorization.Option{authorization.WithModel(schema)}
		if mode == "memory_cache" {
			opts = append(opts, authorization.WithTupleCache(memory.NewTupleCache(), tuplecache.Policy{MaxStaleness: time.Hour}))
		}
		components, err := authorization.New(repos.Repositories, opts...)
		if err != nil {
			b.Fatal(err)
		}
		if components.TupleCache != nil {
			b.Cleanup(func() { _ = components.TupleCache.Close() })
			if err := components.TupleCache.Poll(b.Context()); err != nil {
				b.Fatal(err)
			}
		}
		for _, c := range cases {
			if only != "" && c.name != only {
				continue
			}
			b.Run(c.name, func(b *testing.B) {
				req := authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: c.principal}, Permission: "view", Resource: authmodel.Resource{Type: "space", ID: c.space}}
				var fallbacks uint64
				if components.TupleCache != nil {
					fallbacks = components.TupleCache.Stats().Fallbacks
				}
				b.ReportAllocs()
				for b.Loop() {
					result, err := components.Decisions.Check(b.Context(), req)
					if err != nil || result.Allowed != c.want {
						b.Fatalf("check: %+v/%v", result, err)
					}
				}
				if components.TupleCache != nil && components.TupleCache.Stats().Fallbacks != fallbacks {
					b.Fatalf("cache fell back to durable reads: %+v", components.TupleCache.Stats())
				}
			})
		}
	}
	// On a freshly loaded, un-vacuumed table the generic plan for the exact probe
	// ties the primary key against idx_iam_tuples_relation_resource, and the
	// narrower index wins: it filters every grantee of the wide resource.
	b.Run("durable_unvacuumed", func(b *testing.B) { run(b, "durable", "hit_resource_10000_grantees") })
	// Autovacuum would analyze the table and set its visibility map in production.
	if _, err := db.Exec(b.Context(), "VACUUM ANALYZE "+table); err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"durable", "memory_cache"} {
		b.Run(mode, func(b *testing.B) { run(b, mode, "") })
	}
}
