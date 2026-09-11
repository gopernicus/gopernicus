package mutations

import (
	"context"
	"errors"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

// DecisionView reads authorization inside a guarded mutation's transaction.
// Check uses the current host model and the same limits as Service.Check.
// The remaining methods inspect raw stored facts, independent of that model.
// Every read participates in the repository's concurrency protection.
// A view is valid only during its synchronous guard callback.
type DecisionView interface {
	Check(context.Context, authmodel.CheckRequest) (authmodel.CheckResult, error)
	HasGlobalRole(ctx context.Context, principal authmodel.PrincipalRef, role string) (bool, error)
	// HasRole checks the resource grant, then the principal's global grant.
	HasRole(ctx context.Context, principal authmodel.PrincipalRef, role string, resource authmodel.Resource) (bool, error)
	// CheckRelation expands exact usersets under the configured MaxGraphStates.
	CheckRelation(ctx context.Context, principal authmodel.PrincipalRef, relation string, resource authmodel.Resource) (bool, error)
	// RelationTargets returns raw targets, bounded by MaxRelationTargets.
	RelationTargets(ctx context.Context, resource authmodel.Resource, relation string) ([]relationships.RelationTarget, error)
}

// Keep the adapter reader named so host callbacks receive only DecisionView,
// without promoted store/model-scoping methods.
type permissionView struct {
	store     StoreDecisionView
	engine    *relationships.Service
	roleModel *authmodel.CompiledRoleModel
	limits    authmodel.EvaluationLimits
}

var _ DecisionView = permissionView{}

func (v permissionView) Check(ctx context.Context, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
	if err := req.Validate(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if v.roleModel.DeclaresPermission(req.Resource.Type, req.Permission) {
		return authmodel.EvaluateRolePermission(ctx, v.roleModel, req, v.limits, func(ctx context.Context, roleName string) (bool, error) {
			return v.HasRole(ctx, req.Principal, roleName, req.Resource)
		})
	}
	if v.engine != nil {
		return v.engine.EvaluateWith(ctx, v.store, req)
	}
	if v.roleModel != nil {
		return authmodel.CheckResult{ReasonCode: authmodel.ReasonDenied, Reason: "no rules defined"}, nil
	}
	return authmodel.CheckResult{}, authmodel.ErrNoDecisionKind
}

func (v permissionView) HasGlobalRole(ctx context.Context, principal authmodel.PrincipalRef, role string) (bool, error) {
	if err := principal.Validate(); err != nil {
		return false, err
	}
	if err := authmodel.ValidateRefField("role", role); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	held, err := v.store.HasRole(ctx, Target{Kind: TargetSubject, Type: principal.Type, ID: principal.ID}, role, principal.Type, principal.ID)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return held, nil
}

func (v permissionView) HasRole(ctx context.Context, principal authmodel.PrincipalRef, role string, resource authmodel.Resource) (bool, error) {
	if err := (authmodel.CheckRequest{Principal: principal, Permission: role, Resource: resource}).Validate(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	held, err := v.store.HasRole(ctx, targetForResource(resource), role, principal.Type, principal.ID)
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
	if err := authmodel.ValidateRefField("resource type", resource.Type); err != nil {
		return nil, err
	}
	if err := authmodel.ValidateRefField("resource id", resource.ID); err != nil {
		return nil, err
	}
	if err := authmodel.ValidateRefField("relation", relation); err != nil {
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
