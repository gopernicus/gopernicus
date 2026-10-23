package decisions

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// expressionTarget is what an unbound ad hoc evaluation reports in its decision
// event and log line. The walk records the expression's own reached leaves,
// never the model rules a leaf expands into.
type expressionTarget struct {
	composite          bool
	first, firstDenied leafTarget
	leaves, denied     int
}

type leafTarget struct {
	permission string
	resource   authmodel.Resource
}

// Evaluate validates every branch, then evaluates fixed resource bindings in one
// coherent operation. Runtime slots require EvaluateResolved.
func (s *Service) Evaluate(ctx context.Context, principal authmodel.PrincipalRef, expr Expression) (authmodel.CheckResult, error) {
	log := s.startDecisionLog(ctx, false)
	result, target, err := s.evaluate(ctx, principal, expr)
	log.expression(ctx, "Evaluate", principal, target, result, err)
	return result, err
}

func (s *Service) evaluate(ctx context.Context, principal authmodel.PrincipalRef, expr Expression) (authmodel.CheckResult, expressionTarget, error) {
	return s.evaluateResolved(ctx, principal, expr, nil)
}

// EvaluateResolved evaluates one expression with lazily resolved resource inputs.
// Inputs are pinned across cache fallback; facts and budgets are fresh per attempt.
func (s *Service) EvaluateResolved(ctx context.Context, principal authmodel.PrincipalRef, expr Expression, resolve ResourceResolver) (authmodel.CheckResult, error) {
	log := s.startDecisionLog(ctx, false)
	result, target, err := s.evaluateResolved(ctx, principal, expr, resolve)
	log.expression(ctx, "EvaluateResolved", principal, target, result, err)
	return result, err
}

func (s *Service) evaluateResolved(ctx context.Context, principal authmodel.PrincipalRef, expr Expression, resolve ResourceResolver) (authmodel.CheckResult, expressionTarget, error) {
	if err := authmodel.ValidatePrincipal(principal); err != nil {
		return authmodel.CheckResult{}, expressionTarget{}, err
	}
	hasSlots, err := s.validateAdhocExpression(expr)
	if err != nil {
		return authmodel.CheckResult{}, expressionTarget{}, err
	}
	if hasSlots && resolve == nil {
		return authmodel.CheckResult{}, expressionTarget{}, fmt.Errorf("resource resolver required: %w", sdk.ErrInvalidInput)
	}
	expr = copyExpression(expr)
	resolveSlot := memoResourceResolver(ctx, resolve)
	// One captured value carries both outputs, so the target costs no allocation.
	var out struct {
		result authmodel.CheckResult
		target expressionTarget
	}
	err = s.withOperation(ctx, func(ctx context.Context, view *Service) error {
		var e error
		b := newBudget(view.limits, newMemoReader(view.reader))
		b.resolveSlot = resolveSlot
		out.target = expressionTarget{composite: expr.AnyOf != nil || expr.AllOf != nil}
		b.target = &out.target
		out.result, e = view.evaluateExpression(ctx, authmodel.CheckRequest{Principal: principal}, expr, 0, b, map[stateKey]bool{})
		view.observeDenied(ctx, b, out.result, e)
		return e
	})
	if err != nil {
		return authmodel.CheckResult{}, expressionTarget{}, err
	}
	return out.result, out.target, nil
}

func (s *Service) EvaluateExpressionWith(ctx context.Context, reader tuples.Reader, principal authmodel.PrincipalRef, expr Expression) (authmodel.CheckResult, error) {
	log := s.startDecisionLog(ctx, true)
	result, err := s.evaluateExpressionWith(ctx, reader, principal, expr)
	log.check(ctx, "EvaluateExpressionWith", authmodel.CheckRequest{Principal: principal}, result, err)
	return result, err
}

func (s *Service) evaluateExpressionWith(ctx context.Context, reader tuples.Reader, principal authmodel.PrincipalRef, expr Expression) (authmodel.CheckResult, error) {
	if err := authmodel.ValidatePrincipal(principal); err != nil {
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
	view := s.bound(ctx, reader)
	result, err := view.evaluateExpression(ctx, authmodel.CheckRequest{Principal: principal}, copyExpression(expr), 0, newBudget(view.limits, newMemoReader(view.reader)), map[stateKey]bool{})
	if ctx.Err() != nil {
		return authmodel.CheckResult{}, ctx.Err()
	}
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	return result, nil
}

// record notes one decided leaf. resource is the leaf's resolved resource or
// scope, empty for a global role or an input that was not applicable.
func (t *expressionTarget) record(e Expression, resource authmodel.Resource, allowed bool) {
	leaf := leafTarget{permission: e.Relation, resource: resource}
	switch {
	case e.NamedPermission != "":
		leaf.permission = e.NamedPermission
	case e.Through != "":
		leaf.permission = e.Permission
	case e.RoleName != "":
		leaf.permission = e.RoleName
	}
	if t.leaves == 0 {
		t.first = leaf
	}
	if !allowed {
		if t.denied == 0 {
			t.firstDenied = leaf
		}
		t.denied++
	}
	t.leaves++
}

// of is the first denied leaf of an expression that was not allowed, otherwise
// the first leaf reached.
func (t *expressionTarget) of(allowed bool) leafTarget {
	if !allowed && t.denied > 0 {
		return t.firstDenied
	}
	return t.first
}

func decision(allowed bool, reason string) authmodel.CheckResult {
	code := authmodel.ReasonDenied
	if allowed {
		code = authmodel.ReasonGranted
	}
	return authmodel.CheckResult{Allowed: allowed, ReasonCode: code, Reason: reason}
}
func (s *Service) evaluateExpression(ctx context.Context, req authmodel.CheckRequest, e Expression, depth int, b *budget, stack map[stateKey]bool) (result authmodel.CheckResult, err error) {
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
	if target := b.target; target != nil {
		// Only the ad hoc expression's own leaves are its target: the model rules
		// this leaf reaches walk without one.
		b.target = nil
		defer func() {
			b.target = target
			if err == nil {
				target.record(e, req.Resource, result.Allowed)
			}
		}()
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
	if result, done, err := s.directShortCircuit(ctx, req, e, depth, b); err != nil || done {
		return result, err
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

// directShortCircuit answers a Direct node from the resource's forward set when
// that is exact: a concrete principal tuple grants, and a miss denies when the
// relation admits no userset subject. Otherwise done is false and the caller
// expands subject-first. The node's step is already charged; this charges none.
func (s *Service) directShortCircuit(ctx context.Context, req authmodel.CheckRequest, e Expression, depth int, b *budget) (authmodel.CheckResult, bool, error) {
	if !s.shortCircuits(req.Resource.Type, e.Relation, req.Principal.Type) {
		return authmodel.CheckResult{}, false, nil
	}
	fact, ok := directTuple(req.Resource.Type, req.Resource.ID, e.Relation, req.Principal)
	if !ok {
		return authmodel.CheckResult{}, false, nil
	}
	found, err := s.facts.Contains(ctx, fact)
	if err != nil {
		return authmodel.CheckResult{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, false, err
	}
	if !found && s.compiled.relationHasUsersets(req.Resource.Type, e.Relation) {
		return authmodel.CheckResult{}, false, nil
	}
	outcome := authmodel.ReasonDenied
	if found {
		outcome = authmodel.ReasonDirectHit
	}
	b.record(authmodel.ExplainStep{ResourceType: req.Resource.Type, ResourceID: req.Resource.ID, Scope: fact.Scope, Relation: e.Relation, Outcome: outcome, Kind: authmodel.ExplainKindDirect, Depth: depth, Permission: req.Permission})
	return decision(found, "direct:"+e.Relation), true, nil
}

// shortCircuits reports whether a Direct relation may be answered from the
// principal's concrete tuple: the model must allow that concrete subject shape,
// and the bound view must not be evaluating pure subject-first.
func (s *Service) shortCircuits(resourceType, relation, principalType string) bool {
	return !s.subjectFirst && s.readModel.Allows(resourceType, relation, principalType, "")
}

// directTuple returns the concrete principal tuple for a Direct relation, or
// false when a reference is malformed: expansion quietly denies an off-contract
// stored ID, whereas an exact read would reject it as invalid input.
func directTuple(resourceType, resourceID, relation string, principal authmodel.PrincipalRef) (tuples.Tuple, bool) {
	fact := tuples.Tuple{Scope: tuples.On(resourceType, resourceID), Relation: relation, Subject: tuples.SubjectRef{Type: principal.Type, ID: principal.ID}}
	return fact, fact.Validate() == nil
}
