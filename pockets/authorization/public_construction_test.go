package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

func TestDirectServicesShareTraversalBudget(t *testing.T) {
	ctx := context.Background()
	store := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	rel, err := relationships.NewService(store.Relationships(), hierarchySchema(), relationships.WithLimits(authmodel.EvaluationLimits{MaxGraphStates: 1}))
	if err != nil {
		t.Fatal(err)
	}
	err = rel.RelationshipWriter.CreateRelationships(ctx, []relationships.CreateRelationship{
		{ResourceType: "space", ResourceID: "leaf", Relation: "parent", SubjectType: "space", SubjectID: "root"},
		{ResourceType: "space", ResourceID: "root", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	query := authmodel.CheckRequest{Principal: actorU1().PrincipalRef, Permission: "view", Resource: authmodel.Resource{Type: "space", ID: "leaf"}}
	decision, err := decisions.NewService(decisions.Readers{Relationships: rel.Service})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Limits() != rel.Service.Limits() {
		t.Fatal("decision budget did not inherit the supplied engine budget")
	}
	if got, err := decision.Check(ctx, query); got.Allowed || !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("traversal exceeded inherited budget: decision=%+v err=%v", got, err)
	}
	var guardedRead bool
	guard := contractGuard(func(ctx context.Context, _ mutations.MutationAttempt, view mutations.DecisionView) error {
		guardedRead = true
		got, err := view.Check(ctx, query)
		if got.Allowed {
			t.Error("guard traversal exceeded inherited budget")
		}
		return err
	})
	mutation, err := mutations.NewService(store.Mutations(), mutations.Services{Relationships: rel.Service}, mutations.WithGuard(guard))
	if err != nil {
		t.Fatal(err)
	}
	_, err = mutation.Service.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{ResourceType: "space", ResourceID: "other", Relation: "viewer", Subject: subjU("u1")})
	if !guardedRead || !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("guard traversal escaped the shared budget: read=%t err=%v", guardedRead, err)
	}
	targets, err := rel.Service.GetRelationTargets(ctx, "space", "other", "viewer")
	if err != nil || len(targets) != 0 {
		t.Fatalf("failed guard wrote a tuple: targets=%v err=%v", targets, err)
	}
	for _, limit := range []int{2, authmodel.DefaultMaxGraphStates} {
		bad := authmodel.EvaluationLimits{MaxGraphStates: limit}
		if _, err := decisions.NewService(decisions.Readers{Relationships: rel.Service}, decisions.WithLimits(bad)); !errors.Is(err, authmodel.ErrInvalidLimits) {
			t.Errorf("decision mismatch %d: %v", limit, err)
		}
		if _, err := mutations.NewService(store.Mutations(), mutations.Services{Relationships: rel.Service}, mutations.WithLimits(bad), mutations.WithGuard(guard)); !errors.Is(err, authmodel.ErrInvalidLimits) {
			t.Errorf("mutation mismatch %d: %v", limit, err)
		}
	}
}

func TestDirectMutationServiceRejectsConflictingCompiledModel(t *testing.T) {
	store := memory.New()
	rel, err := relationships.NewService(store.Relationships(), hierarchySchema())
	if err != nil {
		t.Fatal(err)
	}
	roleService, err := roles.NewService(store.Roles())
	if err != nil {
		t.Fatal(err)
	}
	overlapping := authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{"space": {Roles: []string{"viewer"}, Permissions: map[string][]string{"view": {"viewer"}}}}}
	independentlyCompiled, err := authmodel.CompileRoleModel(overlapping, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mutations.NewService(store.Mutations(), mutations.Services{Relationships: rel.Service, Roles: roleService}, mutations.WithRoleModel(independentlyCompiled), mutations.WithGuard(&stubGuard{})); !errors.Is(err, authmodel.ErrModelConflict) {
		t.Fatalf("guard accepted ambiguous permission owner: %v", err)
	}
	if _, err := decisions.NewService(decisions.Readers{Relationships: rel.Service, Roles: roleService}, decisions.WithRoleModel(overlapping)); !errors.Is(err, authmodel.ErrModelConflict) {
		t.Fatalf("ordinary decisions accepted ambiguous owner: %v", err)
	}
}

func hierarchySchema() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "space",
		Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}},
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Direct("viewer"), relationships.Through("parent", "view")),
			},
		},
	}})
}

func TestRelationshipConstructionOptionsValidateFinalBudget(t *testing.T) {
	store := memory.NewRelationships()
	limits := authmodel.EvaluationLimits{MaxGraphStates: 1}
	opts := relationships.WithLimits(limits)
	limits.MaxGraphStates = -1
	rel, err := relationships.NewService(store, hierarchySchema(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Service.Limits().MaxGraphStates != 1 {
		t.Fatal("mutating the caller's budget changed the option")
	}
	reset, err := relationships.NewService(store, hierarchySchema(), opts, relationships.WithLimits(authmodel.EvaluationLimits{}))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Service.Limits().MaxGraphStates != authmodel.DefaultMaxGraphStates {
		t.Fatal("zero budget did not restore defaults")
	}
	if _, err := relationships.NewService(store, hierarchySchema(), relationships.WithLimits(limits)); !errors.Is(err, authmodel.ErrInvalidLimits) {
		t.Fatalf("negative budget: %v", err)
	}
	if _, err := relationships.NewService(store, hierarchySchema(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", err)
	}
}
