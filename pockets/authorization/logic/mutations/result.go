package mutations

type Outcome string

const (
	OutcomeApplied  Outcome = "applied"
	OutcomeNoChange Outcome = "no_change"
	OutcomeNotFound Outcome = "not_found"
)

func (o Outcome) Valid() bool {
	return o == OutcomeApplied || o == OutcomeNoChange || o == OutcomeNotFound
}

type Result struct {
	Outcome Outcome
	// Replayed returns the recorded outcome without writing again.
	Replayed bool
	// Superseded is only set on replay when requested facts are no longer in effect.
	Superseded bool
}
