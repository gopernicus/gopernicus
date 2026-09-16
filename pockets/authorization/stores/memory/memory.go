// Package memory provides one mutex-backed canonical authorization authority.
// Tuple, relationship, mutation and audit views share its facts and write
// boundary. Model-scoped userset expansion mirrors the SQL recursive closure;
// exact roles read the same facts without expansion.
//
// It exists because the pocket's zero-infra consumer proof (examples) and the
// conformance suite (storetest) run on it — the pockets/jobs/stores/memory
// precedent. It remains a package in the authorization core module.
package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/gopernicus/gopernicus/pockets/authorization/internal/tuplekey"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// relRow is one stored relationship tuple.
type relRow struct {
	resourceType    string
	resourceID      string
	relation        string
	subjectType     string
	subjectID       string
	subjectRelation string
}

// state shares one serialization boundary for tuple, role and optional audit
// publication. A write stages owned fact slices before making anything visible.
type state struct {
	mu           sync.Mutex
	facts        map[tuples.Tuple]struct{}
	recordAudit  bool
	auditRecords []audit.Record
}

func newState() *state { return &state{facts: make(map[tuples.Tuple]struct{})} }
func (s *state) relationshipRows() []relRow {
	out := make([]relRow, 0, len(s.facts))
	for t := range s.facts {
		if t.Scope.Kind == tuples.ResourceScope {
			out = append(out, relRow{resourceType: t.Scope.Type, resourceID: t.Scope.ID, relation: t.Relation, subjectType: t.Subject.Type, subjectID: t.Subject.ID, subjectRelation: t.Subject.Relation})
		}
	}
	return out
}

// Relationships is the in-core relationship.Storer.
type Relationships struct {
	model *relationships.ReadModel
	st    *state
}

// NewRelationships builds an empty relationship store over its own private state.
// Use [New] when the relationship, role, and mutation stores must share one lock
// and one snapshot (the atomic write path).
func NewRelationships() *Relationships {
	return &Relationships{st: newState()}
}

var _ relationships.Storer = (*Relationships)(nil)

// CreateRelationships inserts exact canonical facts, including independent labels.
func (r *Relationships) CreateRelationships(ctx context.Context, in []relationships.CreateRelationship) error {
	changes := tuples.Changes{}
	for _, row := range in {
		changes.Add = append(changes.Add, row.Tuple())
	}
	return (&Tuples{st: r.st}).ApplyTuples(ctx, changes)
}
func (r *Relationships) SetRelationTargets(ctx context.Context, rt, id, relation string, in []relationships.CreateRelationship) error {
	subjects := make([]tuples.SubjectRef, 0, len(in))
	for _, row := range in {
		if row.ResourceType != rt || row.ResourceID != id || row.Relation != relation {
			return sdk.ErrInvalidInput
		}
		if err := row.Validate(); err != nil {
			return err
		}
		subjects = append(subjects, row.Subject())
	}
	return (&Tuples{st: r.st}).ReconcileTuples(ctx, tuples.On(rt, id), relation, subjects)
}

// reachable is a subject reference reached during userset expansion — a
// (type, id, relation) triple. An empty relation is the concrete seed subject;
// a non-empty relation is the exact userset the subject belongs to.
type reachable = [3]string

// expandReachable returns every subject reference the concrete subject IS,
// transitively: the seed (subject, "") plus every exact userset
// (resource_type:resource_id#relation) it holds a relation on, walked through
// stored subject_relation state so the userset RELATION is load-bearing (a member
// edge never yields an admin userset). Cycle-safe via the visited set on the full
// triple — the honest mirror of the recursive CTE.
//
// maxExpansionStates bounds the visited-set GROWTH: the walk stops and reports
// overflow (the returned bool) the instant a new distinct state would push the
// count past maxExpansionStates, so a large/deep membership graph does work
// bounded by the budget, never by the graph (F4). maxExpansionStates <= 0 is
// unbounded (the guard-path and lookup callers that opt out of a budget). A
// within-budget graph returns the full reachable set and overflow=false —
// identical to the unbounded walk.
func (r *Relationships) expandReachable(ctx context.Context, subjectType, subjectID string, maxExpansionStates int) (map[reachable]bool, bool) {
	start := reachable{subjectType, subjectID, ""}
	seen := map[reachable]bool{start: true}
	queue := []reachable{start}
	for len(queue) > 0 {
		if ctx.Err() != nil {
			return seen, false
		}
		cur := queue[0]
		queue = queue[1:]
		for _, row := range r.st.relationshipRows() {
			if ctx.Err() != nil {
				return seen, false
			}
			if !r.allows(row) {
				continue
			}
			if row.subjectType != cur[0] || row.subjectID != cur[1] || row.subjectRelation != cur[2] {
				continue
			}
			next := reachable{row.resourceType, row.resourceID, row.relation}
			if !seen[next] {
				if maxExpansionStates > 0 && len(seen) >= maxExpansionStates {
					// Adding this state would push the distinct count past the
					// budget: stop and report overflow (indeterminate), never a
					// truncated reachable set.
					return seen, true
				}
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen, false
}

// CheckRelationWithGroupExpansion reports whether the subject (or any exact
// userset it transitively belongs to) has the relation on the resource. The
// grant tuple's stored subject_relation must match the reached userset relation
// exactly: a group#admin grant is never satisfied by group#member membership.
func (r *Relationships) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	reached, overflow := r.expandReachable(ctx, subjectType, subjectID, maxExpansionStates)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if overflow {
		return false, relationships.ErrExpansionBudgetExceeded
	}
	for _, row := range r.st.relationshipRows() {
		if !r.allows(row) {
			continue
		}
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			reached[reachable{row.subjectType, row.subjectID, row.subjectRelation}] {
			return true, nil
		}
	}
	return false, nil
}

// checkRelationExpandedLocked evaluates against the held snapshot without
// recursively locking. The shared write lock protects every traversed fact,
// including absent memberships, until the guarded write is published.
// A positive expansion bound fails closed when exceeded.
func (r *Relationships) checkRelationExpandedLocked(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (ok, overflow bool) {
	reached, overflow := r.expandReachable(ctx, subjectType, subjectID, maxExpansionStates)
	if overflow {
		return false, true
	}
	for _, row := range r.st.relationshipRows() {
		if !r.allows(row) {
			continue
		}
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			reached[reachable{row.subjectType, row.subjectID, row.subjectRelation}] {
			return true, false
		}
	}
	return false, false
}

// CheckRelationExists reports whether an exact direct tuple is present for a
// CONCRETE subject (no expansion; a stored userset tuple with the same type/id
// does not satisfy a concrete probe).
func (r *Relationships) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	for _, row := range r.st.relationshipRows() {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			row.subjectType == subjectType && row.subjectID == subjectID && row.subjectRelation == "" {
			return true, nil
		}
	}
	return false, nil
}

// GetRelationTargets returns the subjects holding a relation on a resource.
func (r *Relationships) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.getRelationTargetsLocked(resourceType, resourceID, relation), nil
}

// getRelationTargetsLocked is the non-locking core of GetRelationTargets: the
// caller already holds st.mu. It is the read the mutation repository's
// DecisionView uses for a Through hop against the held snapshot.
func (r *Relationships) getRelationTargetsLocked(resourceType, resourceID, relation string) []relationships.RelationTarget {
	var out []relationships.RelationTarget
	for _, row := range r.st.relationshipRows() {
		if !r.allows(row) {
			continue
		}
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation {
			out = append(out, relationships.RelationTarget{
				Type:     row.subjectType,
				ID:       row.subjectID,
				Relation: row.subjectRelation,
			})
		}
	}
	return out
}

// FilterRelation returns the DISTINCT, byte-order sorted subset of resourceIDs
// the subject holds relation on, directly or through exact userset expansion —
// the set form of CheckRelationWithGroupExpansion over ONE shared walk of the
// membership graph. An empty input is an empty result with no walk at all.
func (r *Relationships) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		want[id] = true
	}
	reached, overflow := r.expandReachable(ctx, subjectType, subjectID, maxExpansionStates)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if overflow {
		return nil, relationships.ErrExpansionBudgetExceeded
	}
	return r.distinctResourceIDs(func(row relRow) bool {
		return row.resourceType == resourceType && row.relation == relation && want[row.resourceID] &&
			reached[reachable{row.subjectType, row.subjectID, row.subjectRelation}]
	}), nil
}

// RelationTargetsFor returns the subjects holding relation on each of
// resourceIDs — the set form of GetRelationTargets over one pass of the rows.
// An id with no targets is absent from the map; a duplicated input id carries
// one entry.
func (r *Relationships) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]relationships.RelationTarget, error) {
	out := make(map[string][]relationships.RelationTarget, len(resourceIDs))
	if len(resourceIDs) == 0 {
		return out, nil
	}
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		want[id] = true
	}
	for _, row := range r.st.relationshipRows() {
		if !r.allows(row) {
			continue
		}
		if row.resourceType != resourceType || row.relation != relation || !want[row.resourceID] {
			continue
		}
		out[row.resourceID] = append(out[row.resourceID], relationships.RelationTarget{
			Type:     row.subjectType,
			ID:       row.subjectID,
			Relation: row.subjectRelation,
		})
	}
	return out, nil
}

// CheckBatchDirect returns resourceID -> allowed for one relation across ids,
// with group expansion.
func (r *Relationships) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(resourceIDs))
	out := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		want[id] = true
		out[id] = false
	}
	reached, overflow := r.expandReachable(ctx, subjectType, subjectID, maxExpansionStates)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if overflow {
		return nil, relationships.ErrExpansionBudgetExceeded
	}
	for _, row := range r.st.relationshipRows() {
		if !r.allows(row) {
			continue
		}
		if row.resourceType == resourceType && row.relation == relation && want[row.resourceID] &&
			reached[reachable{row.subjectType, row.subjectID, row.subjectRelation}] {
			out[row.resourceID] = true
		}
	}
	return out, nil
}

// CountByResourceAndRelation counts DIRECT tuples only.
func (r *Relationships) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	n := 0
	for _, row := range r.st.relationshipRows() {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation {
			n++
		}
	}
	return n, nil
}

// DeleteResourceRelationships removes every canonical fact scoped to the resource.
func (r *Relationships) DeleteResourceRelationships(ctx context.Context, rt, id string) error {
	return (&Tuples{st: r.st}).DeleteScope(ctx, tuples.On(rt, id))
}
func (r *Relationships) DeleteRelationshipTarget(ctx context.Context, rt, id, relation string, subject relationships.SubjectRef) error {
	return (&Tuples{st: r.st}).ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{{Scope: tuples.On(rt, id), Relation: relation, Subject: subject}}})
}
func (r *Relationships) DeleteRelationship(ctx context.Context, rt, id, relation, st, sid string) error {
	return r.DeleteRelationshipTarget(ctx, rt, id, relation, relationships.SubjectRef{Type: st, ID: sid})
}
func (r *Relationships) DeleteByResourceAndSubject(ctx context.Context, rt, id, st, sid string) error {
	scope := tuples.On(rt, id)
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := (tuples.SubjectRef{Type: st, ID: sid}).Validate(); err != nil {
		return err
	}
	return r.st.write(ctx, func(next *state) error {
		for t := range next.facts {
			if t.Scope == scope && t.Subject.Type == st && t.Subject.ID == sid {
				delete(next.facts, t)
			}
		}
		return nil
	})
}

// LookupResourceIDs returns the distinct resource IDs where the subject has any
// of the relations (with group expansion), strictly after `after`, capped at
// limit (see relationship.Storer).
func (r *Relationships) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	relSet := make(map[string]bool, len(relations))
	for _, rel := range relations {
		relSet[rel] = true
	}
	reached, _ := r.expandReachable(ctx, subjectType, subjectID, 0)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return capIDs(afterIDs(r.distinctResourceIDs(func(row relRow) bool {
		return row.resourceType == resourceType && relSet[row.relation] &&
			reached[reachable{row.subjectType, row.subjectID, row.subjectRelation}]
	}), after), limit), nil
}

// LookupResourceIDsByRelationTarget returns distinct resource IDs whose relation
// points at any of the target IDs, strictly after `after`, capped at limit.
func (r *Relationships) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targets := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		targets[id] = true
	}
	return capIDs(afterIDs(r.distinctResourceIDs(func(row relRow) bool {
		return row.resourceType == resourceType && row.relation == relation &&
			row.subjectType == targetType && targets[row.subjectID] && row.subjectRelation == ""
	}), after), limit), nil
}

// LookupDescendantResourceIDs walks the union of the self-referential relations
// transitively from the root IDs (cycle-safe fixpoint), then returns the sorted
// closure strictly after `after`, capped at limit.
func (r *Relationships) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	if len(rootIDs) == 0 || len(relations) == 0 {
		return nil, nil
	}
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	relSet := make(map[string]bool, len(relations))
	for _, rel := range relations {
		relSet[rel] = true
	}

	result := map[string]bool{}
	frontier := make(map[string]bool, len(rootIDs))
	for _, id := range rootIDs {
		frontier[id] = true
	}
	for len(frontier) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := map[string]bool{}
		for _, row := range r.st.relationshipRows() {
			if !r.allows(row) {
				continue
			}
			if row.resourceType == resourceType && relSet[row.relation] && row.subjectType == subjectType &&
				row.subjectRelation == "" && frontier[row.subjectID] && !result[row.resourceID] {
				result[row.resourceID] = true
				next[row.resourceID] = true
			}
		}
		frontier = next
	}

	out := make([]string, 0, len(result))
	for id := range result {
		out = append(out, id)
	}
	sort.Strings(out)
	return capIDs(afterIDs(out, after), limit), nil
}

// afterIDs drops the prefix of a sorted id slice that is <= after — the keyset
// half of the lookup contract (after == "" keeps everything).
func afterIDs(ids []string, after string) []string {
	if after == "" {
		return ids
	}
	i := sort.SearchStrings(ids, after)
	if i < len(ids) && ids[i] == after {
		i++
	}
	return ids[i:]
}

// capIDs truncates a sorted, distinct ID slice to at most limit entries. A
// non-positive limit is unbounded (defensive; the engine always passes a
// positive cap of MaxLookupResults+1, so a full-limit return is the overflow
// signal it fails closed on — never a silent truncation to complete).
func capIDs(ids []string, limit int) []string {
	if limit > 0 && len(ids) > limit {
		return ids[:limit]
	}
	return ids
}

// ListRelationshipsBySubject pages the resources a subject relates to.
func (r *Relationships) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationships.SubjectRelationshipFilter, req list.Request) (list.Page[relationships.SubjectRelationship], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var items []relationships.SubjectRelationship
	for _, row := range r.st.relationshipRows() {
		if row.subjectType != subjectType || row.subjectID != subjectID {
			continue
		}
		if filter.ResourceType != nil && *filter.ResourceType != row.resourceType {
			continue
		}
		if filter.Relation != nil && *filter.Relation != row.relation {
			continue
		}
		items = append(items, relationships.SubjectRelationship{
			ResourceType:    row.resourceType,
			ResourceID:      row.resourceID,
			Relation:        row.relation,
			SubjectRelation: row.subjectRelation,
		})
	}
	return pageMemByKey(items, req, relationships.OrderFields, "tuple_key", func(s relationships.SubjectRelationship) string {
		return tupleKey(s.ResourceType, s.ResourceID, s.Relation, subjectType, subjectID, s.SubjectRelation)
	})
}

// ListRelationshipsByResource pages the subjects related to a resource.
func (r *Relationships) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationships.ResourceRelationshipFilter, req list.Request) (list.Page[relationships.ResourceRelationship], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var items []relationships.ResourceRelationship
	for _, row := range r.st.relationshipRows() {
		if row.resourceType != resourceType || row.resourceID != resourceID {
			continue
		}
		if filter.SubjectType != nil && *filter.SubjectType != row.subjectType {
			continue
		}
		if filter.Relation != nil && *filter.Relation != row.relation {
			continue
		}
		items = append(items, relationships.ResourceRelationship{
			SubjectType:     row.subjectType,
			SubjectID:       row.subjectID,
			Relation:        row.relation,
			SubjectRelation: row.subjectRelation,
		})
	}
	return pageMemByKey(items, req, relationships.OrderFields, "tuple_key", func(s relationships.ResourceRelationship) string {
		return tupleKey(resourceType, resourceID, s.Relation, s.SubjectType, s.SubjectID, s.SubjectRelation)
	})
}

// distinctResourceIDs collects the sorted-distinct resource IDs of rows matching
// pred. Caller holds the lock.
func (r *Relationships) distinctResourceIDs(pred func(relRow) bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, row := range r.st.relationshipRows() {
		if !r.allows(row) {
			continue
		}
		if pred(row) && !seen[row.resourceID] {
			seen[row.resourceID] = true
			out = append(out, row.resourceID)
		}
	}
	sort.Strings(out)
	return out
}

func keepRows(rows []relRow, keep func(relRow) bool) []relRow {
	out := rows[:0:0]
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	return out
}

// tupleKey orders the exact six-field relationship identity. Input validation
// forbids the delimiter in every component, including the optional userset.
func tupleKey(resourceType, resourceID, relation, subjectType, subjectID, subjectRelation string) string {
	return tuplekey.Encode(tuples.Tuple{Scope: tuples.On(resourceType, resourceID), Relation: relation, Subject: tuples.SubjectRef{Type: subjectType, ID: subjectID, Relation: subjectRelation}})
}
