package relationships

import (
	"context"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// CheckExplain returns the same error class as Check and the authmodel.Explanation holds
// only the steps gathered before the failure. It is never automatically logged
// or exposed to ordinary callers — a host asks for it explicitly.
// explainTrace is the mutable collector hung off the per-decision budget while a
// CheckExplain runs. It is single-goroutine like the budget it rides.
type explainTrace struct {
	steps []authmodel.ExplainStep
}

func (t *explainTrace) explanation(decision authmodel.Reason) authmodel.Explanation {
	return authmodel.Explanation{Decision: decision, Steps: t.steps}
}

// CheckExplain evaluates req exactly as Check does and additionally returns a
// bounded authmodel.Explanation of the rule/path decisions taken. It shares the SAME
// evaluation code and the SAME work budget as Check — the only difference is a
// trace collector on the budget — so an explain request cannot create a separate,
// more permissive evaluator, cannot change the decision, and fails with the same
// limit class (ErrEvaluationLimit) on the same input. The trace excludes raw
// infrastructure errors; a store/limit failure returns the error and the partial
// steps gathered so far.
func (s *Service) CheckExplain(ctx context.Context, req authmodel.CheckRequest) (authmodel.CheckResult, authmodel.Explanation, error) {
	b := newBudget(s.limits, newMemoReader(s.reader))
	b.trace = &explainTrace{}
	res, err := s.check(ctx, req, b)
	return res, b.trace.explanation(res.ReasonCode), err
}
