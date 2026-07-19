package turso

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// reachableCTE is the relation-aware userset-expansion recursive CTE shared by the
// check and lookup methods. reachable(atype, aid, arelation) is the set of exact
// subject references the concrete subject IS transitively: it seeds with the
// subject itself as a CONCRETE reference (empty arelation), and at each step adds
// every exact userset (resource_type:resource_id#relation) the current reference
// holds a relation on — carrying the tuple's relation into arelation so the
// userset RELATION is load-bearing (a `member` edge yields a `#member` userset,
// never `#admin`). The join matches subject_relation against the reached relation
// state, so a grant referencing `group#admin` is only satisfied by admin
// membership. UNION (never UNION ALL) dedups on the full (atype, aid, arelation)
// key, so a membership CYCLE terminates by construction WITHOUT conflating
// usersets; there is NO depth term — the walk is unbounded, matching the memstore
// graph walk (the engine's MaxThroughDepth is an engine-only bound). Its two base
// placeholders bind (subjectType, subjectID); the seed relation column is the
// empty-string literal.
const reachableCTE = `WITH RECURSIVE reachable(atype, aid, arelation) AS (
	SELECT ?, ?, ''
	UNION
	SELECT r.resource_type, r.resource_id, r.relation
	FROM iam_relationships r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
)`

// boundedReachableCTE is the BUDGETED sibling of reachableCTE used by the two
// CHECK-path methods (CheckRelationWithGroupExpansion, CheckBatchDirect) to bound
// group-expansion work against the engine's MaxGraphStates (F4). Two mechanisms
// bound it and together guarantee that work depends on the CONFIGURED budget, not
// on the adversary's graph size:
//
//   - a `depth` column carries the recursion level, and `WHERE depth < ?`
//     (the max_depth placeholder = maxExpansionStates) stops the recursion from
//     running away down a deep membership chain — at most maxExpansionStates
//     recursion levels regardless of graph shape;
//   - `capped` materializes the DISTINCT reachable states LIMITed to the
//     state_cap placeholder (= maxExpansionStates+1), so the downstream match
//     join never scans more than maxExpansionStates+1 states, and a
//     `count(*) = state_cap` reading is the deterministic OVERFLOW signal the
//     caller maps to relationship.ErrExpansionBudgetExceeded.
//
// Correctness pin: any state a within-budget graph needs has a shortest
// membership path shorter than its distinct-state count <= maxExpansionStates, so
// the depth bound never cuts a within-budget state; reaching a state at depth
// beyond the bound implies > maxExpansionStates distinct states, which the
// state_cap overflow catches. A within-budget graph yields the SAME bool as the
// unbounded reachableCTE. Re-derived in the libSQL dialect (positional binds); the
// storetest suite is the pgx-equivalence proof. Placeholder order: subject_type,
// subject_id, max_depth, state_cap.
const boundedReachableCTE = `WITH RECURSIVE reachable(atype, aid, arelation, depth) AS (
	SELECT ?, ?, '', 0
	UNION
	SELECT r.resource_type, r.resource_id, r.relation, reachable.depth + 1
	FROM iam_relationships r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
	WHERE reachable.depth < ?
),
states AS (
	SELECT DISTINCT atype, aid, arelation FROM reachable
),
capped AS (
	SELECT atype, aid, arelation FROM states LIMIT ?
)`

// relationshipStore fills relationship.Storer over iam_relationships.
type relationshipStore struct {
	db *tursodb.DB
}

func newRelationshipStore(db *tursodb.DB) *relationshipStore {
	return &relationshipStore{db: db}
}

var _ relationship.Storer = (*relationshipStore)(nil)

// subjectRelationshipRow is the db-tagged projection of a ListRelationshipsBySubject row.
type subjectRelationshipRow struct {
	ID           string       `db:"relationship_id"`
	ResourceType string       `db:"resource_type"`
	ResourceID   string       `db:"resource_id"`
	Relation     string       `db:"relation"`
	CreatedAt    tursodb.Time `db:"created_at"`
}

func (r subjectRelationshipRow) toDomain() relationship.SubjectRelationship {
	return relationship.SubjectRelationship{
		ID:           r.ID,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		Relation:     r.Relation,
		CreatedAt:    r.CreatedAt.Time,
	}
}

// resourceRelationshipRow is the db-tagged projection of a ListRelationshipsByResource row.
type resourceRelationshipRow struct {
	ID          string       `db:"relationship_id"`
	SubjectType string       `db:"subject_type"`
	SubjectID   string       `db:"subject_id"`
	Relation    string       `db:"relation"`
	CreatedAt   tursodb.Time `db:"created_at"`
}

func (r resourceRelationshipRow) toDomain() relationship.ResourceRelationship {
	return relationship.ResourceRelationship{
		ID:          r.ID,
		SubjectType: r.SubjectType,
		SubjectID:   r.SubjectID,
		Relation:    r.Relation,
		CreatedAt:   r.CreatedAt.Time,
	}
}

// CheckRelationWithGroupExpansion reports whether the subject — or any group it
// transitively belongs to — holds the relation on the resource. maxExpansionStates
// bounds the group expansion (boundedReachableCTE): more than maxExpansionStates
// distinct reachable states returns relationship.ErrExpansionBudgetExceeded, never
// a deny. maxExpansionStates <= 0 uses the unbounded reachableCTE.
func (s *relationshipStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	if maxExpansionStates <= 0 {
		query := reachableCTE + `
SELECT EXISTS(
	SELECT 1
	FROM iam_relationships r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
	WHERE r.resource_type = ? AND r.resource_id = ? AND r.relation = ?
)`
		return existsQuery(ctx, s.db, query, subjectType, subjectID, resourceType, resourceID, relation)
	}

	query := boundedReachableCTE + `
SELECT
	(SELECT count(*) FROM capped),
	EXISTS(
		SELECT 1
		FROM iam_relationships r
		JOIN capped ON r.subject_type = capped.atype AND r.subject_id = capped.aid AND r.subject_relation = capped.arelation
		WHERE r.resource_type = ? AND r.resource_id = ? AND r.relation = ?
	)`
	var stateCount, matched int
	if err := s.db.QueryRow(ctx, query,
		subjectType, subjectID, maxExpansionStates, maxExpansionStates+1,
		resourceType, resourceID, relation,
	).Scan(&stateCount, &matched); err != nil {
		return false, tursodb.MapError(err)
	}
	if stateCount > maxExpansionStates {
		return false, relationship.ErrExpansionBudgetExceeded
	}
	return matched != 0, nil
}

// GetRelationTargets returns the subjects holding a relation on a resource. An
// empty subject_relation reads back as "" (a concrete subject); a non-empty one
// as the exact userset relation.
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
			Type:     subjectType,
			ID:       subjectID,
			Relation: subjectRelation,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// CheckRelationExists reports whether an exact direct tuple is present for a
// CONCRETE subject (no expansion; subject_relation must be empty — a stored userset
// tuple with the same type/id does not satisfy a concrete probe). Used for the
// platform-admin data-tuple check and last-owner counting.
func (s *relationshipStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ? AND subject_type = ? AND subject_id = ? AND subject_relation = '')`
	return existsQuery(ctx, s.db, q, resourceType, resourceID, relation, subjectType, subjectID)
}

// CheckBatchDirect returns resourceID -> allowed for one relation across the
// requested ids (with group expansion). Every requested id is present in the map
// (default false); matches are set true. maxExpansionStates bounds the shared
// subject expansion (boundedReachableCTE): overflow returns
// relationship.ErrExpansionBudgetExceeded, never a partial map.
// maxExpansionStates <= 0 uses the unbounded reachableCTE.
func (s *relationshipStore) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	out := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		out[id] = false
	}
	if len(resourceIDs) == 0 {
		return out, nil
	}

	if maxExpansionStates <= 0 {
		args := []any{subjectType, subjectID, resourceType, relation}
		for _, id := range resourceIDs {
			args = append(args, id)
		}
		query := reachableCTE + `
SELECT DISTINCT r.resource_id
FROM iam_relationships r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
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

	// The distinct-state count rides every result row via the cnt cross-join, so
	// overflow is detectable even when no resource matches (zero match rows).
	args := []any{subjectType, subjectID, maxExpansionStates, maxExpansionStates + 1, resourceType, relation}
	for _, id := range resourceIDs {
		args = append(args, id)
	}
	query := boundedReachableCTE + `,
cnt AS (SELECT count(*) AS n FROM capped),
matches AS (
	SELECT DISTINCT r.resource_id AS rid
	FROM iam_relationships r
	JOIN capped ON r.subject_type = capped.atype AND r.subject_id = capped.aid AND r.subject_relation = capped.arelation
	WHERE r.resource_type = ? AND r.relation = ? AND r.resource_id IN ` + inClause(len(resourceIDs)) + `
)
SELECT cnt.n, m.rid FROM cnt LEFT JOIN matches m ON 1=1`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, tursodb.MapError(err)
	}
	defer rows.Close()

	overflow := false
	for rows.Next() {
		var stateCount int
		var rid *string
		if err := rows.Scan(&stateCount, &rid); err != nil {
			return nil, tursodb.MapError(err)
		}
		if stateCount > maxExpansionStates {
			overflow = true
		}
		if rid != nil {
			out[*rid] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	if overflow {
		return nil, relationship.ErrExpansionBudgetExceeded
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
	return createRelationships(ctx, s.db, in)
}

func createRelationships(ctx context.Context, db tursodb.Querier, in []relationship.CreateRelationship) error {
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
		return fmt.Errorf("authorization turso store: mixed relationship_id batch (%d empty, %d populated) — the engine mints all-or-none: %w", empty, populated, sdk.ErrInvalidInput)
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
		args = append(args, c.ResourceType, c.ResourceID, c.Relation, c.SubjectType, c.SubjectID, c.SubjectRelation, now)
	}
	buf.WriteString(" ON CONFLICT DO NOTHING")

	if _, err := db.Exec(ctx, buf.String(), args...); err != nil {
		return err
	}
	return nil
}

// SetRelationTargets reconciles one resource+relation inside Turso's
// BEGIN IMMEDIATE transaction. Competing writers serialize before reading, so
// concurrent desired-state moves cannot commit an accidental union. Retrying a
// residual busy error is naturally safe because the operation describes state,
// not a one-time occurrence.
func (s *relationshipStore) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, in []relationship.CreateRelationship) error {
	desired := make(map[relationship.SubjectRef]relationship.CreateRelationship, len(in))
	for _, c := range in {
		if c.ResourceType != resourceType || c.ResourceID != resourceID || c.Relation != relationName {
			return fmt.Errorf("authorization turso store: SetRelationTargets row is outside requested scope: %w", sdk.ErrInvalidInput)
		}
		desired[c.Subject()] = c
	}
	rows := make([]relationship.CreateRelationship, 0, len(desired))
	for _, c := range desired {
		rows = append(rows, c)
	}

	return retryBusy(ctx, func() error {
		return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
			if len(rows) == 0 {
				_, err := tx.Exec(ctx, `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ?`, resourceType, resourceID, relationName)
				return tursodb.MapError(err)
			}

			var predicate strings.Builder
			args := make([]any, 0, len(rows)*3)
			for i, c := range rows {
				if i > 0 {
					predicate.WriteString(" OR ")
				}
				predicate.WriteString("(subject_type = ? AND subject_id = ? AND subject_relation = ?)")
				args = append(args, c.SubjectType, c.SubjectID, c.SubjectRelation)
			}
			pred := predicate.String()

			conflictArgs := []any{resourceType, resourceID, relationName}
			conflictArgs = append(conflictArgs, args...)
			var conflict bool
			conflictQ := `SELECT EXISTS (SELECT 1 FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation <> ? AND (` + pred + `))`
			if err := tx.QueryRow(ctx, conflictQ, conflictArgs...).Scan(&conflict); err != nil {
				return tursodb.MapError(err)
			}
			if conflict {
				return fmt.Errorf("authorization turso store: a desired target already holds a different relation on %s:%s: %w", resourceType, resourceID, sdk.ErrConflict)
			}

			deleteArgs := []any{resourceType, resourceID, relationName}
			deleteArgs = append(deleteArgs, args...)
			deleteQ := `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ? AND NOT (` + pred + `)`
			if _, err := tx.Exec(ctx, deleteQ, deleteArgs...); err != nil {
				return tursodb.MapError(err)
			}
			return createRelationships(ctx, tx, rows)
		})
	})
}

// DeleteResourceRelationships removes every tuple for a resource (idempotent).
func (s *relationshipStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ?`
	_, err := s.db.Exec(ctx, q, resourceType, resourceID)
	return err
}

// DeleteRelationshipTarget removes one exact tuple, including subject_relation.
func (s *relationshipStore) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target relationship.SubjectRef) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ? AND subject_type = ? AND subject_id = ? AND subject_relation = ?`
	_, err := s.db.Exec(ctx, q, resourceType, resourceID, relationName, target.Type, target.ID, target.Relation)
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
	q := tursodb.ListQuery[subjectRelationshipRow]{
		BaseSQL:      `SELECT relationship_id, resource_type, resource_id, relation, created_at FROM iam_relationships ` + where,
		Args:         args,
		OrderFields:  relationship.OrderFields,
		DefaultOrder: relationship.DefaultOrder,
		PK:           "relationship_id",
		OrderValueOf: func(r subjectRelationshipRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r subjectRelationshipRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[relationship.SubjectRelationship]{}, err
	}
	return crud.MapPage(page, subjectRelationshipRow.toDomain), nil
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
	q := tursodb.ListQuery[resourceRelationshipRow]{
		BaseSQL:      `SELECT relationship_id, subject_type, subject_id, relation, created_at FROM iam_relationships ` + where,
		Args:         args,
		OrderFields:  relationship.OrderFields,
		DefaultOrder: relationship.DefaultOrder,
		PK:           "relationship_id",
		OrderValueOf: func(r resourceRelationshipRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r resourceRelationshipRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[relationship.ResourceRelationship]{}, err
	}
	return crud.MapPage(page, resourceRelationshipRow.toDomain), nil
}

// LookupResourceIDs returns the distinct resource IDs (sorted) where the subject
// has any of the relations, with group expansion. It returns at most limit rows:
// the engine passes MaxLookupResults+1 so a full-limit return is a distinguishable
// overflow signal, never a silently truncated complete result.
func (s *relationshipStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string, limit int) ([]string, error) {
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
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
WHERE r.resource_type = ? AND r.relation IN ` + inClause(len(relations)) + `
ORDER BY r.resource_id`
	query, args = withLimit(query, args, limit)
	return queryStrings(ctx, s.db, query, args...)
}

// LookupResourceIDsByRelationTarget returns the distinct resource IDs (sorted)
// whose relation points at any of the target IDs (no expansion), at most limit rows.
func (s *relationshipStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, limit int) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	args := []any{resourceType, relation, targetType}
	for _, id := range targetIDs {
		args = append(args, id)
	}
	query := `SELECT DISTINCT resource_id FROM iam_relationships
WHERE resource_type = ? AND relation = ? AND subject_type = ? AND subject_id IN ` + inClause(len(targetIDs)) + ` AND subject_relation = ''
ORDER BY resource_id`
	query, args = withLimit(query, args, limit)
	return queryStrings(ctx, s.db, query, args...)
}

// LookupDescendantResourceIDs walks a self-referential relation transitively from
// the root IDs (recursive CTE, cycle-safe via UNION dedup). Roots are not
// returned unless a cycle makes one a genuine descendant. Result is sorted and
// bounded at limit rows (the recursive-expansion result cap).
func (s *relationshipStore) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string, limit int) ([]string, error) {
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
	WHERE r.resource_type = ? AND r.relation = ? AND r.subject_type = ? AND r.subject_relation = '' AND r.subject_id IN ` + inClause(len(rootIDs)) + `
	UNION
	SELECT r.resource_id
	FROM iam_relationships r
	JOIN descendants d ON r.subject_id = d.rid
	WHERE r.resource_type = ? AND r.relation = ? AND r.subject_type = ? AND r.subject_relation = ''
)
SELECT DISTINCT rid FROM descendants ORDER BY rid`
	query, args = withLimit(query, args, limit)
	return queryStrings(ctx, s.db, query, args...)
}

// withLimit appends a bounded ` LIMIT ?` and its argument when limit is positive
// (the engine always passes MaxLookupResults+1). A non-positive limit is
// unbounded (defensive).
func withLimit(query string, args []any, limit int) (string, []any) {
	if limit > 0 {
		return query + "\nLIMIT ?", append(args, limit)
	}
	return query, args
}
