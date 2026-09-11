package pgx

import (
	"context"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/jackc/pgx/v5"
)

// mutRelRow is the loaded relationship-row projection the per-operation evaluators
// reason over (the resource is fixed by the command target, so it is not stored).
type mutRelRow struct {
	relation        string
	subjectType     string
	subjectID       string
	subjectRelation string
}

// evaluate dispatches to the per-operation evaluator, applying row changes only
// when the outcome is applied. It returns the domain outcome and whether a change
// was committed (which describes the result). The whole call runs under the
// authorization write lock, so read-then-write is atomic — the SQL mirror of the
// memstore's evaluateLocked under its mutex.
func (m *mutationStore) evaluate(ctx context.Context, tx *writeTx, cmd mutations.Command) (mutations.Outcome, bool, error) {
	switch cmd.Operation {
	case mutations.OpGrant:
		return m.grant(ctx, tx, cmd)
	case mutations.OpRevoke:
		return m.revoke(ctx, tx, cmd)
	case mutations.OpReplace:
		return m.replace(ctx, tx, cmd)
	case mutations.OpPurge:
		return m.purge(ctx, tx, cmd, false)
	case mutations.OpTeardown:
		return m.purge(ctx, tx, cmd, true)
	case mutations.OpRoleAssign:
		return m.roleAssign(ctx, tx, cmd)
	case mutations.OpRoleUnassign:
		return m.roleUnassign(ctx, tx, cmd)
	default:
		// Command.Validate rejects unknown operations before we reach here.
		return mutations.OutcomeNoChange, false, nil
	}
}

// grant adds the command's relationship rows. A row whose subject already holds the
// SAME relation is a per-row no-op; a subject already holding a DIFFERENT relation
// is a one-relation semantic conflict that rolls the whole command back; a grant
// that would leave a protected resource below its guardian minimum is
// invariant-blocked.
func (m *mutationStore) grant(ctx context.Context, tx *writeTx, cmd mutations.Command) (mutations.Outcome, bool, error) {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	current, err := loadResourceRelationships(ctx, tx, m.schema, rt, rid)
	if err != nil {
		return "", false, err
	}
	var adds []mutations.RelationshipRow
	for _, row := range cmd.Relationships {
		existing, ok := findSubject(current, row.Subject)
		if ok {
			if existing.relation == row.Relation {
				continue // exact duplicate — no change for this row
			}
			return mutations.OutcomeSemanticConflict, false, nil
		}
		adds = append(adds, row)
	}
	if len(adds) == 0 {
		return mutations.OutcomeNoChange, false, nil
	}
	next := append(append([]mutRelRow(nil), current...), rowsFromCommand(adds)...)
	if !m.relationshipInvariantOK(rt, next) {
		return mutations.OutcomeInvariantBlocked, false, nil
	}
	for _, a := range adds {
		if err := insertRelationship(ctx, tx, m.schema, rt, rid, a); err != nil {
			return "", false, err
		}
	}
	return mutations.OutcomeApplied, true, nil
}

// revoke removes the command's exact relationship rows. Revoking rows that none
// exist is a committed not_found no-op; a revoke that would drop a protected
// relation below its guardian minimum is invariant-blocked.
func (m *mutationStore) revoke(ctx context.Context, tx *writeTx, cmd mutations.Command) (mutations.Outcome, bool, error) {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	current, err := loadResourceRelationships(ctx, tx, m.schema, rt, rid)
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
		return mutations.OutcomeNotFound, false, nil
	}
	if !m.relationshipInvariantOK(rt, kept) {
		return mutations.OutcomeInvariantBlocked, false, nil
	}
	for _, row := range cmd.Relationships {
		if err := deleteRelationship(ctx, tx, m.schema, rt, rid, row); err != nil {
			return "", false, err
		}
	}
	return mutations.OutcomeApplied, true, nil
}

// replace atomically sets each row's subject to the row's relation on the resource
// — the sanctioned one-relation answer, with no externally visible intermediate state. A subject already at the target relation is a
// per-row no-op; a replace-away that removes the last direct guardian is
// invariant-blocked.
func (m *mutationStore) replace(ctx context.Context, tx *writeTx, cmd mutations.Command) (mutations.Outcome, bool, error) {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	current, err := loadResourceRelationships(ctx, tx, m.schema, rt, rid)
	if err != nil {
		return "", false, err
	}
	next := append([]mutRelRow(nil), current...)
	var updates, inserts []mutations.RelationshipRow
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
		return mutations.OutcomeNoChange, false, nil
	}
	if !m.relationshipInvariantOK(rt, next) {
		return mutations.OutcomeInvariantBlocked, false, nil
	}
	for _, row := range updates {
		if err := replaceRelationship(ctx, tx, m.schema, rt, rid, row); err != nil {
			return "", false, err
		}
	}
	for _, row := range inserts {
		if err := insertRelationship(ctx, tx, m.schema, rt, rid, row); err != nil {
			return "", false, err
		}
	}
	return mutations.OutcomeApplied, true, nil
}

// purge removes every relationship on the resource. An ordinary purge
// (teardown=false) still honors guardian invariants, so purging a protected
// resource is invariant-blocked. Teardown (teardown=true) is the one operation
// allowed to zero a protected scope: it bypasses the invariant and also clears the
// resource's scoped role assignments.
func (m *mutationStore) purge(ctx context.Context, tx *writeTx, cmd mutations.Command, teardown bool) (mutations.Outcome, bool, error) {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	current, err := loadResourceRelationships(ctx, tx, m.schema, rt, rid)
	if err != nil {
		return "", false, err
	}
	removedRel := len(current)

	removedRole := 0
	if teardown {
		removedRole, err = countScopedRoles(ctx, tx, m.schema, rt, rid)
		if err != nil {
			return "", false, err
		}
	}
	if removedRel == 0 && removedRole == 0 {
		return mutations.OutcomeNoChange, false, nil
	}
	// Blast-radius bound: an ordinary purge that would remove more than the
	// service-sourced ceiling (EvaluationLimits.MaxBatchSize) is invariant-blocked,
	// atomically under the scope lock. Teardown is the trusted, unbounded path.
	if !teardown && cmd.MaxAffectedRows > 0 && removedRel > cmd.MaxAffectedRows {
		return mutations.OutcomeInvariantBlocked, false, nil
	}
	if !teardown && !m.relationshipInvariantOK(rt, nil) {
		return mutations.OutcomeInvariantBlocked, false, nil
	}
	if _, err := tx.relationships(ctx, audit.ActionRemoved,
		`DELETE FROM `+m.table("iam_relationships")+` WHERE resource_type = @resource_type AND resource_id = @resource_id`,
		pgx.NamedArgs{"resource_type": rt, "resource_id": rid}); err != nil {
		return "", false, mapMutationError(err)
	}
	if teardown {
		if _, err := tx.roles(ctx, audit.ActionRemoved,
			`DELETE FROM `+m.table("iam_roles")+` WHERE resource_type = @resource_type AND resource_id = @resource_id`,
			pgx.NamedArgs{"resource_type": rt, "resource_id": rid}); err != nil {
			return "", false, mapMutationError(err)
		}
	}
	return mutations.OutcomeApplied, true, nil
}

// roleAssign assigns the command's role rows at the command's scope (a resource
// target is a scoped assignment; a subject scope is a global assignment).
// Exact-duplicate assignments are a no-op.
func (m *mutationStore) roleAssign(ctx context.Context, tx *writeTx, cmd mutations.Command) (mutations.Outcome, bool, error) {
	resType, resID := roleScope(cmd.Target)
	var adds []mutations.RoleRow
	for _, row := range cmd.Roles {
		ok, err := hasExactRole(ctx, tx, m.schema, row.SubjectType, row.SubjectID, row.Role, resType, resID)
		if err != nil {
			return "", false, err
		}
		if ok {
			continue
		}
		adds = append(adds, row)
	}
	if len(adds) == 0 {
		return mutations.OutcomeNoChange, false, nil
	}
	for _, a := range adds {
		if _, err := tx.roles(ctx, audit.ActionAdded,
			`INSERT INTO `+m.table("iam_roles")+` (subject_type, subject_id, role, resource_type, resource_id)
			 VALUES (@subject_type, @subject_id, @role, @resource_type, @resource_id)
			 ON CONFLICT (subject_type, subject_id, role, resource_type, resource_id) DO NOTHING`,
			pgx.NamedArgs{
				"subject_type":  a.SubjectType,
				"subject_id":    a.SubjectID,
				"role":          a.Role,
				"resource_type": resType,
				"resource_id":   resID,
			}); err != nil {
			return "", false, mapMutationError(err)
		}
	}
	return mutations.OutcomeApplied, true, nil
}

// roleUnassign removes the command's exact role rows. Unassigning rows that none
// exist is a committed not_found no-op.
func (m *mutationStore) roleUnassign(ctx context.Context, tx *writeTx, cmd mutations.Command) (mutations.Outcome, bool, error) {
	resType, resID := roleScope(cmd.Target)
	matched := int64(0)
	for _, row := range cmd.Roles {
		n, err := tx.roles(ctx, audit.ActionRemoved,
			`DELETE FROM `+m.table("iam_roles")+` WHERE subject_type = @subject_type AND subject_id = @subject_id AND role = @role AND resource_type = @resource_type AND resource_id = @resource_id`,
			pgx.NamedArgs{
				"subject_type":  row.SubjectType,
				"subject_id":    row.SubjectID,
				"role":          row.Role,
				"resource_type": resType,
				"resource_id":   resID,
			})
		if err != nil {
			return "", false, mapMutationError(err)
		}
		matched += n
	}
	if matched == 0 {
		return mutations.OutcomeNotFound, false, nil
	}
	return mutations.OutcomeApplied, true, nil
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
// Row helpers (all run within the caller's write-locked transaction)
// =============================================================================

// loadResourceRelationships loads every relationship row for a resource.
func loadResourceRelationships(ctx context.Context, tx pgxdb.Querier, schema pgxdb.Schema, resourceType, resourceID string) ([]mutRelRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT relation, subject_type, subject_id, subject_relation FROM `+schema.Table("iam_relationships")+` WHERE resource_type = @resource_type AND resource_id = @resource_id`,
		pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID})
	if err != nil {
		return nil, mapMutationError(err)
	}
	defer rows.Close()
	var out []mutRelRow
	for rows.Next() {
		var r mutRelRow
		if err := rows.Scan(&r.relation, &r.subjectType, &r.subjectID, &r.subjectRelation); err != nil {
			return nil, mapMutationError(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, mapMutationError(err)
	}
	return out, nil
}

// insertRelationship inserts one complete relationship tuple.
func insertRelationship(ctx context.Context, tx *writeTx, schema pgxdb.Schema, resourceType, resourceID string, row mutations.RelationshipRow) error {
	_, err := tx.relationships(ctx, audit.ActionAdded,
		`INSERT INTO `+schema.Table("iam_relationships")+` (resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
		 VALUES (@resource_type, @resource_id, @relation, @subject_type, @subject_id, @subject_relation)`,
		pgx.NamedArgs{
			"resource_type":    resourceType,
			"resource_id":      resourceID,
			"relation":         row.Relation,
			"subject_type":     row.Subject.Type,
			"subject_id":       row.Subject.ID,
			"subject_relation": row.Subject.Relation,
		})
	return mapMutationError(err)
}

// replaceRelationship rewrites the relation of the row matching an exact SubjectRef
// under the transaction lock, recording removal and addition. The unique-subject index guarantees
// at most one such row.
func replaceRelationship(ctx context.Context, tx *writeTx, schema pgxdb.Schema, resourceType, resourceID string, row mutations.RelationshipRow) error {
	_, err := tx.relationships(ctx, audit.ActionRemoved, `DELETE FROM `+schema.Table("iam_relationships")+` WHERE resource_type=@resource_type AND resource_id=@resource_id AND subject_type=@subject_type AND subject_id=@subject_id AND subject_relation=@subject_relation`, pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID, "subject_type": row.Subject.Type, "subject_id": row.Subject.ID, "subject_relation": row.Subject.Relation})
	if err != nil {
		return mapMutationError(err)
	}
	return insertRelationship(ctx, tx, schema, resourceType, resourceID, row)
}

// deleteRelationship removes one exact relationship row (the revoke identity: the
// relation plus the exact SubjectRef).
func deleteRelationship(ctx context.Context, tx *writeTx, schema pgxdb.Schema, resourceType, resourceID string, row mutations.RelationshipRow) error {
	_, err := tx.relationships(ctx, audit.ActionRemoved,
		`DELETE FROM `+schema.Table("iam_relationships")+`
		 WHERE resource_type = @resource_type AND resource_id = @resource_id AND relation = @relation
		   AND subject_type = @subject_type AND subject_id = @subject_id AND subject_relation = @subject_relation`,
		pgx.NamedArgs{
			"resource_type":    resourceType,
			"resource_id":      resourceID,
			"relation":         row.Relation,
			"subject_type":     row.Subject.Type,
			"subject_id":       row.Subject.ID,
			"subject_relation": row.Subject.Relation,
		})
	return mapMutationError(err)
}

// countScopedRoles counts the role assignments scoped to a resource (teardown's
// role sweep set).
func countScopedRoles(ctx context.Context, tx pgxdb.Querier, schema pgxdb.Schema, resourceType, resourceID string) (int, error) {
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM `+schema.Table("iam_roles")+` WHERE resource_type = @resource_type AND resource_id = @resource_id`,
		pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID}).Scan(&n); err != nil {
		return 0, mapMutationError(err)
	}
	return n, nil
}

// hasExactRole reports whether an assignment exists at the EXACT scope.
func hasExactRole(ctx context.Context, tx pgxdb.Querier, schema pgxdb.Schema, subjectType, subjectID, role, resourceType, resourceID string) (bool, error) {
	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM `+schema.Table("iam_roles")+` WHERE subject_type = @subject_type AND subject_id = @subject_id AND role = @role AND resource_type = @resource_type AND resource_id = @resource_id)`,
		pgx.NamedArgs{
			"subject_type":  subjectType,
			"subject_id":    subjectID,
			"role":          role,
			"resource_type": resourceType,
			"resource_id":   resourceID,
		}).Scan(&ok); err != nil {
		return false, mapMutationError(err)
	}
	return ok, nil
}

// roleScope maps a command scope to the role assignment's (resourceType,
// resourceID): a resource target is a scoped assignment; a subject scope is a global
// assignment (empty pair).
func roleScope(scope mutations.Target) (string, string) {
	if scope.Kind == mutations.TargetResource {
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

func relIdentityOf(relation string, subj relationships.SubjectRef) relIdentity {
	return relIdentity{relation, subj.Type, subj.ID, subj.Relation}
}

func rowIdentity(r mutRelRow) relIdentity {
	return relIdentity{r.relation, r.subjectType, r.subjectID, r.subjectRelation}
}

// findSubject returns the loaded row matching an exact SubjectRef (type, id, and
// userset relation) regardless of the row's relation — the one-relation arbiter.
func findSubject(rows []mutRelRow, subj relationships.SubjectRef) (mutRelRow, bool) {
	if i := findSubjectIndex(rows, subj); i >= 0 {
		return rows[i], true
	}
	return mutRelRow{}, false
}

func findSubjectIndex(rows []mutRelRow, subj relationships.SubjectRef) int {
	for i, r := range rows {
		if r.subjectType == subj.Type && r.subjectID == subj.ID && r.subjectRelation == subj.Relation {
			return i
		}
	}
	return -1
}

func rowFromCommand(row mutations.RelationshipRow) mutRelRow {
	return mutRelRow{
		relation:        row.Relation,
		subjectType:     row.Subject.Type,
		subjectID:       row.Subject.ID,
		subjectRelation: row.Subject.Relation,
	}
}

func rowsFromCommand(rows []mutations.RelationshipRow) []mutRelRow {
	out := make([]mutRelRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowFromCommand(r))
	}
	return out
}
