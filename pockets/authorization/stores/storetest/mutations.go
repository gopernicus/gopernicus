package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func runMutations(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	t.Run("StateTransitions", func(t *testing.T) { specStateTransitions(t, newRepos) })
	t.Run("NoPartialBatch", func(t *testing.T) { specNoPartialBatch(t, newRepos) })
	t.Run("PurgeBlockedTeardownClears", func(t *testing.T) { specPurgeTeardown(t, newRepos) })
	t.Run("CurrentGuardAndModelEveryApplication", func(t *testing.T) { specCurrentValidation(t, newRepos) })
	t.Run("GlobalRoleIdentity", func(t *testing.T) { specGlobalRoleIdentity(t, newRepos) })
	t.Run("RoleAssignUnassignTargets", func(t *testing.T) { specRoleTargets(t, newRepos) })
	t.Run("ConcurrentTwoOwnerRevokes", func(t *testing.T) { specConcurrentOwnerRevokes(t, newRepos) })
	t.Run("ConcurrentIdenticalGrants", func(t *testing.T) { specConcurrentGrants(t, newRepos) })
	t.Run("ConcurrentNegativeGuards", func(t *testing.T) { specConcurrentNegativeGuards(t, newRepos) })
	t.Run("ConcurrentReplaceNoAbsentState", func(t *testing.T) { specReplaceNoAbsentState(t, newRepos) })
	t.Run("GuardianEstablishesMinimum", func(t *testing.T) { specGuardianEstablish(t, newRepos) })
	t.Run("GuardCancellation", func(t *testing.T) { specCallbackCancellation(t, newRepos, true) })
	t.Run("ValidatorCancellation", func(t *testing.T) { specCallbackCancellation(t, newRepos, false) })
	t.Run("GuardedPermissionWalksThrough", func(t *testing.T) { specGuardedPermissionThrough(t, newRepos) })
	t.Run("GuardedPermissionExpansionBudgetParity", func(t *testing.T) { specGuardedPermissionExpansionParity(t, newRepos) })
	t.Run("ConcurrentGuardedPermissionThroughRevokeRaces", func(t *testing.T) { specGuardedPermissionThroughRevokeRaces(t, newRepos) })
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprintf("GuardedRoleRevokeRace/global%v", global), func(t *testing.T) { specGuardedRoleRevokeRace(t, newRepos, global) })
	}
	for _, mode := range []string{"scoped", "global", "both", "absent", "removed_from_model"} {
		t.Run("GuardedRolePermission/"+mode, func(t *testing.T) { specGuardedRolePermission(t, newRepos, mode) })
	}
}
func resTarget(id string) mutations.Target {
	return mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: id}
}
func grant(resourceID, relation, subjectID string) mutations.Command {
	return mutations.Command{Target: resTarget(resourceID), Operation: mutations.OpGrant, Relationships: []mutations.RelationshipRow{{Relation: relation, Subject: relationships.SubjectRef{Type: "user", ID: subjectID}}}}
}
func revoke(resourceID, relation, subjectID string) mutations.Command {
	c := grant(resourceID, relation, subjectID)
	c.Operation = mutations.OpRevoke
	return c
}
func replace(resourceID, relation, subjectID string) mutations.Command {
	c := grant(resourceID, relation, subjectID)
	c.Operation = mutations.OpReplace
	return c
}
func mustApply(t *testing.T, m mutations.MutationRepository, cmd mutations.Command) *mutations.Result {
	t.Helper()
	r, err := m.Apply(context.Background(), cmd, nil)
	if err != nil || r == nil {
		t.Fatalf("Apply(%+v): %+v %v", cmd, r, err)
	}
	return r
}
func mustReject(t *testing.T, m mutations.MutationRepository, cmd mutations.Command, want error) {
	t.Helper()
	r, err := m.Apply(context.Background(), cmd, nil)
	if r != nil || !errors.Is(err, want) {
		t.Fatalf("Apply(%+v): %+v %v, want %v", cmd, r, err, want)
	}
}
func relationExists(t *testing.T, r authorization.Repositories, resourceID, relation, subject string) bool {
	t.Helper()
	ok, err := r.Relationships.CheckRelationExists(context.Background(), "doc", resourceID, relation, "user", subject)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}
func specStateTransitions(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	owner := grant("d", "owner", "owner")
	mustApply(t, m, owner)
	viewer := grant("d", "viewer", "u")
	for _, step := range []struct {
		cmd     mutations.Command
		outcome mutations.Outcome
	}{
		{viewer, mutations.OutcomeApplied}, {viewer, mutations.OutcomeNoChange},
		{replace("d", "editor", "u"), mutations.OutcomeApplied},
		{revoke("d", "viewer", "u"), mutations.OutcomeNotFound},
		{revoke("d", "editor", "u"), mutations.OutcomeApplied},
		{revoke("d", "editor", "u"), mutations.OutcomeNotFound},
		{viewer, mutations.OutcomeApplied},
	} {
		if got := mustApply(t, m, step.cmd); got.Outcome != step.outcome {
			t.Fatalf("outcome=%s want=%s", got.Outcome, step.outcome)
		}
	}
	if !relationExists(t, r, "d", "viewer", "u") || relationExists(t, r, "d", "editor", "u") {
		t.Fatal("state-based repeated command failed to restore viewer")
	}
	mustReject(t, m, grant("d", "editor", "u"), mutations.ErrSemanticConflict)
	if !relationExists(t, r, "d", "viewer", "u") {
		t.Fatal("conflict removed original fact")
	}
}
func specNoPartialBatch(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	mustApply(t, m, grant("d", "owner", "owner"))
	mustApply(t, m, grant("d", "viewer", "exists"))
	cmd := grant("d", "viewer", "new")
	cmd.Relationships = append(cmd.Relationships, mutations.RelationshipRow{Relation: "editor", Subject: userRef("exists")})
	mustReject(t, m, cmd, mutations.ErrSemanticConflict)
	if relationExists(t, r, "d", "viewer", "new") || !relationExists(t, r, "d", "viewer", "exists") {
		t.Fatal("conflicting batch partially changed facts")
	}
	for _, op := range []mutations.Operation{mutations.OpGrant, mutations.OpRevoke, mutations.OpReplace} {
		cmd := grant("d", "viewer", "duplicate")
		cmd.Operation = op
		cmd.Relationships = append(cmd.Relationships, cmd.Relationships[0])
		mustReject(t, m, cmd, sdk.ErrInvalidInput)
	}
}
func specPurgeTeardown(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	mustApply(t, m, grant("d", "owner", "owner"))
	mustApply(t, m, mutations.Command{Target: resTarget("d"), Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "u", Role: "editor"}}})
	mustReject(t, m, mutations.Command{Target: resTarget("d"), Operation: mutations.OpPurge}, mutations.ErrInvariantBlocked)
	if !relationExists(t, r, "d", "owner", "owner") {
		t.Fatal("blocked purge changed facts")
	}
	result := mustApply(t, m, mutations.Command{Target: resTarget("d"), Operation: mutations.OpTeardown})
	if result.Outcome != mutations.OutcomeApplied || relationExists(t, r, "d", "owner", "owner") {
		t.Fatal("teardown retained relationships")
	}
	held, err := r.Roles.HasExactRole(context.Background(), "user", "u", "editor", "doc", "d")
	if err != nil || held {
		t.Fatalf("teardown retained role: %v %v", held, err)
	}
	if got := mustApply(t, m, mutations.Command{Target: resTarget("d"), Operation: mutations.OpTeardown}); got.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("empty teardown: %+v", got)
	}
}
func specCurrentValidation(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	cmd := grant("d", "owner", "owner")
	mustApply(t, m, cmd)
	reject := errors.New("current model rejects command")
	if result, err := m.Apply(context.Background(), cmd, func(mutations.Command) error { return reject }); result != nil || !errors.Is(err, reject) {
		t.Fatalf("no-op skipped current model: %+v %v", result, err)
	}
	if result, err := m.ApplyGuarded(context.Background(), cmd, func(context.Context, mutations.StoreDecisionView) error { return sdk.ErrForbidden }, nil); result != nil || !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("no-op skipped guard: %+v %v", result, err)
	}
	if !relationExists(t, r, "d", "owner", "owner") {
		t.Fatal("refused no-op removed original")
	}
}
func specRoleTargets(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	rows := []mutations.RoleRow{{SubjectType: "user", SubjectID: "u", Role: "editor"}}
	global := mutations.Command{Target: mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "u"}, Operation: mutations.OpRoleAssign, Roles: rows}
	local := mutations.Command{Target: resTarget("d"), Operation: mutations.OpRoleAssign, Roles: rows}
	mustApply(t, m, global)
	mustApply(t, m, local)
	local.Operation = mutations.OpRoleUnassign
	if result := mustApply(t, m, local); !result.SameRoleGrantRemains || result.Outcome != mutations.OutcomeApplied {
		t.Fatalf("global fallback annotation lost: %+v", result)
	}
	if result := mustApply(t, m, local); !result.SameRoleGrantRemains || result.Outcome != mutations.OutcomeNotFound {
		t.Fatalf("current no-op annotation lost: %+v", result)
	}
	global.Operation = mutations.OpRoleUnassign
	mustApply(t, m, global)
	if result := mustApply(t, m, local); result.SameRoleGrantRemains {
		t.Fatalf("removed global still effective: %+v", result)
	}
	bad := global
	bad.Roles = []mutations.RoleRow{{SubjectType: "user", SubjectID: "someone-else", Role: "editor"}}
	mustReject(t, m, bad, sdk.ErrInvalidInput)
}
func specGuardianEstablish(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	mustReject(t, m, grant("d", "viewer", "member"), mutations.ErrInvariantBlocked)
	group := grant("d", "owner", "g")
	group.Relationships[0].Subject = relationships.SubjectRef{Type: "group", ID: "g", Relation: "member"}
	mustReject(t, m, group, mutations.ErrInvariantBlocked)
	owner := grant("d", "owner", "owner")
	mustApply(t, m, owner)
	mustReject(t, m, replace("d", "viewer", "owner"), mutations.ErrInvariantBlocked)
	mustReject(t, m, revoke("d", "owner", "owner"), mutations.ErrInvariantBlocked)
	if !relationExists(t, r, "d", "owner", "owner") {
		t.Fatal("guardian block changed owner")
	}
}
func specConcurrentOwnerRevokes(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	for round := 0; round < 6; round++ {
		id := fmt.Sprintf("d%d", round)
		mustApply(t, m, grant(id, "owner", "a"))
		mustApply(t, m, grant(id, "owner", "b"))
		start := make(chan struct{})
		errs := make(chan error, 2)
		for _, who := range []string{"a", "b"} {
			go func() { <-start; _, err := m.Apply(context.Background(), revoke(id, "owner", who), nil); errs <- err }()
		}
		close(start)
		succeeded := 0
		for range 2 {
			err := <-errs
			if err == nil {
				succeeded++
			} else if !errors.Is(err, mutations.ErrInvariantBlocked) && !errors.Is(err, mutations.ErrConcurrentMutation) {
				t.Fatal(err)
			}
		}
		n, err := r.Relationships.CountByResourceAndRelation(context.Background(), "doc", id, "owner")
		if err != nil || n != 1 || succeeded != 1 {
			t.Fatalf("concurrent revokes orphaned target: count=%d success=%d err=%v", n, succeeded, err)
		}
	}
}
func specConcurrentGrants(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	mustApply(t, m, grant("d", "owner", "owner"))
	const writers = 8
	start := make(chan struct{})
	type completion struct {
		result *mutations.Result
		err    error
	}
	results := make(chan completion, writers)
	for range writers {
		go func() {
			<-start
			result, err := m.Apply(context.Background(), grant("d", "viewer", "u"), nil)
			results <- completion{result, err}
		}()
	}
	close(start)
	applied := 0
	for range writers {
		got := <-results
		result, err := got.result, got.err
		if err != nil {
			if !errors.Is(err, mutations.ErrConcurrentMutation) {
				t.Fatal(err)
			}
			continue
		}
		if result.Outcome == mutations.OutcomeApplied {
			applied++
		} else if result.Outcome != mutations.OutcomeNoChange {
			t.Fatalf("duplicate grant: %+v", result)
		}
	}
	n, err := r.Relationships.CountByResourceAndRelation(context.Background(), "doc", "d", "viewer")
	if err != nil || n != 1 || applied != 1 {
		t.Fatalf("duplicate writers: rows=%d applied=%d err=%v", n, applied, err)
	}
}
func specConcurrentNegativeGuards(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	for round := 0; round < 6; round++ {
		a, b := fmt.Sprintf("a%d", round), fmt.Sprintf("b%d", round)
		mustApply(t, m, grant(a, "owner", "owner"))
		mustApply(t, m, grant(b, "owner", "owner"))
		start := make(chan struct{})
		errs := make(chan error, 2)
		for _, pair := range [][2]string{{a, b}, {b, a}} {
			go func() {
				<-start
				_, err := m.ApplyGuarded(context.Background(), grant(pair[0], "viewer", "u"), func(ctx context.Context, v mutations.StoreDecisionView) error {
					exists, err := v.CheckRelation(ctx, resTarget(pair[1]), "viewer", "user", "u")
					if err != nil {
						return err
					}
					if exists {
						return sdk.ErrForbidden
					}
					return nil
				}, nil)
				errs <- err
			}()
		}
		close(start)
		for range 2 {
			err := <-errs
			if err != nil && !errors.Is(err, sdk.ErrForbidden) && !errors.Is(err, mutations.ErrConcurrentMutation) {
				t.Fatal(err)
			}
		}
		if relationExists(t, r, a, "viewer", "u") && relationExists(t, r, b, "viewer", "u") {
			t.Fatal("negative guard write skew committed both grants")
		}
	}
}
func specReplaceNoAbsentState(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	r := newRepos(t)
	m := r.Mutations
	mustApply(t, m, grant("d", "owner", "owner"))
	mustApply(t, m, grant("d", "viewer", "u"))
	var wg sync.WaitGroup
	wg.Add(1)
	errs := make(chan error, 1)
	go func() {
		defer wg.Done()
		for range 20 {
			targets, err := r.Relationships.ListRelationshipsBySubject(context.Background(), "user", "u", relationships.SubjectRelationshipFilter{}, list.Request{})
			if err != nil {
				errs <- err
				return
			}
			if len(targets.Items) != 1 {
				errs <- fmt.Errorf("replacement exposed %d tuples", len(targets.Items))
				return
			}
		}
	}()
	for i := 0; i < 10; i++ {
		relation := "viewer"
		if i%2 == 0 {
			relation = "editor"
		}
		mustApply(t, m, replace("d", relation, "u"))
	}
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}
