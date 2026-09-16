package pgx

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
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
// serializes the guarded read and actual alternating grant/revoke on every call.
func BenchmarkGuardedWriterContentionPostgres(b *testing.B) {
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
					guard := func(ctx context.Context, view mutations.StoreDecisionView) error {
						_, err := view.CheckRelation(ctx, cmd.Target, "viewer", "user", "alice")
						return err
					}
					for next.Add(1) <= int64(b.N) {
						if _, err := repos.Mutations.ApplyGuarded(b.Context(), cmd, guard, nil); err != nil {
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
