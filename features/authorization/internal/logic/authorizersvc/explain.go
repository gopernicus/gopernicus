package authorizersvc

import "context"

// Explain step kinds — the coarse shape of the rule/path decision a step records.
const (
	// ExplainKindDirect is a direct-relation check on the current resource.
	ExplainKindDirect = "direct"
	// ExplainKindThrough is a Through traversal of a relation to another resource.
	ExplainKindThrough = "through"
)

// ExplainStep is one coarse rule/path decision recorded during a traced Check. It
// carries SCHEMA-level identifiers and a stable Outcome code ONLY — never a raw
// infrastructure error string, a stack trace, or any secret. A step is emitted
// as each schema check on the evaluated path resolves; nested Through steps are
// recorded before the parent step that traversed into them.
type ExplainStep struct {
	// ResourceType, ResourceID, and Permission name the (resource, permission)
	// state whose rule this step evaluated.
	ResourceType string
	ResourceID   string
	Permission   string
	// Relation is the direct relation examined (Kind == ExplainKindDirect) or the
	// relation traversed (Kind == ExplainKindThrough).
	Relation string
	Kind     string
	// Depth is the number of Through hops taken to reach this state (0 at the top
	// resource).
	Depth int
	// Outcome is the stable coarse result of this check: ReasonGranted or
	// ReasonDenied.
	Outcome Reason
}

// Explanation is the opt-in, bounded trace returned beside a CheckExplain
// decision. Decision equals the CheckResult.ReasonCode (ReasonGranted or
// ReasonDenied); Steps are the coarse rule/path decisions the engine took. It
// records NO raw infrastructure error: on an evaluation-limit or store failure
// CheckExplain returns the same error class as Check and the Explanation holds
// only the steps gathered before the failure. It is never automatically logged
// or exposed to ordinary callers — a host asks for it explicitly.
type Explanation struct {
	Decision Reason
	Steps    []ExplainStep
}

// explainTrace is the mutable collector hung off the per-decision budget while a
// CheckExplain runs. It is single-goroutine like the budget it rides.
type explainTrace struct {
	steps []ExplainStep
}

func (t *explainTrace) explanation(decision Reason) Explanation {
	return Explanation{Decision: decision, Steps: t.steps}
}

// CheckExplain evaluates req exactly as Check does and additionally returns a
// bounded Explanation of the rule/path decisions taken. It shares the SAME
// evaluation code and the SAME work budget as Check — the only difference is a
// trace collector on the budget — so an explain request cannot create a separate,
// more permissive evaluator, cannot change the decision, and fails with the same
// limit class (ErrEvaluationLimit) on the same input. The trace excludes raw
// infrastructure errors; a store/limit failure returns the error and the partial
// steps gathered so far.
func (s *Service) CheckExplain(ctx context.Context, req CheckRequest) (CheckResult, Explanation, error) {
	b := newBudget(s.limits)
	b.trace = &explainTrace{}
	res, err := s.check(ctx, req, b)
	return res, b.trace.explanation(res.ReasonCode), err
}
