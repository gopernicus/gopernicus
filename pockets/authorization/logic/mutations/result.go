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

type Result struct{ Outcome Outcome }
