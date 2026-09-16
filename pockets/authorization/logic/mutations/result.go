package mutations

type Outcome string

const (
	OutcomeApplied          Outcome = "applied"
	OutcomeNoChange         Outcome = "no_change"
	OutcomeNotFound         Outcome = "not_found"
	OutcomeInvariantBlocked Outcome = "invariant_blocked"
)

func (o Outcome) Valid() bool {
	return o == OutcomeApplied || o == OutcomeNoChange || o == OutcomeNotFound || o == OutcomeInvariantBlocked
}
func (o Outcome) Rejection() error {
	if o == OutcomeInvariantBlocked {
		return ErrInvariantBlocked
	}
	return nil
}

type Result struct{ Outcome Outcome }
