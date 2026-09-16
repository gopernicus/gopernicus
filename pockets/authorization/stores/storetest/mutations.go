package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func runMutations(t *testing.T, newRepos func(*testing.T) Repositories) {
	t.Run("StateTransitions", func(t *testing.T) { specStateTransitions(t, newRepos) })
	t.Run("NoPartialBatch", func(t *testing.T) { specNoPartialBatch(t, newRepos) })
	t.Run("PurgeBlockedTeardownClears", func(t *testing.T) { specPurgeTeardown(t, newRepos) })
	t.Run("CurrentModelEveryApplication", func(t *testing.T) { specCurrentValidation(t, newRepos) })
	t.Run("RoleAssignUnassignTargets", func(t *testing.T) { specRoleTargets(t, newRepos) })
	t.Run("ConcurrentTwoOwnerRevokes", func(t *testing.T) { specConcurrentOwnerRevokes(t, newRepos) })
	t.Run("ConcurrentIdenticalGrants", func(t *testing.T) { specConcurrentGrants(t, newRepos) })
	t.Run("ConcurrentReplaceNoAbsentState", func(t *testing.T) { specReplaceNoAbsentState(t, newRepos) })
	t.Run("IntegrityEstablishesMinimum", func(t *testing.T) { specIntegrityEstablish(t, newRepos) })
	t.Run("IntegrityCountsCanonicalFactsAcrossFacades", func(t *testing.T) { specCrossFacadeIntegrity(t, newRepos) })
	t.Run("ValidatorCancellation", func(t *testing.T) { specCallbackCancellation(t, newRepos) })
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
func swap(resourceID, before, after, subjectID string) mutations.Command {
	return mutations.Command{Target: resTarget(resourceID), Operation: mutations.OpBatch, Tuples: tuples.Changes{Remove: []tuples.Tuple{roleFact("user", subjectID, before, "doc", resourceID)}, Add: []tuples.Tuple{roleFact("user", subjectID, after, "doc", resourceID)}}}
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
func relationExists(t *testing.T, r Repositories, resourceID, relation, subject string) bool {
	t.Helper()
	ok, err := r.Relationships.CheckRelationExists(context.Background(), "doc", resourceID, relation, "user", subject)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}
func specStateTransitions(t *testing.T, newRepos func(*testing.T) Repositories) {
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
		{swap("d", "viewer", "editor", "u"), mutations.OutcomeApplied},
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
	mustApply(t, m, grant("d", "editor", "u"))
	if !relationExists(t, r, "d", "viewer", "u") {
		t.Fatal("independent grant removed original fact")
	}
}
func specNoPartialBatch(t *testing.T, newRepos func(*testing.T) Repositories) {
	r := newRepos(t)
	m := r.Mutations
	mustApply(t, m, grant("d", "owner", "owner"))
	cmd := mutations.Command{Target: resTarget("d"), Operation: mutations.OpBatch, Tuples: tuples.Changes{Add: []tuples.Tuple{roleFact("user", "new", "viewer", "doc", "d"), roleFact("user", "other", "viewer", "doc", "escapes")}}}
	mustReject(t, m, cmd, sdk.ErrInvalidInput)
	if relationExists(t, r, "d", "viewer", "new") {
		t.Fatal("invalid batch partially committed")
	}
	duplicate := grant("d", "viewer", "duplicate")
	duplicate.Relationships = append(duplicate.Relationships, duplicate.Relationships[0])
	mustApply(t, m, duplicate)
	count, err := r.Relationships.CountByResourceAndRelation(t.Context(), "doc", "d", "viewer")
	if err != nil || count != 1 {
		t.Fatalf("duplicate full fact: %d/%v", count, err)
	}
}
func specPurgeTeardown(t *testing.T, newRepos func(*testing.T) Repositories) {
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
	held, err := r.Tuples.Contains(context.Background(), roleFact("user", "u", "editor", "doc", "d"))
	if err != nil || held {
		t.Fatalf("teardown retained role: %v %v", held, err)
	}
	if got := mustApply(t, m, mutations.Command{Target: resTarget("d"), Operation: mutations.OpTeardown}); got.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("empty teardown: %+v", got)
	}
}
func specCurrentValidation(t *testing.T, newRepos func(*testing.T) Repositories) {
	r := newRepos(t)
	m := r.Mutations
	cmd := grant("d", "owner", "owner")
	mustApply(t, m, cmd)
	reject := errors.New("current model rejects command")
	if result, err := m.Apply(context.Background(), cmd, func(mutations.Command) error { return reject }); result != nil || !errors.Is(err, reject) {
		t.Fatalf("no-op skipped current model: %+v %v", result, err)
	}
	if !relationExists(t, r, "d", "owner", "owner") {
		t.Fatal("refused no-op removed original")
	}
}
func specRoleTargets(t *testing.T, newRepos func(*testing.T) Repositories) {
	r := newRepos(t)
	m := r.Mutations
	rows := []mutations.RoleRow{{SubjectType: "user", SubjectID: "u", Role: "editor"}}
	global := mutations.Command{Target: mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "u"}, Operation: mutations.OpRoleAssign, Roles: rows}
	local := mutations.Command{Target: resTarget("d"), Operation: mutations.OpRoleAssign, Roles: rows}
	mustApply(t, m, global)
	mustApply(t, m, grant("d", "owner", "owner"))
	mustApply(t, m, local)
	local.Operation = mutations.OpRoleUnassign
	if result := mustApply(t, m, local); result.Outcome != mutations.OutcomeApplied {
		t.Fatalf("scoped revoke did not apply: %+v", result)
	}
	if result := mustApply(t, m, local); result.Outcome != mutations.OutcomeNotFound {
		t.Fatalf("repeated scoped revoke did not report not-found: %+v", result)
	}
	global.Operation = mutations.OpRoleUnassign
	mustApply(t, m, global)
	mustApply(t, m, local)
	if hasExact(t, r.Tuples, "user", "u", "editor", "", "") {
		t.Fatal("global revoke retained fact")
	}
	bad := global
	bad.Roles = []mutations.RoleRow{{SubjectType: "user", SubjectID: "someone-else", Role: "editor"}}
	mustReject(t, m, bad, sdk.ErrInvalidInput)
}
func specIntegrityEstablish(t *testing.T, newRepos func(*testing.T) Repositories) {
	r := newRepos(t)
	m := r.Mutations
	mustReject(t, m, grant("d", "viewer", "member"), mutations.ErrInvariantBlocked)
	group := grant("d", "owner", "g")
	group.Relationships[0].Subject = relationships.SubjectRef{Type: "group", ID: "g", Relation: "member"}
	mustReject(t, m, group, mutations.ErrInvariantBlocked)
	owner := grant("d", "owner", "owner")
	mustApply(t, m, owner)
	mustReject(t, m, swap("d", "owner", "viewer", "owner"), mutations.ErrInvariantBlocked)
	mustReject(t, m, revoke("d", "owner", "owner"), mutations.ErrInvariantBlocked)
	if !relationExists(t, r, "d", "owner", "owner") {
		t.Fatal("integrity block changed owner")
	}
}
func specConcurrentOwnerRevokes(t *testing.T, newRepos func(*testing.T) Repositories) {
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
func specConcurrentGrants(t *testing.T, newRepos func(*testing.T) Repositories) {
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
func specReplaceNoAbsentState(t *testing.T, newRepos func(*testing.T) Repositories) {
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
	previous := "viewer"
	for i := 0; i < 10; i++ {
		relation := "viewer"
		if i%2 == 0 {
			relation = "editor"
		}
		mustApply(t, m, swap("d", previous, relation, "u"))
		previous = relation
	}
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

func specCrossFacadeIntegrity(t *testing.T, factory func(*testing.T) Repositories) {
	r := factory(t)
	owner := mutations.Command{Target: resTarget("d"), Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "u", Role: "owner"}}}
	mustApply(t, r.Mutations, owner)
	if result := mustApply(t, r.Mutations, grant("d", "owner", "u")); result.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("second facade created a second owner: %+v", result)
	}
	if n, err := r.Relationships.CountByResourceAndRelation(t.Context(), "doc", "d", "owner"); err != nil || n != 1 {
		t.Fatalf("canonical integrity count: %d/%v", n, err)
	}
	owner.Operation = mutations.OpRoleUnassign
	mustReject(t, r.Mutations, owner, mutations.ErrInvariantBlocked)
	mustReject(t, r.Mutations, revoke("d", "owner", "u"), mutations.ErrInvariantBlocked)
	mustReject(t, r.Mutations, swap("d", "owner", "viewer", "u"), mutations.ErrInvariantBlocked)
	second := owner
	second.Operation = mutations.OpRoleAssign
	second.Roles = []mutations.RoleRow{{SubjectType: "user", SubjectID: "other", Role: "owner"}}
	mustApply(t, r.Mutations, second)
	mustApply(t, r.Mutations, swap("d", "owner", "viewer", "u"))
	if !hasExact(t, r.Tuples, "user", "u", "viewer", "doc", "d") || hasExact(t, r.Tuples, "user", "u", "owner", "doc", "d") {
		t.Fatal("atomic swap did not publish its exact delta")
	}
	second.Operation = mutations.OpRoleUnassign
	mustReject(t, r.Mutations, second, mutations.ErrInvariantBlocked)
}
