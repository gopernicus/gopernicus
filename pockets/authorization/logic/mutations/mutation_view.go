package mutations

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// DecisionView reads authorization inside a guarded mutation's transaction.
// Check uses the current host model and the same limits as Service.Check.
// The remaining methods inspect raw stored facts, independent of that model.
// Every read participates in the repository's concurrency protection.
// A view is valid only during its synchronous guard callback.
type DecisionView interface {
	Check(context.Context, authmodel.CheckRequest) (authmodel.CheckResult, error)
	HasRole(ctx context.Context, principal authmodel.PrincipalRef, role string) (bool, error)
	// HasRoleIn checks only the exact resource grant.
	HasRoleIn(ctx context.Context, principal authmodel.PrincipalRef, role string, resource authmodel.Resource) (bool, error)
	// CheckRelation expands exact usersets under the configured MaxGraphStates.
	CheckRelation(ctx context.Context, principal authmodel.PrincipalRef, relation string, resource authmodel.Resource) (bool, error)
	// RelationTargets returns raw targets, bounded by MaxRelationTargets.
	RelationTargets(ctx context.Context, resource authmodel.Resource, relation string) ([]relationships.RelationTarget, error)
}

// Keep the adapter reader named so host callbacks receive only DecisionView,
// without promoted store/model-scoping methods.
type permissionView struct {
	store  StoreDecisionView
	engine *decisions.Service
	limits authmodel.EvaluationLimits
}

var _ DecisionView = permissionView{}

func (v permissionView) Check(ctx context.Context, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
	if err := req.Validate(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if v.engine == nil {
		return authmodel.CheckResult{}, authmodel.ErrNoDecisionKind
	}
	return v.engine.EvaluateWith(ctx, v.store, req)
}
func (v permissionView) HasRole(ctx context.Context, p authmodel.PrincipalRef, role string) (bool, error) {
	return v.exact(ctx, p, role, tuples.Global())
}
func (v permissionView) HasRoleIn(ctx context.Context, p authmodel.PrincipalRef, role string, r authmodel.Resource) (bool, error) {
	return v.exact(ctx, p, role, tuples.On(r.Type, r.ID))
}
func (v permissionView) exact(ctx context.Context, p authmodel.PrincipalRef, role string, scope tuples.Scope) (bool, error) {
	fact := tuples.Tuple{Scope: scope, Relation: role, Subject: tuples.SubjectRef{Type: p.Type, ID: p.ID}}
	if err := fact.Validate(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	held, err := v.store.Contains(ctx, fact)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return held, nil
}

func (v permissionView) CheckRelation(ctx context.Context, principal authmodel.PrincipalRef, relation string, resource authmodel.Resource) (bool, error) {
	if err := (authmodel.CheckRequest{Principal: principal, Permission: relation, Resource: resource}).Validate(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	allowed, err := v.store.CheckRelationBounded(ctx, targetForResource(resource), relation, principal.Type, principal.ID, v.limits.MaxGraphStates)
	if errors.Is(err, relationships.ErrExpansionBudgetExceeded) {
		return false, authmodel.ErrEvaluationLimit
	}
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return allowed, nil
}

func (v permissionView) RelationTargets(ctx context.Context, resource authmodel.Resource, relation string) ([]relationships.RelationTarget, error) {
	if err := tuples.ValidateRefField("resource type", resource.Type); err != nil {
		return nil, err
	}
	if err := tuples.ValidateRefField("resource id", resource.ID); err != nil {
		return nil, err
	}
	if err := tuples.ValidateRefField("relation", relation); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targets, err := v.store.RelationTargets(ctx, targetForResource(resource), relation)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(targets) > v.limits.MaxRelationTargets {
		return nil, authmodel.ErrEvaluationLimit
	}
	return targets, nil
}

func targetForResource(resource authmodel.Resource) Target {
	return Target{Kind: TargetResource, Type: resource.Type, ID: resource.ID}
}
