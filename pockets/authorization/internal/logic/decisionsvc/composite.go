package decisionsvc

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/sdk"
)

// Stable kind names hashed into a lookup cursor's fingerprint. They are wire
// identity, not display text: changing one invalidates every in-flight cursor.
const (
	kindNameRelationship = "relationship"
	kindNameRole         = "role"
)

// kind is the decision surface ONE model-bearing kind exposes to the composite:
// the relationship engine (*authorizersvc.Service) and the roles engine
// (*roleEngine) both satisfy it. The composite selects exactly one per
// (resource type, permission) pair and never merges two answers.
type kind interface {
	DeclaresPermission(resourceType, permission string) bool
	Check(ctx context.Context, req authorizersvc.CheckRequest) (authorizersvc.CheckResult, error)
	CheckExplain(ctx context.Context, req authorizersvc.CheckRequest) (authorizersvc.CheckResult, authorizersvc.Explanation, error)
	CheckBatch(ctx context.Context, reqs []authorizersvc.CheckRequest) ([]authorizersvc.CheckResult, error)
	LookupResources(ctx context.Context, principal authorizersvc.PrincipalRef, permission, resourceType string) (authorizersvc.LookupResult, error)
	LookupResourcesPage(ctx context.Context, principal authorizersvc.PrincipalRef, permission, resourceType, after string, limit int) (authorizersvc.LookupResult, error)
	// ModelDigest is the kind's model identity, hashed into a lookup cursor so a
	// deploy that changes the model invalidates the cursors it minted.
	ModelDigest() string
}

// Composite is the ONE decision surface across the model-bearing kinds. It
// DISPATCHES by pair ownership: a (resource type, permission) pair is declared by
// the relationship Schema or by the RoleModel — never both, which construction
// rejects with ErrModelConflict — so the pair's owning model answers and the
// other kind is not consulted at all.
//
// Consequences of dispatch (as opposed to a cross-kind union): there is no kind
// ordering, no merge, no doubled budget charging, and no cross-kind availability
// coupling — a roles store failure can never affect a relationship-owned pair,
// and vice versa. A single-kind host's composite is a pass-through: identical
// decisions, reasons, traces, and zero-length values.
type Composite struct {
	relationships *authorizersvc.Service // nil = the relationship kind is off
	roles         *roleEngine            // nil = the roles kind carries no model
	// undeclared answers a pair NO model declares, so that "no rules defined" is
	// spoken by a real engine in the shipped wording rather than synthesized here.
	// It exists so that reason has an owner on a ROLES-ONLY host too (a both-kinds
	// host always answers it from the relationship engine), and so its absence is
	// what lets NewComposite return nil for a host with no model-bearing kind.
	undeclared kind
	// limits is the SAME resolved budget the relationship engine holds, so the
	// MaxBatchSize gate below is not doubled in effect.
	limits authorizersvc.EvaluationLimits
}

// NewComposite builds the decision surface over the wired model-bearing kinds.
// relationships is the relationship engine (nil when that kind is off); roles is
// the roles service and model its compiled RoleModel — the roles kind joins the
// decision surface only when model is non-nil, so roles is not consulted (and
// need not be usable) for an unset role model. limits must ALREADY be resolved:
// the composite and the relationship engine share one budget.
//
// It returns nil when NO kind bears a model — the caller's "no decision-capable
// kind is configured" state, which a host-facing surface reports as its own
// sentinel rather than as a deny.
//
// Invariant: callers must never pass a TYPED-NIL roles probe. model != nil is
// the real gate on the roles kind joining the surface; the roles != nil check
// below cannot see through an interface holding a nil pointer. The pocket
// guarantees this by requiring Repositories.Roles whenever Config.RoleModel is
// set (ErrRoleModelWithoutRoles), so a compiled model always comes with a usable
// probe.
func NewComposite(relationships *authorizersvc.Service, roles roleProbe, model *CompiledRoleModel, limits authorizersvc.EvaluationLimits) *Composite {
	c := &Composite{relationships: relationships, limits: limits}
	if model != nil && roles != nil {
		c.roles = newRoleEngine(roles, model, limits)
	}
	switch {
	case c.relationships != nil:
		c.undeclared = c.relationships
	case c.roles != nil:
		c.undeclared = c.roles
	default:
		return nil
	}
	return c
}

// DeclaresPermission reports whether EITHER model declares permission on
// resourceType — the registration-time legality predicate the coordinate gates
// run, satisfied by whichever kind owns the pair.
func (c *Composite) DeclaresPermission(resourceType, permission string) bool {
	if c.relationships != nil && c.relationships.DeclaresPermission(resourceType, permission) {
		return true
	}
	return c.roles != nil && c.roles.DeclaresPermission(resourceType, permission)
}

// ownedByRelationships reports whether the relationship engine answers the pair:
// its schema declares it, or NO model declares it and the relationship engine is
// the wired fallback. It is the one dispatch predicate — Check, CheckExplain,
// CheckBatch, and LookupResources all route through it, so a pair cannot be
// answered by different kinds on different entry points.
func (c *Composite) ownedByRelationships(resourceType, permission string) bool {
	if c.relationships == nil {
		return false
	}
	if c.relationships.DeclaresPermission(resourceType, permission) {
		return true
	}
	if c.roles != nil && c.roles.DeclaresPermission(resourceType, permission) {
		return false
	}
	return true
}

// owner returns the kind that answers the pair. It is never nil: a constructed
// Composite has at least one model-bearing kind, and an undeclared pair falls
// back to it.
func (c *Composite) owner(resourceType, permission string) kind {
	if c.ownedByRelationships(resourceType, permission) {
		return c.relationships
	}
	if c.roles != nil && c.roles.DeclaresPermission(resourceType, permission) {
		return c.roles
	}
	return c.undeclared
}

// ownerWithName returns the kind that answers the pair together with its stable
// NAME ("relationship" or "role"). The name is hashed into a lookup cursor's
// fingerprint, so a cursor minted while one kind owned a pair is refused after
// the pair changes hands.
func (c *Composite) ownerWithName(resourceType, permission string) (kind, string) {
	if c.ownedByRelationships(resourceType, permission) {
		return c.relationships, kindNameRelationship
	}
	return c.owner(resourceType, permission), kindNameRole
}

// =============================================================================
// Decisions
// =============================================================================

// Check evaluates one permission check on the model that DECLARES the request's
// (resource type, permission) pair. The request is validated before any store is
// touched; a pair neither model declares denies with "no rules defined" (from the
// relationship engine when it is wired, else from the roles engine) rather than
// consulting both kinds.
func (c *Composite) Check(ctx context.Context, req authorizersvc.CheckRequest) (authorizersvc.CheckResult, error) {
	if err := req.Validate(); err != nil {
		return authorizersvc.CheckResult{}, err
	}
	return c.owner(req.Resource.Type, req.Permission).Check(ctx, req)
}

// CheckExplain evaluates req as Check does and returns the owning kind's trace.
// The Explanation's Decision is the FINAL CheckResult.ReasonCode, and the steps
// are the owning kind's alone — an explain never shows the work of a kind that
// did not decide.
func (c *Composite) CheckExplain(ctx context.Context, req authorizersvc.CheckRequest) (authorizersvc.CheckResult, authorizersvc.Explanation, error) {
	if err := req.Validate(); err != nil {
		return authorizersvc.CheckResult{}, authorizersvc.Explanation{}, err
	}
	return c.owner(req.Resource.Type, req.Permission).CheckExplain(ctx, req)
}

// CheckBatch evaluates many checks, each on its owning model.
//
// The MaxBatchSize gate is charged ONCE here — the composite owns the decision
// surface, so neither kind re-charges it in effect. A zero-length batch keeps its
// literal identity ((nil, nil)) without touching a kind. Every request is
// validated before ANY store is touched; the requests are then grouped by owning
// kind index-preservingly, the relationship subset goes through the relationship
// engine's own (optimisable) batch path and the roles subset runs sequentially,
// and the two are merged back by index.
func (c *Composite) CheckBatch(ctx context.Context, reqs []authorizersvc.CheckRequest) ([]authorizersvc.CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	// Reject an over-size batch BEFORE any store call: an oversized request is
	// indeterminate work, not a decision.
	if len(reqs) > c.limits.MaxBatchSize {
		return nil, authorizersvc.ErrEvaluationLimit
	}
	for i := range reqs {
		if err := reqs[i].Validate(); err != nil {
			return nil, err
		}
	}

	var relReqs, roleReqs []authorizersvc.CheckRequest
	var relIndex, roleIndex []int
	for i, req := range reqs {
		if c.ownedByRelationships(req.Resource.Type, req.Permission) {
			relReqs = append(relReqs, req)
			relIndex = append(relIndex, i)
			continue
		}
		roleReqs = append(roleReqs, req)
		roleIndex = append(roleIndex, i)
	}

	results := make([]authorizersvc.CheckResult, len(reqs))
	// Each subset is dispatched only when non-empty: an engine's own zero-length
	// identity ((nil, nil)) must never be mistaken for a subset's answers.
	if len(relReqs) > 0 {
		out, err := c.relationships.CheckBatch(ctx, relReqs)
		if err != nil {
			return nil, err
		}
		for j, res := range out {
			results[relIndex[j]] = res
		}
	}
	if len(roleReqs) > 0 {
		out, err := c.roles.CheckBatch(ctx, roleReqs)
		if err != nil {
			return nil, err
		}
		for j, res := range out {
			results[roleIndex[j]] = res
		}
	}
	return results, nil
}

// FilterAuthorized returns only the resource IDs the principal can access. The
// query names ONE resource type and ONE permission, so exactly one model owns
// it and answers the whole call — the batch ceiling is charged once, here,
// before any validation or store call. No IDs is (nil, nil).
//
// The two kinds answer it in the shape each is good at. The RELATIONSHIP kind
// evaluates the candidate set ONCE (authorizersvc.FilterAuthorized →
// evaluateSet): one store read per (branch, hop) over the whole set instead of
// one per (candidate, branch, hop), which is what makes a page of DENIED
// candidates cost branches-and-hops rather than N × branches round trips. The
// ROLES kind keeps its own already-set-shaped answer through CheckBatch, whose
// Unrestricted (globally held role) semantics are untouched.
func (c *Composite) FilterAuthorized(ctx context.Context, principal authorizersvc.PrincipalRef, permission, resourceType string, resourceIDs []string) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	// Reject an over-size candidate set BEFORE any store call, exactly as
	// CheckBatch does — an oversized request is indeterminate work, not a
	// decision.
	if len(resourceIDs) > c.limits.MaxBatchSize {
		return nil, authorizersvc.ErrEvaluationLimit
	}
	if c.ownedByRelationships(resourceType, permission) {
		return c.relationships.FilterAuthorized(ctx, principal, permission, resourceType, resourceIDs)
	}

	reqs := make([]authorizersvc.CheckRequest, len(resourceIDs))
	for i, id := range resourceIDs {
		reqs[i] = authorizersvc.CheckRequest{
			Principal:  principal,
			Permission: permission,
			Resource:   authorizersvc.Resource{Type: resourceType, ID: id},
		}
	}

	results, err := c.CheckBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}

	allowed := make([]string, 0, len(resourceIDs))
	for i, result := range results {
		if result.Allowed {
			allowed = append(allowed, resourceIDs[i])
		}
	}
	return allowed, nil
}

// =============================================================================
// Enumeration
// =============================================================================

// LookupResources returns the owning kind's enumeration verbatim — its own
// result charging, ordering, and (for the roles kind) Unrestricted answer. There
// is no cross-kind union and no halved headroom. The arguments are validated
// before any store is touched.
func (c *Composite) LookupResources(ctx context.Context, principal authorizersvc.PrincipalRef, permission, resourceType string) (authorizersvc.LookupResult, error) {
	if err := principal.Validate(); err != nil {
		return authorizersvc.LookupResult{}, err
	}
	if err := relationship.ValidateRefField("permission", permission); err != nil {
		return authorizersvc.LookupResult{}, err
	}
	if err := relationship.ValidateRefField("resource type", resourceType); err != nil {
		return authorizersvc.LookupResult{}, err
	}
	return c.owner(resourceType, permission).LookupResources(ctx, principal, permission, resourceType)
}

// LookupResourcesIn is the PAGED enumeration surface — the struct-input sibling
// of LookupResources, and the ONE body that pages. It validates the request,
// resolves the page size against the shared budget, dispatches to the SINGLE
// owning kind exactly as Check and LookupResources do (never a cross-kind
// merge), and binds the continuation cursor to that query.
//
//   - LookupRequest.Limit is a PAGE SIZE: 0 means MaxLookupResults; a Limit
//     ABOVE MaxLookupResults is sdk.ErrInvalidInput (the budget bounds the page
//     size); a negative Limit is rejected by Validate.
//   - LookupRequest.After is the previous page's NextCursor. It is decoded
//     against a fingerprint of (owning kind, that kind's model digest,
//     principal, permission, resource type); a cursor from another query, the
//     other kind, or a changed model is ErrInvalidCursor and the client
//     restarts from page one.
//   - NextCursor is set only when HasMore is true. Truncated carries the same
//     value as HasMore for one deprecation release.
//   - The budget is NOT a total cap: it bounds every intermediate node and every
//     self-hierarchy root set, so an overflowing enumeration is
//     ErrEvaluationLimit on every page rather than a short list that looks
//     complete. An Unrestricted answer names no IDs to page and passes through
//     with no cursor.
func (c *Composite) LookupResourcesIn(ctx context.Context, req authorizersvc.LookupRequest) (authorizersvc.LookupResult, error) {
	if err := req.Validate(); err != nil {
		return authorizersvc.LookupResult{}, err
	}
	limit := req.Limit
	switch {
	case limit == 0:
		limit = c.limits.MaxLookupResults
	case limit > c.limits.MaxLookupResults:
		return authorizersvc.LookupResult{}, fmt.Errorf("authorization: lookup limit %d exceeds MaxLookupResults %d: %w",
			limit, c.limits.MaxLookupResults, sdk.ErrInvalidInput)
	}

	owner, ownerName := c.ownerWithName(req.ResourceType, req.Permission)
	fingerprint := authorizersvc.LookupFingerprint(ownerName, owner.ModelDigest(), req.Principal, req.Permission, req.ResourceType)

	after := ""
	if req.After != "" {
		decoded, err := authorizersvc.DecodeLookupCursor(req.After, fingerprint)
		if err != nil {
			return authorizersvc.LookupResult{}, err
		}
		after = decoded
	}

	res, err := owner.LookupResourcesPage(ctx, req.Principal, req.Permission, req.ResourceType, after, limit)
	if err != nil {
		return authorizersvc.LookupResult{}, err
	}
	if res.HasMore && len(res.IDs) > 0 {
		res.NextCursor = authorizersvc.EncodeLookupCursor(res.IDs[len(res.IDs)-1], fingerprint)
	}
	res.Truncated = res.HasMore
	return res, nil
}
