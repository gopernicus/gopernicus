package firestore

import (
	"context"
	"fmt"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// mutationResult owns the evaluated outcome, actual fact writes and role annotation.
type mutationResult struct {
	outcome              mutations.Outcome
	writes               factWrites
	sameRoleGrantRemains bool
}

// tupleReplacement is one subject's in-place move to a new relation: the row as
// it was read, plus the relation it moves to.
type tupleReplacement struct {
	old      relationshipDoc
	relation string
}

// factWrites is the staged write set. It is built during the read phase and
// flushed once, through the SAME helpers every other write path in this package
// uses (putTuple/replaceTuple/dropTuple own a tuple's two documents;
// putRole/dropRole own a role grant), so the mutation path cannot store a row
// whose derived keys or claims disagree with itself.
type factWrites struct {
	drops     []relationshipDoc
	replaces  []tupleReplacement
	creates   []relationshipDoc
	roleDrops []roleDoc
	roleAdds  []roleDoc
}

// flush queues every staged write. Deletes precede creates so a purge and a
// grant in the same command could never race their own documents; no read may
// follow any of it.
func (m factWrites) flush(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, enabled bool) error {
	records, err := m.auditRecords(ctx, enabled)
	if err != nil {
		return err
	}
	for _, row := range m.drops {
		if err := dropTuple(ctx, db, w, row); err != nil {
			return err
		}
	}
	for _, rep := range m.replaces {
		if err := replaceTuple(ctx, db, w, rep.old, rep.relation); err != nil {
			return err
		}
	}
	for _, row := range m.creates {
		if err := putTuple(ctx, db, w, row); err != nil {
			return err
		}
	}
	for _, row := range m.roleDrops {
		if err := dropRole(ctx, db, w, row.SubjectType, row.SubjectID, row.Role, row.ResourceType, row.ResourceID); err != nil {
			return err
		}
	}
	for _, row := range m.roleAdds {
		if err := putRole(ctx, db, w, row); err != nil {
			return err
		}
	}
	return appendAudit(ctx, db, w, records)
}

// evaluate dispatches to the per-operation evaluator. Each one READS what it
// needs (the resource's rows, the exact role documents, the global-role facts an
// annotation rests on) and returns a decision plus a staged write set; none of
// them writes. That split is the Firestore-shaped form of the memstore's
// evaluateLocked and the SQL siblings' evaluate: same outcomes, same guardian,
// same no-partial-batch rule, but every read hoisted ahead of every write
// because a Firestore transaction refuses the other order.
func (s *mutationStore) evaluate(ctx context.Context, r firestoredb.Reader, cmd mutations.Command) (mutationResult, error) {
	switch cmd.Operation {
	case mutations.OpGrant:
		return s.grant(ctx, r, cmd)
	case mutations.OpRevoke:
		return s.revoke(ctx, r, cmd)
	case mutations.OpReplace:
		return s.replace(ctx, r, cmd)
	case mutations.OpPurge:
		return s.purge(ctx, r, cmd, false)
	case mutations.OpTeardown:
		return s.purge(ctx, r, cmd, true)
	case mutations.OpRoleAssign:
		return s.roleAssign(ctx, r, cmd)
	case mutations.OpRoleUnassign:
		return s.roleUnassign(ctx, r, cmd)
	default:
		// Command.Validate rejects unknown operations before we reach here.
		return mutationResult{outcome: mutations.OutcomeNoChange}, nil
	}
}

// grant adds the command's relationship rows. A row whose subject already holds
// the SAME relation is a per-row no-op; a subject already holding a DIFFERENT
// relation is a one-relation semantic conflict that rolls the WHOLE command back
// (no partial batch); a grant that would leave a protected resource below its
// guardian minimum is invariant-blocked.
//
// DEPENDS ON Command.Validate (mutation.go, the relationship branch), which
// rejects a command whose rows name one subject twice — "subject %s:%s#%s
// appears in more than one relationship row of one command". So this loop can
// decide each row against `current` alone: a second row for the same subject
// would otherwise be judged against a stale view and stage two Creates on one
// document id.
func (s *mutationStore) grant(ctx context.Context, r firestoredb.Reader, cmd mutations.Command) (mutationResult, error) {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	current, err := resourceRows(ctx, s.db, r, rt, rid)
	if err != nil {
		return mutationResult{}, err
	}

	var adds []relationshipDoc
	for _, row := range cmd.Relationships {
		existing, ok := findSubject(current, row.Subject)
		if ok {
			if existing.Relation == row.Relation {
				continue // exact duplicate — no change for this row
			}
			return mutationResult{outcome: mutations.OutcomeSemanticConflict}, nil
		}
		adds = append(adds, newMutationRow(rt, rid, row))
	}
	if len(adds) == 0 {
		return mutationResult{outcome: mutations.OutcomeNoChange}, nil
	}
	if !s.invariantOK(rt, append(append([]relationshipDoc(nil), current...), adds...)) {
		return mutationResult{outcome: mutations.OutcomeInvariantBlocked}, nil
	}
	if err := assertClaimsFree(ctx, s.db, r, adds, nil); err != nil {
		return mutationResult{}, err
	}
	return mutationResult{outcome: mutations.OutcomeApplied, writes: factWrites{creates: adds}}, nil
}

// revoke removes the command's exact relationship rows (the relation plus the
// exact SubjectRef). Revoking rows none of which exist is a committed not_found
// no-op; a revoke that would drop a protected relation below its guardian
// minimum is invariant-blocked.
func (s *mutationStore) revoke(ctx context.Context, r firestoredb.Reader, cmd mutations.Command) (mutationResult, error) {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	current, err := resourceRows(ctx, s.db, r, rt, rid)
	if err != nil {
		return mutationResult{}, err
	}

	remove := make(map[relIdentity]bool, len(cmd.Relationships))
	for _, row := range cmd.Relationships {
		remove[relIdentityOf(row.Relation, row.Subject)] = true
	}
	var matched []relationshipDoc
	kept := make([]relationshipDoc, 0, len(current))
	for _, row := range current {
		if remove[rowIdentity(row)] {
			matched = append(matched, row)
			continue
		}
		kept = append(kept, row)
	}
	if len(matched) == 0 {
		return mutationResult{outcome: mutations.OutcomeNotFound}, nil
	}
	if !s.invariantOK(rt, kept) {
		return mutationResult{outcome: mutations.OutcomeInvariantBlocked}, nil
	}
	return mutationResult{outcome: mutations.OutcomeApplied, writes: factWrites{drops: matched}}, nil
}

// replace atomically sets each row's subject to the row's relation on the
// resource — the sanctioned one-relation answer, with no delete/create
// visibility gap. A subject already at the target relation is a per-row no-op; a
// replace-away that removes the last direct guardian is invariant-blocked.
//
// The relation is part of natural identity, so replacement moves the document
// and updates its natural sort position. See replaceTuple.
func (s *mutationStore) replace(ctx context.Context, r firestoredb.Reader, cmd mutations.Command) (mutationResult, error) {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	current, err := resourceRows(ctx, s.db, r, rt, rid)
	if err != nil {
		return mutationResult{}, err
	}

	next := append([]relationshipDoc(nil), current...)
	var writes factWrites
	for _, row := range cmd.Relationships {
		idx := findSubjectIndex(next, row.Subject)
		if idx >= 0 {
			if next[idx].Relation == row.Relation {
				continue
			}
			writes.replaces = append(writes.replaces, tupleReplacement{old: next[idx], relation: row.Relation})
			next[idx].Relation = row.Relation
			continue
		}
		created := newMutationRow(rt, rid, row)
		writes.creates = append(writes.creates, created)
		next = append(next, created)
	}
	if len(writes.replaces) == 0 && len(writes.creates) == 0 {
		return mutationResult{outcome: mutations.OutcomeNoChange}, nil
	}
	if !s.invariantOK(rt, next) {
		return mutationResult{outcome: mutations.OutcomeInvariantBlocked}, nil
	}
	if err := assertClaimsFree(ctx, s.db, r, writes.creates, writes.replaces); err != nil {
		return mutationResult{}, err
	}
	return mutationResult{outcome: mutations.OutcomeApplied, writes: writes}, nil
}

// purge removes every relationship on the resource. An ordinary purge
// (teardown=false) still honors guardian invariants, so purging a protected
// resource is invariant-blocked and cannot silently orphan it. Teardown is the
// one operation allowed to zero a protected scope: it bypasses the invariant and
// also clears the resource's scoped role assignments.
//
// The rows it removes are the rows it READ: a Firestore transaction has no count
// aggregation, so the blast-radius bound and the teardown sweep are both
// computed from the read set rather than from a COUNT(*).
func (s *mutationStore) purge(ctx context.Context, r firestoredb.Reader, cmd mutations.Command, teardown bool) (mutationResult, error) {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	rows, err := resourceRows(ctx, s.db, r, rt, rid)
	if err != nil {
		return mutationResult{}, err
	}
	var roles []roleDoc
	if teardown {
		roles, err = queryRoles(ctx, r, scopedRolesQuery(s.db, rt, rid))
		if err != nil {
			return mutationResult{}, err
		}
	}
	if len(rows) == 0 && len(roles) == 0 {
		return mutationResult{outcome: mutations.OutcomeNoChange}, nil
	}
	if !teardown {
		// Blast-radius bound: an ordinary purge that would remove more than the
		// service-sourced ceiling (EvaluationLimits.MaxBatchSize) is
		// invariant-blocked. Teardown is the trusted, unbounded path.
		if cmd.MaxAffectedRows > 0 && len(rows) > cmd.MaxAffectedRows {
			return mutationResult{outcome: mutations.OutcomeInvariantBlocked}, nil
		}
		if !s.invariantOK(rt, nil) {
			return mutationResult{outcome: mutations.OutcomeInvariantBlocked}, nil
		}
	}
	return mutationResult{
		outcome: mutations.OutcomeApplied,
		writes:  factWrites{drops: rows, roleDrops: roles},
	}, nil
}

// roleAssign assigns the command's role rows at the command's scope (a resource
// scope is a scoped assignment; a subject scope is a global assignment).
// Exact-duplicate assignments leave the stored row untouched.
//
// DEPENDS ON Command.Validate (mutation.go, the role branch), which rejects a
// command carrying the same (subject_type, subject_id, role) row twice before it
// ever reaches a store — "role row %s:%s/%s is duplicated in one command". That
// is why this loop needs no in-batch de-duplication: two rows here cannot map to
// one document id, since the id is exactly that triple plus the command's one
// scope. If Validate ever relaxes it, this loop is where the duplicate would
// become two Creates on one document.
func (s *mutationStore) roleAssign(ctx context.Context, r firestoredb.Reader, cmd mutations.Command) (mutationResult, error) {
	resourceType, resourceID := roleScopeOf(cmd.Target)
	refs := newDocRefs(len(cmd.Roles))
	paths := make([]string, len(cmd.Roles))
	for i, row := range cmd.Roles {
		paths[i] = refs.add(roleRef(s.db, row.SubjectType, row.SubjectID, row.Role, resourceType, resourceID))
	}
	if err := refs.read(ctx, r); err != nil {
		return mutationResult{}, err
	}

	var adds []roleDoc
	for i, row := range cmd.Roles {
		if refs.exists(paths[i]) {
			continue
		}
		adds = append(adds, roleDoc{
			SubjectType:  row.SubjectType,
			SubjectID:    row.SubjectID,
			Role:         row.Role,
			ResourceType: resourceType,
			ResourceID:   resourceID,
		})
	}
	if len(adds) == 0 {
		return mutationResult{outcome: mutations.OutcomeNoChange}, nil
	}
	return mutationResult{outcome: mutations.OutcomeApplied, writes: factWrites{roleAdds: adds}}, nil
}

// roleUnassign removes the command's exact role rows. Unassigning rows none of
// which exist is a committed not_found no-op.
//
// It also resolves same_role_grant_remains, whatever the outcome: for a SCOPED
// unassign, whether a GLOBAL ("", "") assignment for one of the command's exact
// (subject, role) rows still satisfies the scoped HasRole fallback. Those global
// documents are read HERE, in the read phase, because the write phase cannot
// read — and the scoped removal cannot touch them, so reading them before the
// removal is queued gives exactly the answer the memstore computes after it.
func (s *mutationStore) roleUnassign(ctx context.Context, r firestoredb.Reader, cmd mutations.Command) (mutationResult, error) {
	resourceType, resourceID := roleScopeOf(cmd.Target)
	scoped := cmd.Target.Kind == mutations.TargetResource

	refs := newDocRefs(len(cmd.Roles) * 2)
	paths := make([]string, len(cmd.Roles))
	global := make([]string, len(cmd.Roles))
	for i, row := range cmd.Roles {
		paths[i] = refs.add(roleRef(s.db, row.SubjectType, row.SubjectID, row.Role, resourceType, resourceID))
		if scoped {
			global[i] = refs.add(roleRef(s.db, row.SubjectType, row.SubjectID, row.Role, "", ""))
		}
	}
	if err := refs.read(ctx, r); err != nil {
		return mutationResult{}, err
	}

	remains := false
	var matched []roleDoc
	for i, row := range cmd.Roles {
		if scoped && refs.exists(global[i]) {
			remains = true
		}
		if !refs.exists(paths[i]) {
			continue
		}
		matched = append(matched, roleDoc{
			SubjectType:  row.SubjectType,
			SubjectID:    row.SubjectID,
			Role:         row.Role,
			ResourceType: resourceType,
			ResourceID:   resourceID,
		})
	}
	if len(matched) == 0 {
		return mutationResult{outcome: mutations.OutcomeNotFound, sameRoleGrantRemains: remains}, nil
	}
	return mutationResult{
		outcome:              mutations.OutcomeApplied,
		writes:               factWrites{roleDrops: matched},
		sameRoleGrantRemains: remains,
	}, nil
}

// invariantOK reports whether the candidate post-state rows satisfy every
// guardian rule for the resource type: each protected relation must retain at
// least its minimum count of DIRECT guardians (concrete subjects with an EMPTY
// userset relation, so a `group#member` owner never masks the loss of the final
// direct guardian). It is the post-state rule that both blocks that loss AND
// requires the establishing owner grant before any other command on a protected
// resource. rows are already scoped to one resource.
func (s *mutationStore) invariantOK(resourceType string, rows []relationshipDoc) bool {
	for _, rule := range s.guardian.Rules {
		if rule.ResourceType != "" && rule.ResourceType != resourceType {
			continue
		}
		minimum := rule.MinAnchors
		if minimum < 1 {
			minimum = 1
		}
		count := 0
		for _, row := range rows {
			if row.Relation == rule.Relation && row.SubjectRelation == "" {
				count++
			}
		}
		if count < minimum {
			return false
		}
	}
	return true
}

// resourceRows reads every relationship row of one resource through r — the
// population every relationship evaluator reasons over, and the population the
// guardian counts its direct guardians in.
func resourceRows(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID string) ([]relationshipDoc, error) {
	return queryRelationships(ctx, r, db.Collection(collectionRelationships).
		Where("resource_key", "==", resourceKey(resourceType, resourceID)))
}

// assertClaimsFree checks the row and claim before a create, or the destination
// row before a replacement. Replacement updates its existing subject claim in
// place. A collision after the evaluator read the resource is store drift,
// reported as unavailable rather than endlessly retried contention.
func assertClaimsFree(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, rows []relationshipDoc, replacements []tupleReplacement) error {
	if len(rows) == 0 && len(replacements) == 0 {
		return nil
	}
	refs := newDocRefs(len(rows)*writesPerTuple + len(replacements))
	paths := make([][2]string, len(rows))
	for i, row := range rows {
		tuple, subject := claimRefs(db, row)
		paths[i] = [2]string{refs.add(tuple), refs.add(subject)}
	}
	moved := make([]relationshipDoc, len(replacements))
	movedPaths := make([]string, len(replacements))
	for i, rep := range replacements {
		next := rep.old
		next.Relation = rep.relation
		moved[i] = next
		tuple, _ := claimRefs(db, next)
		movedPaths[i] = refs.add(tuple)
	}
	if err := refs.read(ctx, r); err != nil {
		return err
	}
	for i, row := range rows {
		for _, path := range paths[i] {
			if refs.exists(path) {
				return claimDriftError(row, path)
			}
		}
	}
	for i, row := range moved {
		if refs.exists(movedPaths[i]) {
			return claimDriftError(row, movedPaths[i])
		}
	}
	return nil
}

// claimDriftError names the row whose document was already taken and the
// document that took it.
func claimDriftError(row relationshipDoc, path string) error {
	return fmt.Errorf("authorization firestore store: %s:%s#%s <- %s:%s already claims %s while no row was read for it (row/claim drift): %w",
		row.ResourceType, row.ResourceID, row.Relation, row.SubjectType, row.SubjectID, path, sdk.ErrUnavailable)
}

// newMutationRow builds the natural tuple for a command relationship row.
func newMutationRow(resourceType, resourceID string, row mutations.RelationshipRow) relationshipDoc {
	return relationshipDoc{
		ResourceType:    resourceType,
		ResourceID:      resourceID,
		Relation:        row.Relation,
		SubjectType:     row.Subject.Type,
		SubjectID:       row.Subject.ID,
		SubjectRelation: row.Subject.Relation,
	}
}

// roleScopeOf maps a command scope to the role assignment's (resourceType,
// resourceID): a resource scope is a scoped assignment; a subject scope is a
// global assignment (the empty pair, stored as empty strings).
func roleScopeOf(scope mutations.Target) (string, string) {
	if scope.Kind == mutations.TargetResource {
		return scope.Type, scope.ID
	}
	return "", ""
}

// relIdentity is a relationship row's identity for exact revoke matching: the
// relation plus the exact SubjectRef, userset relation included.
type relIdentity struct {
	relation        string
	subjectType     string
	subjectID       string
	subjectRelation string
}

func relIdentityOf(relation string, subject relationships.SubjectRef) relIdentity {
	return relIdentity{relation, subject.Type, subject.ID, subject.Relation}
}

func rowIdentity(row relationshipDoc) relIdentity {
	return relIdentity{row.Relation, row.SubjectType, row.SubjectID, row.SubjectRelation}
}

// findSubject returns the row matching an exact SubjectRef (type, id, and
// userset relation) REGARDLESS of its relation — the one-relation arbiter.
func findSubject(rows []relationshipDoc, subject relationships.SubjectRef) (relationshipDoc, bool) {
	if i := findSubjectIndex(rows, subject); i >= 0 {
		return rows[i], true
	}
	return relationshipDoc{}, false
}

func findSubjectIndex(rows []relationshipDoc, subject relationships.SubjectRef) int {
	for i, row := range rows {
		if row.SubjectType == subject.Type && row.SubjectID == subject.ID && row.SubjectRelation == subject.Relation {
			return i
		}
	}
	return -1
}
