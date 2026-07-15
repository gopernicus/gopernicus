package turso

import (
	"context"
	"sort"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// mutRelRow is the loaded relationship-row projection the per-operation evaluators
// reason over (the resource is fixed by the command scope, so it is not stored).
type mutRelRow struct {
	relation        string
	subjectType     string
	subjectID       string
	subjectRelation string
}

// evaluate dispatches to the per-operation evaluator, applying row changes only
// when the outcome is applied. It returns the domain outcome and whether a change
// was committed (which drives the revision bump). The whole call runs under the
// enclosing BEGIN IMMEDIATE transaction, so read-then-write is atomic — the libSQL
// mirror of the memstore's evaluateLocked under its mutex and the pgx sibling's
// evaluate under FOR UPDATE.
func (m *mutationStore) evaluate(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command) (mutation.Outcome, bool, error) {
	switch cmd.Operation {
	case mutation.OpGrant:
		return m.grant(ctx, tx, cmd)
	case mutation.OpRevoke:
		return m.revoke(ctx, tx, cmd)
	case mutation.OpReplace:
		return m.replace(ctx, tx, cmd)
	case mutation.OpPurge:
		return m.purge(ctx, tx, cmd, false)
	case mutation.OpTeardown:
		return m.purge(ctx, tx, cmd, true)
	case mutation.OpRoleAssign:
		return m.roleAssign(ctx, tx, cmd)
	case mutation.OpRoleUnassign:
		return m.roleUnassign(ctx, tx, cmd)
	default:
		// Command.Validate rejects unknown operations before we reach here.
		return mutation.OutcomeNoChange, false, nil
	}
}

// grant adds the command's relationship rows. A row whose subject already holds the
// SAME relation is a per-row no-op; a subject already holding a DIFFERENT relation
// is a one-relation semantic conflict that rolls the whole command back; a grant
// that would leave a protected resource below its guardian minimum is
// invariant-blocked.
func (m *mutationStore) grant(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command) (mutation.Outcome, bool, error) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	current, err := loadResourceRelationships(ctx, tx, rt, rid)
	if err != nil {
		return "", false, err
	}
	var adds []mutation.RelationshipRow
	for _, row := range cmd.Relationships {
		existing, ok := findSubject(current, row.Subject)
		if ok {
			if existing.relation == row.Relation {
				continue // exact duplicate — no change for this row
			}
			return mutation.OutcomeSemanticConflict, false, nil
		}
		adds = append(adds, row)
	}
	if len(adds) == 0 {
		return mutation.OutcomeNoChange, false, nil
	}
	next := append(append([]mutRelRow(nil), current...), rowsFromCommand(adds)...)
	if !m.relationshipInvariantOK(rt, next) {
		return mutation.OutcomeInvariantBlocked, false, nil
	}
	for _, a := range adds {
		if err := insertRelationship(ctx, tx, rt, rid, a); err != nil {
			return "", false, err
		}
	}
	return mutation.OutcomeApplied, true, nil
}

// revoke removes the command's exact relationship rows. Revoking rows that none
// exist is a committed not_found no-op; a revoke that would drop a protected
// relation below its guardian minimum is invariant-blocked.
func (m *mutationStore) revoke(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command) (mutation.Outcome, bool, error) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	current, err := loadResourceRelationships(ctx, tx, rt, rid)
	if err != nil {
		return "", false, err
	}
	remove := map[relIdentity]bool{}
	for _, row := range cmd.Relationships {
		remove[relIdentityOf(row.Relation, row.Subject)] = true
	}
	matched := 0
	kept := make([]mutRelRow, 0, len(current))
	for _, r := range current {
		if remove[rowIdentity(r)] {
			matched++
			continue
		}
		kept = append(kept, r)
	}
	if matched == 0 {
		return mutation.OutcomeNotFound, false, nil
	}
	if !m.relationshipInvariantOK(rt, kept) {
		return mutation.OutcomeInvariantBlocked, false, nil
	}
	for _, row := range cmd.Relationships {
		if err := deleteRelationship(ctx, tx, rt, rid, row); err != nil {
			return "", false, err
		}
	}
	return mutation.OutcomeApplied, true, nil
}

// replace atomically sets each row's subject to the row's relation on the resource
// — the sanctioned one-relation answer, with no delete/create gap (an in-place
// UPDATE of the relation column). A subject already at the target relation is a
// per-row no-op; a replace-away that removes the last direct guardian is
// invariant-blocked.
func (m *mutationStore) replace(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command) (mutation.Outcome, bool, error) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	current, err := loadResourceRelationships(ctx, tx, rt, rid)
	if err != nil {
		return "", false, err
	}
	next := append([]mutRelRow(nil), current...)
	var updates, inserts []mutation.RelationshipRow
	for _, row := range cmd.Relationships {
		idx := findSubjectIndex(next, row.Subject)
		if idx >= 0 {
			if next[idx].relation == row.Relation {
				continue
			}
			next[idx].relation = row.Relation
			updates = append(updates, row)
			continue
		}
		next = append(next, rowFromCommand(row))
		inserts = append(inserts, row)
	}
	if len(updates) == 0 && len(inserts) == 0 {
		return mutation.OutcomeNoChange, false, nil
	}
	if !m.relationshipInvariantOK(rt, next) {
		return mutation.OutcomeInvariantBlocked, false, nil
	}
	for _, row := range updates {
		if err := replaceRelationship(ctx, tx, rt, rid, row); err != nil {
			return "", false, err
		}
	}
	for _, row := range inserts {
		if err := insertRelationship(ctx, tx, rt, rid, row); err != nil {
			return "", false, err
		}
	}
	return mutation.OutcomeApplied, true, nil
}

// purge removes every relationship on the resource. An ordinary purge
// (teardown=false) still honors guardian invariants, so purging a protected
// resource is invariant-blocked. Teardown (teardown=true) is the one operation
// allowed to zero a protected scope: it bypasses the invariant and also clears the
// resource's scoped role assignments.
func (m *mutationStore) purge(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command, teardown bool) (mutation.Outcome, bool, error) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	current, err := loadResourceRelationships(ctx, tx, rt, rid)
	if err != nil {
		return "", false, err
	}
	removedRel := len(current)

	removedRole := 0
	if teardown {
		removedRole, err = countScopedRoles(ctx, tx, rt, rid)
		if err != nil {
			return "", false, err
		}
	}
	if removedRel == 0 && removedRole == 0 {
		return mutation.OutcomeNoChange, false, nil
	}
	// Blast-radius bound: an ordinary purge that would remove more than the
	// service-sourced ceiling (EvaluationLimits.MaxBatchSize) is invariant-blocked,
	// atomically under the BEGIN IMMEDIATE transaction. Teardown is the trusted,
	// unbounded path.
	if !teardown && cmd.MaxAffectedRows > 0 && removedRel > cmd.MaxAffectedRows {
		return mutation.OutcomeInvariantBlocked, false, nil
	}
	if !teardown && !m.relationshipInvariantOK(rt, nil) {
		return mutation.OutcomeInvariantBlocked, false, nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM iam_relationships WHERE resource_type = ? AND resource_id = ?`,
		rt, rid); err != nil {
		return "", false, err
	}
	if teardown {
		if _, err := tx.Exec(ctx,
			`DELETE FROM iam_roles WHERE resource_type = ? AND resource_id = ?`,
			rt, rid); err != nil {
			return "", false, err
		}
	}
	return mutation.OutcomeApplied, true, nil
}

// roleAssign assigns the command's role rows at the command's scope (a resource
// scope is a scoped assignment; a subject scope is a global assignment).
// Exact-duplicate assignments are a no-op.
func (m *mutationStore) roleAssign(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command) (mutation.Outcome, bool, error) {
	resType, resID := roleScope(cmd.Scope)
	var adds []mutation.RoleRow
	for _, row := range cmd.Roles {
		ok, err := hasExactRoleTx(ctx, tx, row.SubjectType, row.SubjectID, row.Role, resType, resID)
		if err != nil {
			return "", false, err
		}
		if ok {
			continue
		}
		adds = append(adds, row)
	}
	if len(adds) == 0 {
		return mutation.OutcomeNoChange, false, nil
	}
	now := tursodb.FormatTime(time.Now().UTC())
	for _, a := range adds {
		if _, err := tx.Exec(ctx,
			`INSERT INTO iam_roles (subject_type, subject_id, role, resource_type, resource_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(subject_type, subject_id, role, resource_type, resource_id) DO NOTHING`,
			a.SubjectType, a.SubjectID, a.Role, resType, resID, now); err != nil {
			return "", false, err
		}
	}
	return mutation.OutcomeApplied, true, nil
}

// roleUnassign removes the command's exact role rows. Unassigning rows that none
// exist is a committed not_found no-op.
func (m *mutationStore) roleUnassign(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command) (mutation.Outcome, bool, error) {
	resType, resID := roleScope(cmd.Scope)
	matched := int64(0)
	for _, row := range cmd.Roles {
		n, err := tursodb.ExecAffecting(ctx, tx,
			`DELETE FROM iam_roles WHERE subject_type = ? AND subject_id = ? AND role = ? AND resource_type = ? AND resource_id = ?`,
			row.SubjectType, row.SubjectID, row.Role, resType, resID)
		if err != nil {
			return "", false, err
		}
		matched += n
	}
	if matched == 0 {
		return mutation.OutcomeNotFound, false, nil
	}
	return mutation.OutcomeApplied, true, nil
}

// relationshipInvariantOK reports whether the candidate rows satisfy every guardian
// rule for the resource type: each protected relation must retain at least its
// minimum count of DIRECT anchors (concrete subjects with an empty userset
// relation). This is the post-state rule that blocks the loss of the final direct
// guardian AND requires the establishing owner grant before any other command on a
// protected resource.
func (m *mutationStore) relationshipInvariantOK(resourceType string, rows []mutRelRow) bool {
	for _, rule := range m.guardian.Rules {
		if rule.ResourceType != "" && rule.ResourceType != resourceType {
			continue
		}
		min := rule.MinAnchors
		if min < 1 {
			min = 1
		}
		count := 0
		for _, r := range rows {
			if r.relation == rule.Relation && r.subjectRelation == "" {
				count++
			}
		}
		if count < min {
			return false
		}
	}
	return true
}

// =============================================================================
// Row helpers (all run within the caller's BEGIN IMMEDIATE transaction)
// =============================================================================

// loadResourceRelationships loads every relationship row for a resource.
func loadResourceRelationships(ctx context.Context, tx *tursodb.Tx, resourceType, resourceID string) ([]mutRelRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT relation, subject_type, subject_id, subject_relation FROM iam_relationships WHERE resource_type = ? AND resource_id = ?`,
		resourceType, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mutRelRow
	for rows.Next() {
		var r mutRelRow
		if err := rows.Scan(&r.relation, &r.subjectType, &r.subjectID, &r.subjectRelation); err != nil {
			return nil, tursodb.MapError(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// insertRelationship inserts one relationship row, letting the DDL default mint the
// relationship_id (the port is error-only, no RETURNING).
func insertRelationship(ctx context.Context, tx *tursodb.Tx, resourceType, resourceID string, row mutation.RelationshipRow) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, subject_relation, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		resourceType, resourceID, row.Relation, row.Subject.Type, row.Subject.ID, row.Subject.Relation,
		tursodb.FormatTime(time.Now().UTC()))
	return err
}

// replaceRelationship rewrites the relation of the row matching an exact SubjectRef
// in place — no delete/create visibility gap. The unique-subject index guarantees
// at most one such row.
func replaceRelationship(ctx context.Context, tx *tursodb.Tx, resourceType, resourceID string, row mutation.RelationshipRow) error {
	_, err := tx.Exec(ctx,
		`UPDATE iam_relationships SET relation = ?
		 WHERE resource_type = ? AND resource_id = ?
		   AND subject_type = ? AND subject_id = ? AND subject_relation = ?`,
		row.Relation, resourceType, resourceID, row.Subject.Type, row.Subject.ID, row.Subject.Relation)
	return err
}

// deleteRelationship removes one exact relationship row (the revoke identity: the
// relation plus the exact SubjectRef).
func deleteRelationship(ctx context.Context, tx *tursodb.Tx, resourceType, resourceID string, row mutation.RelationshipRow) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM iam_relationships
		 WHERE resource_type = ? AND resource_id = ? AND relation = ?
		   AND subject_type = ? AND subject_id = ? AND subject_relation = ?`,
		resourceType, resourceID, row.Relation, row.Subject.Type, row.Subject.ID, row.Subject.Relation)
	return err
}

// countScopedRoles counts the role assignments scoped to a resource (teardown's
// role sweep set).
func countScopedRoles(ctx context.Context, tx *tursodb.Tx, resourceType, resourceID string) (int, error) {
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM iam_roles WHERE resource_type = ? AND resource_id = ?`,
		resourceType, resourceID).Scan(&n); err != nil {
		return 0, tursodb.MapError(err)
	}
	return n, nil
}

// hasExactRoleTx reports whether an assignment exists at the EXACT scope, read
// through the transaction.
func hasExactRoleTx(ctx context.Context, tx *tursodb.Tx, subjectType, subjectID, role, resourceType, resourceID string) (bool, error) {
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM iam_roles WHERE subject_type = ? AND subject_id = ? AND role = ? AND resource_type = ? AND resource_id = ?)`,
		subjectType, subjectID, role, resourceType, resourceID).Scan(&n); err != nil {
		return false, tursodb.MapError(err)
	}
	return n != 0, nil
}

// roleScope maps a command scope to the role assignment's (resourceType,
// resourceID): a resource scope is a scoped assignment; a subject scope is a global
// assignment (empty pair).
func roleScope(scope mutation.ScopeKey) (string, string) {
	if scope.Kind == mutation.ScopeResource {
		return scope.Type, scope.ID
	}
	return "", ""
}

// relIdentity is a relationship row's identity for exact revoke matching: the
// relation plus the exact SubjectRef (including any userset relation).
type relIdentity struct {
	relation        string
	subjectType     string
	subjectID       string
	subjectRelation string
}

func relIdentityOf(relation string, subj relationship.SubjectRef) relIdentity {
	return relIdentity{relation, subj.Type, subj.ID, subj.Relation}
}

func rowIdentity(r mutRelRow) relIdentity {
	return relIdentity{r.relation, r.subjectType, r.subjectID, r.subjectRelation}
}

// findSubject returns the loaded row matching an exact SubjectRef (type, id, and
// userset relation) regardless of the row's relation — the one-relation arbiter.
func findSubject(rows []mutRelRow, subj relationship.SubjectRef) (mutRelRow, bool) {
	if i := findSubjectIndex(rows, subj); i >= 0 {
		return rows[i], true
	}
	return mutRelRow{}, false
}

func findSubjectIndex(rows []mutRelRow, subj relationship.SubjectRef) int {
	for i, r := range rows {
		if r.subjectType == subj.Type && r.subjectID == subj.ID && r.subjectRelation == subj.Relation {
			return i
		}
	}
	return -1
}

func rowFromCommand(row mutation.RelationshipRow) mutRelRow {
	return mutRelRow{
		relation:        row.Relation,
		subjectType:     row.Subject.Type,
		subjectID:       row.Subject.ID,
		subjectRelation: row.Subject.Relation,
	}
}

func rowsFromCommand(rows []mutation.RelationshipRow) []mutRelRow {
	out := make([]mutRelRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowFromCommand(r))
	}
	return out
}

// =============================================================================
// DecisionView — the dependency-tracking guard reader
// =============================================================================

// decisionView is the dependency-tracking mutation.DecisionView the repository
// supplies to a guard inside the transaction. Every read records the scope key and
// the revision it observed (an absent anchor records 0) so the repository can
// materialize those anchors and re-validate the revisions before commit. Reads run
// through the transaction (*tursodb.Tx), never through the outer Service.
type decisionView struct {
	tx    *tursodb.Tx
	deps  map[string]mutation.Dependency
	order []string
}

var _ mutation.DecisionView = (*decisionView)(nil)

func newDecisionView(tx *tursodb.Tx) *decisionView {
	return &decisionView{tx: tx, deps: map[string]mutation.Dependency{}}
}

// CheckRelation reports whether subjectType:subjectID holds relation on the
// resource named by scope, with group expansion (the reachable CTE), recording the
// scope + revision as a dependency.
func (v *decisionView) CheckRelation(ctx context.Context, scope mutation.ScopeKey, relation, subjectType, subjectID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := v.record(ctx, scope); err != nil {
		return false, err
	}
	q := reachableCTE + `
SELECT EXISTS(
	SELECT 1
	FROM iam_relationships r
	JOIN reachable ON r.subject_type = reachable.atype AND r.subject_id = reachable.aid AND r.subject_relation = reachable.arelation
	WHERE r.resource_type = ? AND r.resource_id = ? AND r.relation = ?
)`
	var n int
	if err := v.tx.QueryRow(ctx, q, subjectType, subjectID, scope.Type, scope.ID, relation).Scan(&n); err != nil {
		return false, tursodb.MapError(err)
	}
	// Record every intermediate resource scope traversed during group expansion:
	// an edge feeding the reachable set lives under its (atype, aid) resource
	// scope, so a concurrent membership revoke bumps that scope's revision. Not
	// recording it would let a guarded mutation commit on a now-false decision.
	if err := v.recordExpansionScopes(ctx, subjectType, subjectID); err != nil {
		return false, err
	}
	return n != 0, nil
}

// recordExpansionScopes records a ScopeResource dependency for every distinct
// (atype, aid) in the subject's reachable set — the intermediate resource scopes
// whose membership edges the CheckRelation expansion traversed. It runs the same
// reachable CTE in the same tx. Recording the seed (the subject itself as a
// resource scope) is a harmless over-record; UNDER-recording is the safety bug.
func (v *decisionView) recordExpansionScopes(ctx context.Context, subjectType, subjectID string) error {
	rows, err := v.tx.Query(ctx, reachableCTE+`
SELECT DISTINCT atype, aid FROM reachable`, subjectType, subjectID)
	if err != nil {
		return tursodb.MapError(err)
	}
	type scopePair struct{ atype, aid string }
	var pairs []scopePair
	for rows.Next() {
		var p scopePair
		if err := rows.Scan(&p.atype, &p.aid); err != nil {
			rows.Close()
			return tursodb.MapError(err)
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return tursodb.MapError(err)
	}
	for _, p := range pairs {
		if err := v.record(ctx, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: p.atype, ID: p.aid}); err != nil {
			return err
		}
	}
	return nil
}

// HasRole reports whether subjectType:subjectID holds role at scope, recording the
// scope + revision as a dependency. It mirrors rolesvc.HasRole's effective
// semantics: an exact-scope match, plus the global fallback (a global assignment
// satisfies a resource-scoped query); a subject-scoped query has no fallback.
func (v *decisionView) HasRole(ctx context.Context, scope mutation.ScopeKey, role, subjectType, subjectID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := v.record(ctx, scope); err != nil {
		return false, err
	}
	var resType, resID string
	if scope.Kind == mutation.ScopeResource {
		resType, resID = scope.Type, scope.ID
	}
	ok, err := hasExactRoleTx(ctx, v.tx, subjectType, subjectID, role, resType, resID)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	if scope.Kind == mutation.ScopeResource {
		// The exact-resource check failed, so the global fallback reads the
		// subject's GLOBAL roles, which serialize into its subject scope. Record
		// that scope regardless of the fallback's result: a concurrent global
		// grant/revoke bumps its revision and must invalidate the decision.
		if err := v.record(ctx, mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: subjectType, ID: subjectID}); err != nil {
			return false, err
		}
		return hasExactRoleTx(ctx, v.tx, subjectType, subjectID, role, "", "")
	}
	return false, nil
}

// Dependencies returns the recorded scopes and revisions sorted by
// ScopeKey.Canonical().
func (v *decisionView) Dependencies() []mutation.Dependency {
	keys := append([]string(nil), v.order...)
	sort.Strings(keys)
	out := make([]mutation.Dependency, 0, len(keys))
	for _, k := range keys {
		out = append(out, v.deps[k])
	}
	return out
}

// record captures a scope dependency once, reading its current (un-materialized)
// revision.
func (v *decisionView) record(ctx context.Context, scope mutation.ScopeKey) error {
	key := scope.Canonical()
	if _, ok := v.deps[key]; ok {
		return nil
	}
	rev, err := scopeRevision(ctx, v.tx, scope)
	if err != nil {
		return err
	}
	v.deps[key] = mutation.Dependency{Scope: scope, Revision: rev}
	v.order = append(v.order, key)
	return nil
}
