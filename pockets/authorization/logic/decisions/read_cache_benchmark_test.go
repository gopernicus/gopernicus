package decisions_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// BenchmarkReadCacheMemory isolates coordinator CPU/allocation cost. Durable
// latency and two-process cache locality belong to the owned host fixture.
func BenchmarkReadCacheMemory(b *testing.B) {
	for _, mode := range []string{"direct", "bypass", "warm", "miss"} {
		for _, path := range []string{"check-allow", "check-deny", "through", "role", "explain", "batch-1", "batch-10", "batch-100", "batch-500", "filter"} {
			b.Run(mode+"/"+path, func(b *testing.B) {
				ctx := context.Background()
				var storeOptions []memory.Option
				if mode != "direct" {
					storeOptions = append(storeOptions, memory.WithCacheReads())
				}
				store := memory.New(storeOptions...)
				cache := cacher.NewMemory()
				opts := []authorization.Option{authorization.WithRelationshipModel(compositeSchema()), authorization.WithRoleModel(compositeRoleModel())}
				if mode != "direct" {
					opts = append(opts, authorization.WithCacher(cache, decisions.CachePolicy{Namespace: b.Name(), MaxStaleness: time.Hour, MaxFillEntries: 2048}))
				}
				c, err := authorization.New(authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), CacheSource: store.CacheSource()}, opts...)
				if err != nil {
					b.Fatal(err)
				}
				if c.ReadCache != nil {
					b.Cleanup(func() { _ = c.ReadCache.Close() })
				}
				var tuples []relationships.CreateRelationship
				ids := make([]string, 500)
				batch := make([]authmodel.CheckRequest, 500)
				for i := range ids {
					ids[i] = fmt.Sprintf("p%d", i)
					tuples = append(tuples, relationships.CreateRelationship{ResourceType: "project", ResourceID: ids[i], Relation: "viewer", SubjectType: "user", SubjectID: "u"})
					batch[i] = authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "u"}, Permission: "view", Resource: authmodel.Resource{Type: "project", ID: ids[i]}}
				}
				tuples = append(tuples, relationships.CreateRelationship{ResourceType: "project", ResourceID: "nested", Relation: "org", SubjectType: "org", SubjectID: "o"}, relationships.CreateRelationship{ResourceType: "org", ResourceID: "o", Relation: "member", SubjectType: "user", SubjectID: "u"})
				if err := store.Relationships().CreateRelationships(ctx, tuples); err != nil {
					b.Fatal(err)
				}
				if err := store.Roles().Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "auditor", ResourceType: "project", ResourceID: "p0"}); err != nil {
					b.Fatal(err)
				}
				if mode == "warm" || mode == "miss" {
					if err := c.ReadCache.Poll(ctx); err != nil {
						b.Fatal(err)
					}
				}
				run := func() error {
					req := batch[0]
					switch path {
					case "check-deny":
						req.Principal.ID = "absent"
					case "through":
						req.Resource.ID = "nested"
					case "role":
						req.Permission = "audit"
					case "explain":
						_, _, err := c.Decisions.CheckExplain(ctx, req)
						return err
					case "filter":
						_, err := c.Decisions.FilterAuthorized(ctx, req.Principal, "view", "project", ids[:100])
						return err
					case "batch-1", "batch-10", "batch-100", "batch-500":
						n := map[string]int{"batch-1": 1, "batch-10": 10, "batch-100": 100, "batch-500": 500}[path]
						_, err := c.Decisions.CheckBatch(ctx, batch[:n])
						return err
					}
					_, err := c.Decisions.Check(ctx, req)
					return err
				}
				if err := run(); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if mode == "miss" {
						if err := cache.DeletePrefix(ctx, ""); err != nil {
							b.Fatal(err)
						}
					}
					if err := run(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
