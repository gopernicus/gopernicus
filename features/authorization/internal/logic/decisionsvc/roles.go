package decisionsvc

import (
	"context"
	"fmt"
	"sort"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// roleProbe is the narrow slice of the roles service the decision engine needs:
// a provenance-reporting scoped role probe and the subject's assignment listing.
// *rolesvc.Service satisfies it; the engine never sees the role store.
type roleProbe interface {
	HasRoleWhere(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (held bool, provenance string, err error)
	ListRoleAssignmentsBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error)
}

// roleEngine answers the decision surface for the ROLES kind: it resolves a
// (resource type, permission) pair to its sorted grantor roles through the
// compiled role model, then probes them at the request's scope.
//
// Cost is bounded by the model — at most 2·|grantors| store probes per check,
// short-circuiting on the first hit — and by the resolved EvaluationLimits for
// the enumeration walk. It never allows on a store error.
type roleEngine struct {
	probe  roleProbe
	model  *CompiledRoleModel
	limits authorizersvc.EvaluationLimits
}

// newRoleEngine builds the roles-kind engine over an already-RESOLVED
// EvaluationLimits (the same resolution the relationship engine holds, so the
// composite's budget is not doubled in effect) and an already-compiled model.
func newRoleEngine(probe roleProbe, model *CompiledRoleModel, limits authorizersvc.EvaluationLimits) *roleEngine {
	return &roleEngine{probe: probe, model: model, limits: limits}
}

// DeclaresPermission reports whether the role model declares permission on
// resourceType — the engine's half of the pair-ownership dispatch predicate.
func (e *roleEngine) DeclaresPermission(resourceType, permission string) bool {
	return e.model.DeclaresPermission(resourceType, permission)
}

// =============================================================================
// Decisions
// =============================================================================

// Check evaluates a permission check against the ROLE MODEL only: the pair's
// sorted grantor roles are probed at the request's scope (with the roles kind's
// global fallback) and the first held role grants. An undeclared pair denies
// with "no rules defined"; no role held denies with "no matching role". Any
// store error returns the error, never an allow.
func (e *roleEngine) Check(ctx context.Context, req authorizersvc.CheckRequest) (authorizersvc.CheckResult, error) {
	return e.check(ctx, req, nil)
}

// CheckExplain evaluates req exactly as Check does and additionally returns a
// bounded Explanation: one ExplainKindRole step per grantor probe, in the
// model's sorted order, up to and including the granting probe. It shares the
// SAME evaluation code as Check — an explain cannot reach a different decision
// or spend more probes.
func (e *roleEngine) CheckExplain(ctx context.Context, req authorizersvc.CheckRequest) (authorizersvc.CheckResult, authorizersvc.Explanation, error) {
	var steps []authorizersvc.ExplainStep
	res, err := e.check(ctx, req, &steps)
	return res, authorizersvc.Explanation{Decision: res.ReasonCode, Steps: steps}, err
}

// CheckBatch evaluates each request sequentially — the roles kind has no
// optimisable batch shape (one probe per grantor per request). The MaxBatchSize
// gate belongs to the composite, which owns the ONE decision surface, so it is
// deliberately not applied twice here.
func (e *roleEngine) CheckBatch(ctx context.Context, reqs []authorizersvc.CheckRequest) ([]authorizersvc.CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	results := make([]authorizersvc.CheckResult, len(reqs))
	for i, req := range reqs {
		res, err := e.Check(ctx, req)
		if err != nil {
			return nil, err
		}
		results[i] = res
	}
	return results, nil
}

// check is the single evaluation funnel shared by Check and CheckExplain. steps
// is nil for an ordinary decision; there is deliberately no second evaluator.
func (e *roleEngine) check(ctx context.Context, req authorizersvc.CheckRequest, steps *[]authorizersvc.ExplainStep) (authorizersvc.CheckResult, error) {
	if err := req.Validate(); err != nil {
		return authorizersvc.CheckResult{}, err
	}
	grantors := e.model.grantors(req.Resource.Type, req.Permission)
	if grantors == nil {
		return authorizersvc.CheckResult{Allowed: false, ReasonCode: authorizersvc.ReasonDenied, Reason: "no rules defined"}, nil
	}

	for _, roleName := range grantors {
		if err := ctx.Err(); err != nil {
			// Never begin a store probe after the caller has canceled: fail closed
			// on the context error, not a deny.
			return authorizersvc.CheckResult{}, err
		}
		held, provenance, err := e.probe.HasRoleWhere(ctx, req.Principal.Type, req.Principal.ID, roleName,
			req.Resource.Type, req.Resource.ID)
		if err != nil {
			return authorizersvc.CheckResult{}, err
		}
		recordRoleStep(steps, req, roleName, provenance, held)
		if held {
			return authorizersvc.CheckResult{
				Allowed:    true,
				ReasonCode: authorizersvc.ReasonGranted,
				Reason:     fmt.Sprintf("role:%s@%s", roleName, provenance),
			}, nil
		}
	}

	return authorizersvc.CheckResult{Allowed: false, ReasonCode: authorizersvc.ReasonDenied, Reason: "no matching role"}, nil
}

// recordRoleStep appends one grantor-probe step to the explain trace when
// tracing is enabled; it is a no-op for an ordinary decision. A role step sits
// at the request's own coordinates: Depth 0 and no Relation, because the roles
// kind takes no traversal hop.
func recordRoleStep(steps *[]authorizersvc.ExplainStep, req authorizersvc.CheckRequest, roleName, provenance string, held bool) {
	if steps == nil {
		return
	}
	outcome := authorizersvc.ReasonDenied
	if held {
		outcome = authorizersvc.ReasonGranted
	}
	*steps = append(*steps, authorizersvc.ExplainStep{
		ResourceType: req.Resource.Type,
		ResourceID:   req.Resource.ID,
		Permission:   req.Permission,
		Kind:         authorizersvc.ExplainKindRole,
		Depth:        0,
		Outcome:      outcome,
		Role:         roleName,
		Scope:        provenance,
	})
}

// =============================================================================
// Enumeration
// =============================================================================

// LookupResources enumerates the resource IDs of a type the principal can access
// with a permission, by walking the principal's role assignments.
//
// An undeclared pair returns an empty, non-nil IDs. A GLOBALLY held granting
// role returns Unrestricted immediately with an empty IDs: the principal reaches
// every resource of the type and the host must skip ID filtering entirely.
// Otherwise the scoped assignments whose role grants the permission on this type
// contribute their resource IDs, sorted and distinct.
//
// Bounded (AZ3-1.3): EVERY scanned assignment row is charged against
// MaxGraphStates — the "work units expanded" dimension — so an adversarial
// assignment count is ErrEvaluationLimit (indeterminate), never an unbounded
// store walk; the running distinct count is charged against MaxLookupResults and
// overflow is ErrEvaluationLimit, NEVER a truncated list presented as complete.
// Cancellation is checked before every page.
//
// The walk is not a snapshot: an assignment created mid-walk may or may not
// appear, and duplicates fold into the distinct set. Check is unaffected.
func (e *roleEngine) LookupResources(ctx context.Context, principal authorizersvc.PrincipalRef, permission, resourceType string) (authorizersvc.LookupResult, error) {
	if err := principal.Validate(); err != nil {
		return authorizersvc.LookupResult{}, err
	}
	if err := relationship.ValidateRefField("permission", permission); err != nil {
		return authorizersvc.LookupResult{}, err
	}
	if err := relationship.ValidateRefField("resource type", resourceType); err != nil {
		return authorizersvc.LookupResult{}, err
	}

	grantors := e.model.grantors(resourceType, permission)
	if grantors == nil {
		return authorizersvc.LookupResult{IDs: []string{}}, nil
	}
	granting := make(map[string]struct{}, len(grantors))
	for _, roleName := range grantors {
		granting[roleName] = struct{}{}
	}

	// scanned mirrors budget.chargeState: exactly MaxGraphStates units may be
	// charged, and the first unit beyond is ErrEvaluationLimit.
	scanned := 0
	seen := make(map[string]struct{})
	var ids []string
	req := crud.ListRequest{Limit: crud.MaxLimit}
	for {
		if err := ctx.Err(); err != nil {
			return authorizersvc.LookupResult{}, err
		}
		page, err := e.probe.ListRoleAssignmentsBySubject(ctx, principal.Type, principal.ID, req)
		if err != nil {
			return authorizersvc.LookupResult{}, err
		}
		for _, assignment := range page.Items {
			if scanned >= e.limits.MaxGraphStates {
				return authorizersvc.LookupResult{}, authorizersvc.ErrEvaluationLimit
			}
			scanned++

			if _, ok := granting[assignment.Role]; !ok {
				continue
			}
			if assignment.ResourceType == "" && assignment.ResourceID == "" {
				// A granting role held GLOBALLY reaches every resource of the type.
				return authorizersvc.LookupResult{IDs: []string{}, Unrestricted: true}, nil
			}
			if assignment.ResourceType != resourceType || assignment.ResourceID == "" {
				// A half-scoped row (type, no id) names no resource: landing ""
				// in IDs would break Check/Lookup parity, since Check rejects it.
				continue
			}
			if _, dup := seen[assignment.ResourceID]; dup {
				continue
			}
			seen[assignment.ResourceID] = struct{}{}
			ids = append(ids, assignment.ResourceID)
			if len(ids) > e.limits.MaxLookupResults {
				return authorizersvc.LookupResult{}, authorizersvc.ErrEvaluationLimit
			}
		}
		if len(page.Items) == 0 {
			// A store reporting HasMore on an empty page cannot advance the
			// cursor; stop rather than spin (no rows means no MaxGraphStates
			// charge, so the budget would never end the walk).
			break
		}
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		req.Cursor = page.NextCursor
	}

	if ids == nil {
		ids = []string{} // guarantee a non-nil slice — empty means no access
	}
	sort.Strings(ids)
	return authorizersvc.LookupResult{IDs: ids}, nil
}
