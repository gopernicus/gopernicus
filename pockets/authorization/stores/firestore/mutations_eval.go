package firestore

import (
	"context"
	"fmt"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrMutationWriteLimit reports a command whose document writes would exceed
// Firestore's per-transaction commit limit. Like ErrTupleWriteLimit on the raw
// write path it wraps sdk.ErrInvalidInput: the command is too large for one
// atomic unit and no retry changes that. The mutation path REFUSES rather than
// splits, because a split is a partially applied command — exactly the atomicity
// mutation.MutationRepository promises. SCHEMA.md §8.3 states the ceiling per
// operation.
var ErrMutationWriteLimit = fmt.Errorf("authorization firestore store: a mutation may change at most %d documents in one transaction (Firestore's commit limit): %w", maxWritesPerTransaction, sdk.ErrInvalidInput)

// mutationWriteLimitError names the operation and the size, so the log line says
// what to split rather than only that something was too big.
func mutationWriteLimitError(op mutation.Operation, writes int) error {
	return fmt.Errorf("authorization firestore store: %s would change %d documents: %w", op, writes, ErrMutationWriteLimit)
}

// mutationResult is what the read-and-evaluate phase produces: the domain
// outcome, whether it changes rows (which drives the revision bump), the
// COMPLETE staged write set, and the operation-specific annotation. Nothing here
// has touched the database as a writer — the caller checks the budget and then
// flushes, which is what keeps every read strictly before every write.
type mutationResult struct {
	outcome              mutation.Outcome
	changed              bool
	writes               mutationWrites
	sameRoleGrantRemains bool
}

// tupleReplacement is one subject's in-place move to a new relation: the row as
// it was read, plus the relation it moves to.
type tupleReplacement struct {
	old      relationshipDoc
	relation string
}

// mutationWrites is the staged write set. It is built during the read phase and
// flushed once, through the SAME helpers every other write path in this package
// uses (putTuple/replaceTuple/dropTuple own a tuple's three documents;
// putRole/dropRole own a role grant), so the mutation path cannot store a row
// whose derived keys or claims disagree with itself.
type mutationWrites struct {
	drops     []relationshipDoc
	replaces  []tupleReplacement
	creates   []relationshipDoc
	roleDrops []roleDoc
	roleAdds  []roleDoc
}

// count is the number of DOCUMENT writes the set commits — the unit Firestore's
// per-transaction limit counts, not the number of rows.
func (m mutationWrites) count() int {
	return (len(m.drops)+len(m.creates))*writesPerTuple +
		len(m.replaces)*writesPerReplacedTuple +
		len(m.roleDrops) + len(m.roleAdds)
}

// flush queues every staged write. Deletes precede creates so a purge and a
// grant in the same command could never race their own documents; no read may
// follow any of it.
func (m mutationWrites) flush(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer) error {
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
	return nil
}

// evaluate dispatches to the per-operation evaluator. Each one READS what it
// needs (the resource's rows, the exact role documents, the global-role facts an
// annotation rests on) and returns a decision plus a staged write set; none of
// them writes. That split is the Firestore-shaped form of the memstore's
// evaluateLocked and the SQL siblings' evaluate: same outcomes, same guardian,
// same no-partial-batch rule, but every read hoisted ahead of every write
// because a Firestore transaction refuses the other order.
func (s *mutationStore) evaluate(ctx context.Context, r firestoredb.Reader, cmd mutation.Command) (mutationResult, error) {
	switch cmd.Operation {
	case mutation.OpGrant:
		return s.grant(ctx, r, cmd)
	case mutation.OpRevoke:
		return s.revoke(ctx, r, cmd)
	case mutation.OpReplace:
		return s.replace(ctx, r, cmd)
	case mutation.OpPurge:
		return s.purge(ctx, r, cmd, false)
	case mutation.OpTeardown:
		return s.purge(ctx, r, cmd, true)
	case mutation.OpRoleAssign:
		return s.roleAssign(ctx, r, cmd)
	case mutation.OpRoleUnassign:
		return s.roleUnassign(ctx, r, cmd)
	default:
		// Command.Validate rejects unknown operations before we reach here.
		return mutationResult{outcome: mutation.OutcomeNoChange}, nil
	}
}

// grant adds the command's relationship rows. A row whose subject already holds
// the SAME relation is a per-row no-op; a subject already holding a DIFFERENT
// relation is a one-relation semantic conflict that rolls the WHOLE command back
// (no partial batch); a grant that would leave a protected resource below its
// guardian minimum is invariant-blocked.
func (s *mutationStore) grant(ctx context.Context, r firestoredb.Reader, cmd mutation.Command) (mutationResult, error) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	current, err := resourceRows(ctx, s.db, r, rt, rid)
	if err != nil {
		return mutationResult{}, err
	}

	now := time.Now().UTC()
	var adds []relationshipDoc
	for _, row := range cmd.Relationships {
		existing, ok := findSubject(current, row.Subject)
		if ok {
			if existing.Relation == row.Relation {
				continue // exact duplicate — no change for this row
			}
			return mutationResult{outcome: mutation.OutcomeSemanticConflict}, nil
		}
		adds = append(adds, newMutationRow(rt, rid, row, now))
	}
	if len(adds) == 0 {
		return mutationResult{outcome: mutation.OutcomeNoChange}, nil
	}
	if !s.invariantOK(rt, append(append([]relationshipDoc(nil), current...), adds...)) {
		return mutationResult{outcome: mutation.OutcomeInvariantBlocked}, nil
	}
	if err := assertClaimsFree(ctx, s.db, r, adds); err != nil {
		return mutationResult{}, err
	}
	return mutationResult{outcome: mutation.OutcomeApplied, changed: true, writes: mutationWrites{creates: adds}}, nil
}

// revoke removes the command's exact relationship rows (the relation plus the
// exact SubjectRef). Revoking rows none of which exist is a committed not_found
// no-op; a revoke that would drop a protected relation below its guardian
// minimum is invariant-blocked.
func (s *mutationStore) revoke(ctx context.Context, r firestoredb.Reader, cmd mutation.Command) (mutationResult, error) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
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
		return mutationResult{outcome: mutation.OutcomeNotFound}, nil
	}
	if !s.invariantOK(rt, kept) {
		return mutationResult{outcome: mutation.OutcomeInvariantBlocked}, nil
	}
	return mutationResult{outcome: mutation.OutcomeApplied, changed: true, writes: mutationWrites{drops: matched}}, nil
}

// replace atomically sets each row's subject to the row's relation on the
// resource — the sanctioned one-relation answer, with no delete/create
// visibility gap. A subject already at the target relation is a per-row no-op; a
// replace-away that removes the last direct guardian is invariant-blocked.
//
// The row MOVES documents (the relation is part of the tuple's document id) but
// keeps its relationship_id and created_at, so the change is invisible to the
// listings' order and to the primary-key claim — the same identity the SQL
// siblings' in-place UPDATE preserves. See replaceTuple.
func (s *mutationStore) replace(ctx context.Context, r firestoredb.Reader, cmd mutation.Command) (mutationResult, error) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	current, err := resourceRows(ctx, s.db, r, rt, rid)
	if err != nil {
		return mutationResult{}, err
	}

	now := time.Now().UTC()
	next := append([]relationshipDoc(nil), current...)
	var writes mutationWrites
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
		created := newMutationRow(rt, rid, row, now)
		writes.creates = append(writes.creates, created)
		next = append(next, created)
	}
	if len(writes.replaces) == 0 && len(writes.creates) == 0 {
		return mutationResult{outcome: mutation.OutcomeNoChange}, nil
	}
	if !s.invariantOK(rt, next) {
		return mutationResult{outcome: mutation.OutcomeInvariantBlocked}, nil
	}
	if err := assertClaimsFree(ctx, s.db, r, writes.creates); err != nil {
		return mutationResult{}, err
	}
	return mutationResult{outcome: mutation.OutcomeApplied, changed: true, writes: writes}, nil
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
func (s *mutationStore) purge(ctx context.Context, r firestoredb.Reader, cmd mutation.Command, teardown bool) (mutationResult, error) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
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
		return mutationResult{outcome: mutation.OutcomeNoChange}, nil
	}
	if !teardown {
		// Blast-radius bound: an ordinary purge that would remove more than the
		// service-sourced ceiling (EvaluationLimits.MaxBatchSize) is
		// invariant-blocked. Teardown is the trusted, unbounded path.
		if cmd.MaxAffectedRows > 0 && len(rows) > cmd.MaxAffectedRows {
			return mutationResult{outcome: mutation.OutcomeInvariantBlocked}, nil
		}
		if !s.invariantOK(rt, nil) {
			return mutationResult{outcome: mutation.OutcomeInvariantBlocked}, nil
		}
	}
	return mutationResult{
		outcome: mutation.OutcomeApplied,
		changed: true,
		writes:  mutationWrites{drops: rows, roleDrops: roles},
	}, nil
}

// roleAssign assigns the command's role rows at the command's scope (a resource
// scope is a scoped assignment; a subject scope is a global assignment).
// Exact-duplicate assignments are a no-op that leaves the stored row — and its
// original created_at — untouched.
func (s *mutationStore) roleAssign(ctx context.Context, r firestoredb.Reader, cmd mutation.Command) (mutationResult, error) {
	resourceType, resourceID := roleScopeOf(cmd.Scope)
	refs := newDocRefs(len(cmd.Roles))
	paths := make([]string, len(cmd.Roles))
	for i, row := range cmd.Roles {
		paths[i] = refs.add(roleRef(s.db, row.SubjectType, row.SubjectID, row.Role, resourceType, resourceID))
	}
	if err := refs.read(ctx, r); err != nil {
		return mutationResult{}, err
	}

	now := time.Now().UTC()
	var adds []roleDoc
	claimed := make(map[string]struct{}, len(cmd.Roles))
	for i, row := range cmd.Roles {
		if refs.exists(paths[i]) {
			continue
		}
		if _, dup := claimed[paths[i]]; dup {
			continue
		}
		claimed[paths[i]] = struct{}{}
		adds = append(adds, roleDoc{
			SubjectType:  row.SubjectType,
			SubjectID:    row.SubjectID,
			Role:         row.Role,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			CreatedAt:    now,
		})
	}
	if len(adds) == 0 {
		return mutationResult{outcome: mutation.OutcomeNoChange}, nil
	}
	return mutationResult{outcome: mutation.OutcomeApplied, changed: true, writes: mutationWrites{roleAdds: adds}}, nil
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
func (s *mutationStore) roleUnassign(ctx context.Context, r firestoredb.Reader, cmd mutation.Command) (mutationResult, error) {
	resourceType, resourceID := roleScopeOf(cmd.Scope)
	scoped := cmd.Scope.Kind == mutation.ScopeResource

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
		return mutationResult{outcome: mutation.OutcomeNotFound, sameRoleGrantRemains: remains}, nil
	}
	return mutationResult{
		outcome:              mutation.OutcomeApplied,
		changed:              true,
		writes:               mutationWrites{roleDrops: matched},
		sameRoleGrantRemains: remains,
	}, nil
}

// invariantOK reports whether the candidate post-state rows satisfy every
// guardian rule for the resource type: each protected relation must retain at
// least its minimum count of DIRECT anchors (concrete subjects with an EMPTY
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
// guardian counts its direct anchors in.
func resourceRows(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID string) ([]relationshipDoc, error) {
	return queryRelationships(ctx, r, db.Collection(collectionRelationships).
		Where("resource_key", "==", resourceKey(resourceType, resourceID)))
}

// assertClaimsFree reads the three documents each new row will Create and
// refuses if any is already taken. It runs in the READ phase because putTuple
// cannot discover a taken claim in the write phase — a transaction refuses a
// read after its first write, and Create's AlreadyExists would arrive at commit,
// after the decision was made.
//
// On a consistent store it never fires: the evaluator has just read the
// resource's rows and established that none of these subjects holds a relation
// there, the claims live and die with their row (putTuple/dropTuple), and the
// relationship_id is freshly minted inside this attempt. A hit therefore means
// row/claim drift, which is a store-integrity failure and not a domain outcome —
// so it is loud, and it is sdk.ErrUnavailable rather than a conflict a caller
// would retry forever.
func assertClaimsFree(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, rows []relationshipDoc) error {
	if len(rows) == 0 {
		return nil
	}
	refs := newDocRefs(len(rows) * writesPerTuple)
	paths := make([][3]string, len(rows))
	for i, row := range rows {
		tuple, subject, id := claimRefs(db, row)
		paths[i] = [3]string{refs.add(tuple), refs.add(subject), refs.add(id)}
	}
	if err := refs.read(ctx, r); err != nil {
		return err
	}
	for i, row := range rows {
		for _, path := range paths[i] {
			if refs.exists(path) {
				return fmt.Errorf("authorization firestore store: %s:%s#%s <- %s:%s already claims %s while no row was read for it (row/claim drift): %w",
					row.ResourceType, row.ResourceID, row.Relation, row.SubjectType, row.SubjectID, path, sdk.ErrUnavailable)
			}
		}
	}
	return nil
}

// newMutationRow builds the document for one new relationship row of a command.
// The relationship_id is minted INSIDE the transaction callback, so a retried
// attempt mints a fresh one rather than reusing an id a losing attempt claimed.
func newMutationRow(resourceType, resourceID string, row mutation.RelationshipRow, now time.Time) relationshipDoc {
	return relationshipDoc{
		RelationshipID:  firestoredb.NewID(),
		ResourceType:    resourceType,
		ResourceID:      resourceID,
		Relation:        row.Relation,
		SubjectType:     row.Subject.Type,
		SubjectID:       row.Subject.ID,
		SubjectRelation: row.Subject.Relation,
		CreatedAt:       now,
	}
}

// roleScopeOf maps a command scope to the role assignment's (resourceType,
// resourceID): a resource scope is a scoped assignment; a subject scope is a
// global assignment (the empty pair, stored as empty strings).
func roleScopeOf(scope mutation.ScopeKey) (string, string) {
	if scope.Kind == mutation.ScopeResource {
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

func relIdentityOf(relation string, subject relationship.SubjectRef) relIdentity {
	return relIdentity{relation, subject.Type, subject.ID, subject.Relation}
}

func rowIdentity(row relationshipDoc) relIdentity {
	return relIdentity{row.Relation, row.SubjectType, row.SubjectID, row.SubjectRelation}
}

// findSubject returns the row matching an exact SubjectRef (type, id, and
// userset relation) REGARDLESS of its relation — the one-relation arbiter.
func findSubject(rows []relationshipDoc, subject relationship.SubjectRef) (relationshipDoc, bool) {
	if i := findSubjectIndex(rows, subject); i >= 0 {
		return rows[i], true
	}
	return relationshipDoc{}, false
}

func findSubjectIndex(rows []relationshipDoc, subject relationship.SubjectRef) int {
	for i, row := range rows {
		if row.SubjectType == subject.Type && row.SubjectID == subject.ID && row.SubjectRelation == subject.Relation {
			return i
		}
	}
	return -1
}
