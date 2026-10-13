package pgx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func liveRepos(t *testing.T) (*pgxdb.DB, storetest.Repositories) { return liveReposWith(t) }
func liveReposNoIntegrity(t *testing.T) (*pgxdb.DB, storetest.Repositories) {
	return liveReposWith(t)
}
func liveReposWith(t *testing.T, opts ...Option) (*pgxdb.DB, storetest.Repositories) {
	t.Helper()
	db := openAndMigrate(t, requireDSN(t))
	repos, err := testRepositories(context.Background(), db, append(storeOptions(t), opts...)...)
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

	db, err := pgxdb.Open(t.Context(), pgxdb.Config{DSN: requireDSN(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fresh, err := testRepositories(t.Context(), db, append(storeOptions(t), WithIntegrityPolicy(mutations.IntegrityPolicy{Rules: []mutations.IntegrityRule{{ResourceType: "doc", Relation: "viewer", MinSubjects: 2}}}))...)
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

// secondHandle opens an independent pool over the same database, as a second
// process would.
func secondHandle(t *testing.T) storetest.Repositories {
	t.Helper()
	db, err := pgxdb.Open(t.Context(), pgxdb.Config{DSN: requireDSN(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repos, err := testRepositories(t.Context(), db, storeOptions(t)...)
	if err != nil {
		t.Fatal(err)
	}
	return repos
}

// fenceRace reports how one Fence and one Apply under the same ID settled.
type fenceRace struct {
	fence  *mutations.FenceResult
	apply  *mutations.Result
	final  mutations.Outcome
	tuple  bool
	winner string
}

func settleFenceRace(t *testing.T, a, b storetest.Repositories, cmd mutations.Command, fence *mutations.FenceResult, apply *mutations.Result) fenceRace {
	t.Helper()
	final, ok, err := b.Operations.Recorded(t.Context(), cmd.OperationID)
	if err != nil || !ok {
		t.Fatalf("final record: %q %v %v", final, ok, err)
	}
	tuple, err := a.Relationships.CheckRelationExists(t.Context(), "doc", cmd.Target.ID, "viewer", "user", "alice")
	if err != nil {
		t.Fatal(err)
	}
	race := fenceRace{fence: fence, apply: apply, final: final, tuple: tuple}
	switch {
	case !apply.Replayed && apply.Outcome == mutations.OutcomeApplied && fence.Recorded && fence.Outcome == mutations.OutcomeApplied && final == mutations.OutcomeApplied && tuple:
		race.winner = "apply"
	case !fence.Recorded && fence.Outcome == mutations.OutcomeFenced && apply.Replayed && apply.Superseded && apply.Outcome == mutations.OutcomeFenced && final == mutations.OutcomeFenced && !tuple:
		race.winner = "fence"
	default:
		t.Fatalf("no single durable ordering: fence=%+v apply=%+v final=%q tuple=%v", fence, apply, final, tuple)
	}
	return race
}

// TestFenceContentionTwoConnections holds the authorization write lock in a raw
// transaction on handle A while a Fence (handle B) and an Apply (A, second
// connection) both wait on it, then releases it: exactly one durable ordering
// results. Both pure orderings are pinned as well.
func TestFenceContentionTwoConnections(t *testing.T) {
	adb, a := liveRepos(t)
	b := secondHandle(t)
	t.Run("Contended", func(t *testing.T) {
		seen := map[string]int{}
		for i := range 6 {
			cmd := grantCmd(fmt.Sprintf("contended-%d", i), "viewer", "alice")
			cmd.OperationID = fmt.Sprintf("invitation:contended-%d", i)
			holder, err := adb.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := holder.Exec(t.Context(), "LOCK TABLE "+qualify(t, "iam_tuples")+" IN SHARE ROW EXCLUSIVE MODE"); err != nil {
				holder.Rollback()
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			var fence *mutations.FenceResult
			var apply *mutations.Result
			var fenceErr, applyErr error
			wg.Go(func() { fence, fenceErr = b.Operations.Fence(t.Context(), cmd.OperationID) })
			wg.Go(func() { apply, applyErr = a.Mutations.Apply(t.Context(), cmd, nil) })
			waitForLockWaiters(t, adb, 2)
			if err := holder.Commit(); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			if fenceErr != nil || applyErr != nil || fence == nil || apply == nil {
				t.Fatalf("contended calls: fence=%+v/%v apply=%+v/%v", fence, fenceErr, apply, applyErr)
			}
			seen[settleFenceRace(t, a, b, cmd, fence, apply).winner]++
		}
		t.Logf("observed orderings over 6 contended rounds: %v", seen)
	})
	t.Run("FenceThenApply", func(t *testing.T) {
		cmd := grantCmd("fence-first", "viewer", "alice")
		cmd.OperationID = "invitation:fence-first"
		fence, err := b.Operations.Fence(t.Context(), cmd.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if race := settleFenceRace(t, a, b, cmd, fence, mustApplyLive(t, a.Mutations, cmd)); race.winner != "fence" {
			t.Fatalf("fence-first ordering: %+v", race)
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
		if race := settleFenceRace(t, a, b, cmd, fence, apply); race.winner != "apply" {
			t.Fatalf("apply-first ordering: %+v", race)
		}
	})
}

// waitForLockWaiters polls pg_locks until n backends wait on iam_tuples.
func waitForLockWaiters(t *testing.T, db *pgxdb.DB, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.QueryRow(t.Context(), `SELECT count(*) FROM pg_catalog.pg_locks WHERE NOT granted AND relation = $1::regclass AND database = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())`, qualify(t, "iam_tuples")).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d lock waiters after 10s", waiting, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
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
