package authorization

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/memstore"
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
	deny map[Operation]bool
	err  error
	seen []MutationAttempt
}

func (g *opGuard) AuthorizeMutation(_ context.Context, attempt MutationAttempt, _ DecisionView) error {
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
func lifecycleModel() Schema {
	return NewSchema([]ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{"edit": AnyOf(Direct("owner"), Direct("editor"))},
		},
	}})
}

// newGuardedLifecycle builds a Service over the real memstore bundle with the
// guardian invariant disabled (empty policy), so the grant/revoke/replace/purge
// lifecycle is exercised without fighting last-owner protection (that is AZ3-3.2).
func newGuardedLifecycle(t *testing.T, guard MutationGuard, limits EvaluationLimits) *Service {
	t.Helper()
	st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, Config{Model: lifecycleModel(), Guard: guard, Limits: limits})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps.Service
}

func mustID(t *testing.T) MutationID {
	t.Helper()
	id, err := NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return id
}

func subjU(id string) SubjectRef { return SubjectRef{Type: "user", ID: id} }

// TestGrantRelationshipGuardedApplies proves a guarded grant flows through the
// repository ApplyGuarded boundary, returns an applied receipt, records the governing
// schema digest, and the guard saw the grant attempt.
func TestGrantRelationshipGuardedApplies(t *testing.T) {
	guard := &opGuard{}
	svc := newGuardedLifecycle(t, guard, EvaluationLimits{})
	ctx := context.Background()

	rcpt, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil {
		t.Fatalf("GrantRelationship: %v", err)
	}
	if rcpt == nil || rcpt.Outcome != OutcomeApplied || rcpt.Replayed {
		t.Fatalf("want applied non-replay receipt, got %+v", rcpt)
	}

	digest, _ := svc.SchemaDigest()
	if digest == "" || rcpt.SchemaDigest != digest {
		t.Fatalf("receipt schema digest %q must equal the governing digest %q", rcpt.SchemaDigest, digest)
	}
	if len(guard.seen) != 1 || guard.seen[0].Operation != OpGrant {
		t.Fatalf("guard did not observe the grant attempt: %+v", guard.seen)
	}

	targets, err := svc.GetRelationTargets(ctx, "doc", "d1", "editor")
	if err != nil || len(targets) != 1 {
		t.Fatalf("grant not visible to reads: targets=%+v err=%v", targets, err)
	}
}

// TestGrantRelationshipExactReplay proves an exact grant replay returns the stored
// receipt with Replayed=true and does not re-apply.
func TestGrantRelationshipExactReplay(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, EvaluationLimits{})
	ctx := context.Background()
	id := mustID(t)
	cmd := GrantRelationshipCommand{MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2")}

	first, err := svc.GrantRelationship(ctx, actorU1(), cmd)
	if err != nil || first.Replayed {
		t.Fatalf("first grant: replayed=%v err=%v", first.Replayed, err)
	}
	replay, err := svc.GrantRelationship(ctx, actorU1(), cmd)
	if err != nil {
		t.Fatalf("replay grant: %v", err)
	}
	if !replay.Replayed || replay.Outcome != OutcomeApplied || replay.MutationID != first.MutationID {
		t.Fatalf("want replay of the original applied receipt, got %+v", replay)
	}
}

// TestGrantRelationshipConflictThenReplace proves the one-relation rule: a different
// relation for an already-related subject is a semantic_conflict (not a silent
// overwrite), and ReplaceRelationship resolves it atomically.
func TestGrantRelationshipConflictThenReplace(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, EvaluationLimits{})
	ctx := context.Background()

	if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	}); err != nil {
		t.Fatalf("seed viewer: %v", err)
	}

	conflict, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil {
		t.Fatalf("conflicting grant returned error, want a semantic_conflict outcome: %v", err)
	}
	if conflict.Outcome != OutcomeSemanticConflict {
		t.Fatalf("want semantic_conflict, got %+v", conflict)
	}

	replaced, err := svc.ReplaceRelationship(ctx, actorU1(), ReplaceRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil || replaced.Outcome != OutcomeApplied {
		t.Fatalf("replace: outcome=%v err=%v", replaced.Outcome, err)
	}
	editors, _ := svc.GetRelationTargets(ctx, "doc", "d1", "editor")
	viewers, _ := svc.GetRelationTargets(ctx, "doc", "d1", "viewer")
	if len(editors) != 1 || len(viewers) != 0 {
		t.Fatalf("replace not atomic: editors=%+v viewers=%+v", editors, viewers)
	}
}

// TestRevokeRelationshipAppliedAndNotFound proves a revoke of a present tuple applies
// and a revoke of an absent tuple is a committed not_found no-op, never an error.
func TestRevokeRelationshipAppliedAndNotFound(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, EvaluationLimits{})
	ctx := context.Background()

	if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	applied, err := svc.RevokeRelationship(ctx, actorU1(), RevokeRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil || applied.Outcome != OutcomeApplied {
		t.Fatalf("revoke present: outcome=%v err=%v", applied.Outcome, err)
	}
	absent, err := svc.RevokeRelationship(ctx, actorU1(), RevokeRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u9"),
	})
	if err != nil || absent.Outcome != OutcomeNotFound {
		t.Fatalf("revoke absent: want not_found no-op, outcome=%v err=%v", absent.Outcome, err)
	}
}

// TestPurgeResourceAuthorizationBound proves the bulk purge respects its
// affected-row bound (the resolved MaxBatchSize): a purge within the bound applies,
// and one that would exceed it is invariant_blocked with nothing removed.
func TestPurgeResourceAuthorizationBound(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, EvaluationLimits{MaxBatchSize: 2})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
			MutationID: mustID(t), ResourceType: "doc", ResourceID: "big", Relation: "viewer", Subject: subjU(u),
		}); err != nil {
			t.Fatalf("seed %s: %v", u, err)
		}
	}
	blocked, err := svc.PurgeResourceAuthorization(ctx, actorU1(), PurgeResourceAuthorizationCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "big",
	})
	if err != nil || blocked.Outcome != OutcomeInvariantBlocked {
		t.Fatalf("over-bound purge: want invariant_blocked, outcome=%v err=%v", blocked.Outcome, err)
	}
	if targets, _ := svc.GetRelationTargets(ctx, "doc", "big", "viewer"); len(targets) != 3 {
		t.Fatalf("blocked purge removed rows: %+v", targets)
	}

	// A resource within the bound purges cleanly.
	if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "small", Relation: "viewer", Subject: subjU("u1"),
	}); err != nil {
		t.Fatalf("seed small: %v", err)
	}
	ok, err := svc.PurgeResourceAuthorization(ctx, actorU1(), PurgeResourceAuthorizationCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "small",
	})
	if err != nil || ok.Outcome != OutcomeApplied {
		t.Fatalf("within-bound purge: outcome=%v err=%v", ok.Outcome, err)
	}
}

// TestPurgeGuardSeparateAction proves the guard distinguishes bulk purge from a
// single grant via MutationAttempt.Operation: a guard denying OpPurge still allows
// grants, and the denied purge commits nothing.
func TestPurgeGuardSeparateAction(t *testing.T) {
	guard := &opGuard{deny: map[Operation]bool{OpPurge: true}}
	svc := newGuardedLifecycle(t, guard, EvaluationLimits{})
	ctx := context.Background()

	if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	}); err != nil {
		t.Fatalf("grant must be allowed while purge is denied: %v", err)
	}
	_, err := svc.PurgeResourceAuthorization(ctx, actorU1(), PurgeResourceAuthorizationCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1",
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("purge with a purge-denying guard: want forbidden, got %v", err)
	}
	if targets, _ := svc.GetRelationTargets(ctx, "doc", "d1", "viewer"); len(targets) != 1 {
		t.Fatalf("denied purge changed state: %+v", targets)
	}
}

// TestGuardDenialCommitsNothing proves a denial never reaches Apply's row changes:
// the denied grant returns a forbidden error, writes no row, and does not consume
// its MutationID (a later allowed grant of the same tuple applies fresh).
func TestGuardDenialCommitsNothing(t *testing.T) {
	guard := &opGuard{err: fmt.Errorf("no: %w", sdk.ErrForbidden)}
	svc := newGuardedLifecycle(t, guard, EvaluationLimits{})
	ctx := context.Background()
	id := mustID(t)

	if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("denied grant: want forbidden, got %v", err)
	}
	if targets, _ := svc.GetRelationTargets(ctx, "doc", "d1", "editor"); len(targets) != 0 {
		t.Fatalf("denial reached Apply and wrote a row: %+v", targets)
	}

	// The denied MutationID is not consumed: reusing it under an allowing guard
	// applies a fresh write (not a replay).
	guard.err = nil
	rcpt, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil || rcpt.Replayed || rcpt.Outcome != OutcomeApplied {
		t.Fatalf("post-denial retry should apply fresh, got %+v err=%v", rcpt, err)
	}
}

// TestGrantReadOnlyPosture proves a nil Guard closes the actor-facing write path:
// GrantRelationship fails with ErrMutationsNotConfigured and writes nothing.
func TestGrantReadOnlyPosture(t *testing.T) {
	st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(), Roles: st.Roles(), Mutations: st.Mutations(),
	}, Config{Model: lifecycleModel()}) // no Guard → read-only posture
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := comps.Service.GrantRelationship(context.Background(), actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("read-only grant: want ErrMutationsNotConfigured, got %v", err)
	}
}

// TestGrantUnwiredRelationshipKind proves the typed relationship mutations fail closed
// with the relationship-kind sentinel when the kind is off.
func TestGrantUnwiredRelationshipKind(t *testing.T) {
	st := memstore.New()
	comps, err := NewService(Repositories{Roles: st.Roles(), Mutations: st.Mutations()},
		Config{Guard: &opGuard{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := comps.Service.GrantRelationship(context.Background(), actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); !errors.Is(err, ErrRelationshipsNotConfigured) {
		t.Fatalf("unwired grant: want ErrRelationshipsNotConfigured, got %v", err)
	}
}

// TestGrantSemanticValidatorRejectsUnknownRelation proves the receipt-absent
// current-schema validator runs inside Apply: granting a relation the schema does not
// declare is rejected and commits nothing.
func TestGrantSemanticValidatorRejectsUnknownRelation(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, EvaluationLimits{})
	ctx := context.Background()
	_, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "bogus", Subject: subjU("u2"),
	})
	if err == nil {
		t.Fatalf("grant of an undeclared relation must be rejected by the semantic validator")
	}
	if targets, _ := svc.GetRelationTargets(ctx, "doc", "d1", "bogus"); len(targets) != 0 {
		t.Fatalf("rejected grant wrote a row: %+v", targets)
	}
}

// TestGrantReplaySurvivesSchemaChange proves the pinned validation order: an exact
// stored replay returns its original receipt (and original schema digest) through a
// service whose CURRENT schema no longer accepts the relation, while a NEW command
// with that relation is rejected by the current-schema validator.
func TestGrantReplaySurvivesSchemaChange(t *testing.T) {
	st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
	ctx := context.Background()

	svcOld, err := NewService(Repositories{
		Relationships: st.Relationships(), Roles: st.Roles(), Mutations: st.Mutations(),
	}, Config{Model: lifecycleModel(), Guard: &opGuard{}})
	if err != nil {
		t.Fatalf("NewService old: %v", err)
	}
	id := mustID(t)
	cmd := GrantRelationshipCommand{MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2")}
	orig, err := svcOld.Service.GrantRelationship(ctx, actorU1(), cmd)
	if err != nil {
		t.Fatalf("original grant: %v", err)
	}

	// A newer schema WITHOUT the editor relation, sharing the same store.
	newerModel := NewSchema([]ResourceSchema{{
		Name: "doc",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"edit": AnyOf(Direct("owner"))},
		},
	}})
	svcNew, err := NewService(Repositories{
		Relationships: st.Relationships(), Roles: st.Roles(), Mutations: st.Mutations(),
	}, Config{Model: newerModel, Guard: &opGuard{}})
	if err != nil {
		t.Fatalf("NewService newer: %v", err)
	}

	replay, err := svcNew.Service.GrantRelationship(ctx, actorU1(), cmd)
	if err != nil {
		t.Fatalf("exact replay under newer schema must succeed, got %v", err)
	}
	if !replay.Replayed || replay.Outcome != OutcomeApplied || replay.SchemaDigest != orig.SchemaDigest {
		t.Fatalf("replay must return the original receipt+digest, got %+v (orig digest %q)", replay, orig.SchemaDigest)
	}

	// A NEW command with the now-undeclared relation is rejected by the current schema.
	if _, err := svcNew.Service.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u9"),
	}); err == nil {
		t.Fatalf("a fresh grant of the undeclared relation must be rejected")
	}
}
