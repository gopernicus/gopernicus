package decisions

import (
	"context"
	"fmt"
	"slices"
	"sort"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// roleProbe is the narrow slice of the roles service the decision engine needs:
// an exact role probe and the subject's assignment listing.
// *roles.Service satisfies it; the engine never sees the role store.
type roleProbe interface {
	HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error)
	ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[roles.Assignment], error)
	LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) (ids []string, unrestricted bool, err error)
}

// roleEngine answers the decision surface for the ROLES kind: it resolves a
// (resource type, permission) pair to its sorted grantor roles through the
// compiled role model, then probes them at the request's scope.
//
// Cost is bounded by the model — at most 2·|grantors| store probes per check,
// short-circuiting on the first hit — and by MaxEvaluationSteps for the root
// and each visited grantor. Enumeration has its own resolved work bounds. It never allows on a store error.
type roleEngine struct {
	probe  roleProbe
	model  *authmodel.CompiledRoleModel
	limits authmodel.EvaluationLimits
}

// newRoleEngine builds the roles-kind engine over an already-RESOLVED
// EvaluationLimits (the same resolution the relationship engine holds, so the
// composite's budget is not doubled in effect) and an already-compiled model.
func newRoleEngine(probe roleProbe, model *authmodel.CompiledRoleModel, limits authmodel.EvaluationLimits) *roleEngine {
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
func (e *roleEngine) Check(ctx context.Context, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
	return e.check(ctx, req, nil, e.probe.HasExactRole)
}

// CheckExplain evaluates req exactly as Check does and additionally returns a
// bounded Explanation: one ExplainKindRole step per grantor probe, in the
// model's sorted order, up to and including the granting probe. It shares the
// SAME evaluation code as Check — an explain cannot reach a different decision
// or spend more probes.
func (e *roleEngine) CheckExplain(ctx context.Context, req authmodel.CheckRequest) (authmodel.CheckResult, authmodel.Explanation, error) {
	var steps []authmodel.ExplainStep
	res, err := e.check(ctx, req, &steps, e.probe.HasExactRole)
	return res, authmodel.Explanation{Decision: res.ReasonCode, Steps: steps}, err
}

// CheckBatch evaluates requests in order and reuses exact/global facts only
// within this call. It preserves grantor order and exact-before-global errors;
// it never promotes a cached global grant ahead of an unread exact scope. The MaxBatchSize
// gate belongs to the composite, which owns the ONE decision surface, so it is
// deliberately not applied twice here.
func (e *roleEngine) CheckBatch(ctx context.Context, reqs []authmodel.CheckRequest) ([]authmodel.CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	results := make([]authmodel.CheckResult, len(reqs))
	readExact := memoRoleReads(e.probe.HasExactRole)
	for i, req := range reqs {
		res, err := e.check(ctx, req, nil, readExact)
		if err != nil {
			return nil, err
		}
		results[i] = res
	}
	return results, nil
}

// check is the single evaluation funnel shared by Check and CheckExplain. steps
// is nil for an ordinary decision; there is deliberately no second evaluator.
func (e *roleEngine) check(ctx context.Context, req authmodel.CheckRequest, steps *[]authmodel.ExplainStep, readExact exactRoleReader) (authmodel.CheckResult, error) {
	var grantingScope string
	result, err := authmodel.EvaluateRolePermission(ctx, e.model, req, e.limits, func(ctx context.Context, roleName string) (bool, error) {
		held, provenance, err := roles.ResolveScope(ctx, req.Resource.Type, req.Resource.ID, func(ctx context.Context, scopeType, scopeID string) (bool, error) {
			return readExact(ctx, req.Principal.Type, req.Principal.ID, roleName, scopeType, scopeID)
		})
		if err != nil {
			return false, err
		}
		recordRoleStep(steps, req, roleName, provenance, held)
		if held {
			grantingScope = provenance
		}
		return held, nil
	})
	if err == nil && result.Allowed {
		result.Reason += "@" + grantingScope
	}
	return result, err
}

// recordRoleStep appends one grantor-probe step to the explain trace when
// tracing is enabled; it is a no-op for an ordinary decision. A role step sits
// at the request's own coordinates: Depth 0 and no Relation, because the roles
// kind takes no traversal hop.
func recordRoleStep(steps *[]authmodel.ExplainStep, req authmodel.CheckRequest, roleName, provenance string, held bool) {
	if steps == nil {
		return
	}
	outcome := authmodel.ReasonDenied
	if held {
		outcome = authmodel.ReasonGranted
	}
	*steps = append(*steps, authmodel.ExplainStep{
		ResourceType: req.Resource.Type,
		ResourceID:   req.Resource.ID,
		Permission:   req.Permission,
		Kind:         authmodel.ExplainKindRole,
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
// Enumeration charges one root plus every compiled grantor to
// MaxEvaluationSteps before any read. It derives a complete set, so it cannot
// use the first-grant short circuit of a single Check. Every scanned row is charged against
// MaxGraphStates — the "work units expanded" dimension — so an adversarial
// assignment count is ErrEvaluationLimit (indeterminate), never an unbounded
// store walk; the running distinct count is charged against MaxLookupResults and
// overflow is ErrEvaluationLimit, NEVER a truncated list presented as complete.
// Cancellation is checked before every page.
//
// The walk is not a snapshot: an assignment created mid-walk may or may not
// appear, and duplicates fold into the distinct set. Check is unaffected.
func (e *roleEngine) LookupResources(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string) (authmodel.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if err := principal.Validate(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if err := authmodel.ValidateRefField("permission", permission); err != nil {
		return authmodel.LookupResult{}, err
	}
	if err := authmodel.ValidateRefField("resource type", resourceType); err != nil {
		return authmodel.LookupResult{}, err
	}

	grantors := e.model.Grantors(resourceType, permission)
	// A complete set considers every grantor, including a possible global
	// assignment. Charge the root and all grantors before starting any read.
	if len(grantors) > e.limits.MaxEvaluationSteps-1 {
		return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
	}
	if grantors == nil {
		return authmodel.LookupResult{IDs: []string{}}, nil
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
	req := list.Request{Limit: list.MaxLimit}
	for {
		if err := ctx.Err(); err != nil {
			return authmodel.LookupResult{}, err
		}
		page, err := e.probe.ListRoleAssignmentsBySubject(ctx, principal, req)
		if err != nil {
			return authmodel.LookupResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return authmodel.LookupResult{}, err
		}
		for _, assignment := range page.Items {
			if err := ctx.Err(); err != nil {
				return authmodel.LookupResult{}, err
			}
			if scanned >= e.limits.MaxGraphStates {
				return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
			}
			scanned++

			if _, ok := granting[assignment.Role]; !ok {
				continue
			}
			if assignment.ResourceType == "" && assignment.ResourceID == "" {
				// A granting role held GLOBALLY reaches every resource of the type.
				return authmodel.LookupResult{IDs: []string{}, Unrestricted: true}, nil
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
				return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
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
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	return authmodel.LookupResult{IDs: ids}, nil
}

// LookupResourcesPage is the roles kind's PAGED enumeration: the resource ids of
// resourceType the principal can access with permission that sort strictly after
// `after`, at most limit of them, plus HasMore.
//
// It is one indexed store read of the pair's compiled grantor roles (A3b), not
// the assignment walk LookupResources performs: it neither scans every
// assignment nor charges MaxGraphStates for assignments irrelevant to the query.
// It charges one root plus every compiled grantor to MaxEvaluationSteps before
// reading, just like the complete-set enumeration. An undeclared pair returns an empty,
// non-nil IDs; a GLOBALLY held granting role returns Unrestricted with an empty
// IDs, no page and no continuation, exactly as the unpaged method does.
func (e *roleEngine) LookupResourcesPage(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType, after string, limit int) (authmodel.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if err := principal.Validate(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if err := authmodel.ValidateRefField("permission", permission); err != nil {
		return authmodel.LookupResult{}, err
	}
	if err := authmodel.ValidateRefField("resource type", resourceType); err != nil {
		return authmodel.LookupResult{}, err
	}
	if limit <= 0 {
		limit = e.limits.MaxLookupResults
	}
	if limit > e.limits.MaxLookupResults {
		return authmodel.LookupResult{}, fmt.Errorf("authorization: lookup page limit exceeds MaxLookupResults: %w", sdk.ErrInvalidInput)
	}

	grantors := e.model.Grantors(resourceType, permission)
	// A complete set considers every grantor, including a possible global
	// assignment. Charge the root and all grantors before starting any read.
	if len(grantors) > e.limits.MaxEvaluationSteps-1 {
		return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
	}
	if grantors == nil {
		return authmodel.LookupResult{IDs: []string{}}, nil
	}
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}

	// limit+1 is the lookahead row that distinguishes "the page ends here" from
	// "there is more".
	ids, unrestricted, err := e.probe.LookupResourceIDsBySubjectAndRoles(ctx, principal.Type, principal.ID, resourceType, grantors, after, limit+1)
	if err != nil {
		return authmodel.LookupResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if unrestricted {
		return authmodel.LookupResult{IDs: []string{}, Unrestricted: true}, nil
	}
	hasMore := len(ids) > limit
	if hasMore {
		// Clip so the returned slice cannot reach the withheld tail.
		ids = slices.Clip(ids[:limit])
	}
	if ids == nil {
		ids = []string{} // guarantee a non-nil slice — empty means no access
	}
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	return authmodel.LookupResult{IDs: ids, HasMore: hasMore}, nil
}

// ModelDigest is the roles kind's model identity for cursor binding: the
// compiled role model's deterministic digest. A deploy that changes the model
// changes it, which invalidates every in-flight lookup cursor bound to this
// kind.
func (e *roleEngine) ModelDigest() string { return e.model.Digest() }
