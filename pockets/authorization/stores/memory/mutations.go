package memory

import (
	"context"
	"slices"

	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
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
	guardian    mutation.GuardianPolicy
	recordAudit bool
}

// WithGuardianPolicy installs the host's relationship invariants. The option
// snapshots its input; the store defaults to an empty policy. NewService checks
// the repository's policy against the host relationship model.
func WithGuardianPolicy(p mutation.GuardianPolicy) Option {
	p.Rules = slices.Clone(p.Rules)
	return func(c *storeConfig) { c.guardian = mutation.GuardianPolicy{Rules: slices.Clone(p.Rules)} }
}

// WithAudit enables atomic recording on every writer in this bundle.
func WithAudit() Option { return func(c *storeConfig) { c.recordAudit = true } }

// New builds relationship, role, and mutation stores sharing one lock and state.
// Guardian protection is opt-in through WithGuardianPolicy.
func New(opts ...Option) *Store {
	cfg := storeConfig{}
	for _, o := range opts {
		if o == nil {
			panic("authorization memory: New received a nil option")
		}
		o(&cfg)
	}
	st := newState()
	st.recordAudit = cfg.recordAudit
	s := &Store{rel: &Relationships{st: st}, rol: &Roles{st: st}}
	s.mut = &Mutations{st: st, rels: s.rel, roles: s.rol, guardian: cfg.guardian}
	return s
}

// Relationships returns the shared-state relationship.Storer.
func (s *Store) Relationships() *Relationships { return s.rel }

// Roles returns the shared-state role.Storer.
func (s *Store) Roles() *Roles { return s.rol }

// Audit returns retained history, including when recording is disabled.
func (s *Store) Audit() *Audit { return &Audit{st: s.rel.st} }

// Mutations returns the shared-state atomic mutation.MutationRepository.
func (s *Store) Mutations() *Mutations { return s.mut }

// Mutations applies guardian checks and changes within the shared write boundary.
type Mutations struct {
	st       *state
	rels     *Relationships
	roles    *Roles
	guardian mutation.GuardianPolicy
}

var _ mutation.MutationRepository = (*Mutations)(nil)

func (m *Mutations) GuardianPolicy() mutation.GuardianPolicy {
	return mutation.GuardianPolicy{Rules: slices.Clone(m.guardian.Rules)}
}
func (m *Mutations) Apply(ctx context.Context, cmd mutation.Command, validate mutation.SemanticValidator) (*mutation.Result, error) {
	return m.ApplyGuarded(ctx, cmd, nil, validate)
}
func (m *Mutations) ApplyGuarded(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	var result *mutation.Result
	err := m.st.write(ctx, func(next *state) error {
		staged := &Mutations{st: next, rels: &Relationships{st: next}, roles: &Roles{st: next}, guardian: m.guardian}
		if guard != nil {
			if err := guard(ctx, &decisionView{m: staged}); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if validate != nil {
			if err := validate(cmd); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		outcome := staged.evaluateLocked(cmd)
		if err := outcome.Rejection(); err != nil {
			return err
		}
		result = &mutation.Result{Outcome: outcome, SameRoleGrantRemains: staged.sameRoleGrantRemainsLocked(cmd)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// sameRoleGrantRemainsLocked reports, for a SCOPED role unassign only, whether a
// GLOBAL ("","") assignment for one of the command's exact (subject, role) rows
// still exists after the unassign. The caller holds st.mu and evaluateLocked has
// already removed the scoped rows, so this read is atomic with the removal — the
// honest same_role_grant_remains answer, never a detached post-commit read. It
// does not claim generic access remains.
func (m *Mutations) sameRoleGrantRemainsLocked(cmd mutation.Command) bool {
	if cmd.Operation != mutation.OpRoleUnassign || cmd.Target.Kind != mutation.TargetResource {
		return false
	}
	for _, row := range cmd.Roles {
		if m.roles.index(row.SubjectType, row.SubjectID, row.Role, "", "") >= 0 {
			return true
		}
	}
	return false
}

// evaluateLocked changes the staged facts only when the outcome is applied.
func (m *Mutations) evaluateLocked(cmd mutation.Command) mutation.Outcome {
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
		return mutation.OutcomeNoChange
	}
}

// grantLocked adds the command's relationship rows. A row whose subject already
// holds the SAME relation is a per-row no-op; a subject already holding a
// DIFFERENT relation is a one-relation semantic conflict that rolls the whole
// command back (no partial batch). A grant that would leave a protected resource
// without its guardian minimum (member/role-first on a fresh protected resource)
// is invariant-blocked.
func (m *Mutations) grantLocked(cmd mutation.Command) mutation.Outcome {
	rt, rid := cmd.Target.Type, cmd.Target.ID
	var adds []relRow
	for _, row := range cmd.Relationships {
		existing, ok := m.subjectRowLocked(rt, rid, row.Subject)
		if ok {
			if existing.relation == row.Relation {
				continue // exact duplicate — no change for this row
			}
			return mutation.OutcomeSemanticConflict
		}
		adds = append(adds, newRelRow(rt, rid, row))
	}
	if len(adds) == 0 {
		return mutation.OutcomeNoChange
	}
	next := append(append([]relRow(nil), m.st.rel...), adds...)
	if !m.relationshipInvariantOK(rt, rid, next) {
		return mutation.OutcomeInvariantBlocked
	}
	m.st.rel = next
	return mutation.OutcomeApplied
}

// revokeLocked removes the command's exact relationship rows. Revoking rows that
// none exist is a committed not_found no-op; a revoke that would drop a protected
// relation below its guardian minimum (the last direct owner) is invariant-blocked.
func (m *Mutations) revokeLocked(cmd mutation.Command) mutation.Outcome {
	rt, rid := cmd.Target.Type, cmd.Target.ID
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
		return mutation.OutcomeNotFound
	}
	if !m.relationshipInvariantOK(rt, rid, kept) {
		return mutation.OutcomeInvariantBlocked
	}
	m.st.rel = kept
	return mutation.OutcomeApplied
}

// replaceLocked atomically sets each row's subject to the row's relation on the
// resource — the sanctioned one-relation answer, with no delete/create gap. A
// subject already at the target relation is a per-row no-op; a replace-away that
// removes the last direct guardian (owner→member) is invariant-blocked.
func (m *Mutations) replaceLocked(cmd mutation.Command) mutation.Outcome {
	rt, rid := cmd.Target.Type, cmd.Target.ID
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
		return mutation.OutcomeNoChange
	}
	if !m.relationshipInvariantOK(rt, rid, next) {
		return mutation.OutcomeInvariantBlocked
	}
	m.st.rel = next
	return mutation.OutcomeApplied
}

// purgeLocked removes every relationship on the resource. An ordinary purge
// (teardown=false) still honors guardian invariants, so purging a protected
// resource is invariant-blocked — it cannot silently orphan it. Teardown
// (teardown=true) is the one operation allowed to zero a protected scope: it
// bypasses the invariant and also clears the resource's scoped role assignments.
func (m *Mutations) purgeLocked(cmd mutation.Command, teardown bool) mutation.Outcome {
	rt, rid := cmd.Target.Type, cmd.Target.ID
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
		return mutation.OutcomeNoChange
	}
	// Blast-radius bound: an ordinary purge that would remove more than the
	// service-sourced ceiling (EvaluationLimits.MaxBatchSize) is invariant-blocked,
	// atomically under the same lock. Teardown is the trusted, unbounded path.
	if !teardown && cmd.MaxAffectedRows > 0 && removed > cmd.MaxAffectedRows {
		return mutation.OutcomeInvariantBlocked
	}
	if !teardown && !m.relationshipInvariantOK(rt, rid, keptRel) {
		return mutation.OutcomeInvariantBlocked
	}
	m.st.rel = keptRel
	m.st.role = keptRole
	return mutation.OutcomeApplied
}

// roleAssignLocked assigns the command's role rows at the command's scope (a
// resource scope is a scoped assignment; a subject scope is a global assignment).
// Exact-duplicate assignments are a no-op.
func (m *Mutations) roleAssignLocked(cmd mutation.Command) mutation.Outcome {
	resType, resID := roleScope(cmd.Target)
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
		})
	}
	if len(adds) == 0 {
		return mutation.OutcomeNoChange
	}
	m.st.role = append(m.st.role, adds...)
	return mutation.OutcomeApplied
}

// roleUnassignLocked removes the command's exact role rows. Unassigning rows that
// none exist is a committed not_found no-op.
func (m *Mutations) roleUnassignLocked(cmd mutation.Command) mutation.Outcome {
	resType, resID := roleScope(cmd.Target)
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
		return mutation.OutcomeNotFound
	}
	m.st.role = kept
	return mutation.OutcomeApplied
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

func (m *Mutations) subjectRowLocked(resourceType, resourceID string, subj relationships.SubjectRef) (relRow, bool) {
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

func relIdentityOf(relation string, subj relationships.SubjectRef) relIdentity {
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
		resourceType:    resourceType,
		resourceID:      resourceID,
		relation:        row.Relation,
		subjectType:     row.Subject.Type,
		subjectID:       row.Subject.ID,
		subjectRelation: row.Subject.Relation,
	}
}

// roleScope maps a command scope to the role assignment's (resourceType,
// resourceID): a resource scope is a scoped assignment; a subject scope is a
// global assignment (empty pair).
func roleScope(scope mutation.Target) (string, string) {
	if scope.Kind == mutation.TargetResource {
		return scope.Type, scope.ID
	}
	return "", ""
}

// decisionView uses non-locking helpers while the outer shared mutex is held.
type decisionView struct{ m *Mutations }

var _ mutation.StoreDecisionView = (*decisionView)(nil)

func (v *decisionView) CheckRelation(ctx context.Context, target mutation.Target, relation, subjectType, subjectID string) (bool, error) {
	return v.CheckRelationBounded(ctx, target, relation, subjectType, subjectID, 0)
}
func (v *decisionView) CheckRelationBounded(ctx context.Context, target mutation.Target, relation, subjectType, subjectID string, bound int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	if target.Kind != mutation.TargetResource {
		return false, mutation.ErrInvalidCommand
	}
	ok, overflow := v.m.rels.checkRelationExpandedLocked(ctx, target.Type, target.ID, relation, subjectType, subjectID, bound)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if overflow {
		return false, relationships.ErrExpansionBudgetExceeded
	}
	return ok, nil
}
func (v *decisionView) RelationTargets(ctx context.Context, target mutation.Target, relation string) ([]relationships.RelationTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if target.Kind != mutation.TargetResource {
		return nil, mutation.ErrInvalidCommand
	}
	return v.m.rels.getRelationTargetsLocked(target.Type, target.ID, relation), nil
}
func (v *decisionView) HasRole(ctx context.Context, target mutation.Target, roleName, subjectType, subjectID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	if err := (mutation.RoleRow{SubjectType: subjectType, SubjectID: subjectID, Role: roleName}).Validate(); err != nil {
		return false, err
	}
	has := v.m.roles.hasRoleEffectiveLocked(target, roleName, subjectType, subjectID)
	return has, nil
}
