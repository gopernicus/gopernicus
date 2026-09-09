package authorizersvc

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
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

// setKey identifies one exact SET read: the sorted, distinct candidate ids of
// the call joined under a separator that cannot occur in a well-formed
// reference component (ValidateRefField rejects control characters), plus the
// same arguments the per-resource key carries. maxExpansionStates is part of it
// for the same reason it is part of directKey — a call under a different bound
// is a DIFFERENT read.
type setKey struct {
	resourceType       string
	ids                string
	relation           string
	subjectType        string
	subjectID          string
	maxExpansionStates int
}

// setIDsKey renders a candidate id list as the canonical, order-independent key
// component of a set read. Two calls over the same ids in any order (or with
// repeats) are the SAME read and share one memo entry.
func setIDsKey(ids []string) string {
	sorted := make([]string, len(ids))
	copy(sorted, ids)
	sort.Strings(sorted)
	return strings.Join(slices.Compact(sorted), "\x00")
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
	inner      PermissionReader
	targets    map[targetsKey][]relationship.RelationTarget
	direct     map[directKey]bool
	setTargets map[setKey]map[string][]relationship.RelationTarget
	setDirect  map[setKey][]string
}

func newMemoReader(inner PermissionReader) *memoReader {
	return &memoReader{
		inner:      inner,
		targets:    make(map[targetsKey][]relationship.RelationTarget),
		direct:     make(map[directKey]bool),
		setTargets: make(map[setKey]map[string][]relationship.RelationTarget),
		setDirect:  make(map[setKey][]string),
	}
}

func (m *memoReader) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	k := directKey{resourceType, resourceID, relation, subjectType, subjectID, maxExpansionStates}
	if allowed, ok := m.direct[k]; ok {
		return allowed, nil
	}
	allowed, err := m.inner.CheckRelationWithGroupExpansion(ctx, resourceType, resourceID, relation, subjectType, subjectID, maxExpansionStates)
	if err != nil {
		return false, err
	}
	if ctx.Err() != nil {
		return allowed, nil
	}
	m.direct[k] = allowed
	return allowed, nil
}

func (m *memoReader) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	k := targetsKey{resourceType, resourceID, relation}
	if targets, ok := m.targets[k]; ok {
		return cloneTargets(targets), nil
	}
	targets, err := m.inner.GetRelationTargets(ctx, resourceType, resourceID, relation)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return targets, nil
	}
	m.targets[k] = cloneTargets(targets)
	return cloneTargets(targets), nil
}

// cloneTargets copies a relation-target slice, preserving nil. RelationTarget is
// a flat value type, so a shallow copy fully isolates the memo from its callers.
func cloneTargets(in []relationship.RelationTarget) []relationship.RelationTarget {
	if in == nil {
		return nil
	}
	out := make([]relationship.RelationTarget, len(in))
	copy(out, in)
	return out
}

// FilterRelation memoizes the SET form of the direct read under the sorted,
// distinct id set plus the same arguments — so a set evaluation that reaches
// one node from two branches, and a batch whose requests share a candidate set,
// read once. Only a successful, uncanceled result is cached, and the cached
// slice is copied on store and on every return.
func (m *memoReader) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	k := setKey{resourceType, setIDsKey(resourceIDs), relation, subjectType, subjectID, maxExpansionStates}
	if ids, ok := m.setDirect[k]; ok {
		return slices.Clone(ids), nil
	}
	ids, err := m.inner.FilterRelation(ctx, resourceType, resourceIDs, relation, subjectType, subjectID, maxExpansionStates)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return ids, nil
	}
	m.setDirect[k] = slices.Clone(ids)
	return slices.Clone(ids), nil
}

// RelationTargetsFor memoizes the SET form of the Through-hop read under the
// same id-set key. The cached map and every slice in it are copied on store and
// on every return, so a caller ranging or mutating the result can never reach
// memo state.
func (m *memoReader) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]relationship.RelationTarget, error) {
	if len(resourceIDs) == 0 {
		return map[string][]relationship.RelationTarget{}, nil
	}
	k := setKey{resourceType: resourceType, ids: setIDsKey(resourceIDs), relation: relation}
	if targets, ok := m.setTargets[k]; ok {
		return cloneTargetsMap(targets), nil
	}
	targets, err := m.inner.RelationTargetsFor(ctx, resourceType, resourceIDs, relation)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return targets, nil
	}
	m.setTargets[k] = cloneTargetsMap(targets)
	return cloneTargetsMap(targets), nil
}

// cloneTargetsMap copies a set-read result: a fresh map whose every entry is a
// fresh slice, so neither the memo nor a caller can observe the other's writes.
func cloneTargetsMap(in map[string][]relationship.RelationTarget) map[string][]relationship.RelationTarget {
	out := make(map[string][]relationship.RelationTarget, len(in))
	for id, targets := range in {
		out[id] = cloneTargets(targets)
	}
	return out
}
