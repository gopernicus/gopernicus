package decisions

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauthorization "github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

// ExpressionCheck is one principal-bound ad hoc expression for EvaluateBatch.
// Every leaf names a fixed resource (On) or the global scope; runtime slots
// need EvaluateResolved.
type ExpressionCheck struct {
	Principal  authmodel.PrincipalRef
	Expression Expression
}

// EvaluateBatch evaluates every expression in ONE coherent operation view, each
// with its own work budget, and returns the decisions in request order. It is
// all-or-nothing: every expression is validated before any read, the first
// failing expression fails the call, and no partial results are returned. A
// batch larger than MaxBatchSize is ErrEvaluationLimit. It lets one batch mix
// named permissions, relations and global roles under one snapshot.
func (s *Service) EvaluateBatch(ctx context.Context, checks []ExpressionCheck) ([]sdkauthorization.Decision, error) {
	log := s.startDecisionLog(ctx, false)
	results, err := s.evaluateBatch(ctx, checks)
	log.batch(ctx, "EvaluateBatch", len(checks), results, err)
	if err != nil {
		return nil, err
	}
	return decisionsOf(results), nil
}

func (s *Service) evaluateBatch(ctx context.Context, checks []ExpressionCheck) ([]authmodel.CheckResult, error) {
	if len(checks) == 0 {
		return nil, nil
	}
	if len(checks) > s.limits.MaxBatchSize {
		return nil, authmodel.ErrEvaluationLimit
	}
	expressions := make([]Expression, len(checks))
	for i, check := range checks {
		if err := authmodel.ValidatePrincipal(check.Principal); err != nil {
			return nil, err
		}
		hasSlots, err := s.validateAdhocExpression(check.Expression)
		if err != nil {
			return nil, err
		}
		if hasSlots {
			return nil, fmt.Errorf("EvaluateBatch expressions cannot carry resource slots: %w", sdk.ErrInvalidInput)
		}
		expressions[i] = copyExpression(check.Expression)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var results []authmodel.CheckResult
	err := s.withOperation(ctx, func(ctx context.Context, view *Service) error {
		results = make([]authmodel.CheckResult, len(expressions))
		reader := newMemoReader(view.reader)
		resolveSlot := memoResourceResolver(ctx, nil)
		for i, expression := range expressions {
			b := newBudget(view.limits, reader)
			b.resolveSlot = resolveSlot
			result, err := view.evaluateExpression(ctx, authmodel.CheckRequest{Principal: checks[i].Principal}, expression, 0, b, map[stateKey]bool{})
			view.observeDenied(ctx, b, result, err)
			if err != nil {
				return err
			}
			results[i] = result
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}
