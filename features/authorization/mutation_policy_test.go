package authorization

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk"
)

// AZ3-3.5 — the service-level mutation policy / retry / stale-revision / audit
// adversarial suite. Everything here runs over the REAL memstore bundle
// (shared-state relationship + role + mutation repositories) composed with a REAL
// MutationGuard policy that reads the dependency-tracking DecisionView — the
// "proof policy" the phase-3 acceptance demands — rather than a stub. The
// repository-level races and outcomes are already pinned by storetest; these cases
// prove the SERVICE composition on top: that policy denial, command failure, a
// domain outcome, and replay metadata cannot be conflated by a caller.

// -----------------------------------------------------------------------------
// The proof policy: a manage-access guard that reads the DecisionView
// -----------------------------------------------------------------------------

// manageGuard is a realistic host MutationGuard: an actor may mutate a resource
// scope only if it holds the `owner` (manage) relation on THAT resource, read
// through the repository's dependency-tracking DecisionView (never the outer
// Service). Global (subject-scoped) mutations are refused outright — their blast
// radius belongs to a trusted holder. This is the proof policy phase 3 requires:
// a guard whose allow decision is itself authorization data, so a revoke of the
// actor's manage grant is a revision-tracked dependency the repository validates
// under lock.
//
// infraErr, when set, is returned verbatim WITHOUT wrapping sdk.ErrForbidden — the
// guard-infrastructure-failure branch, distinct from a denial: it must audit as
// `failed`, not `denied`, and must be distinguishable from a policy denial by a
// caller.
type manageGuard struct {
	mu       sync.Mutex
	seen     []MutationAttempt
	infraErr error
}

func (g *manageGuard) AuthorizeMutation(ctx context.Context, attempt MutationAttempt, view DecisionView) error {
	g.mu.Lock()
	g.seen = append(g.seen, attempt)
	infra := g.infraErr
	g.mu.Unlock()

	if infra != nil {
		return infra // NOT a denial — a coarse guard-backend failure
	}
	if attempt.Scope.Kind != ScopeResource {
		return fmt.Errorf("global mutation requires a trusted holder: %w", sdk.ErrForbidden)
	}
	ok, err := view.CheckRelation(ctx, attempt.Scope, "owner", attempt.Actor.Type, attempt.Actor.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s:%s does not hold manage(owner) on %s: %w", attempt.Actor.Type, attempt.Actor.ID, attempt.Scope, sdk.ErrForbidden)
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
func newProofComponents(t *testing.T, guard MutationGuard, sink AuditSink) Components {
	t.Helper()
	st := memstore.New(memstore.WithGuardianPolicy(mutation.GuardianPolicy{}))
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, Config{Model: lifecycleModel(), Guard: guard, Audit: sink})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// seedOwner trustedly grants owner on a resource (the bootstrap seam) so an actor
// can then prove manage rights through the guard.
func seedOwner(t *testing.T, sm *SystemMutator, resourceID, user string) {
	t.Helper()
	if _, err := sm.GrantRelationship(context.Background(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: resourceID, Relation: "owner", Subject: subjU(user),
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
	comps := newProofComponents(t, guard, nil)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1") // u1 manages d1

	// The owner may grant.
	rcpt, err := comps.Service.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || rcpt.Outcome != OutcomeApplied {
		t.Fatalf("owner grant: outcome=%v err=%v", rcpt.Outcome, err)
	}
	if targets, _ := comps.Service.GetRelationTargets(ctx, "doc", "d1", "viewer"); len(targets) != 1 {
		t.Fatalf("allowed grant did not commit its row: %+v", targets)
	}

	// A non-owner may not.
	nonOwner := Actor{PrincipalRef: PrincipalRef{Type: "user", ID: "u9"}}
	_, err = comps.Service.GrantRelationship(ctx, nonOwner, GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u3"),
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("non-owner grant: want forbidden, got %v", err)
	}
	if targets, _ := comps.Service.GetRelationTargets(ctx, "doc", "d1", "viewer"); len(targets) != 1 {
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
	comps := newProofComponents(t, guard, nil)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1") // only u1 manages d1

	escalator := Actor{PrincipalRef: PrincipalRef{Type: "user", ID: "u5"}}
	_, err := comps.Service.GrantRelationship(ctx, escalator, GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "owner", Subject: subjU("u5"),
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("self-escalation to owner: want forbidden, got %v", err)
	}
	if ok, _ := comps.Service.GetRelationTargets(ctx, "doc", "d1", "owner"); len(ok) != 1 {
		t.Fatalf("self-escalation wrote an owner row: %+v", ok)
	}

	// The same escalator cannot self-grant a lesser relation either.
	_, err = comps.Service.GrantRelationship(ctx, escalator, GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u5"),
	})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("self-grant of editor by a non-manager: want forbidden, got %v", err)
	}
}

// TestConcurrentGuardManageRevokeRaceServicePath is the service-level composition of
// the storetest guard/revoke race, with the REAL proof policy: an actor's guarded
// grant runs concurrently with a trusted revoke of that actor's own manage grant.
// The guard reads `owner u1` on the SAME scope the grant mutates, so the revoke's
// revision bump is a shared-scope dependency the repository re-validates under lock.
// Whenever the revoke wins the interleaving the guarded write is deterministically
// STALE (shared revision moved) or DENIED (guard saw u1 already un-owned) and writes
// nothing — a detached stale ALLOW may never commit. Driven over rounds under -race.
func TestConcurrentGuardManageRevokeRaceServicePath(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard, nil)
	svc, sm := comps.Service, comps.SystemMutator

	const rounds = 8
	for r := 0; r < rounds; r++ {
		res := "svcrace" + strconv.Itoa(r)
		seedOwner(t, sm, res, "u1") // u1 is the manage grant the guarded actor depends on

		// Trusted revoke of u1's own manage grant (an admin/teardown yanking access),
		// racing u1's guarded grant. The trusted path always applies (empty guardian).
		revokeU1 := Command{
			MutationID:    mustID(t),
			Scope:         ScopeKey{Kind: ScopeResource, Type: "doc", ID: res},
			Operation:     OpRevoke,
			Relationships: []RelationshipRow{{Relation: "owner", Subject: subjU("u1")}},
		}

		var wg sync.WaitGroup
		var grantRcpt, revokeRcpt *Receipt
		var grantErr, revokeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			grantRcpt, grantErr = svc.GrantRelationship(context.Background(), actorU1(), GrantRelationshipCommand{
				MutationID: mustID(t), ResourceType: "doc", ResourceID: res, Relation: "viewer", Subject: subjU("u3"),
			})
		}()
		go func() {
			defer wg.Done()
			revokeRcpt, revokeErr = sm.Apply(context.Background(), revokeU1)
		}()
		wg.Wait()

		if revokeErr != nil || revokeRcpt.Outcome != OutcomeApplied {
			t.Fatalf("round %d: trusted revoke must apply, outcome=%v err=%v", r, revokeRcpt.Outcome, revokeErr)
		}

		hasViewer, _ := svc.GetRelationTargets(context.Background(), "doc", res, "viewer")
		switch {
		case grantErr == nil:
			// The guarded write won the lock on a snapshot where u1 still owned res.
			if grantRcpt.Outcome != OutcomeApplied {
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

// TestGuardInfrastructureErrorAuditedFailed proves a guard that fails for an
// INFRASTRUCTURE reason (not a denial) is distinct from a policy denial: the error
// is NOT sdk.ErrForbidden, it commits nothing, and it audits as `failed` — never
// `denied`. Conflating the two would let a transient guard-backend outage read as a
// deliberate authorization decision.
func TestGuardInfrastructureErrorAuditedFailed(t *testing.T) {
	backendDown := errors.New("guard backend unreachable")
	guard := &manageGuard{infraErr: backendDown}
	sink := &stubAuditSink{}
	comps := newProofComponents(t, guard, sink)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1")

	_, err := comps.Service.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err == nil {
		t.Fatalf("a guard infrastructure error must surface as a command error")
	}
	if errors.Is(err, sdk.ErrForbidden) || errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("a guard infrastructure error must NOT be a denial, got %v", err)
	}
	if len(sink.events) != 1 || sink.events[0].Decision != AuditFailed {
		t.Fatalf("guard infra error must audit as failed, got %+v", sink.events)
	}
	if targets, _ := comps.Service.GetRelationTargets(ctx, "doc", "d1", "viewer"); len(targets) != 0 {
		t.Fatalf("a guard infra failure must commit nothing: %+v", targets)
	}
}

// -----------------------------------------------------------------------------
// Invalid actor / invalid proposed tuple — rejected before Apply, ID unconsumed
// -----------------------------------------------------------------------------

// TestMutationInvalidActorRejectedBeforeApply proves an empty/invalid actor is
// refused BEFORE the guard runs and before any write: no receipt, the guard is never
// consulted, and the MutationID is not consumed (reuse with a valid actor applies
// fresh, not as a replay).
func TestMutationInvalidActorRejectedBeforeApply(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard, nil)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1")

	id := mustID(t)
	_, err := comps.Service.GrantRelationship(ctx, Actor{}, GrantRelationshipCommand{
		MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty actor: want invalid-input, got %v", err)
	}
	if guard.seenCount() != 0 {
		t.Fatalf("an invalid actor must be rejected before the guard runs, saw %d attempts", guard.seenCount())
	}

	// The MutationID was not consumed: the same id with a valid actor applies fresh.
	rcpt, err := comps.Service.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || rcpt.Replayed || rcpt.Outcome != OutcomeApplied {
		t.Fatalf("post-rejection reuse must apply fresh, got %+v err=%v", rcpt, err)
	}
}

// TestMutationInvalidProposedTupleRejectedBeforeApply proves a structurally invalid
// proposed tuple (an empty relation) is refused by command validation BEFORE the
// guard and before Apply: no receipt, the guard is never consulted, and the
// MutationID is not consumed.
func TestMutationInvalidProposedTupleRejectedBeforeApply(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard, nil)
	ctx := context.Background()
	seedOwner(t, comps.SystemMutator, "d1", "u1")

	id := mustID(t)
	_, err := comps.Service.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: "", Subject: subjU("u2"), // empty relation
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid proposed tuple: want invalid-input command error, got %v", err)
	}
	if guard.seenCount() != 0 {
		t.Fatalf("a structurally invalid command must be rejected before the guard runs, saw %d", guard.seenCount())
	}

	// The MutationID was not consumed: reuse with a valid relation applies fresh.
	rcpt, err := comps.Service.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || rcpt.Replayed || rcpt.Outcome != OutcomeApplied {
		t.Fatalf("post-rejection reuse must apply fresh, got %+v err=%v", rcpt, err)
	}
}

// -----------------------------------------------------------------------------
// Stale revision: safe reload/retry; policy denial is terminal (never auto-retried)
// -----------------------------------------------------------------------------

// applyWithReload is the caller-side retry protocol these tests document and pin:
// on a STALE-revision command error, reload the current revision and retry (stale is
// retryable); on ANY other error — a policy denial in particular — return
// immediately (denial is terminal). It never re-issues a payload under one
// MutationID (each retry mints a fresh id), so a retry can never apply a different
// payload under one idempotency key (a global stop condition).
func applyWithReload(svc *Service, actor Actor, resourceID, relation, subject string, reload func() *Revision, maxTries int) (*Receipt, error, int) {
	var lastErr error
	tries := 0
	for tries < maxTries {
		tries++
		rcpt, err := svc.GrantRelationship(context.Background(), actor, GrantRelationshipCommand{
			MutationID: MutationID(deriveID(resourceID, relation, subject, tries)), ResourceType: "doc", ResourceID: resourceID,
			Relation: relation, Subject: subjU(subject), ExpectedRevision: reload(),
		})
		if err == nil {
			return rcpt, nil, tries
		}
		lastErr = err
		if !errors.Is(err, ErrStaleRevision) {
			return nil, err, tries // terminal — denial, mismatch, invalid, etc.
		}
		// stale: loop, reload() supplies a fresh expected revision
	}
	return nil, lastErr, tries
}

func deriveID(parts ...any) string {
	s := make([]string, len(parts))
	for i, p := range parts {
		s[i] = fmt.Sprint(p)
	}
	return string(DeriveMutationID(s...))
}

// receiptOutcome is a nil-safe outcome reader for failure messages.
func receiptOutcome(r *Receipt) Outcome {
	if r == nil {
		return "<nil>"
	}
	return r.Outcome
}

// TestStaleRevisionReloadRetryProtocol proves the safe reload/retry rule: a grant
// whose expected revision has gone stale returns ErrStaleRevision, and reloading the
// current revision and retrying succeeds. The stale error is distinguishable from a
// denial by errors.Is, which is what makes an automatic retry safe.
func TestStaleRevisionReloadRetryProtocol(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard, nil)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()
	seedOwner(t, sm, "d1", "u1")

	// Establish a known revision, then move it so a captured revision is stale.
	first, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil {
		t.Fatalf("first grant: %v", err)
	}
	stale := first.Revision
	bumped, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u3"),
	})
	if err != nil {
		t.Fatalf("bumping grant: %v", err)
	}
	current := bumped.Revision

	// A grant carrying the now-stale revision is rejected as a command error.
	_, err = svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u4"),
		ExpectedRevision: &stale,
	})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale expected revision: want ErrStaleRevision, got %v", err)
	}
	if errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("a stale-revision error must not be conflated with a denial")
	}

	// Reload and retry with the current revision: it applies.
	ok, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u4"),
		ExpectedRevision: &current,
	})
	if err != nil || ok.Outcome != OutcomeApplied {
		t.Fatalf("reload+retry at the current revision must apply, outcome=%v err=%v", ok.Outcome, err)
	}
}

// TestStaleRevisionRetryLoopConvergesButDenialTerminal proves the two halves of the
// caller rule with the retry protocol itself: (1) under a concurrently moving scope
// the reload/retry loop converges (an allowed actor eventually commits), and (2) a
// policy denial is NEVER auto-retried — the loop returns after exactly one attempt,
// because the denial error is not a stale error.
func TestStaleRevisionRetryLoopConvergesButDenialTerminal(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard, nil)
	svc, sm := comps.Service, comps.SystemMutator
	seedOwner(t, sm, "d1", "u1")

	// (1) An allowed actor with a reload closure that always supplies the true
	// current revision converges immediately.
	reload := func() *Revision { return nil } // nil = no precondition; unconditional apply
	rcpt, err, tries := applyWithReload(svc, actorU1(), "d1", "viewer", "u2", reload, 5)
	if err != nil || rcpt.Outcome != OutcomeApplied {
		t.Fatalf("reload/retry must converge for an allowed actor: outcome=%v err=%v tries=%d", receiptOutcome(rcpt), err, tries)
	}

	// (2) A denied actor is terminal: the loop retries ONLY on stale, so a denial
	// returns after exactly one attempt — no auto-retry of a policy denial.
	nonOwner := Actor{PrincipalRef: PrincipalRef{Type: "user", ID: "u9"}}
	before := guard.seenCount()
	_, derr, dtries := applyWithReload(svc, nonOwner, "d1", "viewer", "u7", reload, 5)
	if !errors.Is(derr, sdk.ErrForbidden) {
		t.Fatalf("denied actor: want forbidden, got %v", derr)
	}
	if dtries != 1 {
		t.Fatalf("a policy denial must not be auto-retried: took %d tries", dtries)
	}
	if guard.seenCount()-before != 1 {
		t.Fatalf("a denial must consult the guard exactly once, saw %d", guard.seenCount()-before)
	}
}

// -----------------------------------------------------------------------------
// Audit field hygiene: accepted / denied / failed carry only coarse bounded fields
// -----------------------------------------------------------------------------

// TestAuditFieldHygieneAcceptedDeniedFailed drives one accepted, one denied, and one
// failed (stale) actor-facing attempt through a wired sink and asserts the captured
// AuditEvent for each carries only coarse, bounded fields: the opaque MutationID, the
// operation, the scope, the actor, a coarse Decision, and — at most — a stable Reason
// code from the frozen set. No raw guard error string, no proposed-change payload,
// no free-form Detail (that is teardown-only). A reflection check pins that
// AuditEvent structurally cannot carry an error string or an unbounded change payload.
func TestAuditFieldHygieneAcceptedDeniedFailed(t *testing.T) {
	guard := &manageGuard{}
	sink := &stubAuditSink{}
	comps := newProofComponents(t, guard, sink)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()
	seedOwner(t, sm, "d1", "u1")

	// accepted
	if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	}); err != nil {
		t.Fatalf("accepted grant: %v", err)
	}
	// denied (non-owner)
	nonOwner := Actor{PrincipalRef: PrincipalRef{Type: "user", ID: "u9"}}
	if _, err := svc.GrantRelationship(ctx, nonOwner, GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u3"),
	}); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("denied grant: want forbidden, got %v", err)
	}
	// failed (stale revision)
	stale := Revision(999)
	if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u4"),
		ExpectedRevision: &stale,
	}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("failed grant: want stale, got %v", err)
	}

	if len(sink.events) != 3 {
		t.Fatalf("want three audit events, got %d: %+v", len(sink.events), sink.events)
	}
	byDecision := map[AuditDecision]AuditEvent{}
	for _, e := range sink.events {
		byDecision[e.Decision] = e
	}

	acc, ok := byDecision[AuditAccepted]
	if !ok {
		t.Fatalf("missing accepted event: %+v", sink.events)
	}
	if acc.Outcome != OutcomeApplied || acc.Operation != OpGrant || acc.Detail != "" || acc.Reason != "" {
		t.Fatalf("accepted event carries unexpected fields: %+v", acc)
	}

	den, ok := byDecision[AuditDenied]
	if !ok {
		t.Fatalf("missing denied event: %+v", sink.events)
	}
	if den.Outcome != "" || den.Detail != "" || !boundedReason(den.Reason) {
		t.Fatalf("denied event must carry no outcome/detail and only a bounded reason: %+v", den)
	}

	fail, ok := byDecision[AuditFailed]
	if !ok {
		t.Fatalf("missing failed event: %+v", sink.events)
	}
	if fail.Reason != ReasonStaleRevision || fail.Detail != "" || fail.Outcome != "" {
		t.Fatalf("failed event must carry a stable reason code and no raw payload: %+v", fail)
	}

	// Every event names only opaque identifiers already known to the caller — the
	// MutationID and actor/scope — never a raw error string. Structurally pin that
	// AuditEvent cannot carry an error or an unbounded change payload.
	et := reflect.TypeOf(AuditEvent{})
	for i := 0; i < et.NumField(); i++ {
		name := et.Field(i).Name
		switch name {
		case "Change", "Relationships", "Roles", "Payload", "Error", "Err", "Message", "Cause":
			t.Fatalf("AuditEvent.%s could carry an unbounded payload or a raw error string", name)
		}
		if et.Field(i).Type == reflect.TypeOf((*error)(nil)).Elem() {
			t.Fatalf("AuditEvent.%s is an error field — a raw error must never ride the audit event", name)
		}
	}
}

// boundedReason reports whether r is empty or one of the frozen, bounded Reason
// codes — never an arbitrary free-form string.
func boundedReason(r Reason) bool {
	switch r {
	case "", ReasonGranted, ReasonInvalidRequest, ReasonUnknownSymbol, ReasonDenied,
		ReasonEvaluationLimit, ReasonStaleRevision, ReasonInvariantConflict,
		ReasonMutationMismatch, ReasonInfrastructure:
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// The four-way distinguishability matrix (the phase-3 acceptance)
// -----------------------------------------------------------------------------

type mutationClass int

const (
	classPolicyDenial mutationClass = iota
	classCommandFailure
	classDomainOutcome
	classReplay
)

func (c mutationClass) String() string {
	switch c {
	case classPolicyDenial:
		return "policy_denial"
	case classCommandFailure:
		return "command_failure"
	case classDomainOutcome:
		return "domain_outcome"
	case classReplay:
		return "replay"
	default:
		return "unknown"
	}
}

// classifyMutation is the CALLER-side classifier the acceptance demands: from
// exactly (receipt, err) a caller must be able to tell a policy denial, a command
// failure, a domain outcome, and replay metadata apart, with no ambiguity. Denial is
// an authorization error (forbidden/unauthorized); any other error is a command
// failure; a nil error with Replayed is replay metadata; a nil error otherwise is a
// domain outcome (whose Receipt.Outcome the caller then reads).
func classifyMutation(rcpt *Receipt, err error) mutationClass {
	switch {
	case err != nil && (errors.Is(err, sdk.ErrForbidden) || errors.Is(err, sdk.ErrUnauthorized)):
		return classPolicyDenial
	case err != nil:
		return classCommandFailure
	case rcpt != nil && rcpt.Replayed:
		return classReplay
	default:
		return classDomainOutcome
	}
}

// TestMutationDenialFailureOutcomeReplayDistinguishable is the phase-3 acceptance
// table: policy denial, command failure, a domain outcome, and replay metadata each
// arise from the real service and each classify into their own bucket. Critically it
// pins the confusions the packet forbids — a domain outcome (semantic_conflict,
// not_found) is NOT a command failure and NOT a denial; a replay is orthogonal to the
// outcome it carries; a stale/mismatch command failure is NOT a denial.
func TestMutationDenialFailureOutcomeReplayDistinguishable(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard, nil)
	svc, sm := comps.Service, comps.SystemMutator
	ctx := context.Background()
	seedOwner(t, sm, "d1", "u1")

	// A helper to run a fresh guarded grant.
	grant := func(actor Actor, id MutationID, relation, subject string, exp *Revision) (*Receipt, error) {
		return svc.GrantRelationship(ctx, actor, GrantRelationshipCommand{
			MutationID: id, ResourceType: "doc", ResourceID: "d1", Relation: relation, Subject: subjU(subject), ExpectedRevision: exp,
		})
	}

	nonOwner := Actor{PrincipalRef: PrincipalRef{Type: "user", ID: "u9"}}

	// -- policy denial --
	_, derr := grant(nonOwner, mustID(t), "viewer", "z1", nil)
	if got := classifyMutation(nil, derr); got != classPolicyDenial {
		t.Fatalf("denial classified as %s", got)
	}

	// -- command failure: stale revision --
	applied, err := grant(actorU1(), mustID(t), "viewer", "z2", nil)
	if err != nil {
		t.Fatalf("seed for stale: %v", err)
	}
	staleRev := applied.Revision - 1
	_, serr := grant(actorU1(), mustID(t), "viewer", "z3", &staleRev)
	if got := classifyMutation(nil, serr); got != classCommandFailure {
		t.Fatalf("stale revision classified as %s (err=%v)", got, serr)
	}
	if errors.Is(serr, sdk.ErrForbidden) {
		t.Fatalf("a command failure must not be conflated with a denial")
	}

	// -- command failure: MutationID payload mismatch --
	reuse := mustID(t)
	if _, err := grant(actorU1(), reuse, "viewer", "z4", nil); err != nil {
		t.Fatalf("seed for mismatch: %v", err)
	}
	_, merr := grant(actorU1(), reuse, "editor", "z5", nil) // same id, different payload
	if got := classifyMutation(nil, merr); got != classCommandFailure {
		t.Fatalf("payload mismatch classified as %s (err=%v)", got, merr)
	}
	if !errors.Is(merr, ErrMutationMismatch) {
		t.Fatalf("payload mismatch must be the mismatch sentinel, got %v", merr)
	}

	// -- domain outcome: semantic_conflict (a different relation, no replace) --
	if _, err := grant(actorU1(), mustID(t), "viewer", "z6", nil); err != nil {
		t.Fatalf("seed for conflict: %v", err)
	}
	confl, cerr := grant(actorU1(), mustID(t), "editor", "z6", nil)
	if got := classifyMutation(confl, cerr); got != classDomainOutcome {
		t.Fatalf("semantic conflict classified as %s (err=%v)", got, cerr)
	}
	if confl.Outcome != OutcomeSemanticConflict {
		t.Fatalf("expected a semantic_conflict domain outcome, got %+v", confl)
	}

	// -- domain outcome: not_found (revoke absent) — still a nil-error domain outcome --
	nf, nferr := svc.RevokeRelationship(ctx, actorU1(), RevokeRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("absent"),
	})
	if got := classifyMutation(nf, nferr); got != classDomainOutcome || nf.Outcome != OutcomeNotFound {
		t.Fatalf("absent revoke must be a not_found domain outcome, got class=%s outcome=%v err=%v", classifyMutation(nf, nferr), receiptOutcome(nf), nferr)
	}

	// -- replay metadata: orthogonal to the outcome it carries --
	rid := mustID(t)
	firstApply, err := grant(actorU1(), rid, "viewer", "z7", nil)
	if err != nil || firstApply.Replayed {
		t.Fatalf("first apply for replay: replayed=%v err=%v", firstApply.Replayed, err)
	}
	if got := classifyMutation(firstApply, nil); got != classDomainOutcome {
		t.Fatalf("first application must be a domain outcome, got %s", got)
	}
	replay, err := grant(actorU1(), rid, "viewer", "z7", nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := classifyMutation(replay, nil); got != classReplay {
		t.Fatalf("replay must classify as replay, got %s", got)
	}
	// Replay is orthogonal to outcome — the caller can still read the carried outcome.
	if replay.Outcome != firstApply.Outcome {
		t.Fatalf("replay must carry the original outcome (replay != outcome): %v vs %v", replay.Outcome, firstApply.Outcome)
	}
}

// -----------------------------------------------------------------------------
// Concurrent grant with deterministic final state (service path)
// -----------------------------------------------------------------------------

// TestConcurrentGuardedGrantsDeterministicFinalState races many guarded grants of the
// SAME tuple (distinct MutationIDs) from an authorized actor. Whatever the
// interleaving, the final state is deterministic: exactly one row exists, exactly one
// call is a first OutcomeApplied, and every other call is an idempotent no_change —
// never a duplicate row and never a conflict. The repository-level grant/revoke/replace
// determinism under concurrency is proven by storetest's ConcurrentTwoOwnerRevokeRounds
// and ConcurrentReplaceNoAbsentState; this pins the composed guarded service on top.
func TestConcurrentGuardedGrantsDeterministicFinalState(t *testing.T) {
	guard := &manageGuard{}
	comps := newProofComponents(t, guard, nil)
	svc, sm := comps.Service, comps.SystemMutator
	seedOwner(t, sm, "d1", "u1")

	const n = 16
	var wg sync.WaitGroup
	outcomes := make([]Outcome, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rcpt, err := svc.GrantRelationship(context.Background(), actorU1(), GrantRelationshipCommand{
				MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("shared"),
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
		case OutcomeApplied:
			applied++
		case OutcomeNoChange:
			noChange++
		default:
			t.Fatalf("grant %d unexpected outcome %q", i, outcomes[i])
		}
	}
	if applied != 1 || noChange != n-1 {
		t.Fatalf("deterministic final state: want 1 applied + %d no_change, got applied=%d no_change=%d", n-1, applied, noChange)
	}
	if targets, _ := svc.GetRelationTargets(context.Background(), "doc", "d1", "viewer"); len(targets) != 1 {
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
	st := memstore.New() // default guardian policy
	comps, err := NewService(Repositories{
		Relationships: st.Relationships(),
		Roles:         st.Roles(),
		Mutations:     st.Mutations(),
	}, Config{Model: lifecycleModel()})
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
	if _, err := sm.GrantRelationship(ctx, GrantRelationshipCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Relation: "owner", Subject: subjU("u1"),
	}); err != nil {
		t.Fatalf("establish owner: %v", err)
	}

	// Invariant honored on the trusted path: revoking the last owner is
	// invariant_blocked even for SystemMutator (it is not a guardian bypass).
	blocked, err := sm.Apply(ctx, Command{
		MutationID:    mustID(t),
		Scope:         ScopeKey{Kind: ScopeResource, Type: "doc", ID: "d1"},
		Operation:     OpRevoke,
		Relationships: []RelationshipRow{{Relation: "owner", Subject: subjU("u1")}},
	})
	if err != nil {
		t.Fatalf("trusted last-owner revoke must be a domain outcome, got err %v", err)
	}
	if blocked.Outcome != OutcomeInvariantBlocked {
		t.Fatalf("trusted last-owner revoke must be invariant_blocked, got %q", blocked.Outcome)
	}
	if ok, _ := comps.Service.GetRelationTargets(ctx, "doc", "d1", "owner"); len(ok) != 1 {
		t.Fatalf("blocked trusted revoke must leave the owner in place: %+v", ok)
	}

	// Idempotency honored on the trusted path: a stable MutationID replays without a
	// duplicate bump.
	stableID := DeriveMutationID("bootstrap-grant", "doc", "d1", "viewer", "user", "u2")
	firstGrant, err := sm.GrantRelationship(ctx, GrantRelationshipCommand{
		MutationID: stableID, ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || firstGrant.Replayed {
		t.Fatalf("first trusted grant: replayed=%v err=%v", firstGrant.Replayed, err)
	}
	replay, err := sm.GrantRelationship(ctx, GrantRelationshipCommand{
		MutationID: stableID, ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2"),
	})
	if err != nil || !replay.Replayed || replay.Revision != firstGrant.Revision {
		t.Fatalf("trusted replay must not bump: replayed=%v rev(first=%d replay=%d) err=%v", replay.Replayed, firstGrant.Revision, replay.Revision, err)
	}

	// The teardown exception: it IS allowed to zero the protected scope, with its
	// required non-empty reason. This is the sole preconditioned exception.
	torn, err := sm.TeardownAuthorizationScope(ctx, TeardownAuthorizationScopeCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d1", Reason: "resource deleted by admin",
	})
	if err != nil || torn.Outcome != OutcomeApplied {
		t.Fatalf("teardown must apply and zero the protected scope, outcome=%v err=%v", receiptOutcome(torn), err)
	}
	if ok, _ := comps.Service.GetRelationTargets(ctx, "doc", "d1", "owner"); len(ok) != 0 {
		t.Fatalf("teardown must remove the protected owner, got %+v", ok)
	}

	// Teardown still requires its precondition: an empty reason is refused before any
	// write (the exception is preconditioned, not unconditional).
	if _, err := sm.TeardownAuthorizationScope(ctx, TeardownAuthorizationScopeCommand{
		MutationID: mustID(t), ResourceType: "doc", ResourceID: "d2", Reason: "",
	}); !errors.Is(err, ErrTeardownReasonRequired) {
		t.Fatalf("teardown without a reason must be refused, got %v", err)
	}
}
