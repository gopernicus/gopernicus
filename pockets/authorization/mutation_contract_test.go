package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

type contractGuard func(context.Context, mutations.MutationAttempt, mutations.DecisionView) error

func (f contractGuard) AuthorizeMutation(ctx context.Context, attempt mutations.MutationAttempt, view mutations.DecisionView) error {
	return f(ctx, attempt, view)
}

func TestGuardCannotRewriteProposedMutation(t *testing.T) {
	store := memory.New()
	guard := contractGuard(func(_ context.Context, attempt mutations.MutationAttempt, view mutations.DecisionView) error {
		for i := range attempt.Change.Relationships {
			attempt.Change.Relationships[i].Relation = "owner"
		}
		for i := range attempt.Change.Roles {
			attempt.Change.Roles[i].Role = "admin"
		}
		return nil
	})
	comps, err := New(Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}, WithModel(lifecycleModel()), WithGuard(guard))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rel := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u1")}
	_, err = comps.Mutations.GrantRelationship(ctx, actorU1(), rel)
	if err != nil {
		t.Fatal(err)
	}
	for _, replay := range []bool{false, true} {
		if replay {
			got, err := comps.Mutations.GrantRelationship(ctx, actorU1(), rel)
			if err != nil || got.Outcome != mutations.OutcomeNoChange {
				t.Fatalf("replay: %+v, %v", got, err)
			}
		}
		for relation, want := range map[string]bool{"viewer": true, "owner": false} {
			got, err := store.Relationships().CheckRelationExists(ctx, "doc", "d1", relation, "user", "u1")
			if err != nil || got != want {
				t.Fatalf("%s: %v, %v", relation, got, err)
			}
		}
	}
	if _, err := comps.Mutations.AssignRole(ctx, actorU1(), mutations.AssignRoleCommand{Subject: actorU1().PrincipalRef, Role: "viewer", Scope: tuples.Global()}); err != nil {
		t.Fatal(err)
	}
	if ok, err := comps.Roles.HasRole(ctx, actorU1().PrincipalRef, "admin"); err != nil || ok {
		t.Fatalf("guard rewrote role: %v, %v", ok, err)
	}
	if ok, err := comps.Roles.HasRole(ctx, actorU1().PrincipalRef, "viewer"); err != nil || !ok {
		t.Fatalf("requested role missing: %v, %v", ok, err)
	}
}

func TestGuardianPolicyValidatesShapeWithoutRequiringModelCatalog(t *testing.T) {
	for name, rule := range map[string]mutations.GuardianRule{
		"negative minimum": {ResourceType: "doc", Relation: "owner", MinAnchors: -1},
		"invalid relation": {ResourceType: "doc", Relation: "bad\n"},
	} {
		t.Run(name, func(t *testing.T) {
			st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{Rules: []mutations.GuardianRule{rule}}))
			_, err := New(Repositories{Tuples: st.Tuples(), Mutations: st.Mutations()}, WithModel(lifecycleModel()))
			if !errors.Is(err, mutations.ErrInvalidGuardianPolicy) {
				t.Fatalf("invalid guardian: %v", err)
			}
		})
	}
	st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{Rules: []mutations.GuardianRule{{ResourceType: "opaque", Relation: "custodian", MinAnchors: 1}}}))
	comps, err := New(Repositories{Tuples: st.Tuples(), Mutations: st.Mutations()}, WithModel(lifecycleModel()), WithGuard(&stubGuard{}))
	if err != nil {
		t.Fatal(err)
	}
	cmd := mutations.AssignRoleCommand{Subject: prinU("u1"), Role: "custodian", Scope: tuples.On("opaque", "one")}
	if _, err := comps.Mutations.AssignRole(context.Background(), actorU1(), cmd); err != nil {
		t.Fatal(err)
	}
	if _, err := comps.Mutations.UnassignRole(context.Background(), actorU1(), mutations.UnassignRoleCommand(cmd)); !errors.Is(err, mutations.ErrInvariantBlocked) {
		t.Fatalf("opaque guardian bypass: %v", err)
	}
}

func TestEmptyGuardianIsDefaultAndSupportsReaderOnlyModel(t *testing.T) {
	st := memory.New()
	model := decisions.NewSchema([]decisions.ResourceSchema{{Name: "doc", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"reader": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}}}})
	comps, err := New(Repositories{Mutations: st.Mutations(), Tuples: st.Tuples()}, WithModel(model))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := comps.SystemMutator.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "reader", Subject: subjU("u1")}); err != nil {
		t.Fatalf("default policy imposed an undeclared owner: %v", err)
	}
}

type boundedFactView struct {
	stubDecisionView
	bound int
}

func (v *boundedFactView) CheckRelationBounded(_ context.Context, _ mutations.Target, _, _, _ string, bound int) (bool, error) {
	v.bound = bound
	return false, relationships.ErrExpansionBudgetExceeded
}

func TestOpaqueRoleGuardUsesResolvedFactLimits(t *testing.T) {
	adapter := &boundedFactView{}
	repo := &stubMutationRepo{view: adapter, receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	guard := contractGuard(func(ctx context.Context, _ mutations.MutationAttempt, view mutations.DecisionView) error {
		_, err := view.CheckRelation(ctx, actorU1().PrincipalRef, "member", authmodel.Resource{Type: "group", ID: "g1"})
		return err
	})
	comps, err := New(Repositories{Tuples: memory.NewTuples(), Mutations: repo}, WithGuard(guard), WithLimits(authmodel.EvaluationLimits{MaxGraphStates: 3}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = comps.Mutations.AssignRole(context.Background(), actorU1(), mutations.AssignRoleCommand{Subject: actorU1().PrincipalRef, Role: "viewer", Scope: tuples.Global()})
	if !errors.Is(err, authmodel.ErrEvaluationLimit) || adapter.bound != 3 {
		t.Fatalf("raw fact ignored host budget: bound=%d err=%v", adapter.bound, err)
	}
}
