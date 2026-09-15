// Package memory is the public in-core reference implementation of BOTH
// authorization kinds — relationship.Storer (a Go graph-walk ReBAC engine) and
// role.Storer (plain maps). It is mutex-backed and honest: group expansion is
// re-implemented as a real transitive walk (unbounded but cycle-safe via a
// visited set, mirroring the SQL stores' recursive CTE, which terminates by
// UNION dedup alone), unique-tuple enforcement is genuine, and counts are
// direct-only.
//
// It exists because the pocket's zero-infra consumer proof (examples) and the
// conformance suite (storetest) run on it — the pockets/jobs/stores/memory
// precedent. It remains a package in the authorization core module.
package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

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
	rel          []relRow
	role         []roleRow
	recordAudit  bool
	auditRecords []audit.Record
}

func newState() *state { return &state{} }

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

// CreateRelationships inserts tuples with the ON CONFLICT DO NOTHING mirror on
// the unique-SUBJECT key (resource_type, resource_id, subject_type, subject_id,
// subject_relation) — the honest mirror of idx_iam_relationships_unique_subject,
// which excludes the relation. A subject REFERENCE (its type, id, AND userset
// relation) holds at most one relation on a resource, so a second row for the
// SAME subject reference — same relation or different — is skipped silently (nil
// error, the existing tuple unchanged).
// Because the key includes subject_relation, distinct usersets on one resource
// (group:eng#member vs group:eng#admin) are DIFFERENT subject references and BOTH
// persist. The validated full tuple is its identity.
func (r *Relationships) CreateRelationships(ctx context.Context, in []relationships.CreateRelationship) error {
	return r.st.write(ctx, func(next *state) error {
		return (&Relationships{st: next}).createRelationshipsLocked(ctx, in)
	})
}

func (r *Relationships) createRelationshipsLocked(ctx context.Context, in []relationships.CreateRelationship) error {
	if len(in) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, c := range in {
		if err := c.Validate(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, c := range in {
		if r.hasSubjectResource(c.SubjectType, c.SubjectID, c.SubjectRelation, c.ResourceType, c.ResourceID) {
			continue // DO NOTHING
		}
		r.st.rel = append(r.st.rel, relRow{
			resourceType:    c.ResourceType,
			resourceID:      c.ResourceID,
			relation:        c.Relation,
			subjectType:     c.SubjectType,
			subjectID:       c.SubjectID,
			subjectRelation: c.SubjectRelation,
		})
	}
	return nil
}

// SetRelationTargets atomically reconciles one resource+relation under the
// store's shared mutex. Existing desired tuples remain unchanged;
// surplus rows are removed and missing rows are added. A desired subject already
// holding a different relation makes the requested state impossible under the
// one-relation rule, so the method fails before changing anything.
func (r *Relationships) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, in []relationships.CreateRelationship) error {
	return r.st.write(ctx, func(next *state) error {
		return (&Relationships{st: next}).setRelationTargetsLocked(ctx, resourceType, resourceID, relationName, in)
	})
}

func (r *Relationships) setRelationTargetsLocked(ctx context.Context, resourceType, resourceID, relationName string, in []relationships.CreateRelationship) error {

	if err := ctx.Err(); err != nil {
		return err
	}
	desired := make(map[relationships.SubjectRef]struct{}, len(in))
	for _, c := range in {
		if err := c.Validate(); err != nil {
			return err
		}
		if c.ResourceType != resourceType || c.ResourceID != resourceID || c.Relation != relationName {
			return fmt.Errorf("authorization memstore: SetRelationTargets row is outside requested scope: %w", sdk.ErrInvalidInput)
		}
		desired[c.Subject()] = struct{}{}
	}
	for _, row := range r.st.rel {
		ref := relationships.SubjectRef{Type: row.subjectType, ID: row.subjectID, Relation: row.subjectRelation}
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation != relationName {
			if _, ok := desired[ref]; ok {
				return fmt.Errorf("authorization memstore: target %s already holds relation %q on %s:%s: %w",
					ref, row.relation, resourceType, resourceID, sdk.ErrConflict)
			}
		}
	}

	existing := make(map[relationships.SubjectRef]struct{}, len(desired))
	r.st.rel = keepRows(r.st.rel, func(row relRow) bool {
		if row.resourceType != resourceType || row.resourceID != resourceID || row.relation != relationName {
			return true
		}
		ref := relationships.SubjectRef{Type: row.subjectType, ID: row.subjectID, Relation: row.subjectRelation}
		if _, ok := desired[ref]; ok {
			existing[ref] = struct{}{}
			return true
		}
		return false
	})

	for ref := range desired {
		if _, ok := existing[ref]; ok {
			continue
		}
		r.st.rel = append(r.st.rel, relRow{
			resourceType: resourceType, resourceID: resourceID, relation: relationName,
			subjectType: ref.Type, subjectID: ref.ID, subjectRelation: ref.Relation,
		})
	}
	return nil
}

func (r *Relationships) hasSubjectResource(subjectType, subjectID, subjectRelation, resourceType, resourceID string) bool {
	for _, row := range r.st.rel {
		if row.subjectType == subjectType && row.subjectID == subjectID && row.subjectRelation == subjectRelation &&
			row.resourceType == resourceType && row.resourceID == resourceID {
			return true
		}
	}
	return false
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
		for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation {
			n++
		}
	}
	return n, nil
}

// DeleteResourceRelationships removes every tuple for a resource.
func (r *Relationships) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return r.st.write(ctx, func(next *state) error {
		return (&Relationships{st: next}).deleteResourceRelationshipsLocked(ctx, resourceType, resourceID)
	})
}

func (r *Relationships) deleteResourceRelationshipsLocked(ctx context.Context, resourceType, resourceID string) error {
	r.st.rel = keepRows(r.st.rel, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID)
	})
	return nil
}

// DeleteRelationshipTarget removes one exact tuple, including subject_relation.
func (r *Relationships) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target relationships.SubjectRef) error {
	return r.st.write(ctx, func(next *state) error {
		return (&Relationships{st: next}).deleteRelationshipTargetLocked(ctx, resourceType, resourceID, relationName, target)
	})
}

func (r *Relationships) deleteRelationshipTargetLocked(ctx context.Context, resourceType, resourceID, relationName string, target relationships.SubjectRef) error {
	r.st.rel = keepRows(r.st.rel, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relationName &&
			row.subjectType == target.Type && row.subjectID == target.ID && row.subjectRelation == target.Relation)
	})
	return nil
}

// DeleteRelationship removes one exact tuple.
func (r *Relationships) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return r.st.write(ctx, func(next *state) error {
		return (&Relationships{st: next}).deleteRelationshipLocked(ctx, resourceType, resourceID, relation, subjectType, subjectID)
	})
}

func (r *Relationships) deleteRelationshipLocked(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	r.st.rel = keepRows(r.st.rel, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			row.subjectType == subjectType && row.subjectID == subjectID)
	})
	return nil
}

// DeleteByResourceAndSubject removes every relation a subject holds on a resource.
func (r *Relationships) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return r.st.write(ctx, func(next *state) error {
		return (&Relationships{st: next}).deleteByResourceAndSubjectLocked(ctx, resourceType, resourceID, subjectType, subjectID)
	})
}

func (r *Relationships) deleteByResourceAndSubjectLocked(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	r.st.rel = keepRows(r.st.rel, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID &&
			row.subjectType == subjectType && row.subjectID == subjectID)
	})
	return nil
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
		for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	for _, row := range r.st.rel {
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
	return strings.Join([]string{resourceType, resourceID, relation, subjectType, subjectID, subjectRelation}, "\x01")
}
