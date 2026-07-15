package mutation_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
)

// TestIdempotencyOutcomePersistenceContract pins which outcomes commit a durable,
// replayable receipt. This is the retention contract every store must honor:
// applied/no_change/not_found persist; semantic_conflict/invariant_blocked do not
// (so their MutationID is not consumed and a retry re-evaluates).
func TestIdempotencyOutcomePersistenceContract(t *testing.T) {
	persisted := map[mutation.Outcome]bool{
		mutation.OutcomeApplied:          true,
		mutation.OutcomeNoChange:         true,
		mutation.OutcomeNotFound:         true,
		mutation.OutcomeSemanticConflict: false,
		mutation.OutcomeInvariantBlocked: false,
	}
	for o, want := range persisted {
		if !o.Valid() {
			t.Fatalf("%q must be a valid outcome", o)
		}
		if got := o.Persisted(); got != want {
			t.Fatalf("Outcome(%q).Persisted() = %v, want %v", o, got, want)
		}
	}
	if mutation.Outcome("bogus").Valid() {
		t.Fatalf("an unknown outcome must not be Valid")
	}
	if mutation.Outcome("bogus").Persisted() {
		t.Fatalf("an unknown outcome must not be Persisted")
	}
}

// TestReplayIsIndependentOfOutcome proves Replayed is metadata carried on the
// receipt, never itself an outcome value.
func TestReplayIsIndependentOfOutcome(t *testing.T) {
	r := mutation.Receipt{Outcome: mutation.OutcomeApplied, Replayed: false}
	if r.Replayed {
		t.Fatalf("first application must not be a replay")
	}
	replay := r
	replay.Replayed = true
	if replay.Outcome != mutation.OutcomeApplied {
		t.Fatalf("a replay must preserve the original outcome, not become an outcome itself")
	}
	// Replayed is not part of the Outcome vocabulary.
	if mutation.Outcome("replayed").Valid() {
		t.Fatalf("'replayed' must never be a domain outcome")
	}
}
