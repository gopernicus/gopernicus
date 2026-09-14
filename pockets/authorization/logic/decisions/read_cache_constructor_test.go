package decisions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

type inertCacheSource struct{ decisions.CacheSource }

func (s inertCacheSource) Observe(context.Context) (decisions.CacheVersion, error) {
	panic("constructor must not observe")
}
func (s inertCacheSource) ReadSnapshot(context.Context, func(context.Context, decisions.CacheVersion, decisions.CheckReads) error) error {
	panic("constructor must not snapshot")
}

type nilCache struct{ cacher.Storer }

func TestCacheConstructionAndRootForwarding(t *testing.T) {
	store := memory.New(memory.WithCacheReads())
	policy := decisions.CachePolicy{Namespace: "fixture", MaxStaleness: time.Second}
	repos := authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), CacheSource: inertCacheSource{store.CacheSource()}}
	components, err := authorization.New(repos, authorization.WithRelationshipModel(compositeSchema()), authorization.WithRoleModel(compositeRoleModel()), authorization.WithCacher(cacher.Noop{}, policy))
	if err != nil {
		t.Fatal(err)
	}
	if components.ReadCache == nil || components.Decisions.ReadCache() != components.ReadCache {
		t.Fatal("root did not forward same runtime")
	}
	if components.Relationships.CacheBinding() != store.CacheSource().CacheBinding() || components.Roles.CacheBinding() != store.CacheSource().CacheBinding() {
		t.Fatal("services did not forward binding")
	}
	if components.ReadCache.Stats().Ready {
		t.Fatal("runtime starts ready")
	}
	if components.ReadCache.PollInterval() <= 0 {
		t.Fatal("missing resolved interval")
	}
	if err := components.ReadCache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := components.ReadCache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := components.ReadCache.Poll(context.Background()); !errors.Is(err, decisions.ErrCacheClosed) {
		t.Fatal(err)
	}
}

func TestCacheConstructionRefusesMismatchedReaders(t *testing.T) {
	a, b := memory.New(memory.WithCacheReads()), memory.New(memory.WithCacheReads())
	eng, err := relationships.NewService(a.Relationships(), compositeSchema())
	if err != nil {
		t.Fatal(err)
	}
	policy := decisions.CachePolicy{Namespace: "fixture", MaxStaleness: time.Second}
	cases := []struct {
		name      string
		readers   decisions.Readers
		cache     cacher.Storer
		policy    decisions.CachePolicy
		roleModel bool
	}{
		{"source missing", decisions.Readers{Relationships: eng.Service}, cacher.Noop{}, policy, false},
		{"source mismatch", decisions.Readers{Relationships: eng.Service, CacheSource: b.CacheSource()}, cacher.Noop{}, policy, false},
		{"typed nil cache", decisions.Readers{Relationships: eng.Service, CacheSource: a.CacheSource()}, (*nilCache)(nil), policy, false},
		{"policy missing", decisions.Readers{Relationships: eng.Service, CacheSource: a.CacheSource()}, cacher.Noop{}, decisions.CachePolicy{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decisions.NewService(tc.readers, decisions.WithCacher(tc.cache, tc.policy)); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("construction: %v", err)
			}
		})
	}
}

func TestNilCacherDoesNotRequireSourceOrPolicy(t *testing.T) {
	store := memory.New()
	eng, err := relationships.NewService(store.Relationships(), compositeSchema())
	if err != nil {
		t.Fatal(err)
	}
	service, err := decisions.NewService(decisions.Readers{Relationships: eng.Service}, decisions.WithCacher(nil, decisions.CachePolicy{MaxStaleness: -1}))
	if err != nil || service.ReadCache() != nil {
		t.Fatalf("nil cache: %v/%v", service, err)
	}
	if _, err := authorization.New(authorization.Repositories{Roles: store.Roles()}, authorization.WithCacher((*nilCache)(nil), decisions.CachePolicy{})); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("opaque roles typed nil cacher: %v", err)
	}
	if _, err := authorization.New(authorization.Repositories{Roles: store.Roles()}, authorization.WithCacher(cacher.Noop{}, decisions.CachePolicy{Namespace: "fixture", MaxStaleness: time.Second})); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("opaque roles cacher: %v", err)
	}
}

func TestCacheConstructionRelationshipModelWithOpaqueRoles(t *testing.T) {
	store := memory.New(memory.WithCacheReads())
	components, err := authorization.New(
		authorization.Repositories{Relationships: store.Relationships(), Roles: store.Roles(), CacheSource: store.CacheSource()},
		authorization.WithRelationshipModel(compositeSchema()),
		authorization.WithCacher(cacher.Noop{}, decisions.CachePolicy{Namespace: "fixture", MaxStaleness: time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if components.ReadCache == nil || components.Roles == nil {
		t.Fatal("relationship cache and ordinary role service must coexist")
	}
	if components.Decisions.CompiledRoleModel() != nil {
		t.Fatal("construction added a role model")
	}
	got, err := components.Decisions.Check(context.Background(), request("u1", "audit", "project", "p1"))
	if err != nil || got.Allowed || got.Reason != "no rules defined" {
		t.Fatalf("opaque roles changed dispatch: %+v/%v", got, err)
	}
}
