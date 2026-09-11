package mutations

// Outcome describes this application of a command, not a stored receipt.
type Outcome string

const (
	OutcomeApplied  Outcome = "applied"
	OutcomeNoChange Outcome = "no_change"
	OutcomeNotFound Outcome = "not_found"
	// These evaluator outcomes are returned to callers as errors, never results.
	OutcomeSemanticConflict Outcome = "semantic_conflict"
	OutcomeInvariantBlocked Outcome = "invariant_blocked"
)

func (o Outcome) Valid() bool {
	switch o {
	case OutcomeApplied, OutcomeNoChange, OutcomeNotFound, OutcomeSemanticConflict, OutcomeInvariantBlocked:
		return true
	}
	return false
}
func (o Outcome) Rejection() error {
	switch o {
	case OutcomeSemanticConflict:
		return ErrSemanticConflict
	case OutcomeInvariantBlocked:
		return ErrInvariantBlocked
	}
	return nil
}

// Result describes the current application. SameRoleGrantRemains is computed
// atomically for a resource-target role removal: a matching global role still
// exists. It makes no claim about other paths that may authorize access.
type Result struct {
	Outcome              Outcome
	SameRoleGrantRemains bool
}
