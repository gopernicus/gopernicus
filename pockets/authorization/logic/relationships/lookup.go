package relationships

import (
	"context"
	"fmt"
	"slices"
	"sort"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

// LookupResources returns all resource IDs of a type the subject can access with
// a permission (the prefilter pattern: look up authorized IDs, then pass them to
// the repository as WHERE id = ANY(@ids)).
//
// This is pure schema/tuple enumeration: IDs is ALWAYS a non-nil slice, and an
// empty slice means no access. A host that wants admin-sees-everything semantics
// checks for that in its own closure BEFORE calling here (and then skips ID
// filtering entirely) — the engine grants no bypass.
//
// Deterministic ordering (AZ3-1.4): every node's IDs are returned sorted
// ascending, so the same state yields the same slice regardless of source map
// iteration or the path a memoized sub-result was reached by (the AZ3-1.6
// sort-lookup-output direction, applied here to pin the ordering the
// Check/Lookup oracle depends on). Every ID appears exactly once.
//
// Bounded (AZ3-1.3): the enumeration shares one per-decision budget. Every store
// call fetches at most MaxLookupResults+1 IDs, and the running distinct union is
// charged against MaxLookupResults; overflow is ErrEvaluationLimit, NEVER a
// truncated slice presented as complete. Cancellation is checked before each
// store call and recursion.
//
// Check/Lookup parity (AZ3-1.4, D1(c) closed): every resource a concrete-
// principal Check allows for a supported finite query is discoverable here. In
// particular, a self-referential Through hierarchy (space→parent→space) seeds
// its descendant walk from EVERY root the permission grants — direct grants AND
// roots derived through a non-self Through (e.g. Through("org","view")) — not
// direct-only roots. The earlier D1(b) divergence (org-derived roots enumerated
// but their descendants omitted) is removed.
func (s *Service) LookupResources(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string) (authmodel.LookupResult, error) {
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
	result, err := s.lookupResources(ctx, principal, permission, resourceType, newBudget(s.limits, newMemoReader(s.reader)), make(map[nodeKey]bool), make(map[nodeKey]authmodel.LookupResult))
	if err != nil {
		return authmodel.LookupResult{}, err
	}
	return s.verifyLookup(ctx, principal, permission, resourceType, result)
}

// lookupResources enumerates the resource IDs of resourceType the principal can
// access with permission. stack holds the (type, permission) keys on the ACTIVE
// recursion path (cycle detection); memo holds COMPLETED (type, permission)
// results for reuse. The split is the fix for the shared-visited-key bug: a
// completed sub-result is REUSED by a sibling Through relation rather than
// suppressed to empty. The compiler rejects every genuine non-self Through cycle
// (only the same-permission self-hierarchy self-loop is sanctioned, and it is
// resolved by the store's descendant walk below, not this recursion), so this
// recursion is over a DAG and memoization is complete/safe; the stack guard is
// defense-in-depth.
//
// The walk has two phases. First it computes the ROOT set: the union of every
// non-self grant of the permission — direct relations, and Through relations to
// OTHER types (or to the same type under a DIFFERENT permission, which recurses
// on a distinct memo key). Then, if the permission has any same-permission
// self-referential Through relation, it expands the self-hierarchy descendants
// from that full root set (D1(c)) via the store's cycle-safe descendant walk.
func (s *Service) lookupResources(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string, b *budget, stack map[nodeKey]bool, memo map[nodeKey]authmodel.LookupResult) (authmodel.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	key := nodeKey{resourceType, permission}
	if res, done := memo[key]; done {
		return res, nil
	}
	if stack[key] {
		// Genuine cycle on the active path (compiler-forbidden for non-self
		// Through; defensive): contribute nothing without memoizing an incomplete
		// result.
		return authmodel.LookupResult{IDs: []string{}}, nil
	}

	if len(s.compiled.permissionChecks(resourceType, permission)) == 0 {
		res := authmodel.LookupResult{IDs: []string{}}
		memo[key] = res
		return res, nil
	}

	ids, seen, selfRelations, err := s.lookupRoots(ctx, principal, permission, resourceType, b, stack, memo)
	if err != nil {
		return authmodel.LookupResult{}, err
	}

	// ids now holds the ROOT set: every resource granted the permission WITHOUT
	// descending the self-hierarchy. Expand descendants from all of them (D1(c)).
	add := func(newIDs []string) error {
		for _, id := range newIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if b.resultsOverflow(len(ids)) {
			return authmodel.ErrEvaluationLimit
		}
		return nil
	}
	if len(selfRelations) > 0 {
		if err := s.expandSelfHierarchy(ctx, resourceType, selfRelations, ids, "", b.resultFetchCap(), add); err != nil {
			return authmodel.LookupResult{}, err
		}
	}

	if ids == nil {
		ids = []string{} // guarantee a non-nil slice — empty means no access
	}
	sort.Strings(ids) // deterministic ordering, each ID exactly once (dedup above)
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	res := authmodel.LookupResult{IDs: ids}
	memo[key] = res
	return res, nil
}

// lookupRoots computes the ROOT set of (resourceType, permission): the union of
// every non-self grant — direct relations, and Through relations to OTHER types
// (or to the same type under a DIFFERENT permission, which recurses on a
// distinct memo key). It returns the roots in discovery order (the caller
// sorts), the dedup set they were collected into, and the same-permission
// self-referential Through relations the caller must expand descendants over.
//
// It is COMPLETE and budget-bounded, never paged: root id order does not
// constrain descendant id order, so a paged root set could not preserve global
// id order (plan A3). Every store call fetches at most MaxLookupResults+1 and
// the running distinct union is charged against MaxLookupResults; overflow is
// ErrEvaluationLimit, never a truncated list. Cancellation is checked before
// each store call and recursion.
func (s *Service) lookupRoots(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string, b *budget, stack map[nodeKey]bool, memo map[nodeKey]authmodel.LookupResult) (roots []string, seen map[string]bool, selfRelations []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	seen = make(map[string]bool)
	checks := s.compiled.permissionChecks(resourceType, permission)
	if len(checks) == 0 {
		return nil, seen, nil, nil
	}

	key := nodeKey{resourceType, permission}
	stack[key] = true
	defer delete(stack, key)

	add := func(newIDs []string) error {
		for _, id := range newIDs {
			if !seen[id] {
				seen[id] = true
				roots = append(roots, id)
			}
		}
		if b.resultsOverflow(len(roots)) {
			return authmodel.ErrEvaluationLimit
		}
		return nil
	}

	// selfSeen dedups the same-permission self-referential Through relations
	// (target type == resourceType AND target permission == permission) — the
	// sanctioned hierarchy self-loop. Their descendants are expanded from the
	// full root set AFTER the non-self roots are gathered, so a root derived
	// through a non-self Through still seeds descendant expansion (D1(c)).
	selfSeen := make(map[string]bool)

	for _, check := range checks {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		if check.Through == "" {
			found, err := s.reader.LookupResourceIDs(ctx, resourceType, []string{check.Relation}, principal.Type, principal.ID, "", b.resultFetchCap())
			if err != nil {
				return nil, nil, nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, nil, err
			}
			if err := add(found); err != nil {
				return nil, nil, nil, err
			}
			continue
		}

		// The compiled Through target definitions: the sorted concrete
		// resource-target types of the traversed relation. Iterating this
		// precomputed slice keeps the walk deterministic and off the source
		// (mutable) subject maps.
		for _, targetType := range s.compiled.relationResourceTargets(resourceType, check.Through) {
			if targetType == resourceType && check.Permission == permission {
				// Same-permission self-hierarchy: defer to descendant expansion
				// from the full root set (the caller's job). Recursing here would
				// hit the stack cycle guard and contribute nothing; the store's
				// transitive walk resolves the self-loop instead.
				if !selfSeen[check.Through] {
					selfSeen[check.Through] = true
					selfRelations = append(selfRelations, check.Through)
				}
				continue
			}

			// Non-self Through (or same type under a different permission, which
			// recurses on a distinct memo key and terminates): resolve the target
			// set, then map it back to this resource type through the relation.
			targetResult, err := s.lookupResources(ctx, principal, check.Permission, targetType, b, stack, memo)
			if err != nil {
				return nil, nil, nil, err
			}
			if len(targetResult.IDs) == 0 {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, nil, err
			}
			throughIDs, err := s.reader.LookupResourceIDsByRelationTarget(ctx, resourceType, check.Through, targetType, targetResult.IDs, "", b.resultFetchCap())
			if err != nil {
				return nil, nil, nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, nil, err
			}
			if err := add(throughIDs); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	return roots, seen, selfRelations, ctx.Err()
}

// expandSelfHierarchy expands a same-permission self-referential hierarchy: given
// the root resource IDs already granted the permission by non-self means, it adds
// every descendant reachable by transitively following ANY of the self-referential
// relations (space→parent→space) toward a root. The store walks the UNION of the
// relations in ONE cycle-safe recursive call, so a path that ALTERNATES relations
// is complete without an engine-side fixpoint loop; after/limit page the sorted
// closure, not the work the database does to compute it (plan A3).
//
// Every discovered ID is added through add (which dedups, charges the result
// budget on the complete path, and reports overflow as ErrEvaluationLimit — never
// a truncated list).
func (s *Service) expandSelfHierarchy(ctx context.Context, resourceType string, selfRelations, roots []string, after string, limit int, add func([]string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(roots) == 0 {
		return nil // nothing to descend from: no store call
	}
	desc, err := s.reader.LookupDescendantResourceIDs(ctx, resourceType, selfRelations, resourceType, roots, after, limit)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return add(desc)
}

// LookupResourcesPage is the PAGED enumeration behind LookupResourcesIn: the ids
// of resourceType the principal can access with permission that sort strictly
// after `after`, at most limit of them, plus HasMore.
//
// Order is the same global resource-id ascending byte order LookupResources
// returns, so the concatenation of every page equals the plain result exactly —
// same ids, same order, no repeats. There is no snapshot across pages: a keyset
// continuation sees grants that land ahead of it and misses ones that land
// behind it.
//
// What the page bounds and what it does not (plan A2–A4):
//
//   - Every TOP-LEVEL leaf stream — one per direct relation, one per non-self
//     Through hop, and at most one self-hierarchy closure — is read with the
//     page's own `after` and limit+1 rows. The extra row is the HasMore
//     lookahead, and limit+1 distinct rows per stream are sufficient to produce
//     limit distinct union rows plus lookahead.
//   - A non-self Through hop's TARGET set is still the complete, memoized,
//     budget-bounded lookupResources enumeration, computed once per page. A
//     principal over MaxLookupResults targets is ErrEvaluationLimit on EVERY
//     page — the documented intermediate cliff, never a short list.
//   - A self-hierarchy's non-descendant ROOT set is likewise complete and
//     budget-bounded before it seeds one closure stream, because root id order
//     does not constrain descendant id order.
//
// Cancellation is checked before each store call, and the arguments are
// validated exactly as LookupResources validates them.
func (s *Service) LookupResourcesPage(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType, after string, limit int) (authmodel.LookupResult, error) {
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
		limit = s.limits.MaxLookupResults
	}
	if limit > s.limits.MaxLookupResults {
		return authmodel.LookupResult{}, fmt.Errorf("authorization: lookup page limit exceeds MaxLookupResults: %w", sdk.ErrInvalidInput)
	}

	checks := s.compiled.permissionChecks(resourceType, permission)
	if len(checks) == 0 {
		return authmodel.LookupResult{IDs: []string{}}, nil
	}

	b := newBudget(s.limits, newMemoReader(s.reader))
	stack := make(map[nodeKey]bool)
	memo := make(map[nodeKey]authmodel.LookupResult)

	// fetch is the per-stream row cap: one page plus the lookahead row that
	// distinguishes "the page ends here" from "there is more".
	fetch := limit + 1

	seen := make(map[string]bool)
	var union []string
	merge := func(newIDs []string) {
		for _, id := range newIDs {
			if !seen[id] {
				seen[id] = true
				union = append(union, id)
			}
		}
	}

	var selfRelations []string
	selfSeen := make(map[string]bool)

	for _, check := range checks {
		if err := ctx.Err(); err != nil {
			return authmodel.LookupResult{}, err
		}
		if check.Through == "" {
			found, err := s.reader.LookupResourceIDs(ctx, resourceType, []string{check.Relation}, principal.Type, principal.ID, after, fetch)
			if err != nil {
				return authmodel.LookupResult{}, err
			}
			if err := ctx.Err(); err != nil {
				return authmodel.LookupResult{}, err
			}
			merge(found)
			continue
		}

		for _, targetType := range s.compiled.relationResourceTargets(resourceType, check.Through) {
			if targetType == resourceType && check.Permission == permission {
				if !selfSeen[check.Through] {
					selfSeen[check.Through] = true
					selfRelations = append(selfRelations, check.Through)
				}
				continue
			}

			targetResult, err := s.lookupResources(ctx, principal, check.Permission, targetType, b, stack, memo)
			if err != nil {
				return authmodel.LookupResult{}, err
			}
			if len(targetResult.IDs) == 0 {
				continue
			}
			if err := ctx.Err(); err != nil {
				return authmodel.LookupResult{}, err
			}
			throughIDs, err := s.reader.LookupResourceIDsByRelationTarget(ctx, resourceType, check.Through, targetType, targetResult.IDs, after, fetch)
			if err != nil {
				return authmodel.LookupResult{}, err
			}
			if err := ctx.Err(); err != nil {
				return authmodel.LookupResult{}, err
			}
			merge(throughIDs)
		}
	}

	if len(selfRelations) > 0 {
		// The closure needs the COMPLETE root set (after "" and the budget cap):
		// a lexically late root may grant a lexically early descendant.
		roots, _, _, err := s.lookupRoots(ctx, principal, permission, resourceType, b, stack, memo)
		if err != nil {
			return authmodel.LookupResult{}, err
		}
		if err := s.expandSelfHierarchy(ctx, resourceType, selfRelations, roots, after, fetch, func(ids []string) error {
			merge(ids)
			return nil
		}); err != nil {
			return authmodel.LookupResult{}, err
		}
	}

	sort.Strings(union)
	validated, err := s.verifyLookup(ctx, principal, permission, resourceType, authmodel.LookupResult{IDs: union})
	if err != nil {
		return authmodel.LookupResult{}, err
	}
	union = validated.IDs
	hasMore := len(union) > limit
	if hasMore {
		// Clip so the returned slice cannot reach the withheld tail: a caller
		// appending to it would otherwise overwrite ids the next page owns.
		union = slices.Clip(union[:limit])
	}
	if union == nil {
		union = []string{} // guarantee a non-nil slice — empty means no access
	}
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	return authmodel.LookupResult{IDs: union, HasMore: hasMore}, nil
}

// ModelDigest is the relationship kind's model identity for cursor binding: the
// compiled schema digest. A deploy that changes the schema changes it, which
// invalidates every in-flight lookup cursor bound to this kind.
func (s *Service) ModelDigest() string { return s.SchemaDigest() }

// nodeKey contains exact opaque names; punctuation is valid and cannot alias
// another model node through delimiter concatenation.
type nodeKey struct{ resourceType, permission string }

// Enumeration discovers potential grants in reverse. Validate each returned
// resource from its own root so a hierarchy cannot bypass Check's depth/work
// limits by starting at a shallower sibling or a granted ancestor.
func (s *Service) verifyLookup(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string, result authmodel.LookupResult) (authmodel.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	for start := 0; start < len(result.IDs); {
		end := start + min(s.limits.MaxBatchSize, len(result.IDs)-start)
		requests := make([]authmodel.CheckRequest, end-start)
		for i, id := range result.IDs[start:end] {
			requests[i] = authmodel.CheckRequest{Principal: principal, Permission: permission, Resource: authmodel.Resource{Type: resourceType, ID: id}}
		}
		decisions, err := s.CheckBatch(ctx, requests)
		if err != nil {
			return authmodel.LookupResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return authmodel.LookupResult{}, err
		}
		for _, decision := range decisions {
			if !decision.Allowed {
				return authmodel.LookupResult{}, fmt.Errorf("authorization: relationships changed during enumeration: %w", sdk.ErrConflict)
			}
		}
		start = end
	}
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if result.IDs == nil {
		result.IDs = []string{}
	}
	return result, nil
}
