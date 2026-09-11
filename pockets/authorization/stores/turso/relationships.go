package turso

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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

// boundedReachableCTE caps the full-state UNION at budget+1 distinct states.
// A full prefix proves overflow. There is no depth column that can duplicate
// a state at every cycle level. Physical scans remain planner-dependent.
// Placeholder order: subject_type, subject_id, state_cap.
const boundedReachableCTE = reachableCTE + `,
capped AS MATERIALIZED (
 SELECT atype, aid, arelation FROM reachable LIMIT ?
)`

// relationshipStore reads through the ambient querier and routes all writes
// through a join-or-own transaction so facts and optional history commit together.
type relationshipStore struct {
	audit bool
	model *relationships.ReadModel
	db    *tursodb.DB
}

func newRelationshipStore(db *tursodb.DB, cfg config) *relationshipStore {
	return &relationshipStore{db: db, audit: cfg.audit}
}

var _ relationships.Storer = (*relationshipStore)(nil)

// tupleKeyExpr preserves the complete tuple in byte-order listing cursors.
const tupleKeyExpr = "(resource_type || char(1) || resource_id || char(1) || relation || char(1) || subject_type || char(1) || subject_id || char(1) || subject_relation) COLLATE BINARY"

type subjectRelationshipRow struct {
	ResourceType    string `db:"resource_type"`
	ResourceID      string `db:"resource_id"`
	Relation        string `db:"relation"`
	SubjectRelation string `db:"subject_relation"`
	TupleKey        string `db:"tuple_key"`
}

func (r subjectRelationshipRow) toDomain() relationships.SubjectRelationship {
	return relationships.SubjectRelationship{ResourceType: r.ResourceType, ResourceID: r.ResourceID, Relation: r.Relation, SubjectRelation: r.SubjectRelation}
}

type resourceRelationshipRow struct {
	SubjectType     string `db:"subject_type"`
	SubjectID       string `db:"subject_id"`
	Relation        string `db:"relation"`
	SubjectRelation string `db:"subject_relation"`
	TupleKey        string `db:"tuple_key"`
}

func (r resourceRelationshipRow) toDomain() relationships.ResourceRelationship {
	return relationships.ResourceRelationship{SubjectType: r.SubjectType, SubjectID: r.SubjectID, Relation: r.Relation, SubjectRelation: r.SubjectRelation}
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
		return existsQuery(ctx, s.reader(ctx), query, subjectType, subjectID, resourceType, resourceID, relation)
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
	if err := s.reader(ctx).QueryRow(ctx, query,
		subjectType, subjectID, maxExpansionStates+1,
		resourceType, resourceID, relation,
	).Scan(&stateCount, &matched); err != nil {
		return false, tursodb.MapError(err)
	}
	if stateCount > maxExpansionStates {
		return false, relationships.ErrExpansionBudgetExceeded
	}
	return matched != 0, nil
}

// rowQuerier is the Query seam shared by the pool (*tursodb.DB) and a
// transaction (*tursodb.Tx), so one relation-targets reader serves the read side
// and the mutation repository's transaction-bound DecisionView alike.
type rowQuerier interface {
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// GetRelationTargets returns the subjects holding a relation on a resource. An
// empty subject_relation reads back as "" (a concrete subject); a non-empty one
// as the exact userset relation.
func (s *relationshipStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	return relationTargets(ctx, s.reader(ctx), resourceType, resourceID, relation)
}

// relationTargets is the one relation-targets read: the read-side
// GetRelationTargets runs it on the ambient querier (pool or host transaction),
// the DecisionView's RelationTargets on the mutation transaction. Same
// statement, same row order, same mapping.
func relationTargets(ctx context.Context, q rowQuerier, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	const stmt = `SELECT subject_type, subject_id, subject_relation FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ?`
	rows, err := q.Query(ctx, stmt, resourceType, resourceID, relation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []relationships.RelationTarget
	for rows.Next() {
		var subjectType, subjectID, subjectRelation string
		if err := rows.Scan(&subjectType, &subjectID, &subjectRelation); err != nil {
			return nil, tursodb.MapError(err)
		}
		out = append(out, relationships.RelationTarget{
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
	return existsQuery(ctx, s.db.QuerierFrom(ctx), q, resourceType, resourceID, relation, subjectType, subjectID)
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

		matched, err := queryStrings(ctx, s.reader(ctx), query, args...)
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
	args := []any{subjectType, subjectID, maxExpansionStates + 1, resourceType, relation}
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

	rows, err := s.reader(ctx).Query(ctx, query, args...)
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
		return nil, relationships.ErrExpansionBudgetExceeded
	}
	return out, nil
}

// maxSetReadIDs bounds the candidate ids ONE set-read statement binds. libSQL
// binds each id as a positional parameter, so an unchunked `IN (…)` over a
// full-size candidate set would push a statement toward the dialect's
// bind-parameter ceiling (and its expression-depth limit) as the candidate set
// grows. The set reads therefore CHUNK internally and merge — the port forbids
// rejecting an id list, and chunking is invisible in the answer: the ids are
// sorted and de-duplicated before chunking, so the chunks cover disjoint,
// ascending ranges and their concatenation is already the distinct, byte-order
// sorted output the port promises. The pgx sibling binds one text[] and needs no
// chunk; both answer identically, which storetest's SetReads family proves.
const maxSetReadIDs = 500

// setReadIDs folds a candidate list to the DISTINCT ids in byte order — the
// canonical shape the chunked set reads walk, and the shape whose chunks
// concatenate into a sorted, distinct answer.
func setReadIDs(resourceIDs []string) []string {
	out := make([]string, len(resourceIDs))
	copy(out, resourceIDs)
	sort.Strings(out)
	return slices.Compact(out)
}

// FilterRelation returns the DISTINCT, byte-order sorted subset of resourceIDs
// the subject holds relation on, with group expansion — the set form of
// CheckRelationWithGroupExpansion, one statement per chunk of at most
// maxSetReadIDs ids. SQLite's default BINARY collation IS byte order, so the
// ORDER BY needs no collation pin (its pgx sibling pins COLLATE "C" to reach the
// same order).
//
// maxExpansionStates bounds the shared subject expansion (boundedReachableCTE);
// overflow in ANY chunk returns relationship.ErrExpansionBudgetExceeded for the
// whole call, never a short list. maxExpansionStates <= 0 uses the unbounded
// reachableCTE.
func (s *relationshipStore) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	ids := setReadIDs(resourceIDs)
	if len(ids) == 0 {
		return nil, nil
	}
	var out []string
	for start := 0; start < len(ids); start += maxSetReadIDs {
		chunk := ids[start:min(start+maxSetReadIDs, len(ids))]
		matched, err := s.filterRelationChunk(ctx, resourceType, chunk, relation, subjectType, subjectID, maxExpansionStates)
		if err != nil {
			return nil, err
		}
		out = append(out, matched...)
	}
	return out, nil
}

// filterRelationChunk answers FilterRelation for ONE bind-safe chunk of sorted
// candidate ids.
func (s *relationshipStore) filterRelationChunk(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	if maxExpansionStates <= 0 {
		args := []any{subjectType, subjectID, resourceType, relation}
		for _, id := range resourceIDs {
			args = append(args, id)
		}
		query := reachableCTE + `
SELECT DISTINCT r.resource_id
FROM iam_relationships r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
WHERE r.resource_type = ? AND r.relation = ? AND r.resource_id IN ` + inClause(len(resourceIDs)) + `
ORDER BY r.resource_id`
		return queryStrings(ctx, s.reader(ctx), query, args...)
	}

	// The distinct-state count rides every result row via the cnt cross-join, so
	// overflow is detectable even when no resource matches (zero match rows) —
	// the same shape CheckBatchDirect uses.
	args := []any{subjectType, subjectID, maxExpansionStates + 1, resourceType, relation}
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
SELECT cnt.n, m.rid FROM cnt LEFT JOIN matches m ON 1=1 ORDER BY m.rid`

	rows, err := s.reader(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, tursodb.MapError(err)
	}
	defer rows.Close()

	overflow := false
	var out []string
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
			out = append(out, *rid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	if overflow {
		return nil, relationships.ErrExpansionBudgetExceeded
	}
	return out, nil
}

// RelationTargetsFor returns the subjects holding relation on each of
// resourceIDs — the set form of GetRelationTargets, one statement per chunk of
// at most maxSetReadIDs ids, merged into one map. An id with no targets is
// absent from the map; a duplicated input id carries one entry.
func (s *relationshipStore) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]relationships.RelationTarget, error) {
	ids := setReadIDs(resourceIDs)
	out := make(map[string][]relationships.RelationTarget, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	for start := 0; start < len(ids); start += maxSetReadIDs {
		chunk := ids[start:min(start+maxSetReadIDs, len(ids))]
		if err := s.relationTargetsForChunk(ctx, resourceType, chunk, relation, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// relationTargetsForChunk reads ONE bind-safe chunk into the accumulating map.
func (s *relationshipStore) relationTargetsForChunk(ctx context.Context, resourceType string, resourceIDs []string, relation string, out map[string][]relationships.RelationTarget) error {
	args := []any{resourceType, relation}
	for _, id := range resourceIDs {
		args = append(args, id)
	}
	query := `SELECT resource_id, subject_type, subject_id, subject_relation FROM iam_relationships
WHERE resource_type = ? AND relation = ? AND resource_id IN ` + inClause(len(resourceIDs))

	rows, err := s.reader(ctx).Query(ctx, query, args...)
	if err != nil {
		return tursodb.MapError(err)
	}
	defer rows.Close()

	for rows.Next() {
		var resourceID, subjectType, subjectID, subjectRelation string
		if err := rows.Scan(&resourceID, &subjectType, &subjectID, &subjectRelation); err != nil {
			return tursodb.MapError(err)
		}
		out[resourceID] = append(out[resourceID], relationships.RelationTarget{
			Type:     subjectType,
			ID:       subjectID,
			Relation: subjectRelation,
		})
	}
	return tursodb.MapError(rows.Err())
}

// CreateRelationships inserts full tuples as one batch. Exact duplicates and
// competing relations for an already-related subject retain the existing row.
func (s *relationshipStore) CreateRelationships(ctx context.Context, in []relationships.CreateRelationship) error {
	return s.write(ctx, func(tx *writeTx) error { return createRelationships(ctx, tx, in) })
}

func createRelationships(ctx context.Context, db *writeTx, in []relationships.CreateRelationship) error {
	if len(in) == 0 {
		return nil
	}
	for _, row := range in {
		if err := row.Validate(); err != nil {
			return err
		}
	}

	const cols = "resource_type, resource_id, relation, subject_type, subject_id, subject_relation"
	const row = "(?, ?, ?, ?, ?, ?)"
	var buf strings.Builder
	fmt.Fprintf(&buf, "INSERT INTO iam_relationships (%s) VALUES ", cols)
	args := make([]any, 0, len(in)*6)
	for i, c := range in {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(row)
		args = append(args, c.ResourceType, c.ResourceID, c.Relation, c.SubjectType, c.SubjectID, c.SubjectRelation)
	}
	buf.WriteString(" ON CONFLICT DO NOTHING")

	if _, err := db.relationships(ctx, audit.ActionAdded, buf.String(), args...); err != nil {
		return err
	}
	return nil
}

// SetRelationTargets atomically reconciles one resource and relation. Ambient
// calls use a savepoint; locks remain held until the host transaction ends.
func (s *relationshipStore) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, in []relationships.CreateRelationship) error {
	desired := make(map[relationships.SubjectRef]relationships.CreateRelationship, len(in))
	for _, c := range in {
		if err := c.Validate(); err != nil {
			return err
		}
		if c.ResourceType != resourceType || c.ResourceID != resourceID || c.Relation != relationName {
			return fmt.Errorf("authorization turso store: SetRelationTargets row is outside requested scope: %w", sdk.ErrInvalidInput)
		}
		desired[c.Subject()] = c
	}
	rows := make([]relationships.CreateRelationship, 0, len(desired))
	for _, c := range desired {
		rows = append(rows, c)
	}

	return s.write(ctx, func(tx *writeTx) error {
		return s.setRelationTargetsTx(ctx, tx, resourceType, resourceID, relationName, rows)
	})
}

func (s *relationshipStore) setRelationTargetsTx(ctx context.Context, tx *writeTx, resourceType, resourceID, relationName string, rows []relationships.CreateRelationship) error {
	if len(rows) == 0 {
		_, err := tx.relationships(ctx, audit.ActionRemoved, `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ?`, resourceType, resourceID, relationName)
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
	if _, err := tx.relationships(ctx, audit.ActionRemoved, deleteQ, deleteArgs...); err != nil {
		return tursodb.MapError(err)
	}
	return createRelationships(ctx, tx, rows)
}

// DeleteResourceRelationships removes every tuple for a resource (idempotent).
func (s *relationshipStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ?`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.relationships(ctx, audit.ActionRemoved, q, resourceType, resourceID)
		return err
	})
}

// DeleteRelationshipTarget removes one exact tuple, including subject_relation.
func (s *relationshipStore) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target relationships.SubjectRef) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ? AND subject_type = ? AND subject_id = ? AND subject_relation = ?`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.relationships(ctx, audit.ActionRemoved, q, resourceType, resourceID, relationName, target.Type, target.ID, target.Relation)
		return err
	})
}

// DeleteRelationship removes one exact tuple (idempotent — absent is nil).
func (s *relationshipStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ? AND subject_type = ? AND subject_id = ?`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.relationships(ctx, audit.ActionRemoved, q, resourceType, resourceID, relation, subjectType, subjectID)
		return err
	})
}

// DeleteByResourceAndSubject removes every relation a subject holds on a resource
// (idempotent).
func (s *relationshipStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND subject_type = ? AND subject_id = ?`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.relationships(ctx, audit.ActionRemoved, q, resourceType, resourceID, subjectType, subjectID)
		return err
	})
}

// CountByResourceAndRelation counts DIRECT tuples only — never expanded
// membership (the §2.5 security pin: last-owner protection depends on it).
func (s *relationshipStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	const q = `SELECT COUNT(*) FROM iam_relationships WHERE resource_type = ? AND resource_id = ? AND relation = ?`
	var n int
	if err := s.db.QuerierFrom(ctx).QueryRow(ctx, q, resourceType, resourceID, relation).Scan(&n); err != nil {
		return 0, tursodb.MapError(err)
	}
	return n, nil
}

// relationshipsBaseSQL exposes the computed key to the outer keyset predicate.
func relationshipsBaseSQL(columns, where string) string {
	return `SELECT ` + columns + `, tuple_key FROM (
    SELECT ` + columns + `, ` + tupleKeyExpr + ` AS tuple_key
    FROM iam_relationships ` + where + `
) AS r WHERE 1 = 1`
}

// ListRelationshipsBySubject pages complete tuple identities in byte order.
func (s *relationshipStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationships.SubjectRelationshipFilter, req list.Request) (list.Page[relationships.SubjectRelationship], error) {
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
		BaseSQL:      relationshipsBaseSQL("resource_type, resource_id, relation, subject_relation", where),
		Args:         args,
		OrderFields:  relationships.OrderFields,
		DefaultOrder: relationships.DefaultOrder,
		PK:           "tuple_key",
		OrderValueOf: func(r subjectRelationshipRow, _ string) any { return r.TupleKey },
		PKOf:         func(r subjectRelationshipRow) string { return r.TupleKey },
	}
	page, err := tursodb.List(ctx, s.db.QuerierFrom(ctx), q, req)
	if err != nil {
		return list.Page[relationships.SubjectRelationship]{}, err
	}
	return list.MapPage(page, subjectRelationshipRow.toDomain), nil
}

// ListRelationshipsByResource pages complete tuple identities in byte order.
func (s *relationshipStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationships.ResourceRelationshipFilter, req list.Request) (list.Page[relationships.ResourceRelationship], error) {
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
		BaseSQL:      relationshipsBaseSQL("subject_type, subject_id, relation, subject_relation", where),
		Args:         args,
		OrderFields:  relationships.OrderFields,
		DefaultOrder: relationships.DefaultOrder,
		PK:           "tuple_key",
		OrderValueOf: func(r resourceRelationshipRow, _ string) any { return r.TupleKey },
		PKOf:         func(r resourceRelationshipRow) string { return r.TupleKey },
	}
	page, err := tursodb.List(ctx, s.db.QuerierFrom(ctx), q, req)
	if err != nil {
		return list.Page[relationships.ResourceRelationship]{}, err
	}
	return list.MapPage(page, resourceRelationshipRow.toDomain), nil
}

// lookupResourceIDsSQL renders the direct-relation keyset lookup and its args.
// It is split from the method so an EXPLAIN/diagnostic caller can reach the exact
// statement the store runs.
//
// The keyset predicate and the ORDER BY are RAW BYTE order, contractually: the
// engine merges several of these ID streams by Go string comparison
// (relationship.Storer's Bounding note), so a collated order would skip or repeat
// IDs across pages. SQLite's default BINARY collation IS byte order, so — unlike
// the pgx sibling, which pins COLLATE "C" per query — nothing is added here; the
// contract holds because no column in this schema declares a COLLATE clause.
func lookupResourceIDsSQL(resourceType string, relations []string, subjectType, subjectID, after string, limit int) (string, []any) {
	args := []any{subjectType, subjectID, resourceType}
	for _, rel := range relations {
		args = append(args, rel)
	}
	args = append(args, after, after)
	query := reachableCTE + `
SELECT DISTINCT r.resource_id
FROM iam_relationships r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
WHERE r.resource_type = ? AND r.relation IN ` + inClause(len(relations)) + `
  AND (? = '' OR r.resource_id > ?)
ORDER BY r.resource_id`
	return withLimit(query, args, limit)
}

// LookupResourceIDs returns the distinct resource IDs where the subject has any
// of the relations, with group expansion: sorted ascending in BYTE order,
// strictly greater than after (after == "" starts from the beginning), at most
// limit rows. The engine passes MaxLookupResults+1 for a complete enumeration —
// a full-limit return is a distinguishable overflow signal, never a silently
// truncated complete result — or page+1 for a paged one, where the extra row is
// the HasMore lookahead.
func (s *relationshipStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	if len(relations) == 0 {
		return nil, nil
	}
	query, args := lookupResourceIDsSQL(resourceType, relations, subjectType, subjectID, after, limit)
	return queryStrings(ctx, s.reader(ctx), query, args...)
}

// lookupResourceIDsByRelationTargetSQL renders the relation-target keyset lookup
// and its args (split from the method for diagnostics).
func lookupResourceIDsByRelationTargetSQL(resourceType, relation, targetType string, targetIDs []string, after string, limit int) (string, []any) {
	args := []any{resourceType, relation, targetType}
	for _, id := range targetIDs {
		args = append(args, id)
	}
	args = append(args, after, after)
	query := `SELECT DISTINCT resource_id FROM iam_relationships
WHERE resource_type = ? AND relation = ? AND subject_type = ? AND subject_id IN ` + inClause(len(targetIDs)) + ` AND subject_relation = ''
  AND (? = '' OR resource_id > ?)
ORDER BY resource_id`
	return withLimit(query, args, limit)
}

// LookupResourceIDsByRelationTarget returns the distinct resource IDs whose
// relation points at any of the target IDs (concrete subjects, no expansion):
// byte-order sorted, strictly greater than after, at most limit rows. Same keyset
// contract as [relationshipStore.LookupResourceIDs].
func (s *relationshipStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	query, args := lookupResourceIDsByRelationTargetSQL(resourceType, relation, targetType, targetIDs, after, limit)
	return queryStrings(ctx, s.reader(ctx), query, args...)
}

// lookupDescendantResourceIDsSQL renders the descendant-closure statement and its
// args (split from the method for diagnostics). The relation set is closed over
// in BOTH the anchor and the recursive term, so one call follows a path that
// alternates relations.
func lookupDescendantResourceIDsSQL(resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) (string, []any) {
	// Base: children of the roots. Recursive: children of discovered descendants.
	args := []any{resourceType}
	for _, rel := range relations {
		args = append(args, rel)
	}
	args = append(args, subjectType)
	for _, id := range rootIDs {
		args = append(args, id)
	}
	args = append(args, resourceType)
	for _, rel := range relations {
		args = append(args, rel)
	}
	args = append(args, subjectType, after, after)
	query := `WITH RECURSIVE descendants(rid) AS (
	SELECT r.resource_id
	FROM iam_relationships r
	WHERE r.resource_type = ? AND r.relation IN ` + inClause(len(relations)) + ` AND r.subject_type = ? AND r.subject_relation = '' AND r.subject_id IN ` + inClause(len(rootIDs)) + `
	UNION
	SELECT r.resource_id
	FROM iam_relationships r
	JOIN descendants d ON r.subject_id = d.rid
	WHERE r.resource_type = ? AND r.relation IN ` + inClause(len(relations)) + ` AND r.subject_type = ? AND r.subject_relation = ''
)
SELECT DISTINCT rid FROM descendants
WHERE (? = '' OR rid > ?)
ORDER BY rid`
	return withLimit(query, args, limit)
}

// LookupDescendantResourceIDs walks the UNION of the self-referential relations
// transitively from the root IDs (one recursive CTE, cycle-safe via UNION dedup).
// Roots are not returned unless a cycle makes one a genuine descendant.
// after/limit page the sorted closure, not its work: the database computes the
// whole closure on every call (plan A3), so this stream's per-page cost stays the
// closure size.
func (s *relationshipStore) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	if len(rootIDs) == 0 || len(relations) == 0 {
		return nil, nil
	}
	query, args := lookupDescendantResourceIDsSQL(resourceType, relations, subjectType, rootIDs, after, limit)
	return queryStrings(ctx, s.reader(ctx), query, args...)
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

func (s *relationshipStore) write(ctx context.Context, fn func(*writeTx) error) error {
	return runWrite(ctx, s.db, config{audit: s.audit}, fn)
}
