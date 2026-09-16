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
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

func TestDirectServicesShareTraversalBudget(t *testing.T) {
	ctx := context.Background()
	store := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	rel, err := relationships.NewService(store.Tuples())
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
	decision, err := decisions.NewService(store.Tuples(), decisions.WithModel(hierarchySchema()), decisions.WithLimits(authmodel.EvaluationLimits{MaxGraphStates: 1}))
	if err != nil {
		t.Fatal(err)
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
	mutation, err := mutations.NewService(store.Mutations(), decision, mutations.WithGuard(guard))
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

		if _, err := mutations.NewService(store.Mutations(), decision, mutations.WithLimits(bad), mutations.WithGuard(guard)); !errors.Is(err, authmodel.ErrInvalidLimits) {
			t.Errorf("mutation mismatch %d: %v", limit, err)
		}
	}
}

// One explicit expression controls a permission; its role and graph leaves may coexist.
func TestDirectModelComposesExactRoleAndGraph(t *testing.T) {
	store := memory.New()
	m := hierarchySchema()
	resource := m.ResourceTypes["space"]
	resource.Permissions["view"] = decisions.Any(decisions.RoleIn("viewer"), decisions.Direct("viewer"))
	m.ResourceTypes["space"] = resource
	engine, err := decisions.NewService(store.Tuples(), decisions.WithModel(m))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mutations.NewService(store.Mutations(), engine, mutations.WithGuard(&stubGuard{})); err != nil {
		t.Fatal(err)
	}
}

func hierarchySchema() decisions.Model {
	return decisions.NewSchema([]decisions.ResourceSchema{{
		Name: "space",
		Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "space"}}},
				"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]decisions.Expression{
				"view": decisions.AnyOf(decisions.Direct("viewer"), decisions.Through("parent", "view")),
			},
		},
	}})
}

func TestDecisionConstructionOptionsValidateFinalBudget(t *testing.T) {
	store := memory.New().Tuples()
	limits := authmodel.EvaluationLimits{MaxGraphStates: 1}
	opts := decisions.WithLimits(limits)
	limits.MaxGraphStates = -1
	rel, err := decisions.NewService(store, decisions.WithModel(hierarchySchema()), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Limits().MaxGraphStates != 1 {
		t.Fatal("mutating the caller's budget changed the option")
	}
	reset, err := decisions.NewService(store, decisions.WithModel(hierarchySchema()), opts, decisions.WithLimits(authmodel.EvaluationLimits{}))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Limits().MaxGraphStates != authmodel.DefaultMaxGraphStates {
		t.Fatal("zero budget did not restore defaults")
	}
	if _, err := decisions.NewService(store, decisions.WithModel(hierarchySchema()), decisions.WithLimits(limits)); !errors.Is(err, authmodel.ErrInvalidLimits) {
		t.Fatalf("negative budget: %v", err)
	}
	if _, err := decisions.NewService(store, decisions.WithModel(hierarchySchema()), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", err)
	}
}
