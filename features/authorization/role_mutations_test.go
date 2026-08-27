package authorization

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk"
)

// roleScopeGuard is a host MutationGuard that records every attempt and can deny
// GLOBAL role mutations (Scope.Kind == ScopeSubject) while allowing scoped ones —
// proving a guard distinguishes a global role mutation from a scoped one from the
// MutationAttempt alone, even though both share Operation OpRoleAssign/OpRoleUnassign.
type roleScopeGuard struct {
	denyGlobal bool
	seen       []MutationAttempt
}

func (g *roleScopeGuard) AuthorizeMutation(_ context.Context, attempt MutationAttempt, _ DecisionView) error {
	g.seen = append(g.seen, attempt)
	isRole := attempt.Operation == OpRoleAssign || attempt.Operation == OpRoleUnassign
	if g.denyGlobal && isRole && attempt.Scope.Kind == ScopeSubject {
		return fmt.Errorf("global role mutation denied (larger blast radius): %w", sdk.ErrForbidden)
	}
	return nil
}

func prinU(id string) PrincipalRef { return PrincipalRef{Type: "user", ID: id} }

// TestAssignRoleApplies proves a guarded scoped role assign flows through the
// ApplyGuarded boundary, applies, the guard observed a scoped OpRoleAssign attempt,
// and the assignment is visible to HasRole.
func TestAssignRoleApplies(t *testing.T) {
	guard := &roleScopeGuard{}
	svc := newGuardedLifecycle(t, guard, EvaluationLimits{})
	ctx := context.Background()

	rcpt, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	})
	if err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if rcpt == nil || rcpt.Outcome != OutcomeApplied || rcpt.Replayed {
		t.Fatalf("want applied non-replay receipt, got %+v", rcpt)
	}
	if len(guard.seen) != 1 || guard.seen[0].Operation != OpRoleAssign || guard.seen[0].Scope.Kind != ScopeResource {
		t.Fatalf("guard did not observe a scoped role-assign attempt: %+v", guard.seen)
	}
	if ok, err := svc.HasRole(ctx, prinU("u2"), "editor", "doc", "d1"); err != nil || !ok {
		t.Fatalf("assign not visible to HasRole: ok=%v err=%v", ok, err)
	}
}

// TestGlobalRoleGuardSeparateAction proves the guard treats a GLOBAL role mutation as
// a distinct, larger-blast-radius action: a guard denying global roles (ScopeSubject)
// still allows a scoped assignment (ScopeResource), with no change to the Operation
// vocabulary — distinguishability rides the MutationAttempt.Scope alone.
func TestGlobalRoleGuardSeparateAction(t *testing.T) {
	guard := &roleScopeGuard{denyGlobal: true}
	svc := newGuardedLifecycle(t, guard, EvaluationLimits{})
	ctx := context.Background()

	// A scoped assignment is allowed.
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	}); err != nil {
		t.Fatalf("scoped assign must be allowed while global is denied: %v", err)
	}
	// The same role globally is denied — the larger blast radius the guard gates.
	_, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", // global: both resource fields empty
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("global role assign with a global-denying guard: want forbidden, got %v", err)
	}
	if ok, _ := svc.HasRole(ctx, prinU("u2"), "editor", "", ""); ok {
		t.Fatalf("denied global assign changed state")
	}

	// The attempts the guard saw carry the distinguishing scope kinds.
	var sawScoped, sawGlobal bool
	for _, a := range guard.seen {
		if a.Operation == OpRoleAssign && a.Scope.Kind == ScopeResource {
			sawScoped = true
		}
		if a.Operation == OpRoleAssign && a.Scope.Kind == ScopeSubject {
			sawGlobal = true
		}
	}
	if !sawScoped || !sawGlobal {
		t.Fatalf("guard could not distinguish scoped from global role ops: %+v", guard.seen)
	}
}

// TestUnassignRoleSameRoleGrantRemains proves the core acceptance: a scoped unassign
// that leaves a GLOBAL grant for the same role reports SameRoleGrantRemains=true (the
// caller cannot mistake removal of one scoped row for removal of effective access),
// while a scoped unassign with no global grant reports false. The annotation is
// computed inside the repository's atomic critical section.
func TestUnassignRoleSameRoleGrantRemains(t *testing.T) {
	svc := newGuardedLifecycle(t, &roleScopeGuard{}, EvaluationLimits{})
	ctx := context.Background()

	// u2: both a global and a scoped editor grant.
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", // global
	}); err != nil {
		t.Fatalf("seed global: %v", err)
	}
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	}); err != nil {
		t.Fatalf("seed scoped: %v", err)
	}

	res, err := svc.UnassignRole(ctx, actorU1(), UnassignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	})
	if err != nil {
		t.Fatalf("UnassignRole: %v", err)
	}
	if res.Receipt == nil || res.Receipt.Outcome != OutcomeApplied {
		t.Fatalf("want applied receipt, got %+v", res.Receipt)
	}
	if !res.SameRoleGrantRemains {
		t.Fatalf("scoped unassign with a surviving global grant must report SameRoleGrantRemains=true")
	}
	// The scoped row is gone but the role still resolves via the global fallback.
	if ok, err := svc.HasRole(ctx, prinU("u2"), "editor", "doc", "d1"); err != nil || !ok {
		t.Fatalf("global grant must still confer the role after a scoped unassign: ok=%v err=%v", ok, err)
	}

	// u3: only a scoped grant — its removal leaves no equivalent grant.
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u3"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	}); err != nil {
		t.Fatalf("seed u3 scoped: %v", err)
	}
	res3, err := svc.UnassignRole(ctx, actorU1(), UnassignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u3"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	})
	if err != nil {
		t.Fatalf("UnassignRole u3: %v", err)
	}
	if res3.SameRoleGrantRemains {
		t.Fatalf("scoped unassign with no global grant must report SameRoleGrantRemains=false")
	}
	if ok, _ := svc.HasRole(ctx, prinU("u3"), "editor", "doc", "d1"); ok {
		t.Fatalf("u3 should have lost the role entirely")
	}
}

// TestUnassignRoleExactReplay proves exact idempotency: a replayed unassign
// returns the stored receipt (Replayed=true) and does not re-derive the annotation.
func TestUnassignRoleExactReplay(t *testing.T) {
	svc := newGuardedLifecycle(t, &roleScopeGuard{}, EvaluationLimits{})
	ctx := context.Background()
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := UnassignRoleCommand{MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1"}
	first, err := svc.UnassignRole(ctx, actorU1(), cmd)
	if err != nil || first.Receipt.Replayed {
		t.Fatalf("first unassign: replayed=%v err=%v", first.Receipt.Replayed, err)
	}
	replay, err := svc.UnassignRole(ctx, actorU1(), cmd)
	if err != nil {
		t.Fatalf("replay unassign: %v", err)
	}
	if !replay.Receipt.Replayed || replay.Receipt.Outcome != first.Receipt.Outcome || replay.Receipt.MutationID != first.Receipt.MutationID {
		t.Fatalf("want replay of the original receipt, got %+v", replay.Receipt)
	}
	if replay.SameRoleGrantRemains {
		t.Fatalf("a replay must not re-derive SameRoleGrantRemains (first-application annotation)")
	}
}

// TestAssignRoleIdempotentDuplicate proves an exact-duplicate assign is a
// no_change (then a replay), not a conflict or a second row.
func TestAssignRoleIdempotentDuplicate(t *testing.T) {
	svc := newGuardedLifecycle(t, &roleScopeGuard{}, EvaluationLimits{})
	ctx := context.Background()
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	}); err != nil {
		t.Fatalf("first assign: %v", err)
	}
	dup, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	})
	if err != nil || dup.Outcome != OutcomeNoChange {
		t.Fatalf("duplicate assign must be no_change, got outcome=%v err=%v", dup.Outcome, err)
	}
}

// TestRoleGuardedDenialCommitsNothing proves a denial never reaches Apply: the denied
// assign returns forbidden, writes no assignment, and does not consume its MutationID
// (a later allowed assign of the same tuple applies fresh).
func TestRoleGuardedDenialCommitsNothing(t *testing.T) {
	guard := &opGuard{err: fmt.Errorf("no: %w", sdk.ErrForbidden)}
	svc := newGuardedLifecycle(t, guard, EvaluationLimits{})
	ctx := context.Background()
	id := mustID(t)
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: id, Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	}); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("denied assign: want forbidden, got %v", err)
	}
	if ok, _ := svc.HasRole(ctx, prinU("u2"), "editor", "doc", "d1"); ok {
		t.Fatalf("denial reached Apply and wrote an assignment")
	}
	guard.err = nil
	rcpt, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: id, Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	})
	if err != nil || rcpt.Replayed || rcpt.Outcome != OutcomeApplied {
		t.Fatalf("post-denial retry should apply fresh, got %+v err=%v", rcpt, err)
	}
}

// TestRoleGuardedStaleRevision proves the expected-revision precondition is honored on
// the role path: a mismatched expected revision is the stale-revision command error.
func TestRoleGuardedStaleRevision(t *testing.T) {
	svc := newGuardedLifecycle(t, &roleScopeGuard{}, EvaluationLimits{})
	ctx := context.Background()
	stale := Revision(99)
	_, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
		ExpectedRevision: &stale,
	})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale expected revision must reject, got %v", err)
	}
}

// TestRoleGuardedHalfScopedRejected proves a half-scoped resource pair is rejected
// before any write.
func TestRoleGuardedHalfScopedRejected(t *testing.T) {
	svc := newGuardedLifecycle(t, &roleScopeGuard{}, EvaluationLimits{})
	ctx := context.Background()
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", // no ResourceID
	}); !errors.Is(err, ErrHalfScopedRoleScope) {
		t.Fatalf("half-scoped role: want ErrHalfScopedRoleScope, got %v", err)
	}
	if _, err := svc.UnassignRole(ctx, actorU1(), UnassignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceID: "d1", // no ResourceType
	}); !errors.Is(err, ErrHalfScopedRoleScope) {
		t.Fatalf("half-scoped role unassign: want ErrHalfScopedRoleScope, got %v", err)
	}
}

// TestRoleGuardedReadOnlyPosture proves a nil Guard closes the actor-facing role write
// path with ErrMutationsNotConfigured (no default allow).
func TestRoleGuardedReadOnlyPosture(t *testing.T) {
	st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(), Roles: st.Roles(), Mutations: st.Mutations(),
	}, Config{Model: lifecycleModel()}) // no Guard → read-only posture
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := comps.Service.AssignRole(context.Background(), actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	}); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("read-only assign: want ErrMutationsNotConfigured, got %v", err)
	}
}

// TestRoleGuardedUnwiredKind proves the guarded role methods fail closed with the
// roles-kind sentinel when the roles kind is off.
func TestRoleGuardedUnwiredKind(t *testing.T) {
	st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(), Mutations: st.Mutations(), // no Roles
	}, Config{Model: lifecycleModel(), Guard: &roleScopeGuard{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := comps.Service.AssignRole(context.Background(), actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "editor", ResourceType: "doc", ResourceID: "d1",
	}); !errors.Is(err, ErrRolesNotConfigured) {
		t.Fatalf("unwired roles assign: want ErrRolesNotConfigured, got %v", err)
	}
}

// TestRoleCommandRejectsUsersetSubjectsStructurally proves a userset role subject is
// impossible by TYPE: neither the guarded role commands nor the underlying RoleRow
// nor the concrete PrincipalRef carries a Relation field, so there is no runtime
// userset-rejection path — the type prevents it.
func TestRoleCommandRejectsUsersetSubjectsStructurally(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(AssignRoleCommand{}),
		reflect.TypeOf(UnassignRoleCommand{}),
		reflect.TypeOf(RoleRow{}),
		reflect.TypeOf(PrincipalRef{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			if strings.EqualFold(typ.Field(i).Name, "Relation") {
				t.Fatalf("%s carries a Relation field — a userset role subject must be structurally impossible", typ)
			}
		}
	}
	if reflect.TypeOf(AssignRoleCommand{}.Subject) != reflect.TypeOf(PrincipalRef{}) {
		t.Fatalf("AssignRoleCommand.Subject must be a concrete PrincipalRef")
	}
}

// -----------------------------------------------------------------------------
// Assign-time role-model validation (D8)
//
// The model check runs as part of the receipt-ABSENT SemanticValidator inside the
// repository's atomic boundary, composed with the relationship schema validator —
// not as a preflight in the typed methods. So every write path enforces it
// identically, and an exact replay still returns its stored receipt after the
// configured model drops the role.
// -----------------------------------------------------------------------------

// docRoleModel is the roles half of the lifecycle fixture: it shares the "doc"
// resource type with lifecycleModel but owns a DIFFERENT permission, the pair
// split construction permits.
func docRoleModel() RoleModel {
	return RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"doc": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
	}}
}

// newRoleModelComponents builds the full lifecycle bundle (guarded Service +
// trusted SystemMutator) over ONE memstore state with the given role model, so
// the three write paths can be compared on the same fixture.
func newRoleModelComponents(t *testing.T, st *memstore.Store, model RoleModel) Components {
	t.Helper()
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, Config{Model: lifecycleModel(), Guard: &roleScopeGuard{}, RoleModel: model})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func newRoleModelStore() *memstore.Store {
	return memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
}

// TestAssignRoleValidatesAgainstTheRoleModel proves the D8 matrix: a scoped
// assignment needs the role on THAT type, a global one needs it on ANY type, and
// an undeclared pair is refused with ErrInvalidRoleModel naming the symbols —
// before any row is written.
func TestAssignRoleValidatesAgainstTheRoleModel(t *testing.T) {
	comps := newRoleModelComponents(t, newRoleModelStore(), docRoleModel())
	svc := comps.Service
	ctx := context.Background()

	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "auditor", ResourceType: "doc", ResourceID: "d1",
	}); err != nil {
		t.Fatalf("declared scoped assignment: %v", err)
	}
	if _, err := svc.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "auditor", // global
	}); err != nil {
		t.Fatalf("declared global assignment: %v", err)
	}

	cases := map[string]struct {
		cmd       AssignRoleCommand
		wantNamed []string
	}{
		"typo'd role on a modeled type": {
			AssignRoleCommand{Subject: prinU("u3"), Role: "audtor", ResourceType: "doc", ResourceID: "d1"},
			[]string{"audtor", "doc"},
		},
		"declared role on an unmodeled type": {
			AssignRoleCommand{Subject: prinU("u3"), Role: "auditor", ResourceType: "project", ResourceID: "p1"},
			[]string{"auditor", "project"},
		},
		"typo'd role assigned globally": {
			AssignRoleCommand{Subject: prinU("u3"), Role: "audtor"},
			[]string{"audtor"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := tc.cmd
			cmd.MutationID = mustID(t)
			_, err := svc.AssignRole(ctx, actorU1(), cmd)
			if !errors.Is(err, ErrInvalidRoleModel) {
				t.Fatalf("want ErrInvalidRoleModel, got %v", err)
			}
			for _, symbol := range tc.wantNamed {
				if !strings.Contains(err.Error(), symbol) {
					t.Fatalf("message must name %q, got %v", symbol, err)
				}
			}
			if ok, err := svc.HasRole(ctx, cmd.Subject, cmd.Role, cmd.ResourceType, cmd.ResourceID); err != nil || ok {
				t.Fatalf("a refused assignment must write nothing: ok=%v err=%v", ok, err)
			}
		})
	}
}

// TestAssignRoleModelValidationParity proves the check cannot be bypassed by
// choosing a different write path: the guarded typed method, the trusted typed
// method, and the generic trusted Apply all refuse the same undeclared pair,
// because the rule lives in the validator the repository runs.
func TestAssignRoleModelValidationParity(t *testing.T) {
	comps := newRoleModelComponents(t, newRoleModelStore(), docRoleModel())
	ctx := context.Background()

	paths := map[string]func() error{
		"Service.AssignRole (guarded)": func() error {
			_, err := comps.Service.AssignRole(ctx, actorU1(), AssignRoleCommand{
				MutationID: mustID(t), Subject: prinU("u2"), Role: "audtor", ResourceType: "doc", ResourceID: "d1",
			})
			return err
		},
		"SystemMutator.AssignRole (trusted)": func() error {
			_, err := comps.SystemMutator.AssignRole(ctx, AssignRoleCommand{
				MutationID: mustID(t), Subject: prinU("u2"), Role: "audtor", ResourceType: "doc", ResourceID: "d1",
			})
			return err
		},
		"SystemMutator.Apply (generic)": func() error {
			_, err := comps.SystemMutator.Apply(ctx, Command{
				MutationID: mustID(t),
				Scope:      ScopeKey{Kind: ScopeResource, Type: "doc", ID: "d1"},
				Operation:  OpRoleAssign,
				Roles:      []RoleRow{{SubjectType: "user", SubjectID: "u2", Role: "audtor"}},
			})
			return err
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, ErrInvalidRoleModel) {
				t.Fatalf("want ErrInvalidRoleModel, got %v", err)
			}
		})
	}
	if ok, err := comps.Service.HasRole(ctx, prinU("u2"), "audtor", "doc", "d1"); err != nil || ok {
		t.Fatalf("no write path may have applied the undeclared role: ok=%v err=%v", ok, err)
	}
}

// TestAssignRoleWithoutARoleModelStaysOpaque proves hosts with no model are
// untouched: role names remain opaque strings the core does not judge.
func TestAssignRoleWithoutARoleModelStaysOpaque(t *testing.T) {
	comps := newRoleModelComponents(t, newRoleModelStore(), RoleModel{})
	ctx := context.Background()
	if _, err := comps.Service.AssignRole(ctx, actorU1(), AssignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "anything-goes", ResourceType: "doc", ResourceID: "d1",
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
	if err := st.Roles().Assign(ctx, Assignment{
		SubjectType: "user", SubjectID: "u2", Role: "legacy", ResourceType: "doc", ResourceID: "d1",
	}); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	comps := newRoleModelComponents(t, st, docRoleModel())

	if ok, err := comps.Service.HasRole(ctx, prinU("u2"), "legacy", "doc", "d1"); err != nil || !ok {
		t.Fatalf("reads stay opaque: ok=%v err=%v", ok, err)
	}
	if _, err := comps.SystemMutator.UnassignRole(ctx, UnassignRoleCommand{
		MutationID: mustID(t), Subject: prinU("u2"), Role: "legacy", ResourceType: "doc", ResourceID: "d1",
	}); err != nil {
		t.Fatalf("an undeclared role must stay removable: %v", err)
	}
	if ok, err := comps.Service.HasRole(ctx, prinU("u2"), "legacy", "doc", "d1"); err != nil || ok {
		t.Fatalf("legacy row was not removed: ok=%v err=%v", ok, err)
	}
}

// TestAssignRoleReplaySurvivesTheModelDroppingTheRole proves the validator runs
// receipt-ABSENT: an exact replay of an already-applied assignment returns its
// stored receipt even after the host reconfigured a model that no longer declares
// the role — while a FIRST application of that same role is now refused.
func TestAssignRoleReplaySurvivesTheModelDroppingTheRole(t *testing.T) {
	st := newRoleModelStore()
	ctx := context.Background()
	id := mustID(t)
	cmd := AssignRoleCommand{
		MutationID: id, Subject: prinU("u2"), Role: "auditor", ResourceType: "doc", ResourceID: "d1",
	}

	before := newRoleModelComponents(t, st, docRoleModel())
	first, err := before.SystemMutator.AssignRole(ctx, cmd)
	if err != nil || first.Outcome != OutcomeApplied || first.Replayed {
		t.Fatalf("first application: got %+v err=%v", first, err)
	}

	// The host narrows the model: "auditor" is gone.
	narrowed := RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"doc": {Roles: []string{"reviewer"}, Permissions: map[string][]string{"audit": {"reviewer"}}},
	}}
	after := newRoleModelComponents(t, st, narrowed)

	replay, err := after.SystemMutator.AssignRole(ctx, cmd)
	if err != nil {
		t.Fatalf("exact replay must return the stored receipt, got %v", err)
	}
	if !replay.Replayed || replay.PayloadDigest != first.PayloadDigest {
		t.Fatalf("replay: got %+v, want the stored receipt of %+v", replay, first)
	}

	// A NEW assignment of the dropped role is refused under the narrowed model.
	fresh := cmd
	fresh.MutationID = mustID(t)
	fresh.Subject = prinU("u3")
	if _, err := after.SystemMutator.AssignRole(ctx, fresh); !errors.Is(err, ErrInvalidRoleModel) {
		t.Fatalf("first application under the narrowed model: want ErrInvalidRoleModel, got %v", err)
	}
}
