package turso

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// reachableCTE is the group-expansion recursive CTE shared by the check and
// lookup methods. reachable(atype, aid) is the set of identities the subject IS
// transitively: it seeds with the subject itself and, at each step, adds every
// group G for which a tuple `G#member@<current>` exists. UNION (never UNION ALL)
// dedups, so a membership CYCLE terminates by construction; there is NO depth
// term — the walk is unbounded, matching the memstore graph walk (MaxTraversalDepth
// is an engine-only bound). Its two base placeholders bind (subjectType, subjectID).
const reachableCTE = `WITH RECURSIVE reachable(atype, aid) AS (
	SELECT ?, ?
	UNION
	SELECT r.resource_type, r.resource_id
	FROM iam_relationships r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid
	WHERE r.relation = 'member'
)`

// relationshipStore fills relationship.Storer over iam_relationships.
type relationshipStore struct {
	db *tursodb.DB
}

func newRelationshipStore(db *tursodb.DB) *relationshipStore {
	return &relationshipStore{db: db}
}

var _ relationship.Storer = (*relationshipStore)(nil)

// CheckRelationWithGroupExpansion reports whether the subject — or any group it
// transitively belongs to — holds the relation on the resource (unbounded,
// cycle-safe via the reachable CTE).
func (s *relationshipStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	query := reachableCTE + `
SELECT EXISTS(
	SELECT 1
	FROM iam_relationships r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid
	WHERE r.resource_type = ? AND r.resource_id = ? AND r.relation = ?
)`
	return existsQuery(ctx, s.db, query, subjectType, subjectID, resourceType, resourceID, relation)
}

// GetRelationTargets returns the subjects holding a relation on a resource. An
// empty subject_relation reads back as nil (a concrete subject); a non-empty one
// as the userset relation.
func (s *relationshipStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	const q = `SELECT subject_type, subject_id, subject_relation FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ?`
	rows, err := s.db.Query(ctx, q, resourceType, resourceID, relation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []relationship.RelationTarget
	for rows.Next() {
		var subjectType, subjectID, subjectRelation string
		if err := rows.Scan(&subjectType, &subjectID, &subjectRelation); err != nil {
			return nil, tursodb.MapError(err)
		}
		out = append(out, relationship.RelationTarget{
			SubjectType:     subjectType,
			SubjectID:       subjectID,
			SubjectRelation: nullableString(subjectRelation),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// CheckRelationExists reports whether an exact direct tuple is present (no
// expansion, no subject_relation match — the platform-admin data-tuple check).
func (s *relationshipStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ? AND subject_type = ? AND subject_id = ?)`
	return existsQuery(ctx, s.db, q, resourceType, resourceID, relation, subjectType, subjectID)
}

// CheckBatchDirect returns resourceID -> allowed for one relation across the
// requested ids (with group expansion). Every requested id is present in the map
// (default false); matches are set true.
func (s *relationshipStore) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	out := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		out[id] = false
	}
	if len(resourceIDs) == 0 {
		return out, nil
	}

	args := []any{subjectType, subjectID, resourceType, relation}
	for _, id := range resourceIDs {
		args = append(args, id)
	}
	query := reachableCTE + `
SELECT DISTINCT r.resource_id
FROM iam_relationships r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid
WHERE r.resource_type = ? AND r.relation = ? AND r.resource_id IN ` + inClause(len(resourceIDs))

	matched, err := queryStrings(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	for _, id := range matched {
		out[id] = true
	}
	return out, nil
}

// CreateRelationships inserts a batch as one multi-row INSERT ... ON CONFLICT DO
// NOTHING (libsql has no UNNEST). The bare ON CONFLICT covers both unique indexes:
// an exact-duplicate tuple AND a second, different relation for the same
// (subject, resource) are SILENT no-ops (nil error, existing row unchanged),
// never ErrAlreadyExists. The whole batch shares one store-stamped created_at.
//
// Id strategy (Q6): an ALL-empty batch omits the relationship_id column so the
// DDL DEFAULT fills each key; an ALL-populated batch inserts the ids verbatim; a
// MIXED batch is a loud store error (the engine mints all-or-none). There is no
// RETURNING — the port is error-only.
func (s *relationshipStore) CreateRelationships(ctx context.Context, in []relationship.CreateRelationship) error {
	if len(in) == 0 {
		return nil
	}

	empty, populated := 0, 0
	for _, c := range in {
		if c.RelationshipID == "" {
			empty++
		} else {
			populated++
		}
	}
	if empty > 0 && populated > 0 {
		return fmt.Errorf("authorization turso store: mixed relationship_id batch (%d empty, %d populated) — the engine mints all-or-none: %w", empty, populated, errs.ErrInvalidInput)
	}
	withID := populated > 0

	cols := "resource_type, resource_id, relation, subject_type, subject_id, subject_relation, created_at"
	row := "(?, ?, ?, ?, ?, ?, ?)"
	if withID {
		cols = "relationship_id, " + cols
		row = "(?, ?, ?, ?, ?, ?, ?, ?)"
	}

	now := tursodb.FormatTime(time.Now().UTC())
	var buf strings.Builder
	fmt.Fprintf(&buf, "INSERT INTO iam_relationships (%s) VALUES ", cols)
	args := make([]any, 0, len(in)*8)
	for i, c := range in {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(row)
		if withID {
			args = append(args, c.RelationshipID)
		}
		args = append(args, c.ResourceType, c.ResourceID, c.Relation, c.SubjectType, c.SubjectID, subjectRelationValue(c.SubjectRelation), now)
	}
	buf.WriteString(" ON CONFLICT DO NOTHING")

	if _, err := s.db.Exec(ctx, buf.String(), args...); err != nil {
		return err
	}
	return nil
}

// DeleteResourceRelationships removes every tuple for a resource (idempotent).
func (s *relationshipStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ?`
	_, err := s.db.Exec(ctx, q, resourceType, resourceID)
	return err
}

// DeleteRelationship removes one exact tuple (idempotent — absent is nil).
func (s *relationshipStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ? AND subject_type = ? AND subject_id = ?`
	_, err := s.db.Exec(ctx, q, resourceType, resourceID, relation, subjectType, subjectID)
	return err
}

// DeleteByResourceAndSubject removes every relation a subject holds on a resource
// (idempotent).
func (s *relationshipStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND subject_type = ? AND subject_id = ?`
	_, err := s.db.Exec(ctx, q, resourceType, resourceID, subjectType, subjectID)
	return err
}

// CountByResourceAndRelation counts DIRECT tuples only — never expanded
// membership (the §2.5 security pin: last-owner protection depends on it).
func (s *relationshipStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	const q = `SELECT COUNT(*) FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ?`
	var n int
	if err := s.db.QueryRow(ctx, q, resourceType, resourceID, relation).Scan(&n); err != nil {
		return 0, tursodb.MapError(err)
	}
	return n, nil
}

// ListRelationshipsBySubject pages the resources a subject relates to (created_at
// DESC, relationship_id DESC).
func (s *relationshipStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	where := "WHERE subject_type = ? AND subject_id = ?"
	args := []any{subjectType, subjectID}
	if filter.ResourceType != nil {
		where += " AND resource_type = ?"
		args = append(args, *filter.ResourceType)
	}
	if filter.Relation != nil {
		where += " AND relation = ?"
		args = append(args, *filter.Relation)
	}
	q := tursodb.ListQuery[relationship.SubjectRelationship]{
		BaseSQL:      `SELECT relationship_id, resource_type, resource_id, relation, created_at FROM iam_relationships ` + where,
		Args:         args,
		OrderFields:  orderFields,
		DefaultOrder: defaultOrder,
		PK:           "relationship_id",
		Scan:         scanSubjectRelationship,
		OrderValueOf: func(r relationship.SubjectRelationship, _ string) any { return r.CreatedAt },
		PKOf:         func(r relationship.SubjectRelationship) string { return r.ID },
	}
	return tursodb.List(ctx, s.db, q, req)
}

// ListRelationshipsByResource pages the subjects related to a resource (created_at
// DESC, relationship_id DESC).
func (s *relationshipStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	where := "WHERE resource_type = ? AND resource_id = ?"
	args := []any{resourceType, resourceID}
	if filter.SubjectType != nil {
		where += " AND subject_type = ?"
		args = append(args, *filter.SubjectType)
	}
	if filter.Relation != nil {
		where += " AND relation = ?"
		args = append(args, *filter.Relation)
	}
	q := tursodb.ListQuery[relationship.ResourceRelationship]{
		BaseSQL:      `SELECT relationship_id, subject_type, subject_id, relation, created_at FROM iam_relationships ` + where,
		Args:         args,
		OrderFields:  orderFields,
		DefaultOrder: defaultOrder,
		PK:           "relationship_id",
		Scan:         scanResourceRelationship,
		OrderValueOf: func(r relationship.ResourceRelationship, _ string) any { return r.CreatedAt },
		PKOf:         func(r relationship.ResourceRelationship) string { return r.ID },
	}
	return tursodb.List(ctx, s.db, q, req)
}

// LookupResourceIDs returns the distinct resource IDs (sorted) where the subject
// has any of the relations, with group expansion.
func (s *relationshipStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	if len(relations) == 0 {
		return nil, nil
	}
	args := []any{subjectType, subjectID, resourceType}
	for _, rel := range relations {
		args = append(args, rel)
	}
	query := reachableCTE + `
SELECT DISTINCT r.resource_id
FROM iam_relationships r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid
WHERE r.resource_type = ? AND r.relation IN ` + inClause(len(relations)) + `
ORDER BY r.resource_id`
	return queryStrings(ctx, s.db, query, args...)
}

// LookupResourceIDsByRelationTarget returns the distinct resource IDs (sorted)
// whose relation points at any of the target IDs (no expansion).
func (s *relationshipStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	args := []any{resourceType, relation, targetType}
	for _, id := range targetIDs {
		args = append(args, id)
	}
	query := `SELECT DISTINCT resource_id FROM iam_relationships
WHERE resource_type = ? AND relation = ? AND subject_type = ? AND subject_id IN ` + inClause(len(targetIDs)) + `
ORDER BY resource_id`
	return queryStrings(ctx, s.db, query, args...)
}

// LookupDescendantResourceIDs walks a self-referential relation transitively from
// the root IDs (recursive CTE, cycle-safe via UNION dedup, unbounded). Roots are
// not returned unless a cycle makes one a genuine descendant. Result is sorted.
func (s *relationshipStore) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	// Base: children of the roots. Recursive: children of discovered descendants.
	args := []any{resourceType, relation, subjectType}
	for _, id := range rootIDs {
		args = append(args, id)
	}
	args = append(args, resourceType, relation, subjectType)
	query := `WITH RECURSIVE descendants(rid) AS (
	SELECT r.resource_id
	FROM iam_relationships r
	WHERE r.resource_type = ? AND r.relation = ? AND r.subject_type = ? AND r.subject_id IN ` + inClause(len(rootIDs)) + `
	UNION
	SELECT r.resource_id
	FROM iam_relationships r
	JOIN descendants d ON r.subject_id = d.rid
	WHERE r.resource_type = ? AND r.relation = ? AND r.subject_type = ?
)
SELECT DISTINCT rid FROM descendants ORDER BY rid`
	return queryStrings(ctx, s.db, query, args...)
}

// scanSubjectRelationship scans one ListRelationshipsBySubject projection row.
func scanSubjectRelationship(sc scanner) (relationship.SubjectRelationship, error) {
	var (
		r         relationship.SubjectRelationship
		createdAt string
	)
	if err := sc.Scan(&r.ID, &r.ResourceType, &r.ResourceID, &r.Relation, &createdAt); err != nil {
		return relationship.SubjectRelationship{}, tursodb.MapError(err)
	}
	t, err := tursodb.ParseTime(createdAt)
	if err != nil {
		return relationship.SubjectRelationship{}, err
	}
	r.CreatedAt = t
	return r, nil
}

// scanResourceRelationship scans one ListRelationshipsByResource projection row.
func scanResourceRelationship(sc scanner) (relationship.ResourceRelationship, error) {
	var (
		r         relationship.ResourceRelationship
		createdAt string
	)
	if err := sc.Scan(&r.ID, &r.SubjectType, &r.SubjectID, &r.Relation, &createdAt); err != nil {
		return relationship.ResourceRelationship{}, tursodb.MapError(err)
	}
	t, err := tursodb.ParseTime(createdAt)
	if err != nil {
		return relationship.ResourceRelationship{}, err
	}
	r.CreatedAt = t
	return r, nil
}

// subjectRelationValue renders an optional userset relation for storage: nil
// stores as the empty string (the NOT NULL DEFAULT column), matching the
// read-back mapping.
func subjectRelationValue(sr *string) string {
	if sr == nil {
		return ""
	}
	return *sr
}

// nullableString maps a stored subject_relation back to the rim's *string: the
// empty string is a concrete subject (nil), any other value the userset relation.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
