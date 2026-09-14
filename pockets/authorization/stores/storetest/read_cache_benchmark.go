package storetest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// CacheBenchmarkConfig separates metadata/writer overhead from reader adoption.
// A baseline factory must omit optional schema/head initialization entirely.
type CacheBenchmarkConfig struct{ Invalidation, CacheReads, Audit bool }
type CacheBenchmarkFactory func(*testing.B, CacheBenchmarkConfig) authorization.Repositories

// BenchmarkReadCache measures one sequential decision operation over 500 direct
// grants plus a userset and Through branch. These microbenchmarks report mean
// time/allocations, not network p95 or multi-process performance. Cold-cache
// eviction is excluded from the timer; normal cache lookups/fills are included.
func BenchmarkReadCache(b *testing.B, factory CacheBenchmarkFactory) {
	quietCacheBenchmark(b)
	for _, mode := range []string{"baseline", "writer-only", "bypass", "lru-warm", "lru-cold"} {
		for _, path := range []string{"check-allow", "check-deny", "userset", "through", "role", "explain", "role-explain", "userset-explain", "through-explain", "batch-1", "batch-10", "batch-100", "batch-500", "filter"} {
			b.Run(mode+"/"+path, func(b *testing.B) {
				cfg := CacheBenchmarkConfig{Invalidation: mode != "baseline", CacheReads: mode != "baseline" && mode != "writer-only"}
				repos := factory(b, cfg)
				ctx := context.Background()
				cache := cacher.NewMemory(cacher.WithMaxEntries(4096))
				options := []authorization.Option{authorization.WithRelationshipModel(cacheBenchmarkSchema()), authorization.WithRoleModel(cacheBenchmarkRoles())}
				if cfg.CacheReads {
					options = append(options, authorization.WithCacher(cache, decisions.CachePolicy{Namespace: b.Name(), MaxStaleness: time.Hour, CacheTimeout: time.Second, MaxFillEntries: 2048, MaxFillBytes: 4 << 20}))
				}
				parts, err := authorization.New(repos, options...)
				if err != nil {
					b.Fatal(err)
				}
				if parts.ReadCache != nil {
					b.Cleanup(func() { _ = parts.ReadCache.Close() })
				}
				ids := make([]string, 500)
				batch := make([]authmodel.CheckRequest, 500)
				tuples := make([]relationships.CreateRelationship, 0, 504)
				for i := range ids {
					ids[i] = fmt.Sprintf("d%03d", i)
					tuples = append(tuples, relationships.CreateRelationship{ResourceType: "document", ResourceID: ids[i], Relation: "viewer", SubjectType: "user", SubjectID: "reader"})
					batch[i] = authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "reader"}, Permission: "inspect", Resource: authmodel.Resource{Type: "document", ID: ids[i]}}
				}
				tuples = append(tuples,
					relationships.CreateRelationship{ResourceType: "document", ResourceID: "userset", Relation: "viewer", SubjectType: "group", SubjectID: "team", SubjectRelation: "member"},
					relationships.CreateRelationship{ResourceType: "group", ResourceID: "team", Relation: "member", SubjectType: "user", SubjectID: "reader"},
					relationships.CreateRelationship{ResourceType: "document", ResourceID: "nested", Relation: "parent", SubjectType: "folder", SubjectID: "f1"},
					relationships.CreateRelationship{ResourceType: "folder", ResourceID: "f1", Relation: "owner", SubjectType: "user", SubjectID: "reader"})
				if err := repos.Relationships.CreateRelationships(ctx, tuples); err != nil {
					b.Fatal(err)
				}
				if err := repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "reader", Role: "auditor"}); err != nil {
					b.Fatal(err)
				}
				run := func() error {
					req := batch[0]
					req.Permission = "view"
					switch path {
					case "check-deny":
						req.Principal.ID = "absent"
					case "userset", "userset-explain":
						req.Resource.ID = "userset"
					case "through", "through-explain":
						req.Resource.ID = "nested"
					case "role", "role-explain":
						req.Permission = "audit"
					}
					switch path {
					case "explain", "role-explain", "userset-explain", "through-explain":
						result, _, err := parts.Decisions.CheckExplain(ctx, req)
						if err == nil && !result.Allowed {
							return fmt.Errorf("benchmark explain unexpectedly denied")
						}
						return err
					case "batch-1", "batch-10", "batch-100", "batch-500":
						n := map[string]int{"batch-1": 1, "batch-10": 10, "batch-100": 100, "batch-500": 500}[path]
						results, err := parts.Decisions.CheckBatch(ctx, batch[:n])
						if err == nil && (len(results) != n || !results[n-1].Allowed) {
							return fmt.Errorf("benchmark batch unexpectedly denied")
						}
						return err
					case "filter":
						results, err := parts.Decisions.FilterAuthorized(ctx, req.Principal, "inspect", "document", ids[:100])
						if err == nil && len(results) != 100 {
							return fmt.Errorf("benchmark filter result count %d", len(results))
						}
						return err
					default:
						result, err := parts.Decisions.Check(ctx, req)
						if err == nil && result.Allowed == (path == "check-deny") {
							return fmt.Errorf("benchmark check wrong result")
						}
						return err
					}
				}
				if mode == "lru-warm" || mode == "lru-cold" {
					if err := parts.ReadCache.Poll(ctx); err != nil {
						b.Fatal(err)
					}
				}
				if err := run(); err != nil {
					b.Fatal(err)
				}
				if mode == "lru-warm" && path != "filter" {
					before := parts.ReadCache.Stats()
					if err := run(); err != nil {
						b.Fatal(err)
					}
					after := parts.ReadCache.Stats()
					if after.HitComplete != before.HitComplete+1 || after.MissFallback != before.MissFallback {
						b.Fatalf("warm case is not an all-hit operation: before=%+v after=%+v", before, after)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if mode == "lru-cold" {
						b.StopTimer()
						if err := cache.DeletePrefix(ctx, ""); err != nil {
							b.Fatal(err)
						}
						b.StartTimer()
					}
					if err := run(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkCacheWrites measures real grant/revoke pairs with and without the
// invalidation head, independently with audit disabled/enabled. No cacher or
// poller is constructed, so writer-only adoption cost is measured directly.
func BenchmarkCacheWrites(b *testing.B, factory CacheBenchmarkFactory) {
	quietCacheBenchmark(b)
	for _, invalidation := range []bool{false, true} {
		for _, audited := range []bool{false, true} {
			for _, kind := range []string{"relationship", "role"} {
				mode := "baseline"
				if invalidation {
					mode = "writer-only"
				}
				b.Run(fmt.Sprintf("%s/audit-%t/%s-grant-revoke", mode, audited, kind), func(b *testing.B) {
					repos := factory(b, CacheBenchmarkConfig{Invalidation: invalidation, Audit: audited})
					ctx := context.Background()
					if audited {
						ctx = audit.WithSource(ctx, audit.Source{ActorType: "user", ActorID: "benchmark", Reason: "owned benchmark fixture"})
					}
					tuple := relationships.CreateRelationship{ResourceType: "document", ResourceID: "write", Relation: "viewer", SubjectType: "user", SubjectID: "reader"}
					assignment := roles.Assignment{SubjectType: "user", SubjectID: "reader", Role: "auditor", ResourceType: "document", ResourceID: "write"}
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						if kind == "relationship" {
							if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{tuple}); err != nil {
								b.Fatal(err)
							}
							if err := repos.Relationships.DeleteRelationshipTarget(ctx, "document", "write", "viewer", relationships.SubjectRef{Type: "user", ID: "reader"}); err != nil {
								b.Fatal(err)
							}
						} else {
							if err := repos.Roles.Assign(ctx, assignment); err != nil {
								b.Fatal(err)
							}
							if err := repos.Roles.Unassign(ctx, "user", "reader", "auditor", "document", "write"); err != nil {
								b.Fatal(err)
							}
						}
					}
				})
			}
		}
	}
}
func cacheBenchmarkSchema() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{
		{Name: "document", Def: relationships.ResourceTypeDef{Relations: map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}, "parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "folder"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"), relationships.Through("parent", "view")), "inspect": relationships.AnyOf(relationships.Direct("viewer"))}}},
		{Name: "group", Def: relationships.ResourceTypeDef{Relations: map[string]relationships.RelationDef{"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}}}},
		{Name: "folder", Def: relationships.ResourceTypeDef{Relations: map[string]relationships.RelationDef{"owner": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("owner"))}}},
	})
}
func cacheBenchmarkRoles() authmodel.RoleModel {
	return authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{"document": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}}}}
}

func quietCacheBenchmark(b *testing.B) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(previous) })
}
