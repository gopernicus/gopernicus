package storetest

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func runOperationLedger(t *testing.T, newRepos func(*testing.T) Repositories) {
	for _, change := range []string{"revoke", "replace", "teardown"} {
		t.Run("ReplayAfter/"+change, func(t *testing.T) {
			r := newRepos(t)
			mustApply(t, r.Mutations, grant("d", "owner", "owner"))
			cmd := grant("d", "viewer", "u")
			cmd.OperationID = "invitation:one"
			first := mustApply(t, r.Mutations, cmd)
			if first.Replayed || first.Superseded || first.Outcome != mutations.OutcomeApplied {
				t.Fatalf("first: %+v", first)
			}
			replay, err := r.Mutations.Apply(t.Context(), cmd, func(mutations.Command) error { return sdk.ErrInvalidInput })
			if err != nil || !replay.Replayed || replay.Superseded || replay.Outcome != first.Outcome {
				t.Fatalf("replay: %+v %v", replay, err)
			}
			switch change {
			case "revoke":
				mustApply(t, r.Mutations, revoke("d", "viewer", "u"))
			case "replace":
				mustApply(t, r.Mutations, swap("d", "viewer", "editor", "u"))
			case "teardown":
				mustApply(t, r.Mutations, mutations.Command{Target: resTarget("d"), Operation: mutations.OpTeardown})
			}
			replay = mustApply(t, r.Mutations, cmd)
			if !replay.Replayed || !replay.Superseded || replay.Outcome != first.Outcome || relationExists(t, r, "d", "viewer", "u") {
				t.Fatalf("restored superseded grant: %+v", replay)
			}
		})
	}
	for _, change := range []string{"revoke", "replace"} {
		t.Run("ReplayRace/"+change, func(t *testing.T) {
			r := newRepos(t)
			mustApply(t, r.Mutations, grant("d", "owner", "o"))
			cmd := grant("d", "viewer", "u")
			cmd.OperationID = "invitation:race"
			first := mustApply(t, r.Mutations, cmd)
			manager := revoke("d", "viewer", "u")
			if change == "replace" {
				manager = swap("d", "viewer", "editor", "u")
			}
			const n = 8
			type answer struct {
				result *mutations.Result
				err    error
			}
			answers := make(chan answer, n)
			managerAnswer := make(chan answer, 1)
			start := make(chan struct{})
			var ready, done sync.WaitGroup
			var validations atomic.Int32
			validate := func(mutations.Command) error { validations.Add(1); return sdk.ErrInvalidInput }
			ready.Add(n + 1)
			for range n {
				done.Go(func() {
					ready.Done()
					<-start
					got, err := r.Mutations.Apply(t.Context(), cmd, validate)
					answers <- answer{got, err}
				})
			}
			done.Go(func() {
				ready.Done()
				<-start
				got, err := r.Mutations.Apply(t.Context(), manager, nil)
				managerAnswer <- answer{got, err}
			})
			ready.Wait()
			close(start)
			done.Wait()
			close(answers)
			for got := range answers {
				if got.err != nil || got.result == nil || !got.result.Replayed || got.result.Outcome != first.Outcome {
					t.Fatalf("racing replay: %+v %v", got.result, got.err)
				}
			}
			changed := <-managerAnswer
			if changed.err != nil || changed.result == nil || changed.result.Replayed || changed.result.Outcome != mutations.OutcomeApplied {
				t.Fatalf("manager change: %+v %v", changed.result, changed.err)
			}
			if validations.Load() != 0 {
				t.Fatalf("replay called validator %d times", validations.Load())
			}
			if relationExists(t, r, "d", "viewer", "u") {
				t.Fatal("racing retry restored original grant")
			}
			if change == "replace" && !relationExists(t, r, "d", "editor", "u") {
				t.Fatal("racing retry lost replacement role")
			}
			replay := mustApply(t, r.Mutations, cmd)
			if !replay.Replayed || !replay.Superseded || replay.Outcome != first.Outcome {
				t.Fatalf("post-race replay: %+v", replay)
			}
		})
	}

	t.Run("Reconcile", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		cmd := mutations.Command{Target: resTarget("d"), Operation: mutations.OpReconcile, Relation: "viewer", Subjects: []tuples.SubjectRef{{Type: "user", ID: "u"}}, OperationID: "one"}
		mustApply(t, r.Mutations, cmd)
		mustApply(t, r.Mutations, grant("d", "viewer", "other"))
		if got := mustApply(t, r.Mutations, cmd); !got.Replayed || !got.Superseded || !relationExists(t, r, "d", "viewer", "other") {
			t.Fatalf("reconcile replay changed newer set: %+v", got)
		}
	})
	t.Run("EmptyOperationID", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		cmd := grant("d", "viewer", "u")
		mustApply(t, r.Mutations, cmd)
		mustApply(t, r.Mutations, revoke("d", "viewer", "u"))
		if got := mustApply(t, r.Mutations, cmd); got.Replayed || got.Superseded || got.Outcome != mutations.OutcomeApplied || !relationExists(t, r, "d", "viewer", "u") {
			t.Fatalf("empty ID no longer state-based: %+v", got)
		}
	})

	t.Run("Mismatch", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		cmd := grant("d", "viewer", "u")
		cmd.OperationID = "one"
		mustApply(t, r.Mutations, cmd)
		cmd.Relationships[0].Relation = "editor"
		got, err := r.Mutations.Apply(t.Context(), cmd, func(mutations.Command) error { t.Fatal("mismatch reached validator"); return nil })
		if got != nil || !errors.Is(err, mutations.ErrOperationMismatch) || !errors.Is(err, sdk.ErrConflict) || relationExists(t, r, "d", "editor", "u") {
			t.Fatalf("mismatch: %+v %v", got, err)
		}
	})
	t.Run("RefusalNotRecorded", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "u"))
		cmd := revoke("d", "owner", "u")
		cmd.OperationID = "one"
		mustReject(t, r.Mutations, cmd, mutations.ErrInvariantBlocked)
		mustApply(t, r.Mutations, grant("d", "owner", "other"))
		if got := mustApply(t, r.Mutations, cmd); got.Replayed || got.Outcome != mutations.OutcomeApplied {
			t.Fatalf("refusal recorded: %+v", got)
		}
	})
	t.Run("Concurrent", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		cmd := grant("d", "viewer", "u")
		cmd.OperationID = "one"
		const n = 8
		results := make(chan *mutations.Result, n)
		errs := make(chan error, n)
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() { got, err := r.Mutations.Apply(t.Context(), cmd, nil); results <- got; errs <- err })
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		fresh := 0
		for got := range results {
			if got == nil || got.Superseded {
				t.Fatalf("result: %+v", got)
			}
			if !got.Replayed {
				fresh++
			}
		}
		if fresh != 1 {
			t.Fatalf("fresh=%d", fresh)
		}
	})
	t.Run("RecordedNoChange", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		cmd := grant("d", "viewer", "u")
		mustApply(t, r.Mutations, cmd)
		cmd.OperationID = "one"
		if got := mustApply(t, r.Mutations, cmd); got.Outcome != mutations.OutcomeNoChange {
			t.Fatalf("first: %+v", got)
		}
		mustApply(t, r.Mutations, revoke("d", "viewer", "u"))
		got := mustApply(t, r.Mutations, cmd)
		if !got.Replayed || !got.Superseded || got.Outcome != mutations.OutcomeNoChange {
			t.Fatalf("no-change replay: %+v", got)
		}
	})
	t.Run("RecordedNotFound", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		cmd := revoke("d", "viewer", "u")
		cmd.OperationID = "one"
		if got := mustApply(t, r.Mutations, cmd); got.Outcome != mutations.OutcomeNotFound {
			t.Fatalf("first: %+v", got)
		}
		mustApply(t, r.Mutations, grant("d", "viewer", "u"))
		got := mustApply(t, r.Mutations, cmd)
		if !got.Replayed || !got.Superseded || got.Outcome != mutations.OutcomeNotFound || !relationExists(t, r, "d", "viewer", "u") {
			t.Fatalf("not-found replay: %+v", got)
		}
	})
}
