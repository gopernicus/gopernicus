package authorization

import (
	"context"
	"errors"
	"fmt"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

// -----------------------------------------------------------------------------
// Service-level guarded relationship lifecycle, over the REAL memstore bundle
// (shared-state relationship + mutation repositories) — not a stub.
// -----------------------------------------------------------------------------

// opGuard is a host MutationGuard that records every attempt and denies a
// configurable set of operations, proving the guard distinguishes bulk purge from a
// single grant by MutationAttempt.Operation.
type opGuard struct {
	deny map[mutations.Operation]bool
	err  error
	seen []mutations.MutationAttempt
}

func (g *opGuard) AuthorizeMutation(_ context.Context, attempt mutations.MutationAttempt, _ mutations.DecisionView) error {
	g.seen = append(g.seen, attempt)
	if g.err != nil {
		return g.err
	}
	if g.deny[attempt.Operation] {
		return fmt.Errorf("denied %s: %w", attempt.Operation, sdk.ErrForbidden)
	}
	return nil
}

// lifecycleModel declares a resource type with the relations the lifecycle tests
// grant/replace; "edit" is a permission over owner/editor.
func lifecycleModel() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "doc",
		Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"owner":  {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
				"editor": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{"edit": relationships.AnyOf(relationships.Direct("owner"), relationships.Direct("editor"))},
		},
	}})
}

// newGuardedLifecycle builds a Service over the real memstore bundle with the
// guardian invariant disabled (empty policy), so the grant/revoke/replace/purge
// lifecycle is exercised without fighting last-owner protection (that is AZ3-3.2).
func newGuardedLifecycle(t *testing.T, guard mutations.MutationGuard, limits authmodel.EvaluationLimits) Components {
	t.Helper()
	st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	comps, err := New(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, WithRelationshipModel(lifecycleModel()), WithGuard(guard), WithLimits(limits))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func subjU(id string) relationships.SubjectRef { return relationships.SubjectRef{Type: "user", ID: id} }

func TestGrantRelationshipGuardedApplies(t *testing.T) {
	guard := &opGuard{}
	svc := newGuardedLifecycle(t, guard, authmodel.EvaluationLimits{})
	ctx := context.Background()

	rcpt, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil {
		t.Fatalf("GrantRelationship: %v", err)
	}
	if rcpt == nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("want applied non-replay receipt, got %+v", rcpt)
	}

	if len(guard.seen) != 1 || guard.seen[0].Operation != mutations.OpGrant {
		t.Fatalf("guard did not observe the grant attempt: %+v", guard.seen)
	}

	targets, err := svc.Relationships.GetRelationTargets(ctx, "doc", "d1", "editor")
	if err != nil || len(targets) != 1 {
		t.Fatalf("grant not visible to reads: targets=%+v err=%v", targets, err)
	}
}

func TestGrantRelationshipRepeatedCallReportsCurrentState(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, authmodel.EvaluationLimits{})
	ctx := context.Background()
	cmd := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2")}
	for i, want := range []mutations.Outcome{mutations.OutcomeApplied, mutations.OutcomeNoChange} {
		got, err := svc.Mutations.GrantRelationship(ctx, actorU1(), cmd)
		if err != nil || got == nil || got.Outcome != want {
			t.Fatalf("call %d: %+v, %v", i, got, err)
		}
	}
	if _, err := svc.Mutations.RevokeRelationship(ctx, actorU1(), mutations.RevokeRelationshipCommand(cmd)); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Mutations.GrantRelationship(ctx, actorU1(), cmd)
	if err != nil || got == nil || got.Outcome != mutations.OutcomeApplied {
		t.Fatalf("regrant after revoke: %+v, %v", got, err)
	}
}

// TestGrantRelationshipConflictThenReplace proves the one-relation rule: a different
// relation for an already-related subject is a semantic_conflict (not a silent
// overwrite), and ReplaceRelationship resolves it atomically.
func TestGrantRelationshipConflictThenReplace(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, authmodel.EvaluationLimits{})
	ctx := context.Background()

	if _, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	}); err != nil {
		t.Fatalf("seed viewer: %v", err)
	}

	conflict, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if !errors.Is(err, mutations.ErrSemanticConflict) || conflict != nil {
		t.Fatalf("conflicting grant: receipt=%+v err=%v", conflict, err)
	}

	replaced, err := svc.Mutations.ReplaceRelationship(ctx, actorU1(), mutations.ReplaceRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil || replaced.Outcome != mutations.OutcomeApplied {
		t.Fatalf("replace: outcome=%v err=%v", replaced.Outcome, err)
	}
	editors, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "d1", "editor")
	viewers, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "d1", "viewer")
	if len(editors) != 1 || len(viewers) != 0 {
		t.Fatalf("replace not atomic: editors=%+v viewers=%+v", editors, viewers)
	}
}

// TestRevokeRelationshipAppliedAndNotFound proves a revoke of a present tuple applies
// and a revoke of an absent tuple is a committed not_found no-op, never an error.
func TestRevokeRelationshipAppliedAndNotFound(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, authmodel.EvaluationLimits{})
	ctx := context.Background()

	if _, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	applied, err := svc.Mutations.RevokeRelationship(ctx, actorU1(), mutations.RevokeRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil || applied.Outcome != mutations.OutcomeApplied {
		t.Fatalf("revoke present: outcome=%v err=%v", applied.Outcome, err)
	}
	absent, err := svc.Mutations.RevokeRelationship(ctx, actorU1(), mutations.RevokeRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u9"),
	})
	if err != nil || absent.Outcome != mutations.OutcomeNotFound {
		t.Fatalf("revoke absent: want not_found no-op, outcome=%v err=%v", absent.Outcome, err)
	}
}

// TestPurgeResourceAuthorizationBound proves the bulk purge respects its
// affected-row bound (the resolved MaxBatchSize): a purge within the bound applies,
// and one that would exceed it is invariant_blocked with nothing removed.
func TestPurgeResourceAuthorizationBound(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, authmodel.EvaluationLimits{MaxBatchSize: 2})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		if _, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
			ResourceType: "doc", ResourceID: "big", Relation: "viewer", Subject: subjU(u),
		}); err != nil {
			t.Fatalf("seed %s: %v", u, err)
		}
	}
	blocked, err := svc.Mutations.PurgeResourceAuthorization(ctx, actorU1(), mutations.PurgeResourceAuthorizationCommand{
		ResourceType: "doc", ResourceID: "big",
	})
	if !errors.Is(err, mutations.ErrInvariantBlocked) || blocked != nil {
		t.Fatalf("over-bound purge: receipt=%+v err=%v", blocked, err)
	}
	if targets, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "big", "viewer"); len(targets) != 3 {
		t.Fatalf("blocked purge removed rows: %+v", targets)
	}

	// A resource within the bound purges cleanly.
	if _, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "small", Relation: "viewer", Subject: subjU("u1"),
	}); err != nil {
		t.Fatalf("seed small: %v", err)
	}
	ok, err := svc.Mutations.PurgeResourceAuthorization(ctx, actorU1(), mutations.PurgeResourceAuthorizationCommand{
		ResourceType: "doc", ResourceID: "small",
	})
	if err != nil || ok.Outcome != mutations.OutcomeApplied {
		t.Fatalf("within-bound purge: outcome=%v err=%v", ok.Outcome, err)
	}
}

// TestPurgeGuardSeparateAction proves the guard distinguishes bulk purge from a
// single grant via MutationAttempt.Operation: a guard denying mutations.OpPurge still allows
// grants, and the denied purge commits nothing.
func TestPurgeGuardSeparateAction(t *testing.T) {
	guard := &opGuard{deny: map[mutations.Operation]bool{mutations.OpPurge: true}}
	svc := newGuardedLifecycle(t, guard, authmodel.EvaluationLimits{})
	ctx := context.Background()

	if _, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	}); err != nil {
		t.Fatalf("grant must be allowed while purge is denied: %v", err)
	}
	_, err := svc.Mutations.PurgeResourceAuthorization(ctx, actorU1(), mutations.PurgeResourceAuthorizationCommand{
		ResourceType: "doc", ResourceID: "d1",
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("purge with a purge-denying guard: want forbidden, got %v", err)
	}
	if targets, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "d1", "viewer"); len(targets) != 1 {
		t.Fatalf("denied purge changed state: %+v", targets)
	}
}

func TestGuardDenialCommitsNothing(t *testing.T) {
	guard := &opGuard{err: fmt.Errorf("no: %w", sdk.ErrForbidden)}
	svc := newGuardedLifecycle(t, guard, authmodel.EvaluationLimits{})
	ctx := context.Background()

	if _, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("denied grant: want forbidden, got %v", err)
	}
	if targets, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "d1", "editor"); len(targets) != 0 {
		t.Fatalf("denial reached Apply and wrote a row: %+v", targets)
	}

	guard.err = nil
	rcpt, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("post-denial retry should apply fresh, got %+v err=%v", rcpt, err)
	}
}

// TestGrantReadOnlyPosture proves a nil Guard closes the actor-facing write path:
// GrantRelationship fails with ErrMutationsNotConfigured and writes nothing.
func TestGrantReadOnlyPosture(t *testing.T) {
	st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	comps, err := New(Repositories{
		Relationships: st.Relationships(), Roles: st.Roles(), Mutations: st.Mutations(),
	}, WithRelationshipModel(lifecycleModel())) // no Guard → read-only posture
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := comps.Mutations.GrantRelationship(context.Background(), actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); !errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatalf("read-only grant: want ErrMutationsNotConfigured, got %v", err)
	}
}

// TestGrantUnwiredRelationshipKind proves the typed relationship mutations fail closed
// with the relationship-kind sentinel when the kind is off.
func TestGrantUnwiredRelationshipKind(t *testing.T) {
	st := memory.New()
	comps, err := New(Repositories{Roles: st.Roles(), Mutations: st.Mutations()}, WithGuard(&opGuard{}))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := comps.Mutations.GrantRelationship(context.Background(), actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); !errors.Is(err, relationships.ErrRelationshipsNotConfigured) {
		t.Fatalf("unwired grant: want ErrRelationshipsNotConfigured, got %v", err)
	}
}

func TestGrantSemanticValidatorRejectsUnknownRelation(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, authmodel.EvaluationLimits{})
	ctx := context.Background()
	_, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "bogus", Subject: subjU("u2"),
	})
	if err == nil {
		t.Fatalf("grant of an undeclared relation must be rejected by the semantic validator")
	}
	if targets, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "d1", "bogus"); len(targets) != 0 {
		t.Fatalf("rejected grant wrote a row: %+v", targets)
	}
}

func TestRepeatedGrantUsesCurrentSchema(t *testing.T) {
	st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	ctx := context.Background()

	svcOld, err := New(Repositories{
		Relationships: st.Relationships(), Roles: st.Roles(), Mutations: st.Mutations(),
	}, WithRelationshipModel(lifecycleModel()), WithGuard(&opGuard{}))
	if err != nil {
		t.Fatalf("NewService old: %v", err)
	}

	cmd := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2")}
	_, err = svcOld.Mutations.GrantRelationship(ctx, actorU1(), cmd)
	if err != nil {
		t.Fatalf("original grant: %v", err)
	}

	// A newer schema WITHOUT the editor relation, sharing the same store.
	newerModel := relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "doc",
		Def: relationships.ResourceTypeDef{
			Relations:   map[string]relationships.RelationDef{"owner": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]relationships.PermissionRule{"edit": relationships.AnyOf(relationships.Direct("owner"))},
		},
	}})
	svcNew, err := New(Repositories{
		Relationships: st.Relationships(), Roles: st.Roles(), Mutations: st.Mutations(),
	}, WithRelationshipModel(newerModel), WithGuard(&opGuard{}))
	if err != nil {
		t.Fatalf("NewService newer: %v", err)
	}

	result, err := svcNew.Mutations.GrantRelationship(ctx, actorU1(), cmd)
	if err == nil || result != nil {
		t.Fatalf("repeated grant of removed relation: %+v, %v", result, err)
	}
	if _, err := svcNew.SystemMutator.GrantRelationship(ctx, cmd); err == nil {
		t.Fatal("trusted grant bypassed current schema")
	}
	if _, err := svcNew.Mutations.RevokeRelationship(ctx, actorU1(), mutations.RevokeRelationshipCommand(cmd)); err != nil {
		t.Fatalf("removed relation must stay revocable: %v", err)
	}
	// A NEW command with the now-undeclared relation is rejected by the current schema.
	if _, err := svcNew.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u9"),
	}); err == nil {
		t.Fatalf("a fresh grant of the undeclared relation must be rejected")
	}
}
