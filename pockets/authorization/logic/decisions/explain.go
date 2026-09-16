package decisions

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"

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
// steps gathered so far. Snapshot completion failures and cancellation discard
// the provisional decision and trace.
func (s *Service) CheckExplain(ctx context.Context, req authmodel.CheckRequest) (authmodel.CheckResult, authmodel.Explanation, error) {
	if err := req.Validate(); err != nil {
		return authmodel.CheckResult{}, authmodel.Explanation{}, err
	}
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, authmodel.Explanation{}, err
	}
	if !s.DeclaresPermission(req.Resource.Type, req.Permission) {
		return decision(false, "no rules defined"), authmodel.Explanation{Decision: authmodel.ReasonDenied}, nil
	}
	var result authmodel.CheckResult
	var explanation authmodel.Explanation
	var evaluationErr error
	err := s.withOperation(ctx, func(ctx context.Context, view *Service) error {
		result, explanation, evaluationErr = view.checkExplain(ctx, view.reader, req)
		return evaluationErr
	})
	if err != nil {
		// An evaluator failure may retain its partial trace. A failed snapshot
		// completion or cancellation cannot expose a provisional decision/trace.
		if evaluationErr != nil && errors.Is(err, evaluationErr) && ctx.Err() == nil &&
			!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return authmodel.CheckResult{}, explanation, err
		}
		return authmodel.CheckResult{}, authmodel.Explanation{}, err
	}
	return result, explanation, nil
}

// CheckExplainWith traces evaluation using operation-specific model-scoped reads.
func (s *Service) CheckExplainWith(ctx context.Context, source tuples.Reader, req authmodel.CheckRequest) (authmodel.CheckResult, authmodel.Explanation, error) {
	if isNilReader(source) {
		return authmodel.CheckResult{}, authmodel.Explanation{}, fmt.Errorf("nil tuple reader: %w", sdk.ErrInvalidInput)
	}
	view := s.bound(source)
	return view.checkExplain(ctx, view.reader, req)
}

func (s *Service) checkExplain(ctx context.Context, reader CheckReader, req authmodel.CheckRequest) (authmodel.CheckResult, authmodel.Explanation, error) {
	b := newBudget(s.limits, newMemoReader(reader))
	b.trace = &explainTrace{}
	res, err := s.check(ctx, req, b)
	return res, b.trace.explanation(res.ReasonCode), err
}
