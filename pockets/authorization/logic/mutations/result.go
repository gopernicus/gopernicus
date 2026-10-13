package mutations

type Outcome string

const (
	OutcomeApplied  Outcome = "applied"
	OutcomeNoChange Outcome = "no_change"
	OutcomeNotFound Outcome = "not_found"
	// OutcomeFenced is recorded by OperationLedger.Fence: no command can ever commit
	// under that operation ID. Replays return it with Replayed and Superseded set.
	OutcomeFenced Outcome = "fenced"
)

func (o Outcome) Valid() bool {
	return o == OutcomeApplied || o == OutcomeNoChange || o == OutcomeNotFound || o == OutcomeFenced
}

type Result struct {
	Outcome Outcome
	// Replayed returns the recorded outcome without writing again.
	Replayed bool
	// Superseded is only set on replay when requested facts are no longer in
	// effect, or when the operation was fenced.
	Superseded bool
}
