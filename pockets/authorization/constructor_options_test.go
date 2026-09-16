package authorization

import (
	"errors"
	"sync"
	"testing"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestModelOptionsSnapshotBeforeConstructionAndConcurrentReuse(t *testing.T) {
	relationshipModel := validModel()
	roleModel := projectRoleModel()
	for name, resource := range roleModel.ResourceTypes {
		relationshipModel.ResourceTypes[name] = resource
	}
	opts := []Option{WithModel(relationshipModel)}
	roleOption := decisions.WithModel(roleModel)
	original := roleModel.ResourceTypes["project"]
	rel := relationshipModel.ResourceTypes["post"]
	rel.Relations["owner"].AllowedSubjects[0].Type = "unexpected"
	rel.Permissions["delete"].AnyOf[0].Relation = "missing"
	relationshipModel.ResourceTypes["post"] = decisions.ResourceTypeDef{}
	for name := range original.Permissions {
		original.Permissions[name] = decisions.Role("changed")
	}
	delete(roleModel.ResourceTypes, "project")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			store := memory.New()
			comps, err := New(Repositories{Tuples: store.Tuples()}, opts...)
			if err != nil {
				t.Error(err)
				return
			}
			if err := comps.Relationships.ValidateRelation("post", "owner", "user", ""); err != nil {
				t.Error(err)
			}
			if !comps.Decisions.DeclaresPermission("post", "delete") || !comps.Decisions.DeclaresPermission("project", "audit") {
				t.Error("option snapshot lost original model")
			}
			direct, err := decisions.NewService(store.Tuples(), roleOption)
			if err != nil || !direct.DeclaresPermission("project", "audit") {
				t.Errorf("direct option snapshot: %v", err)
			}

		})
	}
	wg.Wait()
}

func TestOptionsReplaceModelsRoutesAndGuardAsWholeValues(t *testing.T) {
	store := memory.New()
	comps, err := New(Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()},
		WithModel(projectRoleModel()), WithModel(decisions.Model{}),
		WithGuard(&stubGuard{}), WithGuard(nil),
		WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate, AssignmentPolicy: refuseAssignment}),
		WithRoleRoutes(authorizationhttp.RoleRoutes{}))
	if err != nil {
		t.Fatal(err)
	}
	if comps.Decisions.DeclaresPermission("project", "audit") || comps.Mutations.Guarded() || comps.HTTP.RoutesEnabled() {
		t.Fatal("replacement merged an earlier policy")
	}
	_, err = New(Repositories{Tuples: store.Tuples()}, WithRoleRoutes(authorizationhttp.RoleRoutes{ListStrategy: "unknown"}))
	if !errors.Is(err, authorizationhttp.ErrInvalidListStrategy) {
		t.Fatalf("orphan invalid listing policy: %v", err)
	}
	_, err = New(Repositories{Tuples: store.Tuples()}, WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate}), WithRoleRoutes(authorizationhttp.RoleRoutes{AssignmentPolicy: refuseAssignment}))
	if !errors.Is(err, authorizationhttp.ErrRoleRouteAssignmentPolicyWithoutRoutes) {
		t.Fatalf("route replacement retained old gate: %v", err)
	}
}

func TestConstructionFamiliesRejectNilOptions(t *testing.T) {
	store := memory.New()
	_, rootErr := New(Repositories{Tuples: store.Tuples()}, nil)
	_, decisionErr := decisions.NewService(store.Tuples(), nil)
	_, mutationErr := mutations.NewService(nil, nil, nil)
	_, httpErr := authorizationhttp.New(authorizationhttp.Services{}, nil)
	for name, err := range map[string]error{"root": rootErr, "decisions": decisionErr, "mutations": mutationErr, "HTTP": httpErr} {
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s nil option: %v", name, err)
		}
	}
}
