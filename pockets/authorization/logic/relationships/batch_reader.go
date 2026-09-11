package relationships

import (
	"cmp"
	"context"
	"slices"
)

// targetsKey identifies one exact GetRelationTargets read: a (resource,
// relation) edge.
type targetsKey struct {
	resourceType string
	resourceID   string
	relation     string
}

// directKey identifies one exact CheckRelationWithGroupExpansion read. It
// includes maxExpansionStates because the expansion bound is an argument of the
// read: a call made under a different bound is a DIFFERENT read, not a hit.
type directKey struct {
	resourceType       string
	resourceID         string
	relation           string
	subjectType        string
	subjectID          string
	maxExpansionStates int
}

// memoReader is the batch-local PermissionReader that shares successful store
// READS across the requests of one sequential batch (B4). It caches nothing but
// the two relationship reads the walk makes, keyed by their exact arguments —
// no decision, no budget, no enumeration is shared. Each request still runs the
// ordinary evaluator with its OWN fresh budget, so depth, distinct graph states
// and per-hop fan-out are charged exactly as a standalone Check charges them and
// every batch result (and every ErrEvaluationLimit) is identical to the
// sequential Check it replaces.
//
// It deliberately never invokes lookupResources or any Lookup* store method: a
// small bounded container listing must not fail because the principal's total
// accessible-container set exceeds MaxLookupResults.
//
// Only SUCCESSFUL results are memoized. An error is never cached (the next
// request re-reads), and neither is a result observed after the context was
// canceled. Cached slices are copied on store and on every return, so a caller
// ranging or mutating a returned slice can never reach memo state.
//
// A memoReader is NOT safe for concurrent use: one batch evaluates on one
// goroutine, exactly like the budget it rides beside.
type memoReader struct {
	inner   PermissionReader
	targets map[targetsKey][]RelationTarget
	direct  map[directKey]bool
}

func newMemoReader(inner PermissionReader) *memoReader {
	return &memoReader{
		inner:   inner,
		targets: make(map[targetsKey][]RelationTarget),
		direct:  make(map[directKey]bool),
	}
}

func (m *memoReader) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	k := directKey{resourceType, resourceID, relation, subjectType, subjectID, maxExpansionStates}
	if allowed, ok := m.direct[k]; ok {
		return allowed, nil
	}
	allowed, err := m.inner.CheckRelationWithGroupExpansion(ctx, resourceType, resourceID, relation, subjectType, subjectID, maxExpansionStates)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.direct[k] = allowed
	return allowed, nil
}

func (m *memoReader) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k := targetsKey{resourceType, resourceID, relation}
	if targets, ok := m.targets[k]; ok {
		return cloneTargets(targets), nil
	}
	targets, err := m.inner.GetRelationTargets(ctx, resourceType, resourceID, relation)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owned := cloneTargets(targets)
	// A store's physical row order must not choose whether a granting target
	// or an erroring target is visited first. Every evaluator entry point,
	// including guarded reads, uses this same canonical order.
	slices.SortFunc(owned, func(a, b RelationTarget) int {
		if order := cmp.Compare(a.Type, b.Type); order != 0 {
			return order
		}
		if order := cmp.Compare(a.ID, b.ID); order != 0 {
			return order
		}
		return cmp.Compare(a.Relation, b.Relation)
	})
	m.targets[k] = owned
	return cloneTargets(owned), nil
}

// cloneTargets copies a relation-target slice, preserving nil. RelationTarget is
// a flat value type, so a shallow copy fully isolates the memo from its callers.
func cloneTargets(in []RelationTarget) []RelationTarget {
	if in == nil {
		return nil
	}
	out := make([]RelationTarget, len(in))
	copy(out, in)
	return out
}
