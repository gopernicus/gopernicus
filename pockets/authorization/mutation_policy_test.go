package authorization

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

// -----------------------------------------------------------------------------
// The proof policy: a manage-access guard that reads the DecisionView
// -----------------------------------------------------------------------------

type manageGuard struct {
	mu       sync.Mutex
	seen     []mutations.MutationAttempt
	infraErr error
}

func (g *manageGuard) AuthorizeMutation(ctx context.Context, attempt mutations.MutationAttempt, view mutations.DecisionView) error {
	g.mu.Lock()
	g.seen = append(g.seen, attempt)
	infra := g.infraErr
	g.mu.Unlock()

	if infra != nil {
		return infra // NOT a denial — a coarse guard-backend failure
	}
	if attempt.Target.Kind != mutations.TargetResource {
		return fmt.Errorf("global mutation requires a trusted holder: %w", sdk.ErrForbidden)
	}
	ok, err := view.CheckRelation(ctx, attempt.Actor.PrincipalRef, "owner", authmodel.Resource{Type: attempt.Target.Type, ID: attempt.Target.ID})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s:%s does not hold manage(owner) on %s: %w", attempt.Actor.Type, attempt.Actor.ID, attempt.Target, sdk.ErrForbidden)
	}
	return nil
}

func (g *manageGuard) seenCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.seen)
}

// newProofComponents builds a Components over the real memstore bundle with the
// guardian invariant DISABLED (empty policy — last-owner protection is AZ3-3.2's
// concern) so the proof-policy cases exercise the guard, not the guardian. The
// SystemMutator is used to SEED the initial manage grant, since the actor path is
// guarded and the very first owner cannot yet prove it manages the resource
// (chicken/egg): bootstrap is a trusted operation, exactly as designed.
func newProofComponents(t *testing.T, guard mutations.MutationGuard) Components {
	t.Helper()
	st := memory.New(memory.WithGuardianPolicy(mutations.GuardianPolicy{}))
	comps, err := New(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, WithRelationshipModel(lifecycleModel()), WithGuard(guard))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// seedOwner trustedly grants owner on a resource (the bootstrap seam) so an actor
// can then prove manage rights through the guard.
func seedOwner(t *testing.T, sm *mutations.SystemMutator, resourceID, user string) {
	t.Helper()
	if _, err := sm.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: resourceID, Relation: "owner", Subject: subjU(user),
	}); err != nil {
		t.Fatalf("seed owner %s on %s: %v", user, resourceID, err)
	}
}

// TestProofPolicyManageGuardAllowsAndDenies proves the guard evaluates the
// DecisionView before any write: an owner (manage) actor's grant commits and the
// row is visible; a non-owner actor's grant is denied (forbidden) and commits
// nothing. The allow decision is authorization data read through the boundary view.
func TestProofPolicyManageGuardAllowsAndDenies(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1") // u1 manages d1

	// The owner may grant.
	rcpt, err := comps.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("owner grant: outcome=%v err=%v", rcpt.Outcome, err)
	}
	if targets, _ := comps.Relationships.GetRelationTargets(ctx, "doc", "d1", "viewer"); len(targets) != 1 {
		t.Fatalf("allowed grant did not commit its row: %+v", targets)
	}

	// A non-owner may not.
	nonOwner := mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: "u9"}}
	_, err = comps.Mutations.GrantRelationship(ctx, nonOwner, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u3"),
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("non-owner grant: want forbidden, got %v", err)
	}
	if targets, _ := comps.Relationships.GetRelationTargets(ctx, "doc", "d1", "viewer"); len(targets) != 1 {
		t.Fatalf("denied grant changed state: %+v", targets)
	}
}

// TestProofPolicySelfEscalationDenied proves the proof policy blocks
// self-grant/self-escalation: an actor that does not already manage a resource
// cannot grant ITSELF the manage (owner) relation on it. The would-be escalator's
// own lack of manage rights is exactly what the guard reads, so the grant is denied
// before Apply and no owner row is written.
func TestProofPolicySelfEscalationDenied(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1") // only u1 manages d1

	escalator := mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: "u5"}}
	_, err := comps.Mutations.GrantRelationship(ctx, escalator, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "owner", Subject: subjU("u5"),
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("self-escalation to owner: want forbidden, got %v", err)
	}
	if ok, _ := comps.Relationships.GetRelationTargets(ctx, "doc", "d1", "owner"); len(ok) != 1 {
		t.Fatalf("self-escalation wrote an owner row: %+v", ok)
	}

	// The same escalator cannot self-grant a lesser relation either.
	_, err = comps.Mutations.GrantRelationship(ctx, escalator, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u5"),
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("self-grant of editor by a non-manager: want forbidden, got %v", err)
	}
}

func TestConcurrentGuardManageRevokeRaceServicePath(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard)
	svc, sm := comps, comps.SystemMutator

	const rounds = 8
	for r := 0; r < rounds; r++ {
		res := "svcrace" + strconv.Itoa(r)
		seedOwner(t, sm, res, "u1") // u1 is the manage grant the guarded actor depends on

		// Trusted revoke of u1's own manage grant (an admin/teardown yanking access),
		// racing u1's guarded grant. The trusted path always applies (empty guardian).
		revokeU1 := mutations.Command{

			Target:        mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: res},
			Operation:     mutations.OpRevoke,
			Relationships: []mutations.RelationshipRow{{Relation: "owner", Subject: subjU("u1")}},
		}

		var wg sync.WaitGroup
		var grantRcpt, revokeRcpt *mutations.Result
		var grantErr, revokeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			grantRcpt, grantErr = svc.Mutations.GrantRelationship(context.Background(), actorU1(), mutations.GrantRelationshipCommand{
				ResourceType: "doc", ResourceID: res, Relation: "viewer", Subject: subjU("u3"),
			})
		}()
		go func() {
			defer wg.Done()
			revokeRcpt, revokeErr = sm.Apply(context.Background(), revokeU1)
		}()
		wg.Wait()

		if revokeErr != nil || revokeRcpt.Outcome != mutations.OutcomeApplied {
			t.Fatalf("round %d: trusted revoke must apply, outcome=%v err=%v", r, revokeRcpt.Outcome, revokeErr)
		}

		hasViewer, _ := svc.Relationships.GetRelationTargets(context.Background(), "doc", res, "viewer")
		switch {
		case grantErr == nil:
			// The guarded write won the lock on a snapshot where u1 still owned res.
			if grantRcpt.Outcome != mutations.OutcomeApplied {
				t.Fatalf("round %d: a nil-error guarded write must be applied, got %q", r, grantRcpt.Outcome)
			}
			if len(hasViewer) != 1 {
				t.Fatalf("round %d: an applied guarded write must leave its row", r)
			}
		case errors.Is(grantErr, sdk.ErrConflict) || errors.Is(grantErr, sdk.ErrForbidden):
			// The revoke won: the guarded write is STALE or DENIED and wrote nothing.
			if grantRcpt != nil {
				t.Fatalf("round %d: a lost guarded write must return no receipt, got %+v", r, grantRcpt)
			}
			if len(hasViewer) != 0 {
				t.Fatalf("round %d: a stale/denied guarded write must not commit its row (detached stale allow)", r)
			}
		default:
			t.Fatalf("round %d: guarded write must be applied, stale, or denied; got rcpt=%+v err=%v", r, grantRcpt, grantErr)
		}
	}
}

// -----------------------------------------------------------------------------
// Guard infrastructure error vs. denial (audit records failed, not denied)
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Invalid actor / invalid proposed tuple — rejected before Apply, ID unconsumed
// -----------------------------------------------------------------------------

func TestMutationInvalidActorRejectedBeforeApply(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1")

	_, err := comps.Mutations.GrantRelationship(ctx, mutations.Actor{}, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty actor: want invalid-input, got %v", err)
	}
	if guard.seenCount() != 0 {
		t.Fatalf("an invalid actor must be rejected before the guard runs, saw %d attempts", guard.seenCount())
	}

	rcpt, err := comps.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("post-rejection reuse must apply fresh, got %+v err=%v", rcpt, err)
	}
}

func TestMutationInvalidProposedTupleRejectedBeforeApply(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1")

	_, err := comps.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "", Subject: subjU("u2"), // empty relation
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid proposed tuple: want invalid-input command error, got %v", err)
	}
	if guard.seenCount() != 0 {
		t.Fatalf("a structurally invalid command must be rejected before the guard runs, saw %d", guard.seenCount())
	}

	rcpt, err := comps.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("post-rejection reuse must apply fresh, got %+v err=%v", rcpt, err)
	}
}

func receiptOutcome(r *mutations.Result) mutations.Outcome {
	if r == nil {
		return "<nil>"
	}
	return r.Outcome
}

// -----------------------------------------------------------------------------
// Audit field hygiene: accepted / denied / failed carry only coarse bounded fields
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// The four-way distinguishability matrix (the phase-3 acceptance)
// -----------------------------------------------------------------------------

func TestConcurrentGuardedGrantsDeterministicFinalState(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard)
	svc, sm := comps, comps.SystemMutator
	seedOwner(t, sm, "d1", "u1")

	const n = 16
	var wg sync.WaitGroup
	outcomes := make([]mutations.Outcome, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rcpt, err := svc.Mutations.GrantRelationship(context.Background(), actorU1(), mutations.GrantRelationshipCommand{
				ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("shared"),
			})
			if err != nil {
				errs[i] = err
				return
			}
			outcomes[i] = rcpt.Outcome
		}(i)
	}
	wg.Wait()

	applied, noChange := 0, 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("guarded grant %d errored (must be a domain outcome): %v", i, errs[i])
		}
		switch outcomes[i] {
		case mutations.OutcomeApplied:
			applied++
		case mutations.OutcomeNoChange:
			noChange++
		default:
			t.Fatalf("grant %d unexpected outcome %q", i, outcomes[i])
		}
	}
	if applied != 1 || noChange != n-1 {
		t.Fatalf("deterministic final state: want 1 applied + %d no_change, got applied=%d no_change=%d", n-1, applied, noChange)
	}
	if targets, _ := svc.Relationships.GetRelationTargets(context.Background(), "doc", "d1", "viewer"); len(targets) != 1 {
		t.Fatalf("final state must have exactly one row, got %+v", targets)
	}
}

// -----------------------------------------------------------------------------
// SystemMutator honors invariant + idempotency, except the teardown exception
// -----------------------------------------------------------------------------

// newDefaultGuardianComponents builds a Components with the DEFAULT guardian policy
// (owner protected, min one direct anchor) so the trusted path's invariant honoring
// can be exercised.
func newDefaultGuardianComponents(t *testing.T) Components {
	t.Helper()
	st := memory.New(memory.WithGuardianPolicy(mutations.DefaultGuardianPolicy()))
	comps, err := New(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, WithRelationshipModel(lifecycleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// TestSystemMutatorHonorsMutationInvariantExceptTeardown proves the trusted
// SystemMutator still obeys the guardian invariant and idempotency — it bypasses only
// the host MutationGuard — while resource TEARDOWN is the one explicit, preconditioned
// exception allowed to zero a protected scope.
func TestSystemMutatorHonorsMutationInvariantExceptTeardown(t *testing.T) {
	comps := newDefaultGuardianComponents(t)
	sm := comps.SystemMutator
	ctx := context.Background()

	// Establish the sole owner (trusted bootstrap).
	if _, err := sm.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "owner", Subject: subjU("u1"),
	}); err != nil {
		t.Fatalf("establish owner: %v", err)
	}

	// Invariant honored on the trusted path: revoking the last owner is
	// invariant_blocked even for SystemMutator (it is not a guardian bypass).
	blocked, err := sm.Apply(ctx, mutations.Command{

		Target:        mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d1"},
		Operation:     mutations.OpRevoke,
		Relationships: []mutations.RelationshipRow{{Relation: "owner", Subject: subjU("u1")}},
	})
	if !errors.Is(err, mutations.ErrInvariantBlocked) || blocked != nil {
		t.Fatalf("trusted last-owner revoke: receipt=%+v err=%v", blocked, err)
	}
	if ok, _ := comps.Relationships.GetRelationTargets(ctx, "doc", "d1", "owner"); len(ok) != 1 {
		t.Fatalf("blocked trusted revoke must leave the owner in place: %+v", ok)
	}

	firstGrant, err := sm.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || firstGrant.Outcome != mutations.OutcomeApplied {
		t.Fatalf("first trusted grant: %+v err=%v", firstGrant, err)
	}
	replay, err := sm.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || replay.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("duplicate grant: %+v, %v", replay, err)
	}

	// The teardown exception: it IS allowed to zero the protected scope, with its
	// required non-empty reason. This is the sole preconditioned exception.
	torn, err := sm.TeardownResourceAuthorization(ctx, mutations.TeardownResourceAuthorizationCommand{
		ResourceType: "doc", ResourceID: "d1", Reason: "resource deleted by admin",
	})
	if err != nil || torn.Outcome != mutations.OutcomeApplied {
		t.Fatalf("teardown must apply and zero the protected scope, outcome=%v err=%v", receiptOutcome(torn), err)
	}
	if ok, _ := comps.Relationships.GetRelationTargets(ctx, "doc", "d1", "owner"); len(ok) != 0 {
		t.Fatalf("teardown must remove the protected owner, got %+v", ok)
	}

	// Teardown still requires its precondition: an empty reason is refused before any
	// write (the exception is preconditioned, not unconditional).
	if _, err := sm.TeardownResourceAuthorization(ctx, mutations.TeardownResourceAuthorizationCommand{
		ResourceType: "doc", ResourceID: "d2", Reason: "",
	}); !errors.Is(err, mutations.ErrTeardownReasonRequired) {
		t.Fatalf("teardown without a reason must be refused, got %v", err)
	}
}
