package pgx

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/jackc/pgx/v5"
)

// reachableCTE renders the relation-aware userset-expansion recursive CTE shared by the
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
// graph walk and the turso sibling (the engine's MaxThroughDepth is an engine-only
// bound). Re-derived in the PostgreSQL dialect (@subject_type/@subject_id
// NamedArgs, ::text-cast seed), not ported from the turso SQL — the shared
// storetest suite is the equivalence proof.
func reachableCTE(schema pgxdb.Schema) string {
	return `WITH RECURSIVE reachable(atype, aid, arelation) AS (
	SELECT @subject_type::text, @subject_id::text, ''::text
	UNION
	SELECT r.resource_type, r.resource_id, r.relation
	FROM ` + schema.Table("iam_relationships") + ` r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
)`
}

// boundedReachableCTE uses the same full-state UNION as unbounded expansion.
// The materialized prefix contains at most budget+1 distinct states: a full
// prefix proves overflow. Omitting depth avoids regenerating cycles at every
// level and lets PostgreSQL stop producing states when the prefix is full.
// Physical scans remain planner-dependent; this is not a hard I/O ceiling.
func boundedReachableCTE(schema pgxdb.Schema) string {
	return reachableCTE(schema) + `,
capped AS MATERIALIZED (
 SELECT atype, aid, arelation FROM reachable LIMIT @state_cap
)`
}

// tupleKeyExpr preserves the complete tuple in byte-order listing cursors.
const tupleKeyExpr = "(resource_type || chr(1) || resource_id || chr(1) || relation || chr(1) || subject_type || chr(1) || subject_id || chr(1) || subject_relation) COLLATE \"C\""

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

// relationshipStore reads through the ambient querier and routes all writes
// through a join-or-own transaction so facts and optional history commit together.
type relationshipStore struct {
	audit  bool
	model  *relationships.ReadModel
	db     *pgxdb.DB
	schema pgxdb.Schema
}

func newRelationshipStore(db *pgxdb.DB, cfg config) *relationshipStore {
	return &relationshipStore{db: db, schema: cfg.schema, audit: cfg.audit}
}

// table renders name under the store's schema — the one chokepoint every
// statement on this store goes through.
func (s *relationshipStore) table(name string) string { return s.schema.Table(name) }

var _ relationships.Storer = (*relationshipStore)(nil)

// CheckRelationWithGroupExpansion reports whether the subject — or any group it
// transitively belongs to — holds the relation on the resource. maxExpansionStates
// bounds the group expansion (boundedReachableCTE): more than maxExpansionStates
// distinct reachable states returns relationship.ErrExpansionBudgetExceeded, never
// a deny. maxExpansionStates <= 0 uses the unbounded reachableCTE.
func (s *relationshipStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	if maxExpansionStates <= 0 {
		q := reachableCTE(s.schema) + `
SELECT EXISTS (
	SELECT 1
	FROM ` + s.table("iam_relationships") + ` r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
	WHERE r.resource_type = @resource_type AND r.resource_id = @resource_id AND r.relation = @relation
)`
		var ok bool
		if err := s.reader(ctx).QueryRow(ctx, q, pgx.NamedArgs{
			"subject_type":  subjectType,
			"subject_id":    subjectID,
			"resource_type": resourceType,
			"resource_id":   resourceID,
			"relation":      relation,
		}).Scan(&ok); err != nil {
			return false, pgxdb.MapError(err)
		}
		return ok, nil
	}

	q := boundedReachableCTE(s.schema) + `
SELECT
	(SELECT count(*) FROM capped) AS state_count,
	EXISTS (
		SELECT 1
		FROM ` + s.table("iam_relationships") + ` r
		JOIN capped ON r.subject_type = capped.atype AND r.subject_id = capped.aid AND r.subject_relation = capped.arelation
		WHERE r.resource_type = @resource_type AND r.resource_id = @resource_id AND r.relation = @relation
	) AS matched`
	var stateCount int
	var matched bool
	if err := s.reader(ctx).QueryRow(ctx, q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
		"state_cap":     maxExpansionStates + 1,
	}).Scan(&stateCount, &matched); err != nil {
		return false, pgxdb.MapError(err)
	}
	if stateCount > maxExpansionStates {
		return false, relationships.ErrExpansionBudgetExceeded
	}
	return matched, nil
}

// rowQuerier is the Query seam shared by the pool (*pgxdb.DB) and a transaction
// (*pgxdb.Tx), so one relation-targets reader serves the read side and the
// mutation repository's transaction-bound DecisionView alike.
type rowQuerier interface {
	Query(ctx context.Context, query string, args ...any) (pgx.Rows, error)
}

// GetRelationTargets returns the subjects holding a relation on a resource. An
// empty subject_relation reads back as "" (a concrete subject); a non-empty one
// as the exact userset relation.
func (s *relationshipStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	return relationTargets(ctx, s.reader(ctx), s.schema, resourceType, resourceID, relation)
}

// relationTargets is the one relation-targets read: the read-side
// GetRelationTargets runs it on the ambient querier (pool or host transaction),
// the DecisionView's RelationTargets on the mutation transaction. Same
// statement, same row order, same mapping.
func relationTargets(ctx context.Context, q rowQuerier, schema pgxdb.Schema, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	stmt := `SELECT subject_type, subject_id, subject_relation FROM ` + schema.Table("iam_relationships") + ` WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation`
	rows, err := q.Query(ctx, stmt, pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID, "relation": relation})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	defer rows.Close()

	var out []relationships.RelationTarget
	for rows.Next() {
		var subjectType, subjectID, subjectRelation string
		if err := rows.Scan(&subjectType, &subjectID, &subjectRelation); err != nil {
			return nil, pgxdb.MapError(err)
		}
		out = append(out, relationships.RelationTarget{
			Type:     subjectType,
			ID:       subjectID,
			Relation: subjectRelation,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	return out, nil
}

// CheckRelationExists reports whether an exact direct tuple is present for a
// CONCRETE subject (no expansion; subject_relation must be empty — a stored userset
// tuple with the same type/id does not satisfy a concrete probe). Used for the
// platform-admin data-tuple check and last-owner counting.
func (s *relationshipStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	q := `SELECT EXISTS (SELECT 1 FROM ` + s.table("iam_relationships") + ` WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation AND subject_type = @subject_type AND subject_id = @subject_id AND subject_relation = '')`
	var ok bool
	if err := s.db.QuerierFrom(ctx).QueryRow(ctx, q, pgx.NamedArgs{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
		"subject_type":  subjectType,
		"subject_id":    subjectID,
	}).Scan(&ok); err != nil {
		return false, pgxdb.MapError(err)
	}
	return ok, nil
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
		q := reachableCTE(s.schema) + `
SELECT DISTINCT r.resource_id
FROM ` + s.table("iam_relationships") + ` r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
WHERE r.resource_type = @resource_type AND r.relation = @relation AND r.resource_id = ANY(@resource_ids::text[])`
		matched, err := queryStrings(ctx, s.reader(ctx), q, pgx.NamedArgs{
			"subject_type":  subjectType,
			"subject_id":    subjectID,
			"resource_type": resourceType,
			"relation":      relation,
			"resource_ids":  resourceIDs,
		})
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
	q := boundedReachableCTE(s.schema) + `,
cnt AS (SELECT count(*) AS n FROM capped),
matches AS (
	SELECT DISTINCT r.resource_id AS rid
	FROM ` + s.table("iam_relationships") + ` r
	JOIN capped ON r.subject_type = capped.atype AND r.subject_id = capped.aid AND r.subject_relation = capped.arelation
	WHERE r.resource_type = @resource_type AND r.relation = @relation AND r.resource_id = ANY(@resource_ids::text[])
)
SELECT cnt.n AS state_count, m.rid
FROM cnt LEFT JOIN matches m ON true`
	rows, err := s.reader(ctx).Query(ctx, q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"resource_type": resourceType,
		"relation":      relation,
		"resource_ids":  resourceIDs,
		"state_cap":     maxExpansionStates + 1,
	})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	defer rows.Close()

	overflow := false
	for rows.Next() {
		var stateCount int
		var rid *string
		if err := rows.Scan(&stateCount, &rid); err != nil {
			return nil, pgxdb.MapError(err)
		}
		if stateCount > maxExpansionStates {
			overflow = true
		}
		if rid != nil {
			out[*rid] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	if overflow {
		return nil, relationships.ErrExpansionBudgetExceeded
	}
	return out, nil
}

// FilterRelation returns the DISTINCT, byte-order sorted subset of resourceIDs
// the subject holds relation on, with group expansion — the set form of
// CheckRelationWithGroupExpansion in ONE statement. The whole id set binds as a
// SINGLE text[] parameter (`resource_id = ANY(@resource_ids)`), so PostgreSQL's
// bind-parameter ceiling is never the bound here and no chunking is needed; the
// 0005 index (resource_type, relation, resource_id COLLATE "C") serves both the
// equality columns and the ordered output.
//
// COLLATE "C" is pinned on the projected/ordered expression for the same reason
// the keyset lookups pin it (see lookupResourceIDsSQL): the engine compares this
// output with Go string comparison, so the order must be RAW BYTE order.
//
// maxExpansionStates bounds the shared subject expansion (boundedReachableCTE);
// overflow returns relationship.ErrExpansionBudgetExceeded, never a short list.
// maxExpansionStates <= 0 uses the unbounded reachableCTE.
func (s *relationshipStore) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}

	if maxExpansionStates <= 0 {
		q := reachableCTE(s.schema) + `
SELECT DISTINCT r.resource_id COLLATE "C" AS resource_id
FROM ` + s.table("iam_relationships") + ` r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
WHERE r.resource_type = @resource_type AND r.relation = @relation AND r.resource_id = ANY(@resource_ids::text[])
ORDER BY r.resource_id COLLATE "C"`
		return queryStrings(ctx, s.reader(ctx), q, pgx.NamedArgs{
			"subject_type":  subjectType,
			"subject_id":    subjectID,
			"resource_type": resourceType,
			"relation":      relation,
			"resource_ids":  resourceIDs,
		})
	}

	// The distinct-state count rides every result row via the cnt cross-join, so
	// overflow is detectable even when no resource matches (zero match rows) —
	// the same shape CheckBatchDirect uses.
	q := boundedReachableCTE(s.schema) + `,
cnt AS (SELECT count(*) AS n FROM capped),
matches AS (
	SELECT DISTINCT r.resource_id COLLATE "C" AS rid
	FROM ` + s.table("iam_relationships") + ` r
	JOIN capped ON r.subject_type = capped.atype AND r.subject_id = capped.aid AND r.subject_relation = capped.arelation
	WHERE r.resource_type = @resource_type AND r.relation = @relation AND r.resource_id = ANY(@resource_ids::text[])
)
SELECT cnt.n AS state_count, m.rid
FROM cnt LEFT JOIN matches m ON true
ORDER BY m.rid COLLATE "C"`
	rows, err := s.reader(ctx).Query(ctx, q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"resource_type": resourceType,
		"relation":      relation,
		"resource_ids":  resourceIDs,
		"state_cap":     maxExpansionStates + 1,
	})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	defer rows.Close()

	overflow := false
	var out []string
	for rows.Next() {
		var stateCount int
		var rid *string
		if err := rows.Scan(&stateCount, &rid); err != nil {
			return nil, pgxdb.MapError(err)
		}
		if stateCount > maxExpansionStates {
			overflow = true
		}
		if rid != nil {
			out = append(out, *rid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	if overflow {
		return nil, relationships.ErrExpansionBudgetExceeded
	}
	return out, nil
}

// RelationTargetsFor returns the subjects holding relation on each of
// resourceIDs — the set form of GetRelationTargets in ONE statement, with the
// id set bound as a single text[] parameter. An id with no targets is absent
// from the map; a duplicated input id carries one entry.
func (s *relationshipStore) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]relationships.RelationTarget, error) {
	out := make(map[string][]relationships.RelationTarget, len(resourceIDs))
	if len(resourceIDs) == 0 {
		return out, nil
	}

	q := `SELECT resource_id, subject_type, subject_id, subject_relation FROM ` + s.table("iam_relationships") + `
WHERE resource_type = @resource_type AND relation = @relation AND resource_id = ANY(@resource_ids::text[])`
	rows, err := s.reader(ctx).Query(ctx, q, pgx.NamedArgs{
		"resource_type": resourceType,
		"relation":      relation,
		"resource_ids":  resourceIDs,
	})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	defer rows.Close()

	for rows.Next() {
		var resourceID, subjectType, subjectID, subjectRelation string
		if err := rows.Scan(&resourceID, &subjectType, &subjectID, &subjectRelation); err != nil {
			return nil, pgxdb.MapError(err)
		}
		out[resourceID] = append(out[resourceID], relationships.RelationTarget{
			Type:     subjectType,
			ID:       subjectID,
			Relation: subjectRelation,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	return out, nil
}

// CreateRelationships inserts full tuples as one batch. Exact duplicates and
// competing relations for an already-related subject retain the existing row.
func (s *relationshipStore) CreateRelationships(ctx context.Context, in []relationships.CreateRelationship) error {
	return s.write(ctx, func(tx *writeTx) error { return createRelationships(ctx, tx, s.schema, in) })
}

func createRelationships(ctx context.Context, db *writeTx, schema pgxdb.Schema, in []relationships.CreateRelationship) error {
	return insertRelationships(ctx, db, schema, in, false)
}

func insertRelationships(ctx context.Context, db *writeTx, schema pgxdb.Schema, in []relationships.CreateRelationship, exactConflictsOnly bool) error {
	if len(in) == 0 {
		return nil
	}
	for _, row := range in {
		if err := row.Validate(); err != nil {
			return err
		}
	}

	n := len(in)
	resourceTypes := make([]string, n)
	resourceIDs := make([]string, n)
	relations := make([]string, n)
	subjectTypes := make([]string, n)
	subjectIDs := make([]string, n)
	subjectRelations := make([]string, n)
	for i, c := range in {
		resourceTypes[i] = c.ResourceType
		resourceIDs[i] = c.ResourceID
		relations[i] = c.Relation
		subjectTypes[i] = c.SubjectType
		subjectIDs[i] = c.SubjectID
		subjectRelations[i] = c.SubjectRelation
	}

	args := pgx.NamedArgs{
		"resource_types":    resourceTypes,
		"resource_ids":      resourceIDs,
		"relations":         relations,
		"subject_types":     subjectTypes,
		"subject_ids":       subjectIDs,
		"subject_relations": subjectRelations,
	}

	q := `INSERT INTO ` + schema.Table("iam_relationships") + ` (resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
SELECT rt, rid, rel, st, sid, sr
FROM UNNEST(@resource_types::text[], @resource_ids::text[], @relations::text[], @subject_types::text[], @subject_ids::text[], @subject_relations::text[])
    AS u(rt, rid, rel, st, sid, sr)`
	if exactConflictsOnly {
		q += ` ON CONFLICT (resource_type, resource_id, relation, subject_type, subject_id, subject_relation) DO NOTHING`
	} else {
		q += ` ON CONFLICT DO NOTHING`
	}

	if _, err := db.relationships(ctx, audit.ActionAdded, q, args); err != nil {
		if exactConflictsOnly && errors.Is(err, sdk.ErrAlreadyExists) {
			return fmt.Errorf("authorization pgx store: a desired target conflicts with an existing relationship: %w", sdk.ErrConflict)
		}
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
			return fmt.Errorf("authorization pgx store: SetRelationTargets row is outside requested scope: %w", sdk.ErrInvalidInput)
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
		_, err := tx.relationships(ctx, audit.ActionRemoved, `DELETE FROM `+s.table("iam_relationships")+` WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation`, pgx.NamedArgs{
			"resource_type": resourceType, "resource_id": resourceID, "relation": relationName,
		})
		return pgxdb.MapError(err)
	}

	subjectTypes := make([]string, len(rows))
	subjectIDs := make([]string, len(rows))
	subjectRelations := make([]string, len(rows))
	for i, c := range rows {
		subjectTypes[i], subjectIDs[i], subjectRelations[i] = c.SubjectType, c.SubjectID, c.SubjectRelation
	}
	args := pgx.NamedArgs{
		"resource_type": resourceType, "resource_id": resourceID, "relation": relationName,
		"subject_types": subjectTypes, "subject_ids": subjectIDs, "subject_relations": subjectRelations,
	}
	conflictQ := `SELECT EXISTS (
	SELECT 1 FROM ` + s.table("iam_relationships") + ` r
	JOIN UNNEST(@subject_types::text[], @subject_ids::text[], @subject_relations::text[]) AS d(st, sid, sr)
	  ON r.subject_type = d.st AND r.subject_id = d.sid AND r.subject_relation = d.sr
	WHERE r.resource_type = @resource_type AND r.resource_id = @resource_id AND r.relation <> @relation
)`
	var conflict bool
	if err := tx.QueryRow(ctx, conflictQ, args).Scan(&conflict); err != nil {
		return pgxdb.MapError(err)
	}
	if conflict {
		return fmt.Errorf("authorization pgx store: a desired target already holds a different relation on %s:%s: %w", resourceType, resourceID, sdk.ErrConflict)
	}

	deleteQ := `DELETE FROM ` + s.table("iam_relationships") + ` r
WHERE r.resource_type = @resource_type AND r.resource_id = @resource_id AND r.relation = @relation
  AND NOT EXISTS (
	SELECT 1 FROM UNNEST(@subject_types::text[], @subject_ids::text[], @subject_relations::text[]) AS d(st, sid, sr)
	WHERE r.subject_type = d.st AND r.subject_id = d.sid AND r.subject_relation = d.sr
  )`
	if _, err := tx.relationships(ctx, audit.ActionRemoved, deleteQ, args); err != nil {
		return pgxdb.MapError(err)
	}
	// Exact duplicates are harmless; any other unique conflict must abort this
	// reconciliation, including a competing relation inserted after the probe.
	return insertRelationships(ctx, tx, s.schema, rows, true)
}

// DeleteResourceRelationships removes every tuple for a resource (idempotent).
func (s *relationshipStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	q := `DELETE FROM ` + s.table("iam_relationships") + ` WHERE resource_type = @resource_type AND resource_id = @resource_id`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.relationships(ctx, audit.ActionRemoved, q, pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID})
		return err
	})
}

// DeleteRelationshipTarget removes one exact tuple, including subject_relation.
func (s *relationshipStore) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target relationships.SubjectRef) error {
	q := `DELETE FROM ` + s.table("iam_relationships") + ` WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation AND subject_type = @subject_type AND subject_id = @subject_id AND subject_relation = @subject_relation`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.relationships(ctx, audit.ActionRemoved, q, pgx.NamedArgs{
			"resource_type": resourceType, "resource_id": resourceID, "relation": relationName,
			"subject_type": target.Type, "subject_id": target.ID, "subject_relation": target.Relation,
		})
		return err
	})
}

// DeleteRelationship removes one exact tuple (idempotent — absent is nil).
func (s *relationshipStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	q := `DELETE FROM ` + s.table("iam_relationships") + ` WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation AND subject_type = @subject_type AND subject_id = @subject_id`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.relationships(ctx, audit.ActionRemoved, q, pgx.NamedArgs{
			"resource_type": resourceType,
			"resource_id":   resourceID,
			"relation":      relation,
			"subject_type":  subjectType,
			"subject_id":    subjectID,
		})
		return err
	})
}

// DeleteByResourceAndSubject removes every relation a subject holds on a resource
// (idempotent).
func (s *relationshipStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	q := `DELETE FROM ` + s.table("iam_relationships") + ` WHERE resource_type = @resource_type AND resource_id = @resource_id AND subject_type = @subject_type AND subject_id = @subject_id`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.relationships(ctx, audit.ActionRemoved, q, pgx.NamedArgs{
			"resource_type": resourceType,
			"resource_id":   resourceID,
			"subject_type":  subjectType,
			"subject_id":    subjectID,
		})
		return err
	})
}

// CountByResourceAndRelation counts DIRECT tuples only — never expanded
// membership (the §2.5 security pin: last-owner protection depends on it).
func (s *relationshipStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	q := `SELECT COUNT(*) FROM ` + s.table("iam_relationships") + ` WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation`
	var n int
	if err := s.db.QuerierFrom(ctx).QueryRow(ctx, q, pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID, "relation": relation}).Scan(&n); err != nil {
		return 0, pgxdb.MapError(err)
	}
	return n, nil
}

// relationshipsBaseSQL exposes the computed key to the outer keyset predicate.
func relationshipsBaseSQL(schema pgxdb.Schema, columns, where string) string {
	return `SELECT ` + columns + `, tuple_key FROM (
    SELECT ` + columns + `, ` + tupleKeyExpr + ` AS tuple_key
    FROM ` + schema.Table("iam_relationships") + where + `
) AS r WHERE 1 = 1`
}

// ListRelationshipsBySubject pages complete tuple identities in byte order.
func (s *relationshipStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationships.SubjectRelationshipFilter, req list.Request) (list.Page[relationships.SubjectRelationship], error) {
	where := " WHERE subject_type = @subject_type AND subject_id = @subject_id"
	args := pgx.NamedArgs{"subject_type": subjectType, "subject_id": subjectID}
	if filter.ResourceType != nil {
		where += " AND resource_type = @resource_type"
		args["resource_type"] = *filter.ResourceType
	}
	if filter.Relation != nil {
		where += " AND relation = @relation"
		args["relation"] = *filter.Relation
	}
	q := pgxdb.ListQuery[subjectRelationshipRow]{
		BaseSQL:      relationshipsBaseSQL(s.schema, "resource_type, resource_id, relation, subject_relation", where),
		Args:         args,
		OrderFields:  relationships.OrderFields,
		DefaultOrder: relationships.DefaultOrder,
		PK:           "tuple_key",
		OrderValueOf: func(r subjectRelationshipRow, _ string) any { return r.TupleKey },
		PKOf:         func(r subjectRelationshipRow) string { return r.TupleKey },
	}
	page, err := pgxdb.List(ctx, s.db.QuerierFrom(ctx), q, req)
	if err != nil {
		return list.Page[relationships.SubjectRelationship]{}, err
	}
	return list.MapPage(page, subjectRelationshipRow.toDomain), nil
}

// ListRelationshipsByResource pages complete tuple identities in byte order.
func (s *relationshipStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationships.ResourceRelationshipFilter, req list.Request) (list.Page[relationships.ResourceRelationship], error) {
	where := " WHERE resource_type = @resource_type AND resource_id = @resource_id"
	args := pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID}
	if filter.SubjectType != nil {
		where += " AND subject_type = @subject_type"
		args["subject_type"] = *filter.SubjectType
	}
	if filter.Relation != nil {
		where += " AND relation = @relation"
		args["relation"] = *filter.Relation
	}
	q := pgxdb.ListQuery[resourceRelationshipRow]{
		BaseSQL:      relationshipsBaseSQL(s.schema, "subject_type, subject_id, relation, subject_relation", where),
		Args:         args,
		OrderFields:  relationships.OrderFields,
		DefaultOrder: relationships.DefaultOrder,
		PK:           "tuple_key",
		OrderValueOf: func(r resourceRelationshipRow, _ string) any { return r.TupleKey },
		PKOf:         func(r resourceRelationshipRow) string { return r.TupleKey },
	}
	page, err := pgxdb.List(ctx, s.db.QuerierFrom(ctx), q, req)
	if err != nil {
		return list.Page[relationships.ResourceRelationship]{}, err
	}
	return list.MapPage(page, resourceRelationshipRow.toDomain), nil
}

// lookupResourceIDsSQL renders the direct-relation keyset lookup and its args.
// It is split from the method so the EXPLAIN test can plan the exact statement
// the store runs.
//
// Every lookup statement in this file pins COLLATE "C" PER QUERY rather than
// leaning on the column's collation. The three lookups are KEYSET reads whose
// output the ENGINE merges across streams with Go string comparison
// (relationship.Storer's Bounding note), so both the order and the `> after`
// predicate must be RAW BYTE order — a locale-collated order would skip or
// repeat IDs across pages. iam_relationships.resource_id is DELIBERATELY left
// uncollated in 0001 (it is a recursion column of the reachable
// userset-expansion CTE, and pinning it in the DDL raises a recursive-term
// collation mismatch, SQLSTATE 42P21), so the byte-order contract is pinned
// here, at every comparison and ORDER BY that carries it, and matched by the
// 0005 index's `resource_id COLLATE "C"` column so the keyset predicate
// range-scans instead of sorting. The collated expression is also what the
// SELECT list projects: PostgreSQL requires a SELECT DISTINCT query's ORDER BY
// expression to appear in the select list, and the projected value is
// byte-identical either way (collation governs comparison, not the text).
func (s *relationshipStore) lookupResourceIDsSQL(resourceType string, relations []string, subjectType, subjectID, after string, limit int) (string, pgx.NamedArgs) {
	q := reachableCTE(s.schema) + `
SELECT DISTINCT r.resource_id COLLATE "C" AS resource_id
FROM ` + s.table("iam_relationships") + ` r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
WHERE r.resource_type = @resource_type AND r.relation = ANY(@relations::text[])
  AND (@after::text = '' OR r.resource_id COLLATE "C" > @after::text)
ORDER BY r.resource_id COLLATE "C"` + limitClause(limit)
	return q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"resource_type": resourceType,
		"relations":     relations,
		"after":         after,
		"limit":         limit,
	}
}

// LookupResourceIDs returns the distinct resource IDs where the subject has any
// of the relations, with group expansion: sorted ascending in BYTE order,
// strictly greater than after (after == "" starts from the beginning), at most
// limit rows (@limit). The engine passes MaxLookupResults+1 for a complete
// enumeration — a full-limit return is a distinguishable overflow signal, never
// a silently truncated complete result — or page+1 for a paged one, where the
// extra row is the HasMore lookahead. See [relationshipStore.lookupResourceIDsSQL]
// for the COLLATE "C" pin.
func (s *relationshipStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	if len(relations) == 0 {
		return nil, nil
	}
	q, args := s.lookupResourceIDsSQL(resourceType, relations, subjectType, subjectID, after, limit)
	return queryStrings(ctx, s.reader(ctx), q, args)
}

// lookupResourceIDsByRelationTargetSQL renders the relation-target keyset lookup
// and its args (split from the method for the EXPLAIN test).
func (s *relationshipStore) lookupResourceIDsByRelationTargetSQL(resourceType, relation, targetType string, targetIDs []string, after string, limit int) (string, pgx.NamedArgs) {
	q := `SELECT DISTINCT resource_id COLLATE "C" AS resource_id FROM ` + s.table("iam_relationships") + `
WHERE resource_type = @resource_type AND relation = @relation AND subject_type = @target_type AND subject_id = ANY(@target_ids::text[]) AND subject_relation = ''
  AND (@after::text = '' OR resource_id COLLATE "C" > @after::text)
ORDER BY resource_id COLLATE "C"` + limitClause(limit)
	return q, pgx.NamedArgs{
		"resource_type": resourceType,
		"relation":      relation,
		"target_type":   targetType,
		"target_ids":    targetIDs,
		"after":         after,
		"limit":         limit,
	}
}

// LookupResourceIDsByRelationTarget returns the distinct resource IDs whose
// relation points at any of the target IDs (concrete subjects, no expansion):
// byte-order sorted, strictly greater than after, at most limit rows. Same
// keyset contract and COLLATE "C" pin as [relationshipStore.LookupResourceIDs].
func (s *relationshipStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	q, args := s.lookupResourceIDsByRelationTargetSQL(resourceType, relation, targetType, targetIDs, after, limit)
	return queryStrings(ctx, s.reader(ctx), q, args)
}

// lookupDescendantResourceIDsSQL renders the descendant-closure statement and its
// args (split from the method for the EXPLAIN test).
func (s *relationshipStore) lookupDescendantResourceIDsSQL(resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) (string, pgx.NamedArgs) {
	q := `WITH RECURSIVE descendants(rid) AS (
	SELECT r.resource_id
	FROM ` + s.table("iam_relationships") + ` r
	WHERE r.resource_type = @resource_type AND r.relation = ANY(@relations::text[]) AND r.subject_type = @subject_type AND r.subject_relation = '' AND r.subject_id = ANY(@root_ids::text[])
	UNION
	SELECT r.resource_id
	FROM ` + s.table("iam_relationships") + ` r
	JOIN descendants d ON r.subject_id = d.rid
	WHERE r.resource_type = @resource_type AND r.relation = ANY(@relations::text[]) AND r.subject_type = @subject_type AND r.subject_relation = ''
)
SELECT DISTINCT rid COLLATE "C" AS rid FROM descendants
WHERE (@after::text = '' OR rid COLLATE "C" > @after::text)
ORDER BY rid COLLATE "C"` + limitClause(limit)
	return q, pgx.NamedArgs{
		"resource_type": resourceType,
		"relations":     relations,
		"subject_type":  subjectType,
		"root_ids":      rootIDs,
		"after":         after,
		"limit":         limit,
	}
}

// LookupDescendantResourceIDs walks the UNION of the self-referential relations
// transitively from the root IDs (one recursive CTE, cycle-safe via UNION dedup;
// the relation set is closed over in BOTH the anchor and the recursive term, so
// a path that alternates relations is followed in one call). Roots are not
// returned unless a cycle makes one a genuine descendant. after/limit page the
// sorted closure, not its work: the database computes the whole closure on every
// call (plan A3), so this stream's per-page cost stays the closure size.
func (s *relationshipStore) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	if len(rootIDs) == 0 || len(relations) == 0 {
		return nil, nil
	}
	q, args := s.lookupDescendantResourceIDsSQL(resourceType, relations, subjectType, rootIDs, after, limit)
	return queryStrings(ctx, s.reader(ctx), q, args)
}

// limitClause appends a bounded LIMIT when limit is positive (the engine always
// passes MaxLookupResults+1). A non-positive limit is unbounded (defensive).
func limitClause(limit int) string {
	if limit > 0 {
		return "\nLIMIT @limit"
	}
	return ""
}

// queryStrings runs a single-column string SELECT on q (the pool, the ambient
// transaction, or a mutation transaction — callers pass s.db.QuerierFrom(ctx))
// and collects the rows.
func queryStrings(ctx context.Context, q rowQuerier, query string, args pgx.NamedArgs) ([]string, error) {
	rows, err := q.Query(ctx, query, args)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	return out, nil
}

func (s *relationshipStore) write(ctx context.Context, fn func(*writeTx) error) error {
	return runWrite(ctx, s.db, config{audit: s.audit, schema: s.schema}, fn)
}
