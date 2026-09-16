package decisions

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// Evaluate validates every branch, then evaluates fixed resource bindings in one
// coherent operation. Runtime slots require EvaluateResolved.
func (s *Service) Evaluate(ctx context.Context, principal authmodel.PrincipalRef, expr Expression) (authmodel.CheckResult, error) {
	log := s.startDecisionLog(ctx, false)
	result, err := s.evaluate(ctx, principal, expr)
	log.check(ctx, "Evaluate", authmodel.CheckRequest{Principal: principal}, result, err)
	return result, err
}

func (s *Service) evaluate(ctx context.Context, principal authmodel.PrincipalRef, expr Expression) (authmodel.CheckResult, error) {
	return s.evaluateResolved(ctx, principal, expr, nil)
}

// EvaluateResolved evaluates one expression with lazily resolved resource inputs.
// Inputs are pinned across cache fallback; facts and budgets are fresh per attempt.
func (s *Service) EvaluateResolved(ctx context.Context, principal authmodel.PrincipalRef, expr Expression, resolve ResourceResolver) (authmodel.CheckResult, error) {
	log := s.startDecisionLog(ctx, false)
	result, err := s.evaluateResolved(ctx, principal, expr, resolve)
	log.check(ctx, "EvaluateResolved", authmodel.CheckRequest{Principal: principal}, result, err)
	return result, err
}

func (s *Service) evaluateResolved(ctx context.Context, principal authmodel.PrincipalRef, expr Expression, resolve ResourceResolver) (authmodel.CheckResult, error) {
	if err := principal.Validate(); err != nil {
		return authmodel.CheckResult{}, err
	}
	hasSlots, err := s.validateAdhocExpression(expr)
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	if hasSlots && resolve == nil {
		return authmodel.CheckResult{}, fmt.Errorf("resource resolver required: %w", sdk.ErrInvalidInput)
	}
	expr = copyExpression(expr)
	resolveSlot := memoResourceResolver(ctx, resolve)
	var result authmodel.CheckResult
	err = s.withOperation(ctx, func(ctx context.Context, view *Service) error {
		var e error
		b := newBudget(view.limits, newMemoReader(view.reader))
		b.resolveSlot = resolveSlot
		result, e = view.evaluateExpression(ctx, authmodel.CheckRequest{Principal: principal}, expr, 0, b, map[stateKey]bool{})
		view.observeDenied(ctx, b, result, e)
		return e
	})
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	return result, nil
}

func (s *Service) EvaluateExpressionWith(ctx context.Context, reader tuples.Reader, principal authmodel.PrincipalRef, expr Expression) (authmodel.CheckResult, error) {
	log := s.startDecisionLog(ctx, true)
	result, err := s.evaluateExpressionWith(ctx, reader, principal, expr)
	log.check(ctx, "EvaluateExpressionWith", authmodel.CheckRequest{Principal: principal}, result, err)
	return result, err
}

func (s *Service) evaluateExpressionWith(ctx context.Context, reader tuples.Reader, principal authmodel.PrincipalRef, expr Expression) (authmodel.CheckResult, error) {
	if err := principal.Validate(); err != nil {
		return authmodel.CheckResult{}, err
	}
	hasSlots, err := s.validateAdhocExpression(expr)
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	if hasSlots {
		return authmodel.CheckResult{}, fmt.Errorf("resource resolver required: %w", sdk.ErrInvalidInput)
	}
	if isNilReader(reader) {
		return authmodel.CheckResult{}, fmt.Errorf("nil tuple reader: %w", sdk.ErrInvalidInput)
	}
	view := s.bound(reader)
	result, err := view.evaluateExpression(ctx, authmodel.CheckRequest{Principal: principal}, copyExpression(expr), 0, newBudget(view.limits, newMemoReader(view.reader)), map[stateKey]bool{})
	if ctx.Err() != nil {
		return authmodel.CheckResult{}, ctx.Err()
	}
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	return result, nil
}
func decision(allowed bool, reason string) authmodel.CheckResult {
	code := authmodel.ReasonDenied
	if allowed {
		code = authmodel.ReasonGranted
	}
	return authmodel.CheckResult{Allowed: allowed, ReasonCode: code, Reason: reason}
}
func (s *Service) evaluateExpression(ctx context.Context, req authmodel.CheckRequest, e Expression, depth int, b *budget, stack map[stateKey]bool) (authmodel.CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if err := b.chargeStep(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if e.AnyOf != nil || e.AllOf != nil {
		all := e.AllOf != nil
		children := e.AnyOf
		if all {
			children = e.AllOf
		}
		for _, child := range children {
			result, err := s.evaluateExpression(ctx, req, child, depth, b, stack)
			if err != nil {
				return authmodel.CheckResult{}, err
			}
			if result.Allowed != all {
				return result, nil
			}
		}
		return decision(all, "expression"), nil
	}
	boundResource := e.Resource != nil || e.ResourceSlot != nil
	if e.Resource != nil {
		req.Resource = *e.Resource
	}
	if e.ResourceSlot != nil {
		resource, applicable, err := b.resolveSlot(ctx, *e.ResourceSlot)
		if err != nil {
			return authmodel.CheckResult{}, err
		}
		if !applicable {
			return decision(false, "resource not applicable"), nil
		}
		req.Resource = resource
	}
	if boundResource && (e.Relation != "" || e.Through != "") {
		if err := b.chargePrimitiveState(req.Resource, e); err != nil {
			return authmodel.CheckResult{}, err
		}
	}
	if e.RoleName != "" {
		scope := tuples.Global()
		if e.CurrentResource || boundResource {
			scope = tuples.On(req.Resource.Type, req.Resource.ID)
		}
		fact := tuples.Tuple{Scope: scope, Relation: e.RoleName, Subject: tuples.SubjectRef{Type: req.Principal.Type, ID: req.Principal.ID}}
		found, err := s.facts.Contains(ctx, fact)
		if err != nil {
			return authmodel.CheckResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return authmodel.CheckResult{}, err
		}
		if !found {
			s.rememberScopedDenial(b, fact)
		}
		b.record(authmodel.ExplainStep{ResourceType: scope.Type, ResourceID: scope.ID, Relation: e.RoleName, Scope: scope, Outcome: outcomeReason(found), Kind: authmodel.ExplainKindExact, Depth: depth, Permission: req.Permission})
		return decision(found, "exact:"+e.RoleName), nil
	}
	if e.NamedPermission != "" {
		req.Permission = e.NamedPermission
		return s.checkPermission(ctx, req, depth, b, stack)
	}
	if e.Through != "" {
		result, err := s.checkThrough(ctx, req, e, depth, b, stack)
		if err != nil {
			return authmodel.CheckResult{}, err
		}
		b.record(authmodel.ExplainStep{ResourceType: req.Resource.Type, ResourceID: req.Resource.ID, Scope: tuples.On(req.Resource.Type, req.Resource.ID), Relation: e.Through, Permission: e.Permission, Outcome: result.ReasonCode, Kind: authmodel.ExplainKindThrough, Depth: depth})
		return result, nil
	}
	found, err := b.reader.CheckRelationWithGroupExpansion(ctx, req.Resource.Type, req.Resource.ID, e.Relation, req.Principal.Type, req.Principal.ID, b.limits.MaxGraphStates)
	if err != nil {
		return authmodel.CheckResult{}, mapExpansionBudget(err)
	}
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, err
	}
	b.record(authmodel.ExplainStep{ResourceType: req.Resource.Type, ResourceID: req.Resource.ID, Scope: tuples.On(req.Resource.Type, req.Resource.ID), Relation: e.Relation, Outcome: outcomeReason(found), Kind: authmodel.ExplainKindDirect, Depth: depth, Permission: req.Permission})
	return decision(found, "direct:"+e.Relation), nil
}

func (c *CompiledModel) ValidateTuple(t tuples.Tuple) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if t.Scope.Kind != tuples.ResourceScope {
		return nil
	}
	subjects, _, constrained := c.relationSubjects(t.Scope.Type, t.Relation)
	if !constrained {
		return nil
	}
	for _, subject := range subjects {
		if subject.Type == t.Subject.Type && subject.Relation == t.Subject.Relation {
			return nil
		}
	}
	return fmt.Errorf("tuple violates declared subject shape: %w", ErrInvalidRelation)
}
