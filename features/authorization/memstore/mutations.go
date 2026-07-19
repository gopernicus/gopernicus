package memstore

import (
	"context"
	"sort"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
)

// Store bundles the three in-core stores — [Relationships], [Roles], and
// [Mutations] — over ONE shared, mutex-guarded [state]. A command applied through
// Mutations is therefore immediately visible to Relationships/Roles reads (and
// vice versa), and Apply serializes against every raw read/write under the same
// lock. This is the reference shape the SQL stores mirror operationally: shared
// tables plus a write-serializing transaction (AZ3-2.3/2.4). Wire all three into
// authorization.Repositories from one Store so the atomic write path shares state
// with the read path.
type Store struct {
	rel *Relationships
	rol *Roles
	mut *Mutations
}

// Option configures a [Store] at construction.
type Option func(*storeConfig)

type storeConfig struct {
	guardian mutation.GuardianPolicy
}

// WithGuardianPolicy overrides the default guardian invariant (owner protected on
// every resource type). Supply an empty policy to declare no invariant, or a
// narrower rule set to protect specific resource types.
func WithGuardianPolicy(p mutation.GuardianPolicy) Option {
	return func(c *storeConfig) { c.guardian = p }
}

// New builds a bundle whose relationship, role, and mutation stores share one lock
// and one snapshot. The mutation repository defaults to the ratified guardian
// policy (mutation.DefaultGuardianPolicy — owner protected, minimum one direct
// anchor) unless [WithGuardianPolicy] overrides it.
func New(opts ...Option) *Store {
	cfg := storeConfig{guardian: mutation.DefaultGuardianPolicy()}
	for _, o := range opts {
		o(&cfg)
	}
	st := newState()
	s := &Store{rel: &Relationships{st: st}, rol: &Roles{st: st}}
	s.mut = &Mutations{st: st, rels: s.rel, roles: s.rol, guardian: cfg.guardian}
	return s
}

// Relationships returns the shared-state relationship.Storer.
func (s *Store) Relationships() *Relationships { return s.rel }

// Roles returns the shared-state role.Storer.
func (s *Store) Roles() *Roles { return s.rol }

// Mutations returns the shared-state atomic mutation.MutationRepository.
func (s *Store) Mutations() *Mutations { return s.mut }

// Mutations is the in-core reference mutation.MutationRepository: it applies one
// [mutation.Command] as a single critical section over the shared [state] mutex,
// covering receipt lookup, current state, guard evaluation, dependency-revision
// validation, invariant evaluation, row changes, revision bump, and receipt
// persistence — the whole atomic contract with no service orchestration.
type Mutations struct {
	st       *state
	rels     *Relationships
	roles    *Roles
	guardian mutation.GuardianPolicy
}

var _ mutation.MutationRepository = (*Mutations)(nil)

// Apply runs the trusted (unguarded) atomic write path. See the port doc comment
// on mutation.MutationRepository for the full ordered contract.
func (m *Mutations) Apply(ctx context.Context, cmd mutation.Command, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	return m.applyLocked(ctx, cmd, validate, nil)
}

// ApplyGuarded runs the actor-facing atomic write path: it evaluates guard against
// a dependency-tracking DecisionView inside the same critical section, then
// validates every observed dependency revision before commit. A nil guard is
// treated as the trusted path.
func (m *Mutations) ApplyGuarded(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	return m.applyLocked(ctx, cmd, validate, guard)
}

// applyLocked is the single critical section. The caller holds st.mu, so the guard
// view reads the held snapshot without recursively locking, and dependency
// revisions observed during the guard cannot change before commit — the honest
// mirror of the SQL stores locking the mutation scope plus dependency anchors in
// canonical order.
func (m *Mutations) applyLocked(ctx context.Context, cmd mutation.Command, validate mutation.SemanticValidator, guard mutation.Guard) (*mutation.Receipt, error) {
	// 1. Authorize the actor (guarded path) FIRST — before the MutationID/digest
	//    check — so an actor-facing replay still runs its guard (possession of a
	//    MutationID is not authority). The view records every scope+revision read.
	var view *decisionView
	if guard != nil {
		view = &decisionView{m: m, deps: map[string]mutation.Dependency{}}
		if err := guard(ctx, view); err != nil {
			return nil, err
		}
	}

	// 2. De-duplicate by MutationID against persisted receipts.
	if existing, ok := m.st.receipts[cmd.MutationID]; ok {
		if existing.PayloadDigest != cmd.PayloadDigest() {
			return nil, mutation.ErrPayloadMismatch
		}
		if view != nil {
			if err := m.validateDependenciesLocked(view); err != nil {
				return nil, err
			}
		}
		replay := existing
		replay.Replayed = true
		return &replay, nil
	}

	// 3. Receipt-absent: validate the guard's observed dependency revisions.
	if view != nil {
		if err := m.validateDependenciesLocked(view); err != nil {
			return nil, err
		}
	}

	// 4. Receipt-absent semantic validation against the CURRENT schema. Skipped on
	//    replay (step 2), which is why an exact stored replay survives a schema that
	//    would now reject the original relation.
	if validate != nil {
		if err := validate(cmd); err != nil {
			return nil, err
		}
	}

	// 5. Expected-revision precondition against the mutation scope.
	current := m.scopeRevisionLocked(cmd.Scope)
	if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != current {
		return nil, mutation.ErrStaleRevision
	}

	// 6. Evaluate invariants and apply ALL requested row changes or NONE.
	outcome, changed := m.evaluateLocked(cmd)
	if outcome == mutation.OutcomeSemanticConflict || outcome == mutation.OutcomeInvariantBlocked {
		// A domain outcome that commits nothing and persists no receipt.
		return m.receipt(cmd, outcome, current), nil
	}

	// 7. Bump the scope revision exactly once on a change; persist the receipt.
	revision := current
	if changed {
		revision = current + 1
		m.st.scopes[cmd.Scope.Canonical()] = revision
	}
	rcpt := m.receipt(cmd, outcome, revision)
	// Op-specific, non-persisted annotation: for a scoped role unassign, report
	// (inside this same locked section, so the answer is atomic with the removal)
	// whether a global grant still satisfies the exact role. It is NOT written to
	// the receipt store, so a later replay returns it false.
	rcpt.SameRoleGrantRemains = m.sameRoleGrantRemainsLocked(cmd)
	if outcome.Persisted() {
		m.st.receipts[cmd.MutationID] = *rcpt
	}
	return rcpt, nil
}

// sameRoleGrantRemainsLocked reports, for a SCOPED role unassign only, whether a
// GLOBAL ("","") assignment for one of the command's exact (subject, role) rows
// still exists after the unassign. The caller holds st.mu and evaluateLocked has
// already removed the scoped rows, so this read is atomic with the removal — the
// honest same_role_grant_remains answer, never a detached post-commit read. It
// does not claim generic access remains.
func (m *Mutations) sameRoleGrantRemainsLocked(cmd mutation.Command) bool {
	if cmd.Operation != mutation.OpRoleUnassign || cmd.Scope.Kind != mutation.ScopeResource {
		return false
	}
	for _, row := range cmd.Roles {
		if m.roles.index(row.SubjectType, row.SubjectID, row.Role, "", "") >= 0 {
			return true
		}
	}
	return false
}

// evaluateLocked dispatches to the per-operation evaluator, mutating the shared
// rows only when the outcome is applied. It returns the domain outcome and whether
// a change was committed (which drives the revision bump).
func (m *Mutations) evaluateLocked(cmd mutation.Command) (mutation.Outcome, bool) {
	switch cmd.Operation {
	case mutation.OpGrant:
		return m.grantLocked(cmd)
	case mutation.OpRevoke:
		return m.revokeLocked(cmd)
	case mutation.OpReplace:
		return m.replaceLocked(cmd)
	case mutation.OpPurge:
		return m.purgeLocked(cmd, false)
	case mutation.OpTeardown:
		return m.purgeLocked(cmd, true)
	case mutation.OpRoleAssign:
		return m.roleAssignLocked(cmd)
	case mutation.OpRoleUnassign:
		return m.roleUnassignLocked(cmd)
	default:
		// Command.Validate rejects unknown operations before we reach here.
		return mutation.OutcomeNoChange, false
	}
}

// grantLocked adds the command's relationship rows. A row whose subject already
// holds the SAME relation is a per-row no-op; a subject already holding a
// DIFFERENT relation is a one-relation semantic conflict that rolls the whole
// command back (no partial batch). A grant that would leave a protected resource
// without its guardian minimum (member/role-first on a fresh protected resource)
// is invariant-blocked.
func (m *Mutations) grantLocked(cmd mutation.Command) (mutation.Outcome, bool) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	var adds []relRow
	for _, row := range cmd.Relationships {
		existing, ok := m.subjectRowLocked(rt, rid, row.Subject)
		if ok {
			if existing.relation == row.Relation {
				continue // exact duplicate — no change for this row
			}
			return mutation.OutcomeSemanticConflict, false
		}
		adds = append(adds, newRelRow(rt, rid, row))
	}
	if len(adds) == 0 {
		return mutation.OutcomeNoChange, false
	}
	next := append(append([]relRow(nil), m.st.rel...), adds...)
	if !m.relationshipInvariantOK(rt, rid, next) {
		return mutation.OutcomeInvariantBlocked, false
	}
	m.st.rel = next
	return mutation.OutcomeApplied, true
}

// revokeLocked removes the command's exact relationship rows. Revoking rows that
// none exist is a committed not_found no-op; a revoke that would drop a protected
// relation below its guardian minimum (the last direct owner) is invariant-blocked.
func (m *Mutations) revokeLocked(cmd mutation.Command) (mutation.Outcome, bool) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	remove := map[relIdentity]bool{}
	for _, row := range cmd.Relationships {
		remove[relIdentityOf(row.Relation, row.Subject)] = true
	}
	matched := 0
	kept := make([]relRow, 0, len(m.st.rel))
	for _, r := range m.st.rel {
		if r.resourceType == rt && r.resourceID == rid && remove[rowIdentity(r)] {
			matched++
			continue
		}
		kept = append(kept, r)
	}
	if matched == 0 {
		return mutation.OutcomeNotFound, false
	}
	if !m.relationshipInvariantOK(rt, rid, kept) {
		return mutation.OutcomeInvariantBlocked, false
	}
	m.st.rel = kept
	return mutation.OutcomeApplied, true
}

// replaceLocked atomically sets each row's subject to the row's relation on the
// resource — the sanctioned one-relation answer, with no delete/create gap. A
// subject already at the target relation is a per-row no-op; a replace-away that
// removes the last direct guardian (owner→member) is invariant-blocked.
func (m *Mutations) replaceLocked(cmd mutation.Command) (mutation.Outcome, bool) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	next := append([]relRow(nil), m.st.rel...)
	changed := false
	for _, row := range cmd.Relationships {
		idx := -1
		for i, r := range next {
			if r.resourceType == rt && r.resourceID == rid &&
				r.subjectType == row.Subject.Type && r.subjectID == row.Subject.ID && r.subjectRelation == row.Subject.Relation {
				idx = i
				break
			}
		}
		if idx >= 0 {
			if next[idx].relation == row.Relation {
				continue
			}
			next[idx].relation = row.Relation
			changed = true
			continue
		}
		next = append(next, newRelRow(rt, rid, row))
		changed = true
	}
	if !changed {
		return mutation.OutcomeNoChange, false
	}
	if !m.relationshipInvariantOK(rt, rid, next) {
		return mutation.OutcomeInvariantBlocked, false
	}
	m.st.rel = next
	return mutation.OutcomeApplied, true
}

// purgeLocked removes every relationship on the resource. An ordinary purge
// (teardown=false) still honors guardian invariants, so purging a protected
// resource is invariant-blocked — it cannot silently orphan it. Teardown
// (teardown=true) is the one operation allowed to zero a protected scope: it
// bypasses the invariant and also clears the resource's scoped role assignments.
func (m *Mutations) purgeLocked(cmd mutation.Command, teardown bool) (mutation.Outcome, bool) {
	rt, rid := cmd.Scope.Type, cmd.Scope.ID
	keptRel := make([]relRow, 0, len(m.st.rel))
	removed := 0
	for _, r := range m.st.rel {
		if r.resourceType == rt && r.resourceID == rid {
			removed++
			continue
		}
		keptRel = append(keptRel, r)
	}

	keptRole := m.st.role
	removedRole := 0
	if teardown {
		keptRole = make([]roleRow, 0, len(m.st.role))
		for _, r := range m.st.role {
			if r.resourceType == rt && r.resourceID == rid {
				removedRole++
				continue
			}
			keptRole = append(keptRole, r)
		}
	}

	if removed == 0 && removedRole == 0 {
		return mutation.OutcomeNoChange, false
	}
	// Blast-radius bound: an ordinary purge that would remove more than the
	// service-sourced ceiling (EvaluationLimits.MaxBatchSize) is invariant-blocked,
	// atomically under the same lock. Teardown is the trusted, unbounded path.
	if !teardown && cmd.MaxAffectedRows > 0 && removed > cmd.MaxAffectedRows {
		return mutation.OutcomeInvariantBlocked, false
	}
	if !teardown && !m.relationshipInvariantOK(rt, rid, keptRel) {
		return mutation.OutcomeInvariantBlocked, false
	}
	m.st.rel = keptRel
	m.st.role = keptRole
	return mutation.OutcomeApplied, true
}

// roleAssignLocked assigns the command's role rows at the command's scope (a
// resource scope is a scoped assignment; a subject scope is a global assignment).
// Exact-duplicate assignments are a no-op.
func (m *Mutations) roleAssignLocked(cmd mutation.Command) (mutation.Outcome, bool) {
	resType, resID := roleScope(cmd.Scope)
	var adds []roleRow
	for _, row := range cmd.Roles {
		if m.roles.index(row.SubjectType, row.SubjectID, row.Role, resType, resID) >= 0 {
			continue
		}
		adds = append(adds, roleRow{
			subjectType:  row.SubjectType,
			subjectID:    row.SubjectID,
			role:         row.Role,
			resourceType: resType,
			resourceID:   resID,
			createdAt:    time.Now().UTC(),
		})
	}
	if len(adds) == 0 {
		return mutation.OutcomeNoChange, false
	}
	m.st.role = append(m.st.role, adds...)
	return mutation.OutcomeApplied, true
}

// roleUnassignLocked removes the command's exact role rows. Unassigning rows that
// none exist is a committed not_found no-op.
func (m *Mutations) roleUnassignLocked(cmd mutation.Command) (mutation.Outcome, bool) {
	resType, resID := roleScope(cmd.Scope)
	remove := map[roleIdentity]bool{}
	for _, row := range cmd.Roles {
		remove[roleIdentity{row.SubjectType, row.SubjectID, row.Role, resType, resID}] = true
	}
	matched := 0
	kept := make([]roleRow, 0, len(m.st.role))
	for _, r := range m.st.role {
		if remove[roleIdentity{r.subjectType, r.subjectID, r.role, r.resourceType, r.resourceID}] {
			matched++
			continue
		}
		kept = append(kept, r)
	}
	if matched == 0 {
		return mutation.OutcomeNotFound, false
	}
	m.st.role = kept
	return mutation.OutcomeApplied, true
}

// relationshipInvariantOK reports whether the candidate relationship rows satisfy
// every guardian rule for the resource: each protected relation must retain at
// least its minimum count of DIRECT anchors (concrete subjects with an empty
// userset relation). This is the post-state rule that both blocks the loss of the
// final direct guardian AND requires the establishing owner grant before any other
// command on a protected resource.
func (m *Mutations) relationshipInvariantOK(resourceType, resourceID string, rows []relRow) bool {
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
			if r.resourceType == resourceType && r.resourceID == resourceID &&
				r.relation == rule.Relation && r.subjectRelation == "" {
				count++
			}
		}
		if count < min {
			return false
		}
	}
	return true
}

func (m *Mutations) scopeRevisionLocked(scope mutation.ScopeKey) mutation.Revision {
	return m.st.scopes[scope.Canonical()]
}

func (m *Mutations) validateDependenciesLocked(view *decisionView) error {
	for _, dep := range view.Dependencies() {
		if m.scopeRevisionLocked(dep.Scope) != dep.Revision {
			return mutation.ErrStaleRevision
		}
	}
	return nil
}

// receipt builds the receipt for a resolved outcome, recording the governing
// SchemaDigest the mutation service stamped onto the command (AZ3-3.1). It is empty
// when the caller supplied no schema (a trusted/migration path); the reference
// store has no non-empty column constraint, so it records the value verbatim, and
// an exact replay returns this same recorded digest.
func (m *Mutations) receipt(cmd mutation.Command, outcome mutation.Outcome, revision mutation.Revision) *mutation.Receipt {
	return &mutation.Receipt{
		MutationID:      cmd.MutationID,
		Scope:           cmd.Scope,
		Operation:       cmd.Operation,
		PayloadEncoding: cmd.PayloadEncoding(),
		PayloadDigest:   cmd.PayloadDigest(),
		Outcome:         outcome,
		Revision:        revision,
		SchemaDigest:    cmd.SchemaDigest,
		Replayed:        false,
		CreatedAt:       time.Now().UTC(),
	}
}

func (m *Mutations) subjectRowLocked(resourceType, resourceID string, subj relationship.SubjectRef) (relRow, bool) {
	for _, r := range m.st.rel {
		if r.resourceType == resourceType && r.resourceID == resourceID &&
			r.subjectType == subj.Type && r.subjectID == subj.ID && r.subjectRelation == subj.Relation {
			return r, true
		}
	}
	return relRow{}, false
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

func rowIdentity(r relRow) relIdentity {
	return relIdentity{r.relation, r.subjectType, r.subjectID, r.subjectRelation}
}

// roleIdentity is a role row's exact 5-tuple identity for unassign matching.
type roleIdentity struct {
	subjectType  string
	subjectID    string
	role         string
	resourceType string
	resourceID   string
}

func newRelRow(resourceType, resourceID string, row mutation.RelationshipRow) relRow {
	return relRow{
		id:              memIDs.MustGenerate(),
		resourceType:    resourceType,
		resourceID:      resourceID,
		relation:        row.Relation,
		subjectType:     row.Subject.Type,
		subjectID:       row.Subject.ID,
		subjectRelation: row.Subject.Relation,
		createdAt:       time.Now().UTC(),
	}
}

// roleScope maps a command scope to the role assignment's (resourceType,
// resourceID): a resource scope is a scoped assignment; a subject scope is a
// global assignment (empty pair).
func roleScope(scope mutation.ScopeKey) (string, string) {
	if scope.Kind == mutation.ScopeResource {
		return scope.Type, scope.ID
	}
	return "", ""
}

// decisionView is the dependency-tracking DecisionView the repository supplies to
// a guard. It reads the held snapshot through the non-locking store helpers and
// records every scope + observed revision so the repository can validate those
// dependencies before commit.
type decisionView struct {
	m     *Mutations
	deps  map[string]mutation.Dependency
	order []string
}

var _ mutation.DecisionView = (*decisionView)(nil)

func (v *decisionView) CheckRelation(ctx context.Context, scope mutation.ScopeKey, relation, subjectType, subjectID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	v.record(scope)
	ok, reached := v.m.rels.checkRelationExpandScopesLocked(scope.Type, scope.ID, relation, subjectType, subjectID)
	// Record every intermediate resource scope traversed during group expansion;
	// its membership edges live under (type, id), so a concurrent revoke bumps
	// that scope's revision and must invalidate the decision. Recording the seed
	// (the subject as a resource scope) is a harmless over-record.
	for ref := range reached {
		v.record(mutation.ScopeKey{Kind: mutation.ScopeResource, Type: ref[0], ID: ref[1]})
	}
	return ok, nil
}

func (v *decisionView) HasRole(ctx context.Context, scope mutation.ScopeKey, role, subjectType, subjectID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	v.record(scope)
	has, consultedGlobal := v.m.roles.hasRoleEffectiveScopesLocked(scope, role, subjectType, subjectID)
	// The global fallback read the subject's global roles, which serialize into
	// its subject scope; record that scope regardless of the result so a
	// concurrent global grant/revoke invalidates the decision.
	if consultedGlobal {
		v.record(mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: subjectType, ID: subjectID})
	}
	return has, nil
}

func (v *decisionView) Dependencies() []mutation.Dependency {
	keys := append([]string(nil), v.order...)
	sort.Strings(keys)
	out := make([]mutation.Dependency, 0, len(keys))
	for _, k := range keys {
		out = append(out, v.deps[k])
	}
	return out
}

func (v *decisionView) record(scope mutation.ScopeKey) {
	key := scope.Canonical()
	if _, ok := v.deps[key]; ok {
		return
	}
	v.deps[key] = mutation.Dependency{Scope: scope, Revision: v.m.scopeRevisionLocked(scope)}
	v.order = append(v.order, key)
}
