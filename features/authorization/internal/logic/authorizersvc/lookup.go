package authorizersvc

import (
	"context"
	"sort"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
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
func (s *Service) LookupResources(ctx context.Context, principal PrincipalRef, permission, resourceType string) (LookupResult, error) {
	if err := principal.Validate(); err != nil {
		return LookupResult{}, err
	}
	if err := relationship.ValidateRefField("permission", permission); err != nil {
		return LookupResult{}, err
	}
	if err := relationship.ValidateRefField("resource type", resourceType); err != nil {
		return LookupResult{}, err
	}
	return s.lookupResources(ctx, principal, permission, resourceType, newBudget(s.limits), make(map[string]bool), make(map[string]LookupResult))
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
func (s *Service) lookupResources(ctx context.Context, principal PrincipalRef, permission, resourceType string, b *budget, stack map[string]bool, memo map[string]LookupResult) (LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return LookupResult{}, err
	}
	key := resourceType + ":" + permission
	if res, done := memo[key]; done {
		return res, nil
	}
	if stack[key] {
		// Genuine cycle on the active path (compiler-forbidden for non-self
		// Through; defensive): contribute nothing without memoizing an incomplete
		// result.
		return LookupResult{IDs: []string{}}, nil
	}

	checks := s.compiled.permissionChecks(resourceType, permission)
	if len(checks) == 0 {
		res := LookupResult{IDs: []string{}}
		memo[key] = res
		return res, nil
	}

	stack[key] = true

	seen := make(map[string]bool)
	var ids []string
	add := func(newIDs []string) error {
		for _, id := range newIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if b.resultsOverflow(len(ids)) {
			return ErrEvaluationLimit
		}
		return nil
	}

	// selfRelations collects the same-permission self-referential Through
	// relations (target type == resourceType AND target permission == permission)
	// — the sanctioned hierarchy self-loop. Their descendants are expanded from
	// the full root set AFTER the non-self roots are gathered, so a root derived
	// through a non-self Through still seeds descendant expansion (D1(c)).
	var selfRelations []string
	selfSeen := make(map[string]bool)

	fail := func(err error) (LookupResult, error) {
		delete(stack, key)
		return LookupResult{}, err
	}

	for _, check := range checks {
		if check.Through == "" {
			found, err := s.store.LookupResourceIDs(ctx, resourceType, []string{check.Relation}, principal.Type, principal.ID, b.resultFetchCap())
			if err != nil {
				return fail(err)
			}
			if err := add(found); err != nil {
				return fail(err)
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
				// from the full root set (below). Recursing here would hit the
				// stack cycle guard and contribute nothing; the store's transitive
				// walk resolves the self-loop instead.
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
				return fail(err)
			}
			if len(targetResult.IDs) == 0 {
				continue
			}
			throughIDs, err := s.store.LookupResourceIDsByRelationTarget(ctx, resourceType, check.Through, targetType, targetResult.IDs, b.resultFetchCap())
			if err != nil {
				return fail(err)
			}
			if err := add(throughIDs); err != nil {
				return fail(err)
			}
		}
	}

	// ids now holds the ROOT set: every resource granted the permission WITHOUT
	// descending the self-hierarchy. Expand descendants from all of them (D1(c)).
	if len(selfRelations) > 0 {
		if err := s.expandSelfHierarchy(ctx, resourceType, selfRelations, ids, b, seen, add); err != nil {
			return fail(err)
		}
	}

	delete(stack, key)
	if ids == nil {
		ids = []string{} // guarantee a non-nil slice — empty means no access
	}
	sort.Strings(ids) // deterministic ordering, each ID exactly once (dedup above)
	res := LookupResult{IDs: ids}
	memo[key] = res
	return res, nil
}

// expandSelfHierarchy expands a same-permission self-referential hierarchy: given
// the root resource IDs already granted the permission by non-self means, it adds
// every descendant reachable by transitively following a self-referential
// relation (space→parent→space) toward a root. The store's descendant walk is
// cycle-safe and does the full transitive closure per relation in one call; the
// outer fixpoint loop only matters when TWO OR MORE self relations interleave (a
// node reached via relation A becomes a root for relation B), and terminates
// because the universe is finite and each round adds only previously-unseen IDs.
//
// Every discovered ID is added through add (which dedups, charges the result
// budget, and reports overflow as ErrEvaluationLimit — never a truncated list).
func (s *Service) expandSelfHierarchy(ctx context.Context, resourceType string, selfRelations, roots []string, b *budget, seen map[string]bool, add func([]string) error) error {
	frontier := append([]string(nil), roots...)
	for len(frontier) > 0 {
		var next []string
		for _, rel := range selfRelations {
			if err := ctx.Err(); err != nil {
				return err
			}
			desc, err := s.store.LookupDescendantResourceIDs(ctx, resourceType, rel, resourceType, frontier, b.resultFetchCap())
			if err != nil {
				return err
			}
			for _, id := range desc {
				if !seen[id] {
					next = append(next, id)
				}
			}
			if err := add(desc); err != nil {
				return err
			}
		}
		frontier = next
	}
	return nil
}
