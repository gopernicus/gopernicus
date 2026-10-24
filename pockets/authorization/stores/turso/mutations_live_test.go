//go:build integration

package turso

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func liveRepos(t *testing.T) (*tursodb.DB, storetest.Repositories) { return liveReposWith(t) }
func liveReposNoIntegrity(t *testing.T) (*tursodb.DB, storetest.Repositories) {
	return liveReposWith(t)
}
func liveReposWith(t *testing.T, opts ...Option) (*tursodb.DB, storetest.Repositories) {
	t.Helper()
	url, token := requireTursoEnv(t)
	db := openAndMigrate(t, url, token)
	repos, err := testRepositories(context.Background(), db, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return db, repos
}
func grantCmd(resourceID, relation, subjectID string) mutations.Command {
	return mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: resourceID}, Operation: mutations.OpGrant, Relationships: []mutations.RelationshipRow{{Relation: relation, Subject: relationships.SubjectRef{Type: "user", ID: subjectID}}}}
}
func mustApplyLive(t *testing.T, m mutations.MutationRepository, cmd mutations.Command) *mutations.Result {
	t.Helper()
	r, err := m.Apply(context.Background(), cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func scopeOf(typ, id string) mutations.Target {
	return mutations.Target{Kind: mutations.TargetResource, Type: typ, ID: id}
}
func edge(op mutations.Operation, target mutations.Target, relation string, subject relationships.SubjectRef) mutations.Command {
	return mutations.Command{Target: target, Operation: op, Relationships: []mutations.RelationshipRow{{Relation: relation, Subject: subject}}}
}
func auditContext() context.Context {
	return audit.WithSource(context.Background(), audit.Source{System: "sql-test", Reason: "test change"})
}
func auditRecords(t *testing.T, r storetest.Repositories) []audit.Record {
	t.Helper()
	page, err := r.Audit.List(context.Background(), audit.Filter{}, list.Request{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}
func TestMutationConcurrentNaturalNoOpsAndAudit(t *testing.T) {
	_, repos := liveReposWith(t, WithAudit())
	ctx := auditContext()
	const n = 24
	for wave := range 2 {
		var wg sync.WaitGroup
		errs := make([]error, n)
		results := make([]*mutations.Result, n)
		for i := range n {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i], errs[i] = repos.Mutations.Apply(ctx, grantCmd("storm", "viewer", fmt.Sprint(i)), nil)
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			want := mutations.OutcomeApplied
			if wave == 1 {
				want = mutations.OutcomeNoChange
			}
			if err != nil || results[i] == nil || results[i].Outcome != want {
				t.Fatalf("wave=%d writer=%d result=%+v err=%v", wave, i, results[i], err)
			}
		}
	}
	records := auditRecords(t, repos)
	if len(records) != n {
		t.Fatalf("audit count=%d want=%d", len(records), n)
	}
	seen := map[string]bool{}
	for _, r := range records {
		if r.Change.Tuple.Scope.Kind != tuples.ResourceScope || r.Change.Tuple.Subject.Relation != "" || seen[r.ID] {
			t.Fatalf("invalid/duplicate record: %+v", r)
		}
		seen[r.ID] = true
	}
}
func TestMutationRefusesAmbientBeforeAnyWrite(t *testing.T) {
	db, repos := liveReposWith(t, WithAudit())
	ctx := auditContext()
	err := db.Transact(ctx, func(ctx context.Context) error {
		r, err := repos.Mutations.Apply(ctx, grantCmd("ambient", "viewer", "alice"), nil)
		if r != nil || !errors.Is(err, mutations.ErrMutationInsideTransaction) {
			t.Fatalf("ambient result=%+v err=%v", r, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if records := auditRecords(t, repos); len(records) != 0 {
		t.Fatalf("refusal audit: %+v", records)
	}
}

func TestOperationLedgerRestart(t *testing.T) {
	_, repos := liveRepos(t)
	cmd := grantCmd("restart", "viewer", "alice")
	cmd.OperationID = "invitation:restart"
	first := mustApplyLive(t, repos.Mutations, cmd)
	if first.Replayed || first.Outcome != mutations.OutcomeApplied {
		t.Fatalf("first: %+v", first)
	}
	url, token := requireTursoEnv(t)
	db, err := tursodb.Open(t.Context(), tursodb.Config{URL: sdk.Secret(url), AuthToken: sdk.Secret(token)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fresh, err := testRepositories(t.Context(), db, WithIntegrityPolicy(mutations.IntegrityPolicy{Rules: []mutations.IntegrityRule{{ResourceType: "doc", Relation: "viewer", MinSubjects: 2}}}))
	if err != nil {
		t.Fatal(err)
	}
	replay := mustApplyLive(t, fresh.Mutations, cmd)
	if !replay.Replayed || replay.Superseded || replay.Outcome != first.Outcome {
		t.Fatalf("restart replay: %+v", replay)
	}
	revoke := cmd
	revoke.OperationID = ""
	revoke.Operation = mutations.OpRevoke
	mustApplyLive(t, repos.Mutations, revoke)
	replay = mustApplyLive(t, repos.Mutations, cmd)
	if !replay.Replayed || !replay.Superseded {
		t.Fatalf("revoked replay: %+v", replay)
	}
}

// fenceHoldTime keeps the raw write lock well under hosted sqld's interactive
// transaction bound while both contenders queue behind it.
const fenceHoldTime = 300 * time.Millisecond

// secondHandle opens an independent connection pool, as a second process would.
func secondHandle(t *testing.T) storetest.Repositories {
	t.Helper()
	url, token := requireTursoEnv(t)
	db, err := tursodb.Open(t.Context(), tursodb.Config{URL: sdk.Secret(url), AuthToken: sdk.Secret(token)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return repos
}

func settleFenceRace(t *testing.T, a, b storetest.Repositories, cmd mutations.Command, fence *mutations.FenceResult, apply *mutations.Result) string {
	t.Helper()
	final, ok, err := b.Operations.Recorded(t.Context(), cmd.OperationID)
	if err != nil || !ok {
		t.Fatalf("final record: %q %v %v", final, ok, err)
	}
	tuple, err := a.Relationships.CheckRelationExists(t.Context(), "doc", cmd.Target.ID, "viewer", "user", "alice")
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case !apply.Replayed && apply.Outcome == mutations.OutcomeApplied && fence.Recorded && fence.Outcome == mutations.OutcomeApplied && final == mutations.OutcomeApplied && tuple:
		return "apply"
	case !fence.Recorded && fence.Outcome == mutations.OutcomeFenced && apply.Replayed && apply.Superseded && apply.Outcome == mutations.OutcomeFenced && final == mutations.OutcomeFenced && !tuple:
		return "fence"
	}
	t.Fatalf("no single durable ordering: fence=%+v apply=%+v final=%q tuple=%v", fence, apply, final, tuple)
	return ""
}

// TestFenceContentionTwoConnections holds BEGIN IMMEDIATE in a raw transaction on
// handle A while a Fence (handle B) and an Apply (A, second connection) queue
// behind it, then releases it. SQLite exposes no wait observability, so each
// round holds the lock for fenceHoldTime and the test repeats. Exactly one
// durable ordering results; both pure orderings are pinned as well.
func TestFenceContentionTwoConnections(t *testing.T) {
	adb, a := liveRepos(t)
	b := secondHandle(t)
	t.Run("Contended", func(t *testing.T) {
		const rounds = 5
		seen := map[string]int{}
		for i := range rounds {
			cmd := grantCmd(fmt.Sprintf("contended-%d", i), "viewer", "alice")
			cmd.OperationID = fmt.Sprintf("invitation:contended-%d", i)
			holder, err := adb.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			var fence *mutations.FenceResult
			var apply *mutations.Result
			var fenceErr, applyErr error
			wg.Go(func() { fence, fenceErr = b.Operations.Fence(t.Context(), cmd.OperationID) })
			wg.Go(func() { apply, applyErr = a.Mutations.Apply(t.Context(), cmd, nil) })
			time.Sleep(fenceHoldTime)
			if err := holder.Commit(); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			if fenceErr != nil || applyErr != nil || fence == nil || apply == nil {
				t.Fatalf("contended calls: fence=%+v/%v apply=%+v/%v", fence, fenceErr, apply, applyErr)
			}
			seen[settleFenceRace(t, a, b, cmd, fence, apply)]++
		}
		t.Logf("observed orderings over %d contended rounds: %v", rounds, seen)
	})
	t.Run("FenceThenApply", func(t *testing.T) {
		cmd := grantCmd("fence-first", "viewer", "alice")
		cmd.OperationID = "invitation:fence-first"
		fence, err := b.Operations.Fence(t.Context(), cmd.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if winner := settleFenceRace(t, a, b, cmd, fence, mustApplyLive(t, a.Mutations, cmd)); winner != "fence" {
			t.Fatalf("fence-first ordering: %s", winner)
		}
	})
	t.Run("ApplyThenFence", func(t *testing.T) {
		cmd := grantCmd("apply-first", "viewer", "alice")
		cmd.OperationID = "invitation:apply-first"
		apply := mustApplyLive(t, a.Mutations, cmd)
		fence, err := b.Operations.Fence(t.Context(), cmd.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if winner := settleFenceRace(t, a, b, cmd, fence, apply); winner != "apply" {
			t.Fatalf("apply-first ordering: %s", winner)
		}
	})
}

// TestFenceRestart proves a fence survives the process: a fresh handle replays
// the fenced ID as fenced and Fence reports the earlier record.
func TestFenceRestart(t *testing.T) {
	_, repos := liveRepos(t)
	cmd := grantCmd("fence-restart", "viewer", "alice")
	cmd.OperationID = "invitation:fence-restart"
	if got, err := repos.Operations.Fence(t.Context(), cmd.OperationID); err != nil || got.Outcome != mutations.OutcomeFenced || got.Recorded {
		t.Fatalf("fence: %+v %v", got, err)
	}
	fresh := secondHandle(t)
	replay, err := fresh.Mutations.Apply(t.Context(), cmd, func(mutations.Command) error { return errors.New("fenced replay reached the validator") })
	if err != nil || replay.Outcome != mutations.OutcomeFenced || !replay.Replayed || !replay.Superseded {
		t.Fatalf("restart replay: %+v %v", replay, err)
	}
	if outcome, ok, err := fresh.Operations.Recorded(t.Context(), cmd.OperationID); err != nil || !ok || outcome != mutations.OutcomeFenced {
		t.Fatalf("restart recorded: %q %v %v", outcome, ok, err)
	}
	if got, err := fresh.Operations.Fence(t.Context(), cmd.OperationID); err != nil || got.Outcome != mutations.OutcomeFenced || !got.Recorded {
		t.Fatalf("restart fence: %+v %v", got, err)
	}
	if ok, err := fresh.Relationships.CheckRelationExists(t.Context(), "doc", "fence-restart", "viewer", "user", "alice"); err != nil || ok {
		t.Fatalf("fenced grant landed: %v %v", ok, err)
	}
}

// TestFenceRefusesAmbient refuses a fence inside a host transaction before any
// write, so the ID stays fenceable afterwards.
func TestFenceRefusesAmbient(t *testing.T) {
	db, repos := liveRepos(t)
	err := db.Transact(t.Context(), func(ctx context.Context) error {
		got, err := repos.Operations.Fence(ctx, "invitation:ambient")
		if got != nil || !errors.Is(err, mutations.ErrMutationInsideTransaction) {
			t.Fatalf("ambient fence: %+v %v", got, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repos.Operations.Recorded(t.Context(), "invitation:ambient"); err != nil || ok {
		t.Fatalf("refused fence recorded: %v %v", ok, err)
	}
}
