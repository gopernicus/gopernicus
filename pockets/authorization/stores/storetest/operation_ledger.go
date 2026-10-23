package storetest

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func runOperationLedger(t *testing.T, newRepos func(*testing.T) Repositories) {
	if newRepos(t).Operations == nil {
		t.Fatal("Repositories.Mutations is wired but Repositories.Operations is nil: a mutation store that honors OperationID must expose its OperationLedger")
	}
	runOperationFence(t, newRepos)
	runOperationExpiry(t, newRepos)
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

// runOperationFence proves the fence half of OperationLedger: a fence and Apply
// are totally ordered under one write boundary, a fenced ID replays as fenced for
// any command without touching facts, and Recorded reads the retained outcome.
func runOperationFence(t *testing.T, newRepos func(*testing.T) Repositories) {
	refuseValidation := func(t *testing.T) mutations.SemanticValidator {
		return func(mutations.Command) error {
			t.Error("fenced replay reached the validator")
			return sdk.ErrInvalidInput
		}
	}
	mustFence := func(t *testing.T, r Repositories, id string) *mutations.FenceResult {
		t.Helper()
		got, err := r.Operations.Fence(t.Context(), id)
		if err != nil || got == nil {
			t.Fatalf("Fence(%q): %+v %v", id, got, err)
		}
		return got
	}
	mustRecorded := func(t *testing.T, r Repositories, id string) (mutations.Outcome, bool) {
		t.Helper()
		outcome, ok, err := r.Operations.Recorded(t.Context(), id)
		if err != nil {
			t.Fatalf("Recorded(%q): %v", id, err)
		}
		return outcome, ok
	}
	assertFencedReplay := func(t *testing.T, got *mutations.Result, err error) {
		t.Helper()
		if err != nil || got == nil || got.Outcome != mutations.OutcomeFenced || !got.Replayed || !got.Superseded {
			t.Fatalf("want fenced replay, got %+v %v", got, err)
		}
	}

	t.Run("FenceBeforeApply", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		if got := mustFence(t, r, "invitation:one"); got.Outcome != mutations.OutcomeFenced || got.Recorded {
			t.Fatalf("first fence: %+v", got)
		}
		cmd := grant("d", "viewer", "u")
		cmd.OperationID = "invitation:one"
		got, err := r.Mutations.Apply(t.Context(), cmd, refuseValidation(t))
		assertFencedReplay(t, got, err)
		if relationExists(t, r, "d", "viewer", "u") {
			t.Fatal("fenced operation granted its tuple")
		}
		if outcome, ok := mustRecorded(t, r, "invitation:one"); !ok || outcome != mutations.OutcomeFenced {
			t.Fatalf("Recorded after fence: %q %v", outcome, ok)
		}
	})

	t.Run("FenceSeenAtReplayLookup", func(t *testing.T) {
		// A command admitted by its host before the fence committed reaches the
		// replay lookup afterwards; the fence short-circuits before validation,
		// planning and integrity, so even a command that would be refused is a
		// fenced replay that writes nothing.
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		mustApply(t, r.Mutations, grant("d", "viewer", "keep"))
		mustFence(t, r, "invitation:paused")
		lastOwner := revoke("d", "owner", "o")
		lastOwner.OperationID = "invitation:paused"
		got, err := r.Mutations.Apply(t.Context(), lastOwner, refuseValidation(t))
		assertFencedReplay(t, got, err)
		if !relationExists(t, r, "d", "owner", "o") || !relationExists(t, r, "d", "viewer", "keep") {
			t.Fatal("fenced replay changed facts")
		}
	})

	for _, tc := range []struct {
		name    string
		cmd     mutations.Command
		outcome mutations.Outcome
		present bool
	}{
		{"applied", grant("d", "viewer", "u"), mutations.OutcomeApplied, true},
		{"no_change", grant("d", "owner", "o"), mutations.OutcomeNoChange, true},
		{"not_found", revoke("d", "viewer", "absent"), mutations.OutcomeNotFound, false},
	} {
		t.Run("FenceAfterApply/"+tc.name, func(t *testing.T) {
			r := newRepos(t)
			mustApply(t, r.Mutations, grant("d", "owner", "o"))
			cmd := tc.cmd
			cmd.OperationID = "invitation:committed"
			if first := mustApply(t, r.Mutations, cmd); first.Replayed || first.Outcome != tc.outcome {
				t.Fatalf("first: %+v", first)
			}
			for range 2 {
				if got := mustFence(t, r, cmd.OperationID); got.Outcome != tc.outcome || !got.Recorded {
					t.Fatalf("fence after commit: %+v", got)
				}
			}
			subject := cmd.Relationships[0]
			if relationExists(t, r, "d", subject.Relation, subject.Subject.ID) != tc.present {
				t.Fatal("fence after commit changed facts")
			}
			if replay := mustApply(t, r.Mutations, cmd); !replay.Replayed || replay.Outcome != tc.outcome {
				t.Fatalf("replay after fence-of-committed: %+v", replay)
			}
			if outcome, ok := mustRecorded(t, r, cmd.OperationID); !ok || outcome != tc.outcome {
				t.Fatalf("Recorded: %q %v", outcome, ok)
			}
		})
	}

	t.Run("RecordedAfterTeardown", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		cmd := grant("d", "viewer", "u")
		cmd.OperationID = "invitation:applied"
		mustApply(t, r.Mutations, cmd)
		mustFence(t, r, "invitation:fenced")
		mustApply(t, r.Mutations, mutations.Command{Target: resTarget("d"), Operation: mutations.OpTeardown})
		for id, want := range map[string]mutations.Outcome{"invitation:applied": mutations.OutcomeApplied, "invitation:fenced": mutations.OutcomeFenced} {
			if outcome, ok := mustRecorded(t, r, id); !ok || outcome != want {
				t.Fatalf("Recorded(%q) after teardown: %q %v, want %q", id, outcome, ok, want)
			}
		}
	})

	t.Run("RecordedAbsent", func(t *testing.T) {
		r := newRepos(t)
		if outcome, ok := mustRecorded(t, r, "invitation:never"); ok || outcome != "" {
			t.Fatalf("absent operation: %q %v", outcome, ok)
		}
		for _, id := range []string{"", "bad\x00id", string([]byte{0xff})} {
			if _, _, err := r.Operations.Recorded(t.Context(), id); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Recorded(%q): want ErrInvalidInput, got %v", id, err)
			}
			if got, err := r.Operations.Fence(t.Context(), id); got != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Fence(%q): want ErrInvalidInput, got %+v %v", id, got, err)
			}
		}
		if outcome, ok := mustRecorded(t, r, "invitation:never"); ok || outcome != "" {
			t.Fatalf("Recorded wrote a record: %q %v", outcome, ok)
		}
	})

	t.Run("FenceIgnoresCommandShape", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		mustApply(t, r.Mutations, grant("d", "viewer", "keep"))
		mustFence(t, r, "invitation:shape")
		for _, cmd := range []mutations.Command{
			grant("d", "viewer", "u"),
			grant("d", "editor", "u"),
			revoke("d", "viewer", "keep"),
			swap("d", "viewer", "editor", "keep"),
			{Target: resTarget("d"), Operation: mutations.OpReconcile, Relation: "viewer", Subjects: []tuples.SubjectRef{{Type: "user", ID: "u"}}},
			{Target: resTarget("d"), Operation: mutations.OpPurge},
		} {
			cmd.OperationID = "invitation:shape"
			got, err := r.Mutations.Apply(t.Context(), cmd, refuseValidation(t))
			if errors.Is(err, mutations.ErrOperationMismatch) {
				t.Fatalf("fenced ID compared a fingerprint for %s: %v", cmd.Operation, err)
			}
			assertFencedReplay(t, got, err)
		}
		if !relationExists(t, r, "d", "owner", "o") || !relationExists(t, r, "d", "viewer", "keep") || relationExists(t, r, "d", "viewer", "u") || relationExists(t, r, "d", "editor", "u") || relationExists(t, r, "d", "editor", "keep") {
			t.Fatal("fenced replays changed facts")
		}
	})

	t.Run("FenceIdempotent", func(t *testing.T) {
		r := newRepos(t)
		if got := mustFence(t, r, "invitation:twice"); got.Outcome != mutations.OutcomeFenced || got.Recorded {
			t.Fatalf("first fence: %+v", got)
		}
		for range 2 {
			if got := mustFence(t, r, "invitation:twice"); got.Outcome != mutations.OutcomeFenced || !got.Recorded {
				t.Fatalf("repeat fence: %+v", got)
			}
		}
		if outcome, ok := mustRecorded(t, r, "invitation:twice"); !ok || outcome != mutations.OutcomeFenced {
			t.Fatalf("Recorded: %q %v", outcome, ok)
		}
	})

	t.Run("FenceConcurrentWithApply", func(t *testing.T) {
		r := newRepos(t)
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		cmd := grant("d", "viewer", "u")
		cmd.OperationID = "invitation:race"
		const n = 4
		fences := make(chan *mutations.FenceResult, n)
		applies := make(chan *mutations.Result, n)
		errs := make(chan error, 2*n)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				<-start
				got, err := r.Operations.Fence(t.Context(), cmd.OperationID)
				fences <- got
				errs <- err
			})
			wg.Go(func() {
				<-start
				got, err := r.Mutations.Apply(t.Context(), cmd, nil)
				applies <- got
				errs <- err
			})
		}
		close(start)
		wg.Wait()
		close(fences)
		close(applies)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("racing call: %v", err)
			}
		}
		final, ok := mustRecorded(t, r, cmd.OperationID)
		if !ok || (final != mutations.OutcomeFenced && final != mutations.OutcomeApplied) {
			t.Fatalf("final record: %q %v", final, ok)
		}
		freshFences, freshApplies := 0, 0
		for got := range fences {
			if got == nil || got.Outcome != final {
				t.Fatalf("fence disagrees with final %q: %+v", final, got)
			}
			if !got.Recorded {
				freshFences++
			}
		}
		for got := range applies {
			if got == nil {
				t.Fatal("nil apply result")
			}
			if !got.Replayed {
				freshApplies++
				continue
			}
			if got.Outcome != final || (final == mutations.OutcomeFenced && !got.Superseded) {
				t.Fatalf("apply replay disagrees with final %q: %+v", final, got)
			}
		}
		if final == mutations.OutcomeFenced {
			if freshFences != 1 || freshApplies != 0 || relationExists(t, r, "d", "viewer", "u") {
				t.Fatalf("fence won but fresh fences=%d fresh applies=%d or the tuple exists", freshFences, freshApplies)
			}
		} else if freshFences != 0 || freshApplies != 1 || !relationExists(t, r, "d", "viewer", "u") {
			t.Fatalf("apply won but fresh fences=%d fresh applies=%d or the tuple is missing", freshFences, freshApplies)
		}
	})
}

// expiryMargin is the δ in before = now + δ: stores stamp commit times from
// their own clock, so a cutoff a little past now covers every record written so
// far.
const expiryMargin = time.Second

// runOperationExpiry proves the OperationExpirer contract: only applied,
// no_change and not_found records at or before the cutoff expire, oldest first
// and bounded by limit; fenced records never expire; limit <= 0 is refused; and
// an expired ID reads as never recorded, so Fence then records it as fenced.
func runOperationExpiry(t *testing.T, newRepos func(*testing.T) Repositories) {
	expiryRepos := func(t *testing.T) Repositories {
		t.Helper()
		r := newRepos(t)
		if r.OperationExpiry == nil {
			t.Skip("Repositories.OperationExpiry not wired — operation expiry NOT verified")
		}
		mustApply(t, r.Mutations, grant("d", "owner", "o"))
		return r
	}
	mustExpire := func(t *testing.T, r Repositories, before time.Time, limit, want int) {
		t.Helper()
		got, err := r.OperationExpiry.Expire(t.Context(), before, limit)
		if err != nil || got != want {
			t.Fatalf("Expire(%s, %d) = %d %v, want %d", before.Format(time.RFC3339Nano), limit, got, err, want)
		}
	}
	assertRecorded := func(t *testing.T, r Repositories, want map[string]mutations.Outcome) {
		t.Helper()
		for id, outcome := range want {
			got, ok, err := r.Operations.Recorded(t.Context(), id)
			if err != nil || ok != (outcome != "") || got != outcome {
				t.Fatalf("Recorded(%q) = %q %v %v, want %q", id, got, ok, err, outcome)
			}
		}
	}

	t.Run("ExpireKeepsFenced", func(t *testing.T) {
		r := expiryRepos(t)
		for id, cmd := range map[string]mutations.Command{
			"op:applied":   grant("d", "viewer", "u"),
			"op:no-change": grant("d", "owner", "o"),
			"op:not-found": revoke("d", "viewer", "absent"),
		} {
			cmd.OperationID = id
			mustApply(t, r.Mutations, cmd)
		}
		for _, id := range []string{"op:fenced", "op:fenced-too"} {
			if got, err := r.Operations.Fence(t.Context(), id); err != nil || got.Outcome != mutations.OutcomeFenced {
				t.Fatalf("Fence(%q): %+v %v", id, got, err)
			}
		}
		before := time.Now().Add(expiryMargin)
		mustExpire(t, r, before, 100, 3)
		mustExpire(t, r, before, 100, 0)
		assertRecorded(t, r, map[string]mutations.Outcome{
			"op:applied": "", "op:no-change": "", "op:not-found": "",
			"op:fenced": mutations.OutcomeFenced, "op:fenced-too": mutations.OutcomeFenced,
		})
		if !relationExists(t, r, "d", "viewer", "u") {
			t.Fatal("expiry removed a fact; it deletes operation records only")
		}
	})

	t.Run("ExpireBoundedAndBoundary", func(t *testing.T) {
		r := expiryRepos(t)
		if r.SetOperationCommittedAt == nil {
			t.Fatal("Repositories.OperationExpiry is wired but SetOperationCommittedAt is nil: the expiry boundary cannot be placed")
		}
		// Microsecond-aligned, so every store keeps the fixture times exactly.
		boundary := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
		for _, record := range []struct {
			id      string
			subject string
			at      time.Time
		}{
			{"op:z", "z", boundary.Add(-2 * time.Microsecond)},
			{"op:y", "y", boundary.Add(-time.Microsecond)},
			{"op:x", "x", boundary.Add(-time.Microsecond)},
			{"op:a", "a", boundary},
			{"op:after", "after", boundary.Add(time.Microsecond)},
		} {
			cmd := grant("d", "viewer", record.subject)
			cmd.OperationID = record.id
			mustApply(t, r.Mutations, cmd)
			r.SetOperationCommittedAt(t, record.id, record.at)
		}
		// Oldest first, ties by operation ID: op:a sorts first by ID but is newest.
		mustExpire(t, r, boundary, 2, 2)
		assertRecorded(t, r, map[string]mutations.Outcome{"op:z": "", "op:x": "", "op:y": mutations.OutcomeApplied, "op:a": mutations.OutcomeApplied, "op:after": mutations.OutcomeApplied})
		// A record exactly at the cutoff is eligible.
		mustExpire(t, r, boundary, 2, 2)
		assertRecorded(t, r, map[string]mutations.Outcome{"op:y": "", "op:a": "", "op:after": mutations.OutcomeApplied})
		mustExpire(t, r, boundary, 2, 0)
		mustExpire(t, r, time.Now().Add(expiryMargin), 10, 1)
		assertRecorded(t, r, map[string]mutations.Outcome{"op:after": ""})
	})

	t.Run("ExpireLimitZeroRefused", func(t *testing.T) {
		r := expiryRepos(t)
		cmd := grant("d", "viewer", "u")
		cmd.OperationID = "op:kept"
		mustApply(t, r.Mutations, cmd)
		for _, limit := range []int{0, -1} {
			if got, err := r.OperationExpiry.Expire(t.Context(), time.Now().Add(expiryMargin), limit); got != 0 || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Expire limit %d = %d %v, want sdk.ErrInvalidInput", limit, got, err)
			}
		}
		assertRecorded(t, r, map[string]mutations.Outcome{"op:kept": mutations.OutcomeApplied})
	})

	t.Run("ExpireThenFenceRecordsFenced", func(t *testing.T) {
		r := expiryRepos(t)
		cmd := grant("d", "viewer", "u")
		cmd.OperationID = "invitation:expired"
		mustApply(t, r.Mutations, cmd)
		before := time.Now().Add(expiryMargin)
		mustExpire(t, r, before, 10, 1)
		assertRecorded(t, r, map[string]mutations.Outcome{cmd.OperationID: ""})
		got, err := r.Operations.Fence(t.Context(), cmd.OperationID)
		if err != nil || got.Outcome != mutations.OutcomeFenced || got.Recorded {
			t.Fatalf("Fence after expiry: %+v %v", got, err)
		}
		replay, err := r.Mutations.Apply(t.Context(), cmd, func(mutations.Command) error { return sdk.ErrInvalidInput })
		if err != nil || replay.Outcome != mutations.OutcomeFenced || !replay.Replayed || !replay.Superseded {
			t.Fatalf("Apply after the fence: %+v %v", replay, err)
		}
		mustExpire(t, r, before, 10, 0)
		assertRecorded(t, r, map[string]mutations.Outcome{cmd.OperationID: mutations.OutcomeFenced})
		if !relationExists(t, r, "d", "viewer", "u") {
			t.Fatal("expiry or the fence removed the committed grant")
		}
	})
}
