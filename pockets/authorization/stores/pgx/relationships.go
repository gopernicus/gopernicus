package pgx

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
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
	SELECT @subject_type::text COLLATE "C", @subject_id::text COLLATE "C", ''::text COLLATE "C"
	UNION
	SELECT r.resource_type, r.resource_id, r.relation
	FROM ` + resourceTupleTable(schema) + ` r
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

// relationshipStore reads through the ambient querier and routes all writes
// through a join-or-own transaction so facts and optional history commit together.
type relationshipStore struct {
	integrity    mutations.IntegrityPolicy
	tupleBinding string
	readQuerier  pgxdb.Querier
	audit        bool
	model        *relationships.ReadModel
	db           *pgxdb.DB
	schema       pgxdb.Schema
}

func newRelationshipStore(db *pgxdb.DB, cfg config) *relationshipStore {
	return &relationshipStore{db: db, tupleBinding: cfg.tupleBinding, schema: cfg.schema, audit: cfg.audit, integrity: cfg.integrity}
}

// table renders name under the store's schema — the one chokepoint every
// statement on this store goes through.
func resourceTupleTable(schema pgxdb.Schema) string {
	return "(SELECT * FROM " + schema.Table("iam_tuples") + " WHERE scope_kind=2)"
}
func (s *relationshipStore) resourceTable() string { return resourceTupleTable(s.schema) }

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
	FROM ` + s.resourceTable() + ` r
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
		FROM ` + s.resourceTable() + ` r
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

// rowQuerier is the Query seam shared by pools and transaction-bound graph readers.
type rowQuerier interface {
	Query(ctx context.Context, query string, args ...any) (pgx.Rows, error)
}

// GetRelationTargets returns the subjects holding a relation on a resource. An
// empty subject_relation reads back as "" (a concrete subject); a non-empty one
// as the exact userset relation.
func (s *relationshipStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	return relationTargets(ctx, s.reader(ctx), s.schema, resourceType, resourceID, relation)
}

// relationTargets reads through the supplied raw or model-filtered querier.
func relationTargets(ctx context.Context, q rowQuerier, schema pgxdb.Schema, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	stmt := `SELECT subject_type, subject_id, subject_relation FROM ` + resourceTupleTable(schema) + ` WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation`
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
// tuple with the same type/id does not satisfy a concrete probe).
func (s *relationshipStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	view, err := s.rawView(ctx)
	if err != nil {
		return false, err
	}
	return view.Service.CheckRelationExists(ctx, resourceType, resourceID, relation, subjectType, subjectID)
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
FROM ` + s.resourceTable() + ` r
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
	FROM ` + s.resourceTable() + ` r
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
// canonical lookup index (resource_type, relation, resource_id COLLATE "C") serves both the
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
FROM ` + s.resourceTable() + ` r
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
	FROM ` + s.resourceTable() + ` r
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

	q := `SELECT resource_id, subject_type, subject_id, subject_relation FROM ` + s.resourceTable() + `
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

func (s *relationshipStore) tuples() *tupleStore {
	return newTupleStore(s.db, config{schema: s.schema, audit: s.audit, integrity: s.integrity, tupleBinding: s.tupleBinding})
}
func (s *relationshipStore) CreateRelationships(ctx context.Context, in []relationships.CreateRelationship) error {
	changes := tuples.Changes{Add: make([]tuples.Tuple, len(in))}
	for i, row := range in {
		changes.Add[i] = row.Tuple()
	}
	return s.tuples().ApplyTuples(ctx, changes)
}
func (s *relationshipStore) SetRelationTargets(ctx context.Context, rt, rid, relation string, in []relationships.CreateRelationship) error {
	subjects := make([]tuples.SubjectRef, len(in))
	for i, row := range in {
		if err := row.Validate(); err != nil {
			return err
		}
		if row.ResourceType != rt || row.ResourceID != rid || row.Relation != relation {
			return sdk.ErrInvalidInput
		}
		subjects[i] = row.Subject()
	}
	return s.tuples().ReconcileTuples(ctx, tuples.On(rt, rid), relation, subjects)
}
func (s *relationshipStore) DeleteResourceRelationships(ctx context.Context, rt, rid string) error {
	return s.tuples().DeleteScope(ctx, tuples.On(rt, rid))
}
func (s *relationshipStore) DeleteRelationshipTarget(ctx context.Context, rt, rid, relation string, subject relationships.SubjectRef) error {
	return s.tuples().ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{{Scope: tuples.On(rt, rid), Relation: relation, Subject: subject}}})
}
func (s *relationshipStore) DeleteRelationship(ctx context.Context, rt, rid, relation, st, sid string) error {
	return s.DeleteRelationshipTarget(ctx, rt, rid, relation, tuples.SubjectRef{Type: st, ID: sid})
}
func (s *relationshipStore) DeleteByResourceAndSubject(ctx context.Context, rt, rid, st, sid string) error {
	scope := tuples.On(rt, rid)
	subject := tuples.SubjectRef{Type: st, ID: sid}
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := subject.Validate(); err != nil {
		return err
	}
	return s.write(ctx, func(tx *writeTx) error {
		tx.touch(scope)
		a := &tupleArgs{}
		where := "scope_kind=2 AND resource_type=" + a.ref(rt) + " AND resource_id=" + a.ref(rid) + " AND subject_type=" + a.ref(st) + " AND subject_id=" + a.ref(sid)
		_, err := tx.tuples(ctx, audit.ActionRemoved, "DELETE FROM "+s.tuples().table()+" WHERE "+where, a.params()...)
		return err
	})
}

// CountByResourceAndRelation counts stored facts, including userset references,
// without graph expansion or model filtering.
func (s *relationshipStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	view, err := s.rawView(ctx)
	if err != nil {
		return 0, err
	}
	return view.Service.CountByResourceAndRelation(ctx, resourceType, resourceID, relation)
}

// ListRelationshipsBySubject pages complete tuple identities in byte order.
func (s *relationshipStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationships.SubjectRelationshipFilter, req list.Request) (list.Page[relationships.SubjectRelationship], error) {
	view, err := s.rawView(ctx)
	if err != nil {
		return list.Page[relationships.SubjectRelationship]{}, err
	}
	return view.Service.ListRelationshipsBySubject(ctx, subjectType, subjectID, filter, req)
}

// ListRelationshipsByResource pages complete tuple identities in byte order.
func (s *relationshipStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationships.ResourceRelationshipFilter, req list.Request) (list.Page[relationships.ResourceRelationship], error) {
	view, err := s.rawView(ctx)
	if err != nil {
		return list.Page[relationships.ResourceRelationship]{}, err
	}
	return view.Service.ListRelationshipsByResource(ctx, resourceType, resourceID, filter, req)
}

// lookupResourceIDsSQL renders the direct-relation keyset lookup and its args.
// It is split from the method so the EXPLAIN test can plan the exact statement
// the store runs.
//
// Lookups pin byte ordering in their projection, cursor comparison and ORDER BY.
// The canonical columns and recursive anchors also use C collation, so compound
// queries remain compatible under non-C database defaults. SELECT DISTINCT must
// project the same collated expression used by ORDER BY.
func (s *relationshipStore) lookupResourceIDsSQL(resourceType string, relations []string, subjectType, subjectID, after string, limit int) (string, pgx.NamedArgs) {
	q := reachableCTE(s.schema) + `
SELECT DISTINCT r.resource_id COLLATE "C" AS resource_id
FROM ` + s.resourceTable() + ` r
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
	q := `SELECT DISTINCT resource_id COLLATE "C" AS resource_id FROM ` + s.resourceTable() + `
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
	FROM ` + s.resourceTable() + ` r
	WHERE r.resource_type = @resource_type AND r.relation = ANY(@relations::text[]) AND r.subject_type = @subject_type AND r.subject_relation = '' AND r.subject_id = ANY(@root_ids::text[])
	UNION
	SELECT r.resource_id
	FROM ` + s.resourceTable() + ` r
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
	return runWrite(ctx, s.db, config{audit: s.audit, integrity: s.integrity, schema: s.schema}, fn)
}

// rawView shares canonical projections while preserving the raw facade's ambient
// joining contract. Standalone lists own a snapshot; an ambient host controls its
// transaction's isolation. Decision snapshots retain their stricter requirements.
func (s *relationshipStore) rawView(ctx context.Context) (relationships.Components, error) {
	facts := s.tuples()
	if tx, ok := pgxdb.TxFromContext(ctx); ok {
		facts.readQuerier = tx
	}
	return relationships.NewService(facts)
}
