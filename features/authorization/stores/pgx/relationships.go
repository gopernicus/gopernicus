package pgx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
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
// graph walk and the turso sibling (the engine's MaxThroughDepth is an engine-only
// bound). Re-derived in the PostgreSQL dialect (@subject_type/@subject_id
// NamedArgs, ::text-cast seed), not ported from the turso SQL — the shared
// storetest suite is the equivalence proof.
const reachableCTE = `WITH RECURSIVE reachable(atype, aid, arelation) AS (
	SELECT @subject_type::text, @subject_id::text, ''::text
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
//   - a `depth` column carries the recursion level, and `WHERE depth < @max_depth`
//     (max_depth = maxExpansionStates) stops the recursion from running away down
//     a deep membership chain — the recursion performs at most maxExpansionStates
//     levels regardless of graph shape;
//   - `capped` materializes the DISTINCT reachable states LIMITed to @state_cap
//     (= maxExpansionStates+1), so the downstream match join never scans more than
//     maxExpansionStates+1 states, and a `count(*) = @state_cap` reading is the
//     deterministic OVERFLOW signal the caller maps to ErrExpansionBudgetExceeded.
//
// Correctness pin: any state a within-budget graph needs has a shortest
// membership path of length < its distinct-state count <= maxExpansionStates, so
// depth < @max_depth never cuts a within-budget state; and reaching any state at
// depth > max_depth implies > maxExpansionStates distinct states along the path,
// which the state_cap overflow catches. A graph that fits within the budget thus
// yields the SAME bool as the unbounded reachableCTE. UNION dedups on the full
// (atype, aid, arelation, depth) row, so cycles terminate by the depth bound.
const boundedReachableCTE = `WITH RECURSIVE reachable(atype, aid, arelation, depth) AS (
	SELECT @subject_type::text, @subject_id::text, ''::text, 0
	UNION
	SELECT r.resource_type, r.resource_id, r.relation, reachable.depth + 1
	FROM iam_relationships r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
	WHERE reachable.depth < @max_depth
),
states AS (
	SELECT DISTINCT atype, aid, arelation FROM reachable
),
capped AS (
	SELECT atype, aid, arelation FROM states LIMIT @state_cap
)`

// subjectRelationshipRow is the db-tagged projection of a ListRelationshipsBySubject row.
type subjectRelationshipRow struct {
	ID           string    `db:"relationship_id"`
	ResourceType string    `db:"resource_type"`
	ResourceID   string    `db:"resource_id"`
	Relation     string    `db:"relation"`
	CreatedAt    time.Time `db:"created_at"`
}

func (r subjectRelationshipRow) toDomain() relationship.SubjectRelationship {
	return relationship.SubjectRelationship{
		ID:           r.ID,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		Relation:     r.Relation,
		CreatedAt:    r.CreatedAt.UTC(),
	}
}

// resourceRelationshipRow is the db-tagged projection of a ListRelationshipsByResource row.
type resourceRelationshipRow struct {
	ID          string    `db:"relationship_id"`
	SubjectType string    `db:"subject_type"`
	SubjectID   string    `db:"subject_id"`
	Relation    string    `db:"relation"`
	CreatedAt   time.Time `db:"created_at"`
}

func (r resourceRelationshipRow) toDomain() relationship.ResourceRelationship {
	return relationship.ResourceRelationship{
		ID:          r.ID,
		SubjectType: r.SubjectType,
		SubjectID:   r.SubjectID,
		Relation:    r.Relation,
		CreatedAt:   r.CreatedAt.UTC(),
	}
}

// relationshipStore fills relationship.Storer over iam_relationships.
type relationshipStore struct {
	db *pgxdb.DB
}

func newRelationshipStore(db *pgxdb.DB) *relationshipStore {
	return &relationshipStore{db: db}
}

var _ relationship.Storer = (*relationshipStore)(nil)

// CheckRelationWithGroupExpansion reports whether the subject — or any group it
// transitively belongs to — holds the relation on the resource. maxExpansionStates
// bounds the group expansion (boundedReachableCTE): more than maxExpansionStates
// distinct reachable states returns relationship.ErrExpansionBudgetExceeded, never
// a deny. maxExpansionStates <= 0 uses the unbounded reachableCTE.
func (s *relationshipStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	if maxExpansionStates <= 0 {
		q := reachableCTE + `
SELECT EXISTS (
	SELECT 1
	FROM iam_relationships r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
	WHERE r.resource_type = @resource_type AND r.resource_id = @resource_id AND r.relation = @relation
)`
		var ok bool
		if err := s.db.QueryRow(ctx, q, pgx.NamedArgs{
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

	q := boundedReachableCTE + `
SELECT
	(SELECT count(*) FROM capped) AS state_count,
	EXISTS (
		SELECT 1
		FROM iam_relationships r
		JOIN capped ON r.subject_type = capped.atype AND r.subject_id = capped.aid AND r.subject_relation = capped.arelation
		WHERE r.resource_type = @resource_type AND r.resource_id = @resource_id AND r.relation = @relation
	) AS matched`
	var stateCount int
	var matched bool
	if err := s.db.QueryRow(ctx, q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
		"max_depth":     maxExpansionStates,
		"state_cap":     maxExpansionStates + 1,
	}).Scan(&stateCount, &matched); err != nil {
		return false, pgxdb.MapError(err)
	}
	if stateCount > maxExpansionStates {
		return false, relationship.ErrExpansionBudgetExceeded
	}
	return matched, nil
}

// GetRelationTargets returns the subjects holding a relation on a resource. An
// empty subject_relation reads back as "" (a concrete subject); a non-empty one
// as the exact userset relation.
func (s *relationshipStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	const q = `SELECT subject_type, subject_id, subject_relation FROM iam_relationships WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID, "relation": relation})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	defer rows.Close()

	var out []relationship.RelationTarget
	for rows.Next() {
		var subjectType, subjectID, subjectRelation string
		if err := rows.Scan(&subjectType, &subjectID, &subjectRelation); err != nil {
			return nil, pgxdb.MapError(err)
		}
		out = append(out, relationship.RelationTarget{
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
	const q = `SELECT EXISTS (SELECT 1 FROM iam_relationships WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation AND subject_type = @subject_type AND subject_id = @subject_id AND subject_relation = '')`
	var ok bool
	if err := s.db.QueryRow(ctx, q, pgx.NamedArgs{
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
		q := reachableCTE + `
SELECT DISTINCT r.resource_id
FROM iam_relationships r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
WHERE r.resource_type = @resource_type AND r.relation = @relation AND r.resource_id = ANY(@resource_ids::text[])`
		matched, err := s.queryStrings(ctx, q, pgx.NamedArgs{
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
	q := boundedReachableCTE + `,
cnt AS (SELECT count(*) AS n FROM capped),
matches AS (
	SELECT DISTINCT r.resource_id AS rid
	FROM iam_relationships r
	JOIN capped ON r.subject_type = capped.atype AND r.subject_id = capped.aid AND r.subject_relation = capped.arelation
	WHERE r.resource_type = @resource_type AND r.relation = @relation AND r.resource_id = ANY(@resource_ids::text[])
)
SELECT cnt.n AS state_count, m.rid
FROM cnt LEFT JOIN matches m ON true`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"resource_type": resourceType,
		"relation":      relation,
		"resource_ids":  resourceIDs,
		"max_depth":     maxExpansionStates,
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
		return nil, relationship.ErrExpansionBudgetExceeded
	}
	return out, nil
}

// CreateRelationships inserts a batch as one INSERT ... SELECT FROM UNNEST(...) ON
// CONFLICT DO NOTHING (the postgres bulk-insert analog of turso's multi-row
// VALUES). The bare ON CONFLICT covers both unique indexes: an exact-duplicate
// tuple AND a second, different relation for the same (subject, resource) are
// SILENT no-ops (nil error, existing row unchanged), never ErrAlreadyExists. The
// whole batch shares one store-stamped created_at, broadcast as a scalar.
//
// Id strategy (Q6): an ALL-empty batch DROPS the relationship_ids array and the
// relationship_id column so the DDL DEFAULT (gen_random_uuid()::text) fills each
// key; an ALL-populated batch includes them; a MIXED batch is a loud store error
// (the engine mints all-or-none). There is no RETURNING — the port is error-only.
func (s *relationshipStore) CreateRelationships(ctx context.Context, in []relationship.CreateRelationship) error {
	return createRelationships(ctx, s.db, in)
}

func createRelationships(ctx context.Context, db pgxdb.Querier, in []relationship.CreateRelationship) error {
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
		return fmt.Errorf("authorization pgx store: mixed relationship_id batch (%d empty, %d populated) — the engine mints all-or-none: %w", empty, populated, sdk.ErrInvalidInput)
	}
	withID := populated > 0

	n := len(in)
	resourceTypes := make([]string, n)
	resourceIDs := make([]string, n)
	relations := make([]string, n)
	subjectTypes := make([]string, n)
	subjectIDs := make([]string, n)
	subjectRelations := make([]string, n)
	var ids []string
	if withID {
		ids = make([]string, n)
	}
	for i, c := range in {
		if withID {
			ids[i] = c.RelationshipID
		}
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
		"created_at":        time.Now().UTC(),
	}

	var q string
	if withID {
		args["relationship_ids"] = ids
		q = `INSERT INTO iam_relationships (relationship_id, resource_type, resource_id, relation, subject_type, subject_id, subject_relation, created_at)
SELECT rel_id, rt, rid, rel, st, sid, sr, @created_at::timestamptz
FROM UNNEST(@relationship_ids::text[], @resource_types::text[], @resource_ids::text[], @relations::text[], @subject_types::text[], @subject_ids::text[], @subject_relations::text[])
	AS u(rel_id, rt, rid, rel, st, sid, sr)
ON CONFLICT DO NOTHING`
	} else {
		q = `INSERT INTO iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, subject_relation, created_at)
SELECT rt, rid, rel, st, sid, sr, @created_at::timestamptz
FROM UNNEST(@resource_types::text[], @resource_ids::text[], @relations::text[], @subject_types::text[], @subject_ids::text[], @subject_relations::text[])
	AS u(rt, rid, rel, st, sid, sr)
ON CONFLICT DO NOTHING`
	}

	if _, err := db.Exec(ctx, q, args); err != nil {
		return err
	}
	return nil
}

// SetRelationTargets serializes calls for one resource+relation with a
// transaction-scoped advisory lock, then reconciles the set in one transaction.
// The lock prevents two concurrent desired-state moves from committing a union.
func (s *relationshipStore) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, in []relationship.CreateRelationship) error {
	desired := make(map[relationship.SubjectRef]relationship.CreateRelationship, len(in))
	for _, c := range in {
		if c.ResourceType != resourceType || c.ResourceID != resourceID || c.Relation != relationName {
			return fmt.Errorf("authorization pgx store: SetRelationTargets row is outside requested scope: %w", sdk.ErrInvalidInput)
		}
		desired[c.Subject()] = c
	}
	rows := make([]relationship.CreateRelationship, 0, len(desired))
	for _, c := range desired {
		rows = append(rows, c)
	}

	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		lockKey := resourceType + "\x1f" + resourceID + "\x1f" + relationName
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(@lock_key, 0))`, pgx.NamedArgs{"lock_key": lockKey}); err != nil {
			return pgxdb.MapError(err)
		}

		if len(rows) == 0 {
			_, err := tx.Exec(ctx, `DELETE FROM iam_relationships WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation`, pgx.NamedArgs{
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
		const conflictQ = `SELECT EXISTS (
	SELECT 1 FROM iam_relationships r
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

		const deleteQ = `DELETE FROM iam_relationships r
WHERE r.resource_type = @resource_type AND r.resource_id = @resource_id AND r.relation = @relation
  AND NOT EXISTS (
	SELECT 1 FROM UNNEST(@subject_types::text[], @subject_ids::text[], @subject_relations::text[]) AS d(st, sid, sr)
	WHERE r.subject_type = d.st AND r.subject_id = d.sid AND r.subject_relation = d.sr
  )`
		if _, err := tx.Exec(ctx, deleteQ, args); err != nil {
			return pgxdb.MapError(err)
		}
		return createRelationships(ctx, tx, rows)
	})
}

// DeleteResourceRelationships removes every tuple for a resource (idempotent).
func (s *relationshipStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = @resource_type AND resource_id = @resource_id`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID})
	return err
}

// DeleteRelationshipTarget removes one exact tuple, including subject_relation.
func (s *relationshipStore) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target relationship.SubjectRef) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation AND subject_type = @subject_type AND subject_id = @subject_id AND subject_relation = @subject_relation`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"resource_type": resourceType, "resource_id": resourceID, "relation": relationName,
		"subject_type": target.Type, "subject_id": target.ID, "subject_relation": target.Relation,
	})
	return err
}

// DeleteRelationship removes one exact tuple (idempotent — absent is nil).
func (s *relationshipStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation AND subject_type = @subject_type AND subject_id = @subject_id`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
		"subject_type":  subjectType,
		"subject_id":    subjectID,
	})
	return err
}

// DeleteByResourceAndSubject removes every relation a subject holds on a resource
// (idempotent).
func (s *relationshipStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	const q = `DELETE FROM iam_relationships WHERE resource_type = @resource_type AND resource_id = @resource_id AND subject_type = @subject_type AND subject_id = @subject_id`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"subject_type":  subjectType,
		"subject_id":    subjectID,
	})
	return err
}

// CountByResourceAndRelation counts DIRECT tuples only — never expanded
// membership (the §2.5 security pin: last-owner protection depends on it).
func (s *relationshipStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	const q = `SELECT COUNT(*) FROM iam_relationships WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation`
	var n int
	if err := s.db.QueryRow(ctx, q, pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID, "relation": relation}).Scan(&n); err != nil {
		return 0, pgxdb.MapError(err)
	}
	return n, nil
}

// ListRelationshipsBySubject pages the resources a subject relates to (created_at
// DESC, relationship_id DESC).
func (s *relationshipStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
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
		BaseSQL:      `SELECT relationship_id, resource_type, resource_id, relation, created_at FROM iam_relationships` + where,
		Args:         args,
		OrderFields:  relationship.OrderFields,
		DefaultOrder: relationship.DefaultOrder,
		PK:           "relationship_id",
		OrderValueOf: func(r subjectRelationshipRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r subjectRelationshipRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[relationship.SubjectRelationship]{}, err
	}
	return crud.MapPage(page, subjectRelationshipRow.toDomain), nil
}

// ListRelationshipsByResource pages the subjects related to a resource (created_at
// DESC, relationship_id DESC).
func (s *relationshipStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
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
		BaseSQL:      `SELECT relationship_id, subject_type, subject_id, relation, created_at FROM iam_relationships` + where,
		Args:         args,
		OrderFields:  relationship.OrderFields,
		DefaultOrder: relationship.DefaultOrder,
		PK:           "relationship_id",
		OrderValueOf: func(r resourceRelationshipRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r resourceRelationshipRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[relationship.ResourceRelationship]{}, err
	}
	return crud.MapPage(page, resourceRelationshipRow.toDomain), nil
}

// LookupResourceIDs returns the distinct resource IDs (sorted) where the subject
// has any of the relations, with group expansion. It returns at most limit rows
// (@limit): the engine passes MaxLookupResults+1 so a full-limit return is a
// distinguishable overflow signal, never a silently truncated complete result.
func (s *relationshipStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string, limit int) ([]string, error) {
	if len(relations) == 0 {
		return nil, nil
	}
	q := reachableCTE + `
SELECT DISTINCT r.resource_id
FROM iam_relationships r
JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
WHERE r.resource_type = @resource_type AND r.relation = ANY(@relations::text[])
ORDER BY r.resource_id` + limitClause(limit)
	return s.queryStrings(ctx, q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"resource_type": resourceType,
		"relations":     relations,
		"limit":         limit,
	})
}

// LookupResourceIDsByRelationTarget returns the distinct resource IDs (sorted)
// whose relation points at any of the target IDs (no expansion), at most limit
// rows.
func (s *relationshipStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, limit int) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	q := `SELECT DISTINCT resource_id FROM iam_relationships
WHERE resource_type = @resource_type AND relation = @relation AND subject_type = @target_type AND subject_id = ANY(@target_ids::text[]) AND subject_relation = ''
ORDER BY resource_id` + limitClause(limit)
	return s.queryStrings(ctx, q, pgx.NamedArgs{
		"resource_type": resourceType,
		"relation":      relation,
		"target_type":   targetType,
		"target_ids":    targetIDs,
		"limit":         limit,
	})
}

// LookupDescendantResourceIDs walks a self-referential relation transitively from
// the root IDs (recursive CTE, cycle-safe via UNION dedup). Roots are not
// returned unless a cycle makes one a genuine descendant. Result is sorted and
// bounded at limit rows (the recursive-expansion result cap).
func (s *relationshipStore) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string, limit int) ([]string, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	q := `WITH RECURSIVE descendants(rid) AS (
	SELECT r.resource_id
	FROM iam_relationships r
	WHERE r.resource_type = @resource_type AND r.relation = @relation AND r.subject_type = @subject_type AND r.subject_relation = '' AND r.subject_id = ANY(@root_ids::text[])
	UNION
	SELECT r.resource_id
	FROM iam_relationships r
	JOIN descendants d ON r.subject_id = d.rid
	WHERE r.resource_type = @resource_type AND r.relation = @relation AND r.subject_type = @subject_type AND r.subject_relation = ''
)
SELECT DISTINCT rid FROM descendants ORDER BY rid` + limitClause(limit)
	return s.queryStrings(ctx, q, pgx.NamedArgs{
		"resource_type": resourceType,
		"relation":      relation,
		"subject_type":  subjectType,
		"root_ids":      rootIDs,
		"limit":         limit,
	})
}

// limitClause appends a bounded LIMIT when limit is positive (the engine always
// passes MaxLookupResults+1). A non-positive limit is unbounded (defensive).
func limitClause(limit int) string {
	if limit > 0 {
		return "\nLIMIT @limit"
	}
	return ""
}

// queryStrings runs a single-column string SELECT and collects the rows.
func (s *relationshipStore) queryStrings(ctx context.Context, query string, args pgx.NamedArgs) ([]string, error) {
	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	return out, nil
}
