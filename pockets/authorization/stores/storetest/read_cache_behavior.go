package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// RunReadCache exercises public decisions over a real authority. Each factory
// must return an empty authority/cache pair; it retains resource ownership.
func RunReadCache(t *testing.T, factory func(*testing.T) authorization.Repositories, cacheFactory func(*testing.T) cacher.Storer) {
	t.Helper()
	for _, scenario := range []string{"warm and failure", "mixed generation"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			repos := factory(t)
			source := &cacheBehaviorSource{CacheSource: repos.CacheSource}
			repos.CacheSource = source
			cache := &cacheBehaviorBytes{Storer: cacheFactory(t)}
			components, err := authorization.New(repos,
				authorization.WithRelationshipModel(relationships.NewSchema([]relationships.ResourceSchema{{Name: "document", Def: relationships.ResourceTypeDef{
					Relations:   map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
					Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))},
				}}})),
				authorization.WithRoleModel(authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{"document": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}}}}),
				authorization.WithCacher(cache, decisions.CachePolicy{Namespace: t.Name(), MaxStaleness: time.Minute}),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := components.ReadCache.Close(); err != nil {
					t.Error(err)
				}
			})
			relation := relationships.CreateRelationship{ResourceType: "document", ResourceID: "a", Relation: "viewer", SubjectType: "user", SubjectID: "u"}
			if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{relation}); err != nil {
				t.Fatal(err)
			}
			req := authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "u"}, Permission: "view", Resource: authmodel.Resource{Type: "document", ID: "a"}}
			roleReq := req
			roleReq.Permission, roleReq.Resource.ID = "audit", "b"
			checkBatch := func(requests []authmodel.CheckRequest, want ...bool) {
				t.Helper()
				got, err := components.Decisions.CheckBatch(ctx, requests)
				if err != nil || len(got) != len(want) {
					t.Fatalf("batch=%+v/%v", got, err)
				}
				for i := range got {
					if got[i].Allowed != want[i] {
						t.Fatalf("batch=%+v want=%v", got, want)
					}
				}
			}
			checkBatch([]authmodel.CheckRequest{req}, true)
			if cache.gets != 0 || source.snapshots != 0 || components.ReadCache.Stats().BypassCold == 0 {
				t.Fatal("cold runtime did cache work or failed to report bypass")
			}
			if err := components.ReadCache.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			checkBatch([]authmodel.CheckRequest{req}, true)
			if source.snapshots != 1 || cache.sets == 0 {
				t.Fatal("first miss did not publish one snapshot")
			}
			checkBatch([]authmodel.CheckRequest{req}, true)
			if source.snapshots != 1 || components.ReadCache.Stats().HitComplete == 0 {
				t.Fatal("warm batch missed")
			}
			if scenario == "mixed generation" {
				// A cached relationship grant is followed by a role miss. Between
				// those reads revoke the relationship, then grant the role: no
				// committed state ever contains both grants.
				cache.beforeMiss = func() {
					if err := repos.Relationships.DeleteResourceRelationships(ctx, "document", "a"); err != nil {
						t.Fatal(err)
					}
					if err := repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "auditor", ResourceType: "document", ResourceID: "b"}); err != nil {
						t.Fatal(err)
					}
				}
				checkBatch([]authmodel.CheckRequest{req, roleReq}, false, true)
				if cache.beforeMiss != nil || source.snapshots != 2 {
					t.Fatal("mixed cache operation did not restart once in a snapshot")
				}
				return
			}
			cache.failure = errors.New("cache unavailable")
			checkBatch([]authmodel.CheckRequest{req, roleReq, req}, true, false, true)
			if source.snapshots != 2 || components.ReadCache.Stats().CacheErrors == 0 {
				t.Fatal("cache outage did not fall back once")
			}
			cache.failure = nil
			if err := repos.Relationships.DeleteResourceRelationships(ctx, "document", "a"); err != nil {
				t.Fatal(err)
			}
			if err := components.ReadCache.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			checkBatch([]authmodel.CheckRequest{req}, false)
			checkBatch([]authmodel.CheckRequest{req}, false)
			before := cache.gets
			filtered, err := components.Decisions.FilterAuthorized(ctx, req.Principal, "view", "document", []string{"a"})
			if err != nil || len(filtered) != 0 || cache.gets != before {
				t.Fatalf("filter consulted cache: %v/%v", filtered, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := components.Decisions.Check(canceled, req); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled check=%v", err)
			}
		})
	}
}

type cacheBehaviorSource struct {
	decisions.CacheSource
	snapshots int
}

func (s *cacheBehaviorSource) ReadSnapshot(ctx context.Context, fn func(context.Context, decisions.CacheVersion, decisions.CheckReads) error) error {
	s.snapshots++
	return s.CacheSource.ReadSnapshot(ctx, fn)
}

// These counters/hooks are used only by sequential operations in this harness.
type cacheBehaviorBytes struct {
	cacher.Storer
	gets, sets int
	failure    error
	beforeMiss func()
}

func (s *cacheBehaviorBytes) Get(ctx context.Context, key string) ([]byte, bool, error) {
	s.gets++
	if s.failure != nil {
		return nil, false, s.failure
	}
	value, hit, err := s.Storer.Get(ctx, key)
	if !hit && err == nil && s.beforeMiss != nil {
		hook := s.beforeMiss
		s.beforeMiss = nil
		hook()
	}
	return value, hit, err
}

func (s *cacheBehaviorBytes) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	s.sets++
	if s.failure != nil {
		return s.failure
	}
	return s.Storer.Set(ctx, key, value, ttl)
}
