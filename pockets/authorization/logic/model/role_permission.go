package model

import (
	"context"
)

// EvaluateRolePermission evaluates the compiled model's sorted grantor roles
// using the caller's scoped probe. Ordinary decisions supply their read service;
// guarded decisions supply the transaction-bound dependency-tracking view.
// limits must be resolved. One step is charged for the root and one for each
// visited grantor, even when the caller can reuse a previous store result.
func EvaluateRolePermission(ctx context.Context, model *CompiledRoleModel, req CheckRequest, limits EvaluationLimits, hasRole func(context.Context, string) (bool, error)) (CheckResult, error) {
	if err := req.Validate(); err != nil {
		return CheckResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CheckResult{}, err
	}
	if limits.MaxEvaluationSteps < 1 {
		return CheckResult{}, ErrEvaluationLimit
	}
	remaining := limits.MaxEvaluationSteps - 1 // the root decision
	grantors := model.Grantors(req.Resource.Type, req.Permission)
	if grantors == nil {
		return CheckResult{Allowed: false, ReasonCode: ReasonDenied, Reason: "no rules defined"}, nil
	}
	for _, name := range grantors {
		if err := ctx.Err(); err != nil {
			return CheckResult{}, err
		}
		if remaining == 0 {
			return CheckResult{}, ErrEvaluationLimit
		}
		remaining--
		held, err := hasRole(ctx, name)
		if err != nil {
			return CheckResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return CheckResult{}, err
		}
		if held {
			return CheckResult{Allowed: true, ReasonCode: ReasonGranted, Reason: "role:" + name}, nil
		}
	}
	return CheckResult{Allowed: false, ReasonCode: ReasonDenied, Reason: "no matching role"}, nil
}
