package authorization

import (
	"errors"
	"sync"
	"testing"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestModelOptionsSnapshotBeforeConstructionAndConcurrentReuse(t *testing.T) {
	relationshipModel := validModel()
	roleModel := projectRoleModel()
	opts := []Option{WithRelationshipModel(relationshipModel), WithRoleModel(roleModel)}
	roleOption := decisions.WithRoleModel(roleModel)
	original := roleModel.ResourceTypes["project"]
	rel := relationshipModel.ResourceTypes["post"]
	rel.Relations["owner"].AllowedSubjects[0].Type = "unexpected"
	rel.Permissions["delete"].AnyOf[0].Relation = "missing"
	relationshipModel.ResourceTypes["post"] = relationships.ResourceTypeDef{}
	original.Roles[0] = "changed"
	for _, grantors := range original.Permissions {
		grantors[0] = "changed"
	}
	delete(roleModel.ResourceTypes, "project")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			store := memory.New()
			comps, err := New(Repositories{Relationships: store.Relationships(), Roles: store.Roles()}, opts...)
			if err != nil {
				t.Error(err)
				return
			}
			if err := comps.Relationships.ValidateRelation("post", "owner", "user", ""); err != nil {
				t.Error(err)
			}
			if !comps.Decisions.DeclaresPermission("post", "delete") || !comps.Decisions.CompiledRoleModel().DeclaresRole("project", "auditor") {
				t.Error("option snapshot lost original model")
			}
			roleService, err := roles.NewService(store.Roles())
			if err != nil {
				t.Error(err)
				return
			}
			direct, err := decisions.NewService(decisions.Readers{Roles: roleService}, roleOption)
			if err != nil || !direct.CompiledRoleModel().DeclaresRole("project", "auditor") {
				t.Errorf("direct option snapshot: %v", err)
			}
		})
	}
	wg.Wait()
}

func TestOptionsReplaceModelsRoutesAndGuardAsWholeValues(t *testing.T) {
	store := memory.New()
	comps, err := New(Repositories{Roles: store.Roles(), Mutations: store.Mutations()},
		WithRoleModel(projectRoleModel()), WithRoleModel(authmodel.RoleModel{}),
		WithGuard(&stubGuard{}), WithGuard(nil),
		WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate, AssignmentPolicy: refuseAssignment}),
		WithRoleRoutes(authorizationhttp.RoleRoutes{}))
	if err != nil {
		t.Fatal(err)
	}
	if comps.Decisions != nil || comps.Mutations.Guarded() || comps.HTTP.RoutesEnabled() {
		t.Fatal("replacement merged an earlier policy")
	}
	_, err = New(Repositories{Roles: store.Roles()}, WithRoleRoutes(authorizationhttp.RoleRoutes{ListStrategy: "unknown"}))
	if !errors.Is(err, authorizationhttp.ErrInvalidListStrategy) {
		t.Fatalf("orphan invalid listing policy: %v", err)
	}
	_, err = New(Repositories{Roles: store.Roles()}, WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: passRoleRouteGate}), WithRoleRoutes(authorizationhttp.RoleRoutes{AssignmentPolicy: refuseAssignment}))
	if !errors.Is(err, authorizationhttp.ErrRoleRouteAssignmentPolicyWithoutRoutes) {
		t.Fatalf("route replacement retained old gate: %v", err)
	}
}

func TestConstructionFamiliesRejectNilOptions(t *testing.T) {
	store := memory.New()
	_, rootErr := New(Repositories{Roles: store.Roles()}, nil)
	_, decisionErr := decisions.NewService(decisions.Readers{}, nil)
	_, mutationErr := mutations.NewService(nil, mutations.Services{}, nil)
	_, httpErr := authorizationhttp.New(authorizationhttp.Services{}, nil)
	for name, err := range map[string]error{"root": rootErr, "decisions": decisionErr, "mutations": mutationErr, "HTTP": httpErr} {
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s nil option: %v", name, err)
		}
	}
}
