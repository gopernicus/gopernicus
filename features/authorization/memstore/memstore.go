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

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
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
	subjectRelation *string
	createdAt       time.Time
}

// Relationships is the in-core relationship.Storer.
type Relationships struct {
	mu   sync.Mutex
	rows []relRow
}

// NewRelationships builds an empty relationship store.
func NewRelationships() *Relationships {
	return &Relationships{}
}

var _ relationship.Storer = (*Relationships)(nil)

// CreateRelationships inserts tuples with the ON CONFLICT DO NOTHING mirror on
// the (subject, resource) unique key: a subject holds at most one relation on a
// resource, so a second (subject, resource) row — same relation or different —
// is skipped silently (nil error, the existing row's id + created_at retained,
// no minted id leaked). An empty incoming id is assigned here (the DEFAULT
// mirror). The whole batch shares one created_at, making the id the load-bearing
// keyset tiebreak.
func (r *Relationships) CreateRelationships(ctx context.Context, in []relationship.CreateRelationship) error {
	if len(in) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	for _, c := range in {
		if r.hasSubjectResource(c.SubjectType, c.SubjectID, c.ResourceType, c.ResourceID) {
			continue // DO NOTHING
		}
		id := c.RelationshipID
		if id == "" {
			id = memIDs.MustGenerate()
		}
		r.rows = append(r.rows, relRow{
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

func (r *Relationships) hasSubjectResource(subjectType, subjectID, resourceType, resourceID string) bool {
	for _, row := range r.rows {
		if row.subjectType == subjectType && row.subjectID == subjectID &&
			row.resourceType == resourceType && row.resourceID == resourceID {
			return true
		}
	}
	return false
}

// expandSubjectGroups returns every (type, id) the subject belongs to, following
// `member` edges transitively from the subject. Cycle-safe via the visited set;
// unbounded (no depth term) — the honest mirror of the recursive CTE.
func (r *Relationships) expandSubjectGroups(subjectType, subjectID string) map[[2]string]bool {
	start := [2]string{subjectType, subjectID}
	seen := map[[2]string]bool{start: true}
	queue := [][2]string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, row := range r.rows {
			if row.relation != "member" || row.subjectType != cur[0] || row.subjectID != cur[1] {
				continue
			}
			g := [2]string{row.resourceType, row.resourceID}
			if !seen[g] {
				seen[g] = true
				queue = append(queue, g)
			}
		}
	}
	return seen
}

// CheckRelationWithGroupExpansion reports whether the subject (or any group it
// transitively belongs to) has the relation on the resource.
func (r *Relationships) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	groups := r.expandSubjectGroups(subjectType, subjectID)
	for _, row := range r.rows {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			groups[[2]string{row.subjectType, row.subjectID}] {
			return true, nil
		}
	}
	return false, nil
}

// CheckRelationExists reports whether an exact direct tuple is present (no expansion).
func (r *Relationships) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			row.subjectType == subjectType && row.subjectID == subjectID {
			return true, nil
		}
	}
	return false, nil
}

// GetRelationTargets returns the subjects holding a relation on a resource.
func (r *Relationships) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []relationship.RelationTarget
	for _, row := range r.rows {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation {
			out = append(out, relationship.RelationTarget{
				SubjectType:     row.subjectType,
				SubjectID:       row.subjectID,
				SubjectRelation: row.subjectRelation,
			})
		}
	}
	return out, nil
}

// CheckBatchDirect returns resourceID -> allowed for one relation across ids,
// with group expansion.
func (r *Relationships) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := make(map[string]bool, len(resourceIDs))
	out := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		want[id] = true
		out[id] = false
	}
	groups := r.expandSubjectGroups(subjectType, subjectID)
	for _, row := range r.rows {
		if row.resourceType == resourceType && row.relation == relation && want[row.resourceID] &&
			groups[[2]string{row.subjectType, row.subjectID}] {
			out[row.resourceID] = true
		}
	}
	return out, nil
}

// CountByResourceAndRelation counts DIRECT tuples only.
func (r *Relationships) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, row := range r.rows {
		if row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation {
			n++
		}
	}
	return n, nil
}

// DeleteResourceRelationships removes every tuple for a resource.
func (r *Relationships) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = keepRows(r.rows, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID)
	})
	return nil
}

// DeleteRelationship removes one exact tuple.
func (r *Relationships) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = keepRows(r.rows, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID && row.relation == relation &&
			row.subjectType == subjectType && row.subjectID == subjectID)
	})
	return nil
}

// DeleteByResourceAndSubject removes every relation a subject holds on a resource.
func (r *Relationships) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = keepRows(r.rows, func(row relRow) bool {
		return !(row.resourceType == resourceType && row.resourceID == resourceID &&
			row.subjectType == subjectType && row.subjectID == subjectID)
	})
	return nil
}

// LookupResourceIDs returns the distinct resource IDs where the subject has any
// of the relations (with group expansion).
func (r *Relationships) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	relSet := make(map[string]bool, len(relations))
	for _, rel := range relations {
		relSet[rel] = true
	}
	groups := r.expandSubjectGroups(subjectType, subjectID)
	return r.distinctResourceIDs(func(row relRow) bool {
		return row.resourceType == resourceType && relSet[row.relation] &&
			groups[[2]string{row.subjectType, row.subjectID}]
	}), nil
}

// LookupResourceIDsByRelationTarget returns distinct resource IDs whose relation
// points at any of the target IDs.
func (r *Relationships) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	targets := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		targets[id] = true
	}
	return r.distinctResourceIDs(func(row relRow) bool {
		return row.resourceType == resourceType && row.relation == relation &&
			row.subjectType == targetType && targets[row.subjectID]
	}), nil
}

// LookupDescendantResourceIDs walks a self-referential relation transitively
// from the root IDs (cycle-safe fixpoint).
func (r *Relationships) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	result := map[string]bool{}
	frontier := make(map[string]bool, len(rootIDs))
	for _, id := range rootIDs {
		frontier[id] = true
	}
	for len(frontier) > 0 {
		next := map[string]bool{}
		for _, row := range r.rows {
			if row.resourceType == resourceType && row.relation == relation && row.subjectType == subjectType &&
				frontier[row.subjectID] && !result[row.resourceID] {
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
	return out, nil
}

// ListRelationshipsBySubject pages the resources a subject relates to.
func (r *Relationships) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var items []relationship.SubjectRelationship
	for _, row := range r.rows {
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
	return pageMem(items, req, func(s relationship.SubjectRelationship) (time.Time, string) {
		return s.CreatedAt, s.ID
	})
}

// ListRelationshipsByResource pages the subjects related to a resource.
func (r *Relationships) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var items []relationship.ResourceRelationship
	for _, row := range r.rows {
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
	return pageMem(items, req, func(s relationship.ResourceRelationship) (time.Time, string) {
		return s.CreatedAt, s.ID
	})
}

// distinctResourceIDs collects the sorted-distinct resource IDs of rows matching
// pred. Caller holds the lock.
func (r *Relationships) distinctResourceIDs(pred func(relRow) bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, row := range r.rows {
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
