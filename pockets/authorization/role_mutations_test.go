package authorization

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func prinU(id string) authmodel.PrincipalRef { return authmodel.PrincipalRef{Type: "user", ID: id} }

func TestAssignRoleApplies(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()

	rcpt, err := svc.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{
		Subject: prinU("u2"), Role: "editor", Scope: tuples.On("doc", "d1"),
	})
	if err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if rcpt == nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("want applied non-replay receipt, got %+v", rcpt)
	}
	if ok, err := svc.Roles.HasRoleIn(ctx, prinU("u2"), "editor", authmodel.Resource{Type: "doc", ID: "d1"}); err != nil || !ok {
		t.Fatalf("assign not visible to HasRole: ok=%v err=%v", ok, err)
	}
}

func TestUnassignRoleRemovesOnlyItsExactScope(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()
	for _, scope := range []tuples.Scope{tuples.Global(), tuples.On("doc", "d1")} {
		if _, err := svc.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{Subject: prinU("u2"), Role: "editor", Scope: scope}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := svc.Mutations.UnassignRole(ctx, mutations.UnassignRoleCommand{Subject: prinU("u2"), Role: "editor", Scope: tuples.On("doc", "d1")})
	if err != nil || result.Outcome != mutations.OutcomeApplied {
		t.Fatalf("unassign: %+v, %v", result, err)
	}
	if held, err := svc.Roles.HasRoleIn(ctx, prinU("u2"), "editor", authmodel.Resource{Type: "doc", ID: "d1"}); err != nil || held {
		t.Fatalf("removed scoped fact: %v, %v", held, err)
	}
	if held, err := svc.Roles.HasRole(ctx, prinU("u2"), "editor"); err != nil || !held {
		t.Fatalf("surviving global fact: %v, %v", held, err)
	}
}

func TestRepeatedUnassignReportsExactFactState(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()
	assign := mutations.AssignRoleCommand{Subject: prinU("u2"), Role: "editor", Scope: tuples.On("doc", "d1")}
	if _, err := svc.Mutations.AssignRole(ctx, assign); err != nil {
		t.Fatal(err)
	}
	cmd := mutations.UnassignRoleCommand(assign)
	first, err := svc.Mutations.UnassignRole(ctx, cmd)
	if err != nil || first.Outcome != mutations.OutcomeApplied {
		t.Fatalf("first: %+v, %v", first, err)
	}
	assign.Scope = tuples.Global()
	if _, err := svc.Mutations.AssignRole(ctx, assign); err != nil {
		t.Fatal(err)
	}
	next, err := svc.Mutations.UnassignRole(ctx, cmd)
	if err != nil || next.Outcome != mutations.OutcomeNotFound {
		t.Fatalf("current global fallback: %+v, %v", next, err)
	}
}

func TestAssignRoleIdempotentDuplicate(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()
	if _, err := svc.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{
		Subject: prinU("u2"), Role: "editor", Scope: tuples.On("doc", "d1"),
	}); err != nil {
		t.Fatalf("first assign: %v", err)
	}
	dup, err := svc.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{
		Subject: prinU("u2"), Role: "editor", Scope: tuples.On("doc", "d1"),
	})
	if err != nil || dup.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("duplicate assign must be no_change, got outcome=%v err=%v", dup.Outcome, err)
	}
}

// TestRoleWriteHalfScopedRejected proves a half-scoped resource pair is rejected
// before any write.
func TestRoleWriteHalfScopedRejected(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()
	if _, err := svc.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{
		Subject: prinU("u2"), Role: "editor", Scope: tuples.On("doc", ""), // no ResourceID
	}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("half-scoped role: want ErrHalfScopedRoleScope, got %v", err)
	}
	if _, err := svc.Mutations.UnassignRole(ctx, mutations.UnassignRoleCommand{
		Subject: prinU("u2"), Role: "editor", Scope: tuples.On("", "d1"), // no ResourceType
	}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("half-scoped role unassign: want ErrHalfScopedRoleScope, got %v", err)
	}
}

func TestRoleWriteReadOnlyPosture(t *testing.T) {
	st := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{}))
	comps, err := New(Repositories{
		Tuples: st.Tuples(),
	}, WithModel(lifecycleModel())) // no Guard → read-only posture
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := comps.Mutations.AssignRole(context.Background(), mutations.AssignRoleCommand{
		Subject: prinU("u2"), Role: "editor", Scope: tuples.On("doc", "d1"),
	}); !errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatalf("read-only assign: want ErrMutationsNotConfigured, got %v", err)
	}
}

func TestRoleWritesShareDeclaredRelationshipConstraints(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()
	_, err := svc.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{Subject: authmodel.PrincipalRef{Type: "service", ID: "s1"}, Role: "editor", Scope: tuples.On("doc", "d1")})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("role facade bypassed editor subject constraint: %v", err)
	}
}

// TestRoleCommandRejectsUsersetSubjectsStructurally proves a userset role subject is
// impossible by TYPE: neither the guarded role commands nor the underlying RoleRow
// nor the concrete PrincipalRef carries a Relation field, so there is no runtime
// userset-rejection path — the type prevents it.
func TestRoleCommandRejectsUsersetSubjectsStructurally(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(mutations.AssignRoleCommand{Scope: tuples.Global()}),
		reflect.TypeOf(mutations.UnassignRoleCommand{Scope: tuples.Global()}),
		reflect.TypeOf(mutations.RoleRow{}),
		reflect.TypeOf(authmodel.PrincipalRef{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			if strings.EqualFold(typ.Field(i).Name, "Relation") {
				t.Fatalf("%s carries a Relation field — a userset role subject must be structurally impossible", typ)
			}
		}
	}
	if reflect.TypeOf(mutations.AssignRoleCommand{Scope: tuples.Global()}.Subject) != reflect.TypeOf(authmodel.PrincipalRef{}) {
		t.Fatalf("AssignRoleCommand.Subject must be a concrete PrincipalRef")
	}
}

func docRoleModel() decisions.Model {
	return decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"doc": {Permissions: map[string]decisions.Expression{"audit": decisions.Any(decisions.RoleIn("auditor"), decisions.Role("auditor"))}}}}
}

func newRoleModelComponents(t *testing.T, st *memory.Store, model decisions.Model) Components {
	t.Helper()
	comps, err := New(Repositories{
		Tuples: st.Tuples(), Mutations: st.Mutations(),
	}, WithModel(model))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func newRoleModelStore() *memory.Store {
	return memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{}))
}

func TestNamedPermissionsDoNotImposeARoleCatalog(t *testing.T) {
	comps := newRoleModelComponents(t, newRoleModelStore(), docRoleModel())
	ctx := context.Background()
	for _, scope := range []tuples.Scope{tuples.Global(), tuples.On("doc", "d1"), tuples.On("project", "p1")} {
		if _, err := comps.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{Subject: prinU("u2"), Role: "opaque", Scope: scope}); err != nil {
			t.Fatalf("opaque fact at %+v: %v", scope, err)
		}
	}
	got, err := comps.Decisions.Check(ctx, authmodel.CheckRequest{Principal: prinU("u2"), Permission: "audit", Resource: authmodel.Resource{Type: "doc", ID: "d1"}})
	if err != nil || got.Allowed {
		t.Fatalf("opaque labels do not imply permission: %+v, %v", got, err)
	}
}

// TestAssignRoleModelValidationParity proves the check cannot be bypassed by
// choosing a different write path: the guarded typed method, the trusted typed
// method, and the generic trusted Apply all refuse the same undeclared pair,
// because the rule lives in the validator the repository runs.
func TestAssignRoleModelValidationParity(t *testing.T) {
	model := docRoleModel()
	def := model.ResourceTypes["doc"]
	def.Relations = map[string]decisions.RelationDef{"audtor": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "service"}}}}
	model.ResourceTypes["doc"] = def
	comps := newRoleModelComponents(t, newRoleModelStore(), model)
	ctx := context.Background()

	paths := map[string]func() error{
		"Service.AssignRole (guarded)": func() error {
			_, err := comps.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{
				Subject: prinU("u2"), Role: "audtor", Scope: tuples.On("doc", "d1"),
			})
			return err
		},
		"TupleWriter.AssignRole (trusted)": func() error {
			_, err := comps.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{
				Subject: prinU("u2"), Role: "audtor", Scope: tuples.On("doc", "d1"),
			})
			return err
		},
		"TupleWriter.Apply (generic)": func() error {
			_, err := comps.Mutations.Apply(ctx, mutations.Command{

				Target:    mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d1"},
				Operation: mutations.OpRoleAssign,
				Roles:     []mutations.RoleRow{{SubjectType: "user", SubjectID: "u2", Role: "audtor"}},
			})
			return err
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("want invalid input, got %v", err)
			}
		})
	}
	if ok, err := comps.Roles.HasRoleIn(ctx, prinU("u2"), "audtor", authmodel.Resource{Type: "doc", ID: "d1"}); err != nil || ok {
		t.Fatalf("no write path may have applied the undeclared role: ok=%v err=%v", ok, err)
	}
}

// TestAssignRoleWithoutARoleModelStaysOpaque proves hosts with no model are
// untouched: role names remain opaque strings the core does not judge.
func TestAssignRoleWithoutARoleModelStaysOpaque(t *testing.T) {
	comps := newRoleModelComponents(t, newRoleModelStore(), decisions.Model{})
	ctx := context.Background()
	if _, err := comps.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{
		Subject: prinU("u2"), Role: "anything-goes", Scope: tuples.On("doc", "d1"),
	}); err != nil {
		t.Fatalf("with no model every role stays assignable: %v", err)
	}
}

// TestUnassignRoleStaysOpaqueUnderARoleModel proves the asymmetry: a stored row
// the current model cannot express is still REMOVABLE and still readable — only
// assignment is judged, so no historical row needs a migration.
func TestUnassignRoleStaysOpaqueUnderARoleModel(t *testing.T) {
	st := newRoleModelStore()
	ctx := context.Background()
	// A row from before the model (or from a model that has since dropped it).
	if err := st.Tuples().ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{{Scope: tuples.On("doc", "d1"), Relation: "legacy", Subject: tuples.SubjectRef{Type: "user", ID: "u2"}}}}); err != nil {
		t.Fatal(err)
	}

	comps := newRoleModelComponents(t, st, docRoleModel())

	if ok, err := comps.Roles.HasRoleIn(ctx, prinU("u2"), "legacy", authmodel.Resource{Type: "doc", ID: "d1"}); err != nil || !ok {
		t.Fatalf("reads stay opaque: ok=%v err=%v", ok, err)
	}
	if _, err := comps.Mutations.UnassignRole(ctx, mutations.UnassignRoleCommand{
		Subject: prinU("u2"), Role: "legacy", Scope: tuples.On("doc", "d1"),
	}); err != nil {
		t.Fatalf("an undeclared role must stay removable: %v", err)
	}
	if ok, err := comps.Roles.HasRoleIn(ctx, prinU("u2"), "legacy", authmodel.Resource{Type: "doc", ID: "d1"}); err != nil || ok {
		t.Fatalf("legacy row was not removed: ok=%v err=%v", ok, err)
	}
}

func TestRepeatedRoleAssignmentUsesCurrentModel(t *testing.T) {
	st := newRoleModelStore()
	ctx := context.Background()

	cmd := mutations.AssignRoleCommand{
		Subject: prinU("u2"), Role: "auditor", Scope: tuples.On("doc", "d1"),
	}

	before := newRoleModelComponents(t, st, docRoleModel())
	first, err := before.Mutations.AssignRole(ctx, cmd)
	if err != nil || first.Outcome != mutations.OutcomeApplied {
		t.Fatalf("first application: got %+v err=%v", first, err)
	}

	// The host narrows the model: "auditor" is gone.
	narrowed := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"doc": {Permissions: map[string]decisions.Expression{"audit": decisions.Any(decisions.RoleIn("reviewer"), decisions.Role("reviewer"))}},
	}}
	after := newRoleModelComponents(t, st, narrowed)

	result, err := after.Mutations.AssignRole(ctx, cmd)
	if err != nil || result.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("trusted repeated assign: %+v, %v", result, err)
	}
	result, err = after.Mutations.AssignRole(ctx, cmd)
	if err != nil || result.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("guarded repeated assign: %+v, %v", result, err)
	}
	// Permission grantors change independently of assignable facts.
	fresh := cmd

	fresh.Subject = prinU("u3")
	if _, err := after.Mutations.AssignRole(ctx, fresh); err != nil {
		t.Fatalf("first application under the narrowed model: want invalid input, got %v", err)
	}
}
