package authorization

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func newProofComponents(t *testing.T) Components {
	t.Helper()
	st := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{}))
	comps, err := New(Repositories{
		Tuples: st.Tuples(), Mutations: st.Mutations(),
	}, WithModel(lifecycleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// seedOwner trustedly grants owner on a resource (the bootstrap seam) so an actor
// can then prove manage rights through the guard.
func seedOwner(t *testing.T, sm *mutations.Service, resourceID, user string) {
	t.Helper()
	if _, err := sm.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: resourceID, Relation: "owner", Subject: subjU(user),
	}); err != nil {
		t.Fatalf("seed owner %s on %s: %v", user, resourceID, err)
	}
}

// -----------------------------------------------------------------------------
// Guard infrastructure error vs. denial (audit records failed, not denied)
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Invalid actor / invalid proposed tuple — rejected before Apply, ID unconsumed
// -----------------------------------------------------------------------------

func TestMutationInvalidProposedTupleRejectedBeforeApply(t *testing.T) {
	comps := newProofComponents(t)
	ctx := context.Background()
	seedOwner(t, comps.Mutations, "d1", "u1")

	_, err := comps.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "", Subject: subjU("u2"), // empty relation
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid proposed tuple: want invalid-input command error, got %v", err)
	}

	rcpt, err := comps.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
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

func TestConcurrentGrantsDeterministicFinalState(t *testing.T) {
	comps := newProofComponents(t)
	svc, sm := comps, comps.Mutations
	seedOwner(t, sm, "d1", "u1")

	const n = 16
	var wg sync.WaitGroup
	outcomes := make([]mutations.Outcome, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rcpt, err := svc.Mutations.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{
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

// newDefaultIntegrityComponents builds a Components with the DEFAULT integrity policy
// (owner protected, min one direct anchor) so the trusted path's invariant honoring
// can be exercised.
func newDefaultIntegrityComponents(t *testing.T) Components {
	t.Helper()
	st := memory.New(memory.WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()))
	comps, err := New(Repositories{
		Tuples: st.Tuples(), Mutations: st.Mutations(),
	}, WithModel(lifecycleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func TestTupleWriterHonorsMutationInvariantExceptTeardown(t *testing.T) {
	comps := newDefaultIntegrityComponents(t)
	sm := comps.Mutations
	ctx := context.Background()

	// Establish the sole owner (trusted bootstrap).
	if _, err := sm.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "owner", Subject: subjU("u1"),
	}); err != nil {
		t.Fatalf("establish owner: %v", err)
	}

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
