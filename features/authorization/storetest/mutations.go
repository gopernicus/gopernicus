package storetest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// runMutations is the reference specification for the atomic write contract
// (mutation.MutationRepository, AZ3-0.4). It is skipped when the Mutations port is
// not wired (deny-by-absence, matching the other kinds) and executed for real by
// the phase-2 store adapters (AZ3-2.2/2.3/2.4). Every case asserts a property the
// port doc comments make normative and that a detached read/check/write
// implementation cannot satisfy.
func runMutations(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	t.Run("ExactReplayReturnsOriginalReceipt", func(t *testing.T) {
		specExactReplay(t, newRepos)
	})
	t.Run("MutationIDPayloadMismatchChangesNothing", func(t *testing.T) {
		specPayloadMismatch(t, newRepos)
	})
	t.Run("StaleRevisionRejected", func(t *testing.T) {
		specStaleRevision(t, newRepos)
	})
	t.Run("RollbackLeavesNoTrace", func(t *testing.T) {
		specRollback(t, newRepos)
	})
	t.Run("NoPartialBatch", func(t *testing.T) {
		specNoPartialBatch(t, newRepos)
	})
	t.Run("ConcurrentSingleWinner", func(t *testing.T) {
		specConcurrentSingleWinner(t, newRepos)
	})
	t.Run("GrantRevokeReplaceRevisions", func(t *testing.T) {
		specGrantRevokeReplaceRevisions(t, newRepos)
	})
	t.Run("PurgeBlockedTeardownClears", func(t *testing.T) {
		specPurgeTeardown(t, newRepos)
	})
	t.Run("RoleAssignUnassignScopes", func(t *testing.T) {
		specRoleScopes(t, newRepos)
	})
	t.Run("ExpectedRevisionAndNoOp", func(t *testing.T) {
		specExpectedRevisionNoOp(t, newRepos)
	})
	t.Run("ReplayAfterSchemaChange", func(t *testing.T) {
		specReplayAfterSchemaChange(t, newRepos)
	})
	t.Run("GuardianEstablishesMinimum", func(t *testing.T) {
		specGuardianEstablish(t, newRepos)
	})
	t.Run("LastOwnerGuardianScenarios", func(t *testing.T) {
		specLastOwnerGuardianScenarios(t, newRepos)
	})
	t.Run("GuardedViewReadsAndDenies", func(t *testing.T) {
		specGuardedView(t, newRepos)
	})
	t.Run("ConcurrentReplayStorm", func(t *testing.T) {
		specReplayStorm(t, newRepos)
	})
	t.Run("ConcurrentStaleWriterStorm", func(t *testing.T) {
		specStaleWriterStorm(t, newRepos)
	})
	t.Run("ConcurrentMixedKindScopedMutation", func(t *testing.T) {
		specMixedKind(t, newRepos)
	})
	t.Run("ConcurrentGuardRevokeRacesGuardedMutation", func(t *testing.T) {
		specGuardRevokeRace(t, newRepos)
	})
	t.Run("ConcurrentCrossScopeGuardRevokeRacesGuardedMutation", func(t *testing.T) {
		specCrossScopeGuardRevokeRace(t, newRepos)
	})
	t.Run("ConcurrentTwoOwnerRevokeRounds", func(t *testing.T) {
		specTwoOwnerRevokeRounds(t, newRepos)
	})
	t.Run("ConcurrentReplaceNoAbsentState", func(t *testing.T) {
		specReplaceNoAbsentState(t, newRepos)
	})
	t.Run("ConcurrentReceiptRevisionForensics", func(t *testing.T) {
		specReceiptRevisionForensics(t, newRepos)
	})
	t.Run("CrossScopeBatchRejectedNoStateChange", func(t *testing.T) {
		specCrossScopeBatchRejected(t, newRepos)
	})
	t.Run("ContextCancellationNoStateChange", func(t *testing.T) {
		specContextCancellation(t, newRepos)
	})
	t.Run("IntraCommandDuplicateSubjectRejected", func(t *testing.T) {
		specIntraCommandDuplicateSubject(t, newRepos)
	})
}

func revoke(id mutation.MutationID, resourceID, relation, subjectID string) mutation.Command {
	return mutation.Command{
		MutationID:    id,
		Scope:         resScope(resourceID),
		Operation:     mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: relationship.SubjectRef{Type: "user", ID: subjectID}}},
	}
}

func replace(id mutation.MutationID, resourceID, relation, subjectID string) mutation.Command {
	return mutation.Command{
		MutationID:    id,
		Scope:         resScope(resourceID),
		Operation:     mutation.OpReplace,
		Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: relationship.SubjectRef{Type: "user", ID: subjectID}}},
	}
}

func mustID(t *testing.T) mutation.MutationID {
	t.Helper()
	id, err := mutation.NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return id
}

func resScope(id string) mutation.ScopeKey {
	return mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: id}
}

func grant(id mutation.MutationID, resourceID, relation, subjectID string) mutation.Command {
	return mutation.Command{
		MutationID:    id,
		Scope:         resScope(resourceID),
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: relationship.SubjectRef{Type: "user", ID: subjectID}}},
	}
}

func mustApply(t *testing.T, m mutation.MutationRepository, cmd mutation.Command) *mutation.Receipt {
	t.Helper()
	rcpt, err := m.Apply(context.Background(), cmd, nil)
	if err != nil {
		t.Fatalf("Apply(%s): %v", cmd.Operation, err)
	}
	if rcpt == nil {
		t.Fatalf("Apply(%s) returned (nil, nil) — a domain outcome must carry a receipt", cmd.Operation)
	}
	return rcpt
}

// specExactReplay: applying the same command twice returns the original receipt
// with Replayed=true, no revision bump, and one committed change.
func specExactReplay(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	cmd := grant(mustID(t), "d1", "owner", "u1")

	first := mustApply(t, m, cmd)
	if first.Outcome != mutation.OutcomeApplied {
		t.Fatalf("first apply outcome = %q, want applied", first.Outcome)
	}
	if first.Replayed {
		t.Fatalf("first apply must not be a replay")
	}

	second := mustApply(t, m, cmd)
	if !second.Replayed {
		t.Fatalf("second apply of the same command must be a replay")
	}
	if second.Outcome != first.Outcome || second.Revision != first.Revision {
		t.Fatalf("replay must return the original outcome/revision: first=%+v second=%+v", first, second)
	}
	if second.PayloadDigest != first.PayloadDigest || second.SchemaDigest != first.SchemaDigest {
		t.Fatalf("replay must return the original digests")
	}
}

// specPayloadMismatch: a second command reusing the MutationID with a different
// payload is the stable mismatch command error and changes nothing.
func specPayloadMismatch(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	id := mustID(t)
	mustApply(t, m, grant(id, "d1", "owner", "u1"))

	reuse := grant(id, "d1", "member", "u2") // same id, different payload
	rcpt, err := m.Apply(context.Background(), reuse, nil)
	if err == nil {
		t.Fatalf("payload reuse must be a command error, got receipt %+v", rcpt)
	}
	if rcpt != nil {
		t.Fatalf("payload mismatch must return no receipt")
	}
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("mismatch must wrap sdk.ErrConflict, got %v", err)
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(context.Background(), "doc", "d1", "member", "user", "u2"); ok {
			t.Fatalf("a mismatched-payload command must not have written its row")
		}
	}
}

// specStaleRevision: an expected revision that no longer matches is rejected and
// changes nothing.
func specStaleRevision(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	seed := mustApply(t, m, grant(mustID(t), "d1", "owner", "u1"))

	stale := seed.Revision // this revision is already consumed
	cmd := grant(mustID(t), "d1", "viewer", "u2")
	cmd.ExpectedRevision = &stale
	// Advance the scope so the expected revision is stale.
	mustApply(t, m, grant(mustID(t), "d1", "editor", "u3"))

	rcpt, err := m.Apply(context.Background(), cmd, nil)
	if err == nil {
		t.Fatalf("stale expected revision must be a command error, got receipt %+v", rcpt)
	}
	if rcpt != nil {
		t.Fatalf("stale revision must return no receipt")
	}
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale revision must wrap sdk.ErrConflict, got %v", err)
	}
}

// specRollback: an invariant-blocked command changes nothing, persists no
// receipt, and leaves the guardian in place; a retry re-evaluates.
func specRollback(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	mustApply(t, m, grant(mustID(t), "d1", "owner", "u1")) // sole owner

	revoke := mutation.Command{
		MutationID:    mustID(t),
		Scope:         resScope("d1"),
		Operation:     mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}}},
	}
	rcpt, err := m.Apply(context.Background(), revoke, nil)
	if err != nil {
		t.Fatalf("a blocked invariant is a domain outcome, not an error: %v", err)
	}
	if rcpt == nil || rcpt.Outcome != mutation.OutcomeInvariantBlocked {
		t.Fatalf("removing the last owner must be OutcomeInvariantBlocked, got %+v", rcpt)
	}
	if rcpt.Outcome.Persisted() {
		t.Fatalf("invariant_blocked must not persist a receipt")
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(context.Background(), "doc", "d1", "owner", "user", "u1"); !ok {
			t.Fatalf("blocked revoke must leave the sole owner in place")
		}
	}
}

// specNoPartialBatch: a multi-row grant where one row conflicts applies NONE of
// its rows.
func specNoPartialBatch(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	mustApply(t, m, grant(mustID(t), "d1", "owner", "u1"))

	// u1 already holds owner; a batch that grants u1 a DIFFERENT relation plus a
	// clean row for u2 must roll back entirely under the one-relation rule.
	batch := mutation.Command{
		MutationID: mustID(t),
		Scope:      resScope("d1"),
		Operation:  mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{
			{Relation: "viewer", Subject: relationship.SubjectRef{Type: "user", ID: "u2"}},
			{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "u1"}}, // conflicts
		},
	}
	rcpt, err := m.Apply(context.Background(), batch, nil)
	if err != nil {
		t.Fatalf("a semantic conflict is a domain outcome, not an error: %v", err)
	}
	if rcpt == nil || rcpt.Outcome != mutation.OutcomeSemanticConflict {
		t.Fatalf("conflicting batch must be OutcomeSemanticConflict, got %+v", rcpt)
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(context.Background(), "doc", "d1", "viewer", "user", "u2"); ok {
			t.Fatalf("no row may commit when the batch conflicts (u2's clean row leaked)")
		}
	}
}

// specConcurrentSingleWinner: two goroutines each revoke a different one of the
// two owners; exactly one commits and one owner always remains.
func specConcurrentSingleWinner(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	mustApply(t, m, grant(mustID(t), "d1", "owner", "u1"))
	mustApply(t, m, grant(mustID(t), "d1", "owner", "u2"))

	revokeOwner := func(user string) mutation.Command {
		return mutation.Command{
			MutationID:    mustID(t),
			Scope:         resScope("d1"),
			Operation:     mutation.OpRevoke,
			Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: user}}},
		}
	}

	var wg sync.WaitGroup
	outcomes := make([]mutation.Outcome, 2)
	users := []string{"u1", "u2"}
	for i := range users {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rcpt, err := m.Apply(context.Background(), revokeOwner(users[i]), nil)
			if err != nil {
				t.Errorf("revoke %s errored (must be a domain outcome): %v", users[i], err)
				return
			}
			outcomes[i] = rcpt.Outcome
		}(i)
	}
	wg.Wait()

	applied := 0
	blocked := 0
	for _, o := range outcomes {
		switch o {
		case mutation.OutcomeApplied:
			applied++
		case mutation.OutcomeInvariantBlocked:
			blocked++
		}
	}
	if applied != 1 || blocked != 1 {
		t.Fatalf("exactly one owner-revoke must win: applied=%d blocked=%d (outcomes=%v)", applied, blocked, outcomes)
	}
	if repos.Relationships != nil {
		if n, _ := repos.Relationships.CountByResourceAndRelation(context.Background(), "doc", "d1", "owner"); n != 1 {
			t.Fatalf("exactly one owner must remain, got %d", n)
		}
	}
}

// specGrantRevokeReplaceRevisions exercises grant/revoke/replace mechanics and
// proves the scope revision increments exactly once per applied change and never
// on a no-op (duplicate) or a not_found (absent revoke).
func specGrantRevokeReplaceRevisions(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	m := repos.Mutations

	own := mustApply(t, m, grant(mustID(t), "d1", "owner", "u1"))
	if own.Outcome != mutation.OutcomeApplied || own.Revision != 1 {
		t.Fatalf("establishing owner grant: outcome=%q revision=%d, want applied/1", own.Outcome, own.Revision)
	}
	view := mustApply(t, m, grant(mustID(t), "d1", "viewer", "u2"))
	if view.Outcome != mutation.OutcomeApplied || view.Revision != 2 {
		t.Fatalf("viewer grant: outcome=%q revision=%d, want applied/2", view.Outcome, view.Revision)
	}

	dup := mustApply(t, m, grant(mustID(t), "d1", "viewer", "u2")) // exact duplicate
	if dup.Outcome != mutation.OutcomeNoChange || dup.Revision != 2 {
		t.Fatalf("duplicate grant must be no_change with no bump: outcome=%q revision=%d", dup.Outcome, dup.Revision)
	}

	repl := mustApply(t, m, replace(mustID(t), "d1", "editor", "u2"))
	if repl.Outcome != mutation.OutcomeApplied || repl.Revision != 3 {
		t.Fatalf("replace viewer→editor: outcome=%q revision=%d, want applied/3", repl.Outcome, repl.Revision)
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "editor", "user", "u2"); !ok {
			t.Fatalf("replace must leave u2 as editor")
		}
		if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "viewer", "user", "u2"); ok {
			t.Fatalf("replace must remove u2's prior viewer relation with no gap")
		}
	}

	rev := mustApply(t, m, revoke(mustID(t), "d1", "editor", "u2"))
	if rev.Outcome != mutation.OutcomeApplied || rev.Revision != 4 {
		t.Fatalf("revoke editor: outcome=%q revision=%d, want applied/4", rev.Outcome, rev.Revision)
	}
	absent := mustApply(t, m, revoke(mustID(t), "d1", "editor", "u2")) // already gone
	if absent.Outcome != mutation.OutcomeNotFound || absent.Revision != 4 {
		t.Fatalf("revoke of an absent row must be not_found with no bump: outcome=%q revision=%d", absent.Outcome, absent.Revision)
	}
}

// specPurgeTeardown proves ordinary purge cannot orphan a protected resource
// (invariant_blocked) while teardown is the one operation allowed to zero it.
func specPurgeTeardown(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	m := repos.Mutations
	mustApply(t, m, grant(mustID(t), "d1", "owner", "u1"))
	mustApply(t, m, grant(mustID(t), "d1", "viewer", "u2"))

	purge := mutation.Command{MutationID: mustID(t), Scope: resScope("d1"), Operation: mutation.OpPurge}
	blocked := mustApply(t, m, purge)
	if blocked.Outcome != mutation.OutcomeInvariantBlocked {
		t.Fatalf("ordinary purge of a protected resource must be invariant_blocked, got %q", blocked.Outcome)
	}
	if blocked.Outcome.Persisted() {
		t.Fatalf("invariant_blocked purge must not persist a receipt")
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1"); !ok {
			t.Fatalf("blocked purge must leave the owner in place")
		}
	}

	teardown := mutation.Command{MutationID: mustID(t), Scope: resScope("d1"), Operation: mutation.OpTeardown}
	cleared := mustApply(t, m, teardown)
	if cleared.Outcome != mutation.OutcomeApplied {
		t.Fatalf("teardown must clear the protected resource, got %q", cleared.Outcome)
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1"); ok {
			t.Fatalf("teardown must remove the owner")
		}
		if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "viewer", "user", "u2"); ok {
			t.Fatalf("teardown must remove every relationship")
		}
	}
}

// specRoleScopes proves scoped and global role assign/unassign mechanics and that
// resource-scoped and subject-scoped role mutations advance independent revisions.
func specRoleScopes(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	m := repos.Mutations

	scopedAssign := mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "editor"}},
	}
	sa := mustApply(t, m, scopedAssign)
	if sa.Outcome != mutation.OutcomeApplied || sa.Revision != 1 {
		t.Fatalf("scoped assign: outcome=%q revision=%d, want applied/1", sa.Outcome, sa.Revision)
	}
	if repos.Roles != nil {
		if ok, _ := repos.Roles.HasExactRole(ctx, "user", "u1", "editor", "doc", "d1"); !ok {
			t.Fatalf("scoped assign must be visible at the exact scope")
		}
	}

	globalAssign := mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "u1"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "admin"}},
	}
	ga := mustApply(t, m, globalAssign)
	if ga.Outcome != mutation.OutcomeApplied || ga.Revision != 1 {
		t.Fatalf("global assign advances its own subject scope: outcome=%q revision=%d, want applied/1", ga.Outcome, ga.Revision)
	}
	if repos.Roles != nil {
		if ok, _ := repos.Roles.HasExactRole(ctx, "user", "u1", "admin", "", ""); !ok {
			t.Fatalf("global assign must be visible at the global scope")
		}
	}

	scopedUnassign := mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:  mutation.OpRoleUnassign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "editor"}},
	}
	su := mustApply(t, m, scopedUnassign)
	if su.Outcome != mutation.OutcomeApplied || su.Revision != 2 {
		t.Fatalf("scoped unassign: outcome=%q revision=%d, want applied/2", su.Outcome, su.Revision)
	}
	// same_role_grant_remains (AZ3-3.3), computed inside Apply's atomic critical
	// section: u1's only global role is "admin", so removing the scoped "editor"
	// leaves no equivalent grant for that exact role.
	if su.SameRoleGrantRemains {
		t.Fatalf("scoped unassign with no matching global grant must report SameRoleGrantRemains=false")
	}
	absent := mustApply(t, m, scopedUnassign2(mustID(t)))
	if absent.Outcome != mutation.OutcomeNotFound {
		t.Fatalf("unassign of an absent role must be not_found, got %q", absent.Outcome)
	}

	// Now the true case: a GLOBAL "reviewer" grant plus a scoped one; unassigning the
	// scoped row must report SameRoleGrantRemains=true across every dialect, because
	// the global grant still satisfies the exact role via the scoped HasRole fallback.
	mustApply(t, m, mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "u1"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "reviewer"}},
	})
	mustApply(t, m, mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "reviewer"}},
	})
	remains := mustApply(t, m, mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:  mutation.OpRoleUnassign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "reviewer"}},
	})
	if remains.Outcome != mutation.OutcomeApplied || !remains.SameRoleGrantRemains {
		t.Fatalf("scoped unassign with a surviving global grant must report SameRoleGrantRemains=true, got outcome=%q remains=%v", remains.Outcome, remains.SameRoleGrantRemains)
	}
	if repos.Roles != nil {
		if ok, _ := repos.Roles.HasExactRole(ctx, "user", "u1", "reviewer", "doc", "d1"); ok {
			t.Fatalf("the scoped reviewer row must be gone after unassign")
		}
		if ok, _ := repos.Roles.HasExactRole(ctx, "user", "u1", "reviewer", "", ""); !ok {
			t.Fatalf("the global reviewer grant must remain")
		}
	}
}

func scopedUnassign2(id mutation.MutationID) mutation.Command {
	return mutation.Command{
		MutationID: id,
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:  mutation.OpRoleUnassign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "editor"}},
	}
}

// specExpectedRevisionNoOp proves an expected revision that matches lets the
// command through and that a no-op under an expected revision neither errors nor
// bumps the scope.
func specExpectedRevisionNoOp(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	seed := mustApply(t, m, grant(mustID(t), "d1", "owner", "u1"))

	cmd := grant(mustID(t), "d1", "viewer", "u2")
	cmd.ExpectedRevision = &seed.Revision // current revision — must pass
	ok := mustApply(t, m, cmd)
	if ok.Outcome != mutation.OutcomeApplied || ok.Revision != seed.Revision+1 {
		t.Fatalf("matching expected revision must apply and bump: outcome=%q revision=%d", ok.Outcome, ok.Revision)
	}

	noop := grant(mustID(t), "d1", "viewer", "u2") // duplicate
	noop.ExpectedRevision = &ok.Revision
	res := mustApply(t, m, noop)
	if res.Outcome != mutation.OutcomeNoChange || res.Revision != ok.Revision {
		t.Fatalf("no-op under expected revision must not bump: outcome=%q revision=%d", res.Outcome, res.Revision)
	}
}

// specReplayAfterSchemaChange proves the pinned validation order across a schema
// upgrade: an exact stored replay returns its original receipt even after a newer
// schema rejects the original relation, while a NEW command carrying that relation
// runs the current-schema semantic validator and is rejected.
func specReplayAfterSchemaChange(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	m := repos.Mutations

	// Originally applied with no additional semantic check.
	original := grant(mustID(t), "d1", "owner", "u1")
	first := mustApply(t, m, original)
	if first.Outcome != mutation.OutcomeApplied {
		t.Fatalf("original apply outcome = %q, want applied", first.Outcome)
	}

	// A newer schema now rejects the `owner` relation.
	rejectOwner := func(cmd mutation.Command) error {
		for _, row := range cmd.Relationships {
			if row.Relation == "owner" {
				return fmt.Errorf("relation %q no longer accepted: %w", row.Relation, sdk.ErrInvalidInput)
			}
		}
		return nil
	}

	// Exact replay under the stricter schema: the validator is SKIPPED (receipt
	// present) so the original receipt is returned unchanged.
	replay, err := m.Apply(ctx, original, rejectOwner)
	if err != nil {
		t.Fatalf("exact replay must not run the current-schema validator: %v", err)
	}
	if !replay.Replayed || replay.Outcome != first.Outcome || replay.Revision != first.Revision || replay.PayloadDigest != first.PayloadDigest {
		t.Fatalf("replay must return the original receipt verbatim: first=%+v replay=%+v", first, replay)
	}

	// A NEW command with the now-rejected relation IS validated and rejected.
	fresh := grant(mustID(t), "d1", "owner", "u2")
	rcpt, err := m.Apply(ctx, fresh, rejectOwner)
	if err == nil {
		t.Fatalf("a receipt-absent command must run the current-schema validator, got receipt %+v", rcpt)
	}
	if rcpt != nil {
		t.Fatalf("a rejected command must return no receipt")
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("schema rejection must surface the validator error, got %v", err)
	}
}

// specGuardianEstablish proves the guardian post-state rule: a member-first
// command on a fresh protected resource is blocked until an owner establishes the
// minimum; the establishing owner grant succeeds; and revoking or replacing away
// the last direct owner is blocked.
func specGuardianEstablish(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	m := repos.Mutations

	// Member-first on a fresh protected resource: blocked (no owner yet).
	memberFirst := grant(mustID(t), "d9", "member", "u1")
	blocked := mustApply(t, m, memberFirst)
	if blocked.Outcome != mutation.OutcomeInvariantBlocked {
		t.Fatalf("member-first on a protected resource must be invariant_blocked, got %q", blocked.Outcome)
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d9", "member", "user", "u1"); ok {
			t.Fatalf("a blocked member-first command must write nothing")
		}
	}

	// The establishing owner grant succeeds.
	if got := mustApply(t, m, grant(mustID(t), "d9", "owner", "u1")); got.Outcome != mutation.OutcomeApplied {
		t.Fatalf("establishing owner grant must apply, got %q", got.Outcome)
	}
	// With an owner in place a member grant is now allowed.
	if got := mustApply(t, m, grant(mustID(t), "d9", "member", "u2")); got.Outcome != mutation.OutcomeApplied {
		t.Fatalf("member grant with an owner present must apply, got %q", got.Outcome)
	}
	// Revoking the sole owner is blocked.
	if got := mustApply(t, m, revoke(mustID(t), "d9", "owner", "u1")); got.Outcome != mutation.OutcomeInvariantBlocked {
		t.Fatalf("revoking the last owner must be invariant_blocked, got %q", got.Outcome)
	}
	// Replacing the sole owner away is blocked.
	if got := mustApply(t, m, replace(mustID(t), "d9", "member", "u1")); got.Outcome != mutation.OutcomeInvariantBlocked {
		t.Fatalf("replacing the last owner away must be invariant_blocked, got %q", got.Outcome)
	}
}

// specLastOwnerGuardianScenarios covers the AZ3-3.2 named scenarios that the
// guardian post-state rule must handle, on every backend: self-removal,
// replacing owner→member, a group (userset) owner that is NOT a direct anchor,
// and an absent target. Two concurrent removals are proven separately by
// ConcurrentTwoOwnerRevokeRounds.
func specLastOwnerGuardianScenarios(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	groupOwner := func(id mutation.MutationID, resourceID string) mutation.Command {
		return mutation.Command{
			MutationID:    id,
			Scope:         resScope(resourceID),
			Operation:     mutation.OpGrant,
			Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "group", ID: "g1", Relation: "member"}}},
		}
	}

	// --- self-removal: an owner revoking their own owner grant ---
	// The sole owner cannot self-remove; with a co-owner the self-removal applies.
	t.Run("self-removal", func(t *testing.T) {
		repos := newRepos(t)
		m := repos.Mutations
		mustApply(t, m, grant(mustID(t), "s1", "owner", "u1"))
		if got := mustApply(t, m, revoke(mustID(t), "s1", "owner", "u1")); got.Outcome != mutation.OutcomeInvariantBlocked {
			t.Fatalf("sole owner self-removal must be invariant_blocked, got %q", got.Outcome)
		}
		mustApply(t, m, grant(mustID(t), "s1", "owner", "u2"))
		if got := mustApply(t, m, revoke(mustID(t), "s1", "owner", "u1")); got.Outcome != mutation.OutcomeApplied {
			t.Fatalf("self-removal with a co-owner must apply, got %q", got.Outcome)
		}
		if repos.Relationships != nil {
			if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "s1", "owner", "user", "u2"); !ok {
				t.Fatalf("the co-owner must remain after self-removal")
			}
		}
	})

	// --- replacing owner→member ---
	// A sole owner replaced away to member removes the last direct guardian.
	t.Run("replace-owner-to-member", func(t *testing.T) {
		repos := newRepos(t)
		m := repos.Mutations
		mustApply(t, m, grant(mustID(t), "s2", "owner", "u1"))
		if got := mustApply(t, m, replace(mustID(t), "s2", "member", "u1")); got.Outcome != mutation.OutcomeInvariantBlocked {
			t.Fatalf("replacing the sole owner away must be invariant_blocked, got %q", got.Outcome)
		}
		mustApply(t, m, grant(mustID(t), "s2", "owner", "u2"))
		if got := mustApply(t, m, replace(mustID(t), "s2", "member", "u1")); got.Outcome != mutation.OutcomeApplied {
			t.Fatalf("replacing one owner away with a co-owner present must apply, got %q", got.Outcome)
		}
	})

	// --- group owner: a group#member owner is NOT a direct anchor ---
	// A group-expanded owner never masks the loss of the final DIRECT guardian.
	t.Run("group-owner-not-direct-anchor", func(t *testing.T) {
		repos := newRepos(t)
		m := repos.Mutations
		// A userset owner alone on a fresh protected resource cannot establish the
		// minimum (it is not a direct anchor).
		if got := mustApply(t, m, groupOwner(mustID(t), "s3")); got.Outcome != mutation.OutcomeInvariantBlocked {
			t.Fatalf("a group (userset) owner alone must be invariant_blocked, got %q", got.Outcome)
		}
		// Establish a concrete owner, then add the group owner alongside it.
		mustApply(t, m, grant(mustID(t), "s3", "owner", "u1"))
		if got := mustApply(t, m, groupOwner(mustID(t), "s3")); got.Outcome != mutation.OutcomeApplied {
			t.Fatalf("a group owner with a concrete owner present must apply, got %q", got.Outcome)
		}
		// Revoking the concrete owner leaves only the group owner — still blocked,
		// because the group owner is not a direct anchor.
		if got := mustApply(t, m, revoke(mustID(t), "s3", "owner", "u1")); got.Outcome != mutation.OutcomeInvariantBlocked {
			t.Fatalf("revoking the last DIRECT owner must be invariant_blocked even with a group owner present, got %q", got.Outcome)
		}
	})

	// --- absent target: revoking a never-granted tuple is a committed not_found ---
	t.Run("absent-target", func(t *testing.T) {
		repos := newRepos(t)
		m := repos.Mutations
		mustApply(t, m, grant(mustID(t), "s4", "owner", "u1"))
		absent := revoke(mustID(t), "s4", "viewer", "u9")
		got := mustApply(t, m, absent)
		if got.Outcome != mutation.OutcomeNotFound {
			t.Fatalf("revoking an absent tuple must be not_found, got %q", got.Outcome)
		}
		if got.Outcome.Persisted() != true {
			t.Fatalf("not_found is a committed, replayable outcome")
		}
		replay := mustApply(t, m, absent)
		if !replay.Replayed || replay.Outcome != mutation.OutcomeNotFound {
			t.Fatalf("replay of an absent-target revoke must return the stored not_found receipt, got %+v", replay)
		}
	})
}

// specGuardedView proves the actor-facing ApplyGuarded path: the guard reads
// authorization data through the dependency-tracking DecisionView (never the outer
// service), an allow commits, and a denial is a command error that writes nothing.
func specGuardedView(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	m := repos.Mutations
	mustApply(t, m, grant(mustID(t), "d1", "owner", "u1")) // establish

	// Guard allows only if the named principal is an owner of the mutation scope.
	guardOwnedBy := func(principal string) mutation.Guard {
		return func(ctx context.Context, view mutation.DecisionView) error {
			ok, err := view.CheckRelation(ctx, resScope("d1"), "owner", "user", principal)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%s is not an owner: %w", principal, sdk.ErrForbidden)
			}
			return nil
		}
	}

	allowed, err := m.ApplyGuarded(ctx, grant(mustID(t), "d1", "viewer", "u2"), guardOwnedBy("u1"), nil)
	if err != nil {
		t.Fatalf("guarded grant by an owner must be allowed: %v", err)
	}
	if allowed.Outcome != mutation.OutcomeApplied {
		t.Fatalf("guarded grant outcome = %q, want applied", allowed.Outcome)
	}

	denied, err := m.ApplyGuarded(ctx, grant(mustID(t), "d1", "viewer", "u3"), guardOwnedBy("nobody"), nil)
	if err == nil {
		t.Fatalf("a guard denial must be a command error, got receipt %+v", denied)
	}
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("guard denial must surface the guard error, got %v", err)
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "viewer", "user", "u3"); ok {
			t.Fatalf("a denied guarded command must write nothing")
		}
	}
}

// specReplayStorm hammers one MutationID from many goroutines: exactly one first
// application commits and every other call returns the same stored receipt as a
// replay, leaving one row and one revision bump.
func specReplayStorm(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	cmd := grant(mustID(t), "d1", "owner", "u1")

	const n = 24
	var wg sync.WaitGroup
	replays := make([]bool, n)
	revs := make([]mutation.Revision, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rcpt, err := m.Apply(context.Background(), cmd, nil)
			if err != nil {
				errs[i] = err
				return
			}
			replays[i] = rcpt.Replayed
			revs[i] = rcpt.Revision
		}(i)
	}
	wg.Wait()

	firsts := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("replay storm call %d errored: %v", i, errs[i])
		}
		if !replays[i] {
			firsts++
		}
		if revs[i] != 1 {
			t.Fatalf("every replay must carry the original revision 1, got %d", revs[i])
		}
	}
	if firsts != 1 {
		t.Fatalf("exactly one first application must commit, got %d", firsts)
	}
	if repos.Relationships != nil {
		if got, _ := repos.Relationships.CountByResourceAndRelation(context.Background(), "doc", "d1", "owner"); got != 1 {
			t.Fatalf("a replay storm must leave exactly one owner, got %d", got)
		}
	}
}

// specStaleWriterStorm has many goroutines race one expected revision: exactly one
// wins and every loser is a stale-revision command error, leaving a single change.
func specStaleWriterStorm(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	seed := mustApply(t, m, grant(mustID(t), "d1", "owner", "u1"))

	const n = 24
	var wg sync.WaitGroup
	applied := make([]bool, n)
	stale := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := grant(mustID(t), "d1", "viewer", "u"+strconv.Itoa(100+i))
			exp := seed.Revision
			cmd.ExpectedRevision = &exp
			rcpt, err := m.Apply(context.Background(), cmd, nil)
			switch {
			case err == nil && rcpt.Outcome == mutation.OutcomeApplied:
				applied[i] = true
			case errors.Is(err, sdk.ErrConflict):
				stale[i] = true
			default:
				t.Errorf("stale writer %d: unexpected result rcpt=%+v err=%v", i, rcpt, err)
			}
		}(i)
	}
	wg.Wait()

	wins, losses := 0, 0
	for i := 0; i < n; i++ {
		if applied[i] {
			wins++
		}
		if stale[i] {
			losses++
		}
	}
	if wins != 1 || losses != n-1 {
		t.Fatalf("exactly one writer at the expected revision must win: applied=%d stale=%d", wins, losses)
	}
}

// specMixedKind races a relationship grant and a scoped role assign on the SAME
// resource scope: both commit, both advance the one shared resource-scope revision,
// and the final revisions are the distinct pair {2, 3}.
func specMixedKind(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	repos := newRepos(t)
	m := repos.Mutations
	mustApply(t, m, grant(mustID(t), "d1", "owner", "u1")) // revision 1, establishes owner

	roleAssign := mutation.Command{
		MutationID: mustID(t),
		Scope:      mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "d1"},
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u3", Role: "editor"}},
	}
	relGrant := grant(mustID(t), "d1", "viewer", "u2")

	var wg sync.WaitGroup
	revs := make([]mutation.Revision, 2)
	outs := make([]mutation.Outcome, 2)
	cmds := []mutation.Command{relGrant, roleAssign}
	for i := range cmds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rcpt, err := m.Apply(context.Background(), cmds[i], nil)
			if err != nil {
				t.Errorf("mixed-kind command %d errored: %v", i, err)
				return
			}
			revs[i] = rcpt.Revision
			outs[i] = rcpt.Outcome
		}(i)
	}
	wg.Wait()

	for i := range outs {
		if outs[i] != mutation.OutcomeApplied {
			t.Fatalf("mixed-kind command %d outcome = %q, want applied", i, outs[i])
		}
	}
	got := map[mutation.Revision]bool{revs[0]: true, revs[1]: true}
	if !got[2] || !got[3] || revs[0] == revs[1] {
		t.Fatalf("relationship + scoped-role mutation must advance one shared scope to {2,3}, got %v", revs)
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "viewer", "user", "u2"); !ok {
			t.Fatalf("the relationship grant must be visible after the race")
		}
	}
	if repos.Roles != nil {
		if ok, _ := repos.Roles.HasExactRole(ctx, "user", "u3", "editor", "doc", "d1"); !ok {
			t.Fatalf("the scoped role assign must be visible after the race")
		}
	}
}

// specGuardRevokeRace races a guarded mutation against a revoke of the exact grant
// the guard depends on. The guard reads `owner u1` on the SAME scope the write
// mutates, so the revoke's revision bump is a shared-scope dependency the
// repository re-validates under lock. Either the revoke or the guarded write may
// win, but a DETACHED STALE ALLOW may never commit: whenever the revoke wins the
// interleaving the guarded write is deterministically STALE (the shared scope
// revision moved between the guard read and the lock) or DENIED (the guard saw u1
// already un-owned) and writes nothing — never a committed viewer row on a stale
// allow. A second owner (u2) keeps the guardian minimum so the revoke itself is a
// clean applied outcome. Driven over rounds under -race across both dialects.
func specGuardRevokeRace(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations

	const rounds = 8
	for r := 0; r < rounds; r++ {
		res := "grd" + strconv.Itoa(r)
		// Two owners so revoking u1 is not last-owner-blocked; u1 is the grant the
		// guard depends on.
		mustApply(t, m, grant(mustID(t), res, "owner", "u1"))
		mustApply(t, m, grant(mustID(t), res, "owner", "u2"))

		// The guard allows only while u1 still holds owner on the mutation scope, so
		// the revoke's revision bump invalidates a stale allow.
		guard := func(ctx context.Context, view mutation.DecisionView) error {
			ok, err := view.CheckRelation(ctx, resScope(res), "owner", "user", "u1")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("u1 is not an owner: %w", sdk.ErrForbidden)
			}
			return nil
		}

		guardedCmd := grant(mustID(t), res, "viewer", "u3")
		revokeCmd := revoke(mustID(t), res, "owner", "u1")

		var wg sync.WaitGroup
		var guardedRcpt, revokeRcpt *mutation.Receipt
		var guardedErr, revokeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			guardedRcpt, guardedErr = m.ApplyGuarded(context.Background(), guardedCmd, guard, nil)
		}()
		go func() {
			defer wg.Done()
			revokeRcpt, revokeErr = m.Apply(context.Background(), revokeCmd, nil)
		}()
		wg.Wait()

		// The revoke always commits (u2 keeps the guardian minimum).
		if revokeErr != nil {
			t.Fatalf("round %d: owner revoke errored (must be a domain outcome): %v", r, revokeErr)
		}
		if revokeRcpt.Outcome != mutation.OutcomeApplied {
			t.Fatalf("round %d: owner revoke outcome = %q, want applied", r, revokeRcpt.Outcome)
		}

		switch {
		case guardedErr == nil:
			// The guarded write won the lock: it committed on a snapshot where u1 was
			// still owner, so its viewer row is present.
			if guardedRcpt.Outcome != mutation.OutcomeApplied {
				t.Fatalf("round %d: a nil-error guarded write must be applied, got %q", r, guardedRcpt.Outcome)
			}
			if repos.Relationships != nil {
				if ok, _ := repos.Relationships.CheckRelationExists(context.Background(), "doc", res, "viewer", "user", "u3"); !ok {
					t.Fatalf("round %d: an applied guarded write must leave its row", r)
				}
			}
		case errors.Is(guardedErr, sdk.ErrConflict) || errors.Is(guardedErr, sdk.ErrForbidden):
			// The revoke won: the guarded write is STALE (the shared scope revision
			// moved) or DENIED (the guard saw u1 already un-owned). Never a committed
			// stale allow — it wrote nothing.
			if guardedRcpt != nil {
				t.Fatalf("round %d: a lost guarded write must return no receipt, got %+v", r, guardedRcpt)
			}
			if repos.Relationships != nil {
				if ok, _ := repos.Relationships.CheckRelationExists(context.Background(), "doc", res, "viewer", "user", "u3"); ok {
					t.Fatalf("round %d: a stale/denied guarded write must not commit its row (detached stale allow)", r)
				}
			}
		default:
			t.Fatalf("round %d: guarded write must be applied, stale, or denied; got rcpt=%+v err=%v", r, guardedRcpt, guardedErr)
		}

		// The guardian minimum held throughout: u2 remains owner.
		if repos.Relationships != nil {
			if ok, _ := repos.Relationships.CheckRelationExists(context.Background(), "doc", res, "owner", "user", "u2"); !ok {
				t.Fatalf("round %d: u2 must remain owner (guardian minimum)", r)
			}
		}
	}
}

// specCrossScopeGuardRevokeRace is the F2 cross-scope corroboration: the guard
// reads a relation on the mutation scope (doc:res) that is satisfied ONLY through
// alice's membership on a DIFFERENT resource scope (group:g), so group:g is a
// dependency the guard traversed but does not mutate. It races the guarded write
// against a committed revoke of that membership on group:g. Because the mutation
// scope (doc:res) and the dependency scope (group:g) are DIFFERENT, only the
// dependency-revalidation discipline (the F2 fix — record every intermediate
// expansion scope) can catch a stale allow; the mutation-scope lock alone cannot.
//
// Honest scope of the proof: this is CORROBORATION, not the deterministic F2
// proof. The deterministic proof is the per-store white-box dependency-completeness
// tests (the DecisionView records group:g). Under full-write-serialization stores
// (memstore mutex, turso BEGIN IMMEDIATE) the guarded apply and the revoke cannot
// interleave mid-flight, so the guard reads a consistent committed snapshot and the
// outcome is applied XOR denied — the stale window is architecturally precluded,
// not merely unobserved. Only pgx's per-anchor locking opens the record-then-commit
// window, and even there the interleaving is scheduler-dependent. So this case
// asserts the SAFETY INVARIANT that holds on all three: the revoke always commits
// cleanly, and the guarded write is either applied (its viewer row present) or
// cleanly aborted (stale/denied, NO receipt, NO committed row) — never a torn state
// and never a (nil, nil).
func specCrossScopeGuardRevokeRace(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	if repos.Relationships == nil {
		t.Skip("relationships kind not wired — cross-scope guard race needs group expansion")
	}

	groupScope := func(id string) mutation.ScopeKey {
		return mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "group", ID: id}
	}
	grantRow := func(scope mutation.ScopeKey, relation string, subj relationship.SubjectRef) mutation.Command {
		return mutation.Command{
			MutationID:    mustID(t),
			Scope:         scope,
			Operation:     mutation.OpGrant,
			Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: subj}},
		}
	}

	const rounds = 24
	for r := 0; r < rounds; r++ {
		res := "xdoc" + strconv.Itoa(r)
		grp := "eng" + strconv.Itoa(r)

		// group:grp — concrete owner first (guardian minimum), then alice as member.
		// The member edge lives under group:grp's resource scope.
		mustApply(t, m, grantRow(groupScope(grp), "owner", relationship.SubjectRef{Type: "user", ID: "ownerG"}))
		mustApply(t, m, grantRow(groupScope(grp), "member", relationship.SubjectRef{Type: "user", ID: "alice"}))
		// doc:res — concrete owner first, then an editor userset for group:grp#member,
		// so alice is editor on doc:res ONLY via her group:grp membership.
		mustApply(t, m, grant(mustID(t), res, "owner", "ownerD"))
		mustApply(t, m, grantRow(resScope(res), "editor", relationship.SubjectRef{Type: "group", ID: grp, Relation: "member"}))

		// The guard authorizes only while alice holds editor on doc:res, which the
		// expansion satisfies through group:grp#member — so the F2 fix records
		// group:grp as a dependency and a concurrent membership revoke on that scope
		// invalidates a stale allow.
		guard := func(ctx context.Context, view mutation.DecisionView) error {
			ok, err := view.CheckRelation(ctx, resScope(res), "editor", "user", "alice")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("alice is not an editor: %w", sdk.ErrForbidden)
			}
			return nil
		}

		guardedCmd := grant(mustID(t), res, "viewer", "u3")
		revokeMembership := mutation.Command{
			MutationID:    mustID(t),
			Scope:         groupScope(grp),
			Operation:     mutation.OpRevoke,
			Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: relationship.SubjectRef{Type: "user", ID: "alice"}}},
		}

		var wg sync.WaitGroup
		var guardedRcpt, revokeRcpt *mutation.Receipt
		var guardedErr, revokeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			guardedRcpt, guardedErr = m.ApplyGuarded(context.Background(), guardedCmd, guard, nil)
		}()
		go func() {
			defer wg.Done()
			revokeRcpt, revokeErr = m.Apply(context.Background(), revokeMembership, nil)
		}()
		wg.Wait()

		// The membership revoke always commits cleanly (group:grp keeps its owner).
		if revokeErr != nil {
			t.Fatalf("round %d: membership revoke errored (must be a domain outcome): %v", r, revokeErr)
		}
		if revokeRcpt.Outcome != mutation.OutcomeApplied {
			t.Fatalf("round %d: membership revoke outcome = %q, want applied", r, revokeRcpt.Outcome)
		}

		viewerPresent, _ := repos.Relationships.CheckRelationExists(context.Background(), "doc", res, "viewer", "user", "u3")
		switch {
		case guardedErr == nil:
			// The guarded write committed: it must have locked and revalidated its
			// group:grp dependency BEFORE the revoke bumped it, so the decision held at
			// commit — its viewer row is present.
			if guardedRcpt == nil || guardedRcpt.Outcome != mutation.OutcomeApplied {
				t.Fatalf("round %d: a nil-error guarded write must be applied, got %+v", r, guardedRcpt)
			}
			if !viewerPresent {
				t.Fatalf("round %d: an applied guarded write must leave its viewer row", r)
			}
		case errors.Is(guardedErr, sdk.ErrConflict) || errors.Is(guardedErr, sdk.ErrForbidden):
			// The revoke won: the guarded write is STALE (the group:grp dependency
			// revision moved between the guard read and the commit lock — the F2 path)
			// or DENIED (the guard read a snapshot where alice was already un-membered).
			// Either way it committed NOTHING — never a stale-allow viewer row.
			if guardedRcpt != nil {
				t.Fatalf("round %d: a cleanly aborted guarded write must return no receipt, got %+v", r, guardedRcpt)
			}
			if viewerPresent {
				t.Fatalf("round %d: a stale/denied guarded write must not commit its viewer row (cross-scope stale allow)", r)
			}
		default:
			t.Fatalf("round %d: guarded write must be applied, stale, or denied; got rcpt=%+v err=%v", r, guardedRcpt, guardedErr)
		}
	}
}

// specTwoOwnerRevokeRounds drives repeated rounds of two concurrent last-owner
// revokes. GuardianEstablishesMinimum proves the invariant statically and
// ConcurrentSingleWinner proves one round; this drives many rounds so the
// single-database-arbiter guarantee is exercised across interleavings under -race:
// each round, exactly one revoke commits and exactly one is invariant_blocked, and
// exactly one owner always remains — the two owners can never both be removed.
func specTwoOwnerRevokeRounds(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations

	const rounds = 12
	for r := 0; r < rounds; r++ {
		res := "own" + strconv.Itoa(r)
		mustApply(t, m, grant(mustID(t), res, "owner", "u1"))
		mustApply(t, m, grant(mustID(t), res, "owner", "u2"))

		cmds := []mutation.Command{
			revoke(mustID(t), res, "owner", "u1"),
			revoke(mustID(t), res, "owner", "u2"),
		}
		var wg sync.WaitGroup
		outs := make([]mutation.Outcome, 2)
		errs := make([]error, 2)
		for i := range cmds {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rcpt, err := m.Apply(context.Background(), cmds[i], nil)
				if err != nil {
					errs[i] = err
					return
				}
				outs[i] = rcpt.Outcome
			}(i)
		}
		wg.Wait()

		for i := range errs {
			if errs[i] != nil {
				t.Fatalf("round %d: revoke %d errored (must be a domain outcome): %v", r, i, errs[i])
			}
		}
		applied, blocked := 0, 0
		for _, o := range outs {
			switch o {
			case mutation.OutcomeApplied:
				applied++
			case mutation.OutcomeInvariantBlocked:
				blocked++
			}
		}
		if applied != 1 || blocked != 1 {
			t.Fatalf("round %d: exactly one last-owner revoke must win: applied=%d blocked=%d (outcomes=%v)", r, applied, blocked, outs)
		}
		if repos.Relationships != nil {
			if n, _ := repos.Relationships.CountByResourceAndRelation(context.Background(), "doc", res, "owner"); n != 1 {
				t.Fatalf("round %d: exactly one owner must remain, got %d", r, n)
			}
		}
	}
}

// specReplaceNoAbsentState proves atomic replace exposes no absent/intermediate
// state to a concurrent reader. A writer repeatedly OpReplace's u1's relation,
// toggling viewer<->member, while readers continuously enumerate the resource in a
// SINGLE store call (one snapshot). u1 must appear in EXACTLY one relation on every
// snapshot — never zero (a delete/create gap) and never two (a duplicate). A second
// owner (u2) keeps the guardian minimum; u1 never touches owner. Under -race across
// both dialects this fails if replace is not a single atomic in-place change.
func specReplaceNoAbsentState(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	if repos.Relationships == nil {
		t.Skip("replace-visibility needs the relationship read side")
	}
	ctx := context.Background()
	res := "rep"
	mustApply(t, m, grant(mustID(t), res, "owner", "u2"))  // guardian minimum
	mustApply(t, m, grant(mustID(t), res, "viewer", "u1")) // the subject we toggle

	const toggles = 40
	subjectType := "user"
	filter := relationship.ResourceRelationshipFilter{SubjectType: &subjectType}
	done := make(chan struct{})
	var wg sync.WaitGroup

	// Writer: atomically replace u1's relation, toggling viewer<->member.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		rel := "member"
		for i := 0; i < toggles; i++ {
			if _, err := m.Apply(ctx, replace(mustID(t), res, rel, "u1"), nil); err != nil {
				t.Errorf("replace toggle %d errored: %v", i, err)
				return
			}
			if rel == "member" {
				rel = "viewer"
			} else {
				rel = "member"
			}
		}
	}()

	// Readers: on every single-call snapshot u1 must hold exactly one relation.
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				page, err := repos.Relationships.ListRelationshipsByResource(ctx, "doc", res, filter, crud.ListRequest{})
				if err != nil {
					t.Errorf("reader list: %v", err)
					return
				}
				count := 0
				for _, it := range page.Items {
					if it.SubjectID == "u1" {
						count++
					}
				}
				if count != 1 {
					t.Errorf("atomic replace must keep u1 in exactly one relation, snapshot saw %d (0=absent gap, 2=duplicate)", count)
					return
				}
			}
		}()
	}
	wg.Wait()

	// End state: exactly one relation present for u1.
	asViewer, _ := repos.Relationships.CheckRelationExists(ctx, "doc", res, "viewer", "user", "u1")
	asMember, _ := repos.Relationships.CheckRelationExists(ctx, "doc", res, "member", "user", "u1")
	if asViewer == asMember {
		t.Fatalf("after the toggles u1 must hold exactly one relation: viewer=%v member=%v", asViewer, asMember)
	}
}

// specReceiptRevisionForensics runs a concurrent grant storm and then runs the
// post-storm forensic: the N applied receipts carry a gapless, duplicate-free
// revision run agreeing with the final anchor; replaying each command returns the
// ORIGINAL receipt verbatim (Replayed=true, identical revision/outcome/digests,
// preserved CreatedAt) with no anchor bump — proving no duplicate or divergent
// receipt was minted and that permanent retention keeps the receipt replayable (the
// port exposes no expiry surface; the stored expires_at NULL column is asserted by
// each dialect's SQL forensic). The anchor's N bumps agree with N committed owner
// rows. This is the shared forensic every backend's conformance runs.
func specReceiptRevisionForensics(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	res := "storm"

	const n = 20
	cmds := make([]mutation.Command, n)
	for i := range cmds {
		cmds[i] = grant(mustID(t), res, "owner", "o"+strconv.Itoa(i)) // all owner → all applied, guardian-safe
	}

	var wg sync.WaitGroup
	firsts := make([]*mutation.Receipt, n)
	errs := make([]error, n)
	for i := range cmds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			firsts[i], errs[i] = m.Apply(context.Background(), cmds[i], nil)
		}(i)
	}
	wg.Wait()

	appliedRevs := make([]mutation.Revision, 0, n)
	for i := range cmds {
		if errs[i] != nil {
			t.Fatalf("storm grant %d errored: %v", i, errs[i])
		}
		if firsts[i].Outcome != mutation.OutcomeApplied {
			t.Fatalf("storm grant %d outcome = %q, want applied", i, firsts[i].Outcome)
		}
		if firsts[i].Replayed {
			t.Fatalf("storm grant %d must be a first application, not a replay", i)
		}
		appliedRevs = append(appliedRevs, firsts[i].Revision)
	}

	// Revision agreement: the N applied receipts form a gapless 1..N run agreeing
	// with the final anchor.
	assertGaplessRevisions(t, appliedRevs, 1)
	if got := anchorRevision(t, m, resScope(res)); got != mutation.Revision(n) {
		t.Fatalf("the final scope anchor must agree with the last applied revision: got %d want %d", got, n)
	}

	// Replay metadata: each replay returns the original receipt verbatim, no bump.
	for i := range cmds {
		replay, err := m.Apply(context.Background(), cmds[i], nil)
		if err != nil {
			t.Fatalf("replay of storm grant %d errored: %v", i, err)
		}
		if !replay.Replayed {
			t.Fatalf("replay of storm grant %d must be a replay", i)
		}
		if replay.Revision != firsts[i].Revision || replay.Outcome != firsts[i].Outcome ||
			replay.PayloadDigest != firsts[i].PayloadDigest || replay.SchemaDigest != firsts[i].SchemaDigest ||
			!replay.CreatedAt.Equal(firsts[i].CreatedAt) {
			t.Fatalf("replay of storm grant %d diverged: first=%+v replay=%+v", i, firsts[i], replay)
		}
	}
	if got := anchorRevision(t, m, resScope(res)); got != mutation.Revision(n) {
		t.Fatalf("replays must not move the anchor: got %d want %d", got, n)
	}

	// Anchor vs row state: the N revision bumps agree with N committed owner rows.
	if repos.Relationships != nil {
		if got, _ := repos.Relationships.CountByResourceAndRelation(context.Background(), "doc", res, "owner"); got != n {
			t.Fatalf("the scope anchor (%d) must agree with committed owner rows, got %d", n, got)
		}
	}
}

// specCrossScopeBatchRejected proves a cross-scope misuse is rejected as a command
// error before any row/revision/receipt change. A Command is structurally
// single-scope, so the closest constructible misuse is (a) a subject-scoped role
// row whose subject disagrees with the scope subject — the rows imply a different
// scope than Command.Scope names — and (b) an operation/rows mismatch. Both are
// invalid-command errors that write nothing: the seeded resource revision is
// unchanged, the misuse scope anchor never advanced past revision 0, and the
// MutationID is NOT consumed (reuse with a valid payload applies fresh).
func specCrossScopeBatchRejected(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations

	seed := mustApply(t, m, grant(mustID(t), "d1", "owner", "u1")) // resource scope at a known revision

	subjectScope := mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: "user", ID: "u1"}
	misuseID := mustID(t)
	crossScope := mutation.Command{
		MutationID: misuseID,
		Scope:      subjectScope,
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u2", Role: "editor"}}, // subject != scope subject
	}
	rcpt, err := m.Apply(context.Background(), crossScope, nil)
	if err == nil {
		t.Fatalf("a row whose implied scope disagrees with Command.Scope must be a command error, got receipt %+v", rcpt)
	}
	if rcpt != nil {
		t.Fatalf("a rejected cross-scope command must return no receipt")
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("cross-scope misuse must be an invalid-command error, got %v", err)
	}

	// An operation/rows mismatch is the same class of structural rejection.
	mismatch := mutation.Command{
		MutationID: mustID(t),
		Scope:      resScope("d1"),
		Operation:  mutation.OpGrant,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u3", Role: "editor"}}, // grant must not carry role rows
	}
	if _, err := m.Apply(context.Background(), mismatch, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("an operation/rows mismatch must be an invalid-command error, got %v", err)
	}

	// Zero state change: the seeded resource revision is unchanged, and the misuse
	// subject scope never advanced past revision 0 (semantically absent).
	if got := anchorRevision(t, m, resScope("d1")); got != seed.Revision {
		t.Fatalf("a rejected command must not move the seeded scope revision: got %d want %d", got, seed.Revision)
	}
	if got := anchorRevision(t, m, subjectScope); got != 0 {
		t.Fatalf("a rejected command must not advance its own scope anchor: got revision %d", got)
	}

	// The MutationID was not consumed: reuse with a valid payload applies fresh.
	fresh := mutation.Command{
		MutationID: misuseID,
		Scope:      subjectScope,
		Operation:  mutation.OpRoleAssign,
		Roles:      []mutation.RoleRow{{SubjectType: "user", SubjectID: "u1", Role: "editor"}},
	}
	ok, err := m.Apply(context.Background(), fresh, nil)
	if err != nil {
		t.Fatalf("a structurally rejected MutationID must not be consumed; reuse with a valid payload must apply: %v", err)
	}
	if ok.Replayed {
		t.Fatalf("the MutationID of a rejected command must not have been consumed (got a replay)")
	}
}

// specContextCancellation proves a context cancelled before Apply is a command
// error with no state change and no receipt. Infrastructure error MAPPING is
// store-specific (a recovered guard panic maps to sdk.ErrUnavailable — proven by
// each dialect's TestMutationGuardPanicRollsBack); here the shared suite pins the
// cancellation surface: context.Canceled, no row, no revision bump, and a
// MutationID that was never consumed.
func specContextCancellation(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	repos := newRepos(t)
	m := repos.Mutations
	seed := mustApply(t, m, grant(mustID(t), "d1", "owner", "u1"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := grant(mustID(t), "d1", "viewer", "u2")
	rcpt, err := m.Apply(ctx, cmd, nil)
	if err == nil {
		t.Fatalf("Apply under a cancelled context must be a command error, got receipt %+v", rcpt)
	}
	if rcpt != nil {
		t.Fatalf("a cancelled Apply must return no receipt")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled Apply must surface context.Canceled, got %v", err)
	}

	// No state change.
	if got := anchorRevision(t, m, resScope("d1")); got != seed.Revision {
		t.Fatalf("a cancelled Apply must not move the scope revision: got %d want %d", got, seed.Revision)
	}
	if repos.Relationships != nil {
		if ok, _ := repos.Relationships.CheckRelationExists(context.Background(), "doc", "d1", "viewer", "user", "u2"); ok {
			t.Fatalf("a cancelled Apply must write no row")
		}
	}
	// The MutationID was not consumed: the same command applies fresh under a live
	// context.
	retry, err := m.Apply(context.Background(), cmd, nil)
	if err != nil {
		t.Fatalf("a cancelled Apply must not consume its MutationID; retry must apply: %v", err)
	}
	if retry.Replayed {
		t.Fatalf("a cancelled Apply must not have persisted a receipt (got a replay on retry)")
	}
}

// specIntraCommandDuplicateSubject proves the dialect-agnostic domain rule that a
// single command may not carry two rows for the SAME subject reference. Under the
// one-relation invariant two relations for one subject reference in one command is
// intrinsically contradictory, and an exact-duplicate row is non-canonical; both
// variants are rejected as ErrInvalidCommand by Command.Validate — before any
// evaluator on every backend — so nothing persists and the MutationID is not
// consumed. It guards against the store divergence a per-store unique index would
// otherwise produce (memstore adding both rows while the SQL stores reject one).
func specIntraCommandDuplicateSubject(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	subj := relationship.SubjectRef{Type: "user", ID: "u1"}

	variants := []struct {
		name string
		rows []mutation.RelationshipRow
	}{
		{"DifferentRelations", []mutation.RelationshipRow{
			{Relation: "owner", Subject: subj},
			{Relation: "member", Subject: subj},
		}},
		{"SameRelation", []mutation.RelationshipRow{
			{Relation: "owner", Subject: subj},
			{Relation: "owner", Subject: subj},
		}},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			repos := newRepos(t)
			m := repos.Mutations
			id := mustID(t)
			cmd := mutation.Command{
				MutationID:    id,
				Scope:         resScope("d1"),
				Operation:     mutation.OpGrant,
				Relationships: v.rows,
			}
			rcpt, err := m.Apply(ctx, cmd, nil)
			if !errors.Is(err, mutation.ErrInvalidCommand) {
				t.Fatalf("duplicate-subject command must reject with ErrInvalidCommand, got receipt=%+v err=%v", rcpt, err)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("ErrInvalidCommand must wrap sdk.ErrInvalidInput, got %v", err)
			}
			if rcpt != nil {
				t.Fatalf("a rejected command must return a nil receipt, got %+v", rcpt)
			}

			// Nothing persisted: neither row committed.
			if repos.Relationships != nil {
				if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1"); ok {
					t.Fatalf("no row may persist for a rejected duplicate-subject command")
				}
				if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "member", "user", "u1"); ok {
					t.Fatalf("no row may persist for a rejected duplicate-subject command")
				}
			}
			// The scope anchor never advanced past revision 0.
			if got := anchorRevision(t, m, resScope("d1")); got != 0 {
				t.Fatalf("a rejected command must not advance the scope anchor, got revision %d", got)
			}

			// The MutationID was not consumed: a later VALID command reusing it
			// applies as a first application (never a replay).
			reuse := grant(id, "d1", "owner", "u1")
			applied := mustApply(t, m, reuse)
			if applied.Replayed {
				t.Fatalf("a rejected command must not consume its MutationID; reuse must not replay")
			}
			if applied.Outcome != mutation.OutcomeApplied {
				t.Fatalf("reuse of the MutationID in a valid command must apply, got outcome %q", applied.Outcome)
			}
		})
	}
}

// anchorRevision reads a scope's CURRENT revision through a guaranteed no-op — the
// only port-level way to observe an anchor without mutating it. For a resource
// scope it revokes an absent probe subject; for a subject scope it unassigns an
// absent probe role. Either is OutcomeNotFound: it carries the current revision
// with no bump. It is the forensic helper's anchor probe.
func anchorRevision(t *testing.T, m mutation.MutationRepository, scope mutation.ScopeKey) mutation.Revision {
	t.Helper()
	var cmd mutation.Command
	switch scope.Kind {
	case mutation.ScopeResource:
		cmd = mutation.Command{
			MutationID:    mustID(t),
			Scope:         scope,
			Operation:     mutation.OpRevoke,
			Relationships: []mutation.RelationshipRow{{Relation: "az3probe", Subject: relationship.SubjectRef{Type: "probe", ID: "az3probe-" + string(mustID(t))}}},
		}
	case mutation.ScopeSubject:
		cmd = mutation.Command{
			MutationID: mustID(t),
			Scope:      scope,
			Operation:  mutation.OpRoleUnassign,
			Roles:      []mutation.RoleRow{{SubjectType: scope.Type, SubjectID: scope.ID, Role: "az3probe"}},
		}
	default:
		t.Fatalf("anchorRevision: unknown scope kind %q", scope.Kind)
	}
	rcpt, err := m.Apply(context.Background(), cmd, nil)
	if err != nil {
		t.Fatalf("anchorRevision probe on %s errored: %v", scope, err)
	}
	if rcpt.Outcome != mutation.OutcomeNotFound {
		t.Fatalf("anchorRevision probe on %s must be a not_found no-op, got %q", scope, rcpt.Outcome)
	}
	return rcpt.Revision
}

// assertGaplessRevisions asserts the applied revisions form a contiguous,
// duplicate-free run base..base+len-1 — no revision gap (a lost bump) and no
// duplicate revision (two applied changes claiming one revision).
func assertGaplessRevisions(t *testing.T, revs []mutation.Revision, base mutation.Revision) {
	t.Helper()
	seen := make(map[mutation.Revision]bool, len(revs))
	for _, r := range revs {
		if seen[r] {
			t.Fatalf("duplicate revision %d in applied run %v", r, revs)
		}
		seen[r] = true
	}
	for i := 0; i < len(revs); i++ {
		want := base + mutation.Revision(i)
		if !seen[want] {
			t.Fatalf("revision gap: %d missing from the applied run %v", want, revs)
		}
	}
}
