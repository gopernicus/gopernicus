package authorization

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/decisionsvc"
)

// permissionView is the DecisionView a host MutationGuard receives: the store's
// transaction-bound, dependency-recording StoreDecisionView plus
// CheckPermission, which drives the relationship engine's ONE walk
// (authorizersvc.Service.EvaluateWith) over the view's two primitives. The
// traversal therefore lives in the engine, once — a host guard never re-walks
// the ancestor chain in its own code, and never drops to a detached Check.
type permissionView struct {
	mutation.StoreDecisionView
	engine    *authorizersvc.Service         // nil = relationship kind off
	roleModel *decisionsvc.CompiledRoleModel // nil = no role model
}

var _ mutation.DecisionView = permissionView{}

// CheckPermission evaluates permission for principalType:principalID on the
// resource named by scope exactly as the read-side Check would, inside the
// mutation boundary, with every navigated scope recorded as a dependency.
//
// Order of refusals, all before any store read: a non-resource scope or a
// malformed request is ErrInvalidRequest / invalid input; a pair the roles
// model declares is ErrPermissionOwnedByRoles (the guard uses HasRole for it);
// a deployment with no relationship kind is ErrRelationshipsNotConfigured. The
// ownership check precedes the nil-engine branch so mixed-model and roles-only
// deployments answer the same sentinel for a roles-owned pair.
func (v permissionView) CheckPermission(ctx context.Context, scope mutation.ScopeKey, permission, principalType, principalID string) (bool, error) {
	if scope.Kind != mutation.ScopeResource {
		return false, fmt.Errorf("%w: CheckPermission requires a resource scope, got %q", ErrInvalidRequest, scope.Kind)
	}
	req := authorizersvc.CheckRequest{
		Principal:  authorizersvc.PrincipalRef{Type: principalType, ID: principalID},
		Permission: permission,
		Resource:   authorizersvc.Resource{Type: scope.Type, ID: scope.ID},
	}
	if err := req.Validate(); err != nil {
		return false, err
	}
	if v.roleModel.DeclaresPermission(scope.Type, permission) {
		return false, ErrPermissionOwnedByRoles
	}
	if v.engine == nil {
		return false, ErrRelationshipsNotConfigured
	}
	res, err := v.engine.EvaluateWith(ctx, viewReader{view: v.StoreDecisionView}, req)
	if err != nil {
		return false, err
	}
	return res.Allowed, nil
}

// viewReader adapts a StoreDecisionView to the engine's PermissionReader: the
// walk's direct-relation read becomes the view's BOUNDED check with the engine's
// MaxGraphStates forwarded unchanged (so expansion overflow is the same
// evaluation-limit outcome the read side reports), and the Through-hop target
// read becomes the view's record-before-read RelationTargets.
type viewReader struct {
	view mutation.StoreDecisionView
}

func (r viewReader) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	scope := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: resourceType, ID: resourceID}
	return r.view.CheckRelationBounded(ctx, scope, relation, subjectType, subjectID, maxExpansionStates)
}

func (r viewReader) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	scope := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: resourceType, ID: resourceID}
	return r.view.RelationTargets(ctx, scope, relation)
}
