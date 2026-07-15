// Package memstore is the public in-core reference implementation of BOTH
// authorization kinds — relationship.Storer (a Go graph-walk ReBAC engine) and
// role.Storer (plain maps). It is mutex-backed and honest: group expansion is
// re-implemented as a real transitive walk (unbounded but cycle-safe via a
// visited set, mirroring the SQL stores' recursive CTE, which terminates by
// UNION dedup alone), unique-tuple enforcement is genuine, and counts are
// direct-only.
//
// It exists because the feature's zero-infra consumer proof (examples) and the
// conformance suite (storetest) run on it — the features/jobs/memstore
// precedent. It is never a stores/memory module.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// memIDs assigns a relationship_id when an incoming tuple carries none — the
// honest mirror of the schema DEFAULT (a fresh random key). A caller using the
// nanoid engine generator supplies ids already; a cryptids.Database generator
// leaves them empty and this fills them here.
var memIDs = cryptids.IDGenerator{}

// relRow is one stored relationship tuple.
type relRow struct {
	id              string
	resourceType    string
	resourceID      string
	relation        string
	subjectType     string
	subjectID       string
	subjectRelation string
	createdAt       time.Time
}

// state is the ONE mutex-guarded backing store the three in-core stores share
// when they are bundled by [New]: the relationship rows, the role rows, the scope
// revision anchors, and the mutation receipts all live behind st.mu. Sharing one
// lock is what lets the [Mutations] repository apply a command atomically (receipt
// lookup, current state, guard evaluation, invariant, row changes, revision bump,
// receipt persistence) while a raw [Relationships]/[Roles] read observes exactly
// the committed snapshot — the honest mirror of the SQL stores' shared tables plus
// a serializing transaction. A standalone [NewRelationships]/[NewRoles] owns its
// own private state (its own mutex), so the raw-store conformance is unaffected.
type state struct {
	mu       sync.Mutex
	rel      []relRow
	role     []roleRow
	scopes   map[string]mutation.Revision
	receipts map[mutation.MutationID]mutation.Receipt
}

func newState() *state {
	return &state{
		scopes:   map[string]mutation.Revision{},
		receipts: map[mutation.MutationID]mutation.Receipt{},
	}
}

// Relationships is the in-core relationship.Storer.
type Relationships struct {
	st *state
}

// NewRelationships builds an empty relationship store over its own private state.
// Use [New] when the relationship, role, and mutation stores must share one lock
// and one snapshot (the atomic write path).
func NewRelationships() *Relationships {
	return &Relationships{st: newState()}
}

var _ relationship.Storer = (*Relationships)(nil)

// CreateRelationships inserts tuples with the ON CONFLICT DO NOTHING mirror on
// the unique-SUBJECT key (resource_type, resource_id, subject_type, subject_id,
// subject_relation) — the honest mirror of idx_iam_relationships_unique_subject,
// which excludes the relation. A subject REFERENCE (its type, id, AND userset
// relation) holds at most one relation on a resource, so a second row for the
// SAME subject reference — same relation or different — is skipped silently (nil
// error, the existing row's id + created_at retained, no minted id leaked).
// Because the key includes subject_relation, distinct usersets on one resource
// (group:eng#member vs group:eng#admin) are DIFFERENT subject references and BOTH
// persist. An empty incoming id is assigned here (the DEFAULT mirror). The whole
// batch shares one created_at, making the id the load-bearing keyset tiebreak.
func (r *Relationships) CreateRelationships(ctx context.Context, in []relationship.CreateRelationship) error {
	if len(in) == 0 {
		return nil
	}
	r.st.mu.Lock()
	defer r.st.mu.Unlock()

	now := time.Now().UTC()
	for _, c := range in {
		if r.hasSubjectResource(c.SubjectType, c.SubjectID, c.SubjectRelation, c.ResourceType, c.ResourceID) {
			continue // DO NOTHING
		}
		id := c.RelationshipID
		if id == "" {
			id = memIDs.MustGenerate()
		}
		r.st.rel = append(r.st.rel, relRow{
			id:              id,
			resourceType:    c.ResourceType,
			resourceID:      c.ResourceID,
			relation:        c.Relation,
			subjectType:     c.SubjectType,
			subjectID:       c.SubjectID,
			subjectRelation: c.SubjectRelation,
			createdAt:       now,
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
func (r *Relationships) expandReachable(subjectType, subjectID string, maxExpansionStates int) (map[reachable]bool, bool) {
	start := reachable{subjectType, subjectID, ""}
	seen := map[reachable]bool{start: true}
	queue := []reachable{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, row := range r.st.rel {
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
	reached, overflow := r.expandReachable(subjectType, subjectID, maxExpansionStates)
	if overflow {
		return false, relationship.ErrExpansionBudgetExceeded
	}
	for _, row := range r.st.rel {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			reached[reachable{row.subjectType, row.subjectID, row.subjectRelation}] {
			return true, nil
		}
	}
	return false, nil
}

// checkRelationExpandScopesLocked is the non-locking core of
// CheckRelationWithGroupExpansion: the caller already holds st.mu. It is the read
// path the mutation repository's DecisionView uses to evaluate a guard against the
// held snapshot WITHOUT recursively locking. Alongside the boolean it returns the
// SET of subject references reached during expansion — every (type, id) whose
// membership edges the walk traversed — so the DecisionView can record each as a
// ScopeResource dependency; a concurrent membership revoke on an intermediate
// group scope then invalidates a guarded decision.
func (r *Relationships) checkRelationExpandScopesLocked(resourceType, resourceID, relation, subjectType, subjectID string) (bool, map[reachable]bool) {
	reached, _ := r.expandReachable(subjectType, subjectID, 0)
	for _, row := range r.st.rel {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			reached[reachable{row.subjectType, row.subjectID, row.subjectRelation}] {
			return true, reached
		}
	}
	return false, reached
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
func (r *Relationships) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var out []relationship.RelationTarget
	for _, row := range r.st.rel {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation {
			out = append(out, relationship.RelationTarget{
				Type:     row.subjectType,
				ID:       row.subjectID,
				Relation: row.subjectRelation,
			})
		}
	}
	return out, nil
}

// CheckBatchDirect returns resourceID -> allowed for one relation across ids,
// with group expansion.
func (r *Relationships) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	want := make(map[string]bool, len(resourceIDs))
	out := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		want[id] = true
		out[id] = false
	}
	reached, overflow := r.expandReachable(subjectType, subjectID, maxExpansionStates)
	if overflow {
		return nil, relationship.ErrExpansionBudgetExceeded
	}
	for _, row := range r.st.rel {
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
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	r.st.rel = keepRows(r.st.rel, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID)
	})
	return nil
}

// DeleteRelationship removes one exact tuple.
func (r *Relationships) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	r.st.rel = keepRows(r.st.rel, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			row.subjectType == subjectType && row.subjectID == subjectID)
	})
	return nil
}

// DeleteByResourceAndSubject removes every relation a subject holds on a resource.
func (r *Relationships) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	r.st.rel = keepRows(r.st.rel, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID &&
			row.subjectType == subjectType && row.subjectID == subjectID)
	})
	return nil
}

// LookupResourceIDs returns the distinct resource IDs where the subject has any
// of the relations (with group expansion), capped at limit (see relationship.Storer).
func (r *Relationships) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string, limit int) ([]string, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	relSet := make(map[string]bool, len(relations))
	for _, rel := range relations {
		relSet[rel] = true
	}
	reached, _ := r.expandReachable(subjectType, subjectID, 0)
	return capIDs(r.distinctResourceIDs(func(row relRow) bool {
		return row.resourceType == resourceType && relSet[row.relation] &&
			reached[reachable{row.subjectType, row.subjectID, row.subjectRelation}]
	}), limit), nil
}

// LookupResourceIDsByRelationTarget returns distinct resource IDs whose relation
// points at any of the target IDs, capped at limit.
func (r *Relationships) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, limit int) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	targets := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		targets[id] = true
	}
	return capIDs(r.distinctResourceIDs(func(row relRow) bool {
		return row.resourceType == resourceType && row.relation == relation &&
			row.subjectType == targetType && targets[row.subjectID] && row.subjectRelation == ""
	}), limit), nil
}

// LookupDescendantResourceIDs walks a self-referential relation transitively
// from the root IDs (cycle-safe fixpoint), capped at limit.
func (r *Relationships) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string, limit int) ([]string, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	r.st.mu.Lock()
	defer r.st.mu.Unlock()

	result := map[string]bool{}
	frontier := make(map[string]bool, len(rootIDs))
	for _, id := range rootIDs {
		frontier[id] = true
	}
	for len(frontier) > 0 {
		next := map[string]bool{}
		for _, row := range r.st.rel {
			if row.resourceType == resourceType && row.relation == relation && row.subjectType == subjectType &&
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
	return capIDs(out, limit), nil
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
func (r *Relationships) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var items []relationship.SubjectRelationship
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
		items = append(items, relationship.SubjectRelationship{
			ID:           row.id,
			ResourceType: row.resourceType,
			ResourceID:   row.resourceID,
			Relation:     row.relation,
			CreatedAt:    row.createdAt,
		})
	}
	return pageMem(items, req, relationship.OrderFields, func(s relationship.SubjectRelationship) (time.Time, string) {
		return s.CreatedAt, s.ID
	})
}

// ListRelationshipsByResource pages the subjects related to a resource.
func (r *Relationships) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var items []relationship.ResourceRelationship
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
		items = append(items, relationship.ResourceRelationship{
			ID:          row.id,
			SubjectType: row.subjectType,
			SubjectID:   row.subjectID,
			Relation:    row.relation,
			CreatedAt:   row.createdAt,
		})
	}
	return pageMem(items, req, relationship.OrderFields, func(s relationship.ResourceRelationship) (time.Time, string) {
		return s.CreatedAt, s.ID
	})
}

// distinctResourceIDs collects the sorted-distinct resource IDs of rows matching
// pred. Caller holds the lock.
func (r *Relationships) distinctResourceIDs(pred func(relRow) bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, row := range r.st.rel {
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
