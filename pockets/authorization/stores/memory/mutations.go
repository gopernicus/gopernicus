package memory

import (
	"context"
	"slices"

	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// Store bundles Tuples, Relationships and Mutations over one canonical fact set.
// Every facade shares the same mutex and write boundary. Wire repositories from
// one Store so guarded mutations, raw writes and reads share that authority.
type Store struct {
	rel *Relationships
	tup *Tuples
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

// New builds tuple, relationship and mutation repositories sharing one state.
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

	s := &Store{rel: &Relationships{st: st}, tup: &Tuples{st: st}}
	s.mut = &Mutations{st: st, rels: s.rel, guardian: cfg.guardian}
	return s
}

// Relationships returns the shared-state relationship.Storer.
func (s *Store) Relationships() *Relationships { return s.rel }

// Tuples returns the one authority used by role and relationship facades.
func (s *Store) Tuples() *Tuples { return s.tup }

// Audit returns retained history, including when recording is disabled.
func (s *Store) Audit() *Audit { return &Audit{st: s.rel.st} }

// Mutations returns the shared-state atomic mutation.MutationRepository.
func (s *Store) Mutations() *Mutations { return s.mut }

// Mutations applies guardian checks and changes within the shared write boundary.
type Mutations struct {
	st       *state
	rels     *Relationships
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
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var result *mutation.Result
	err := m.st.write(ctx, func(next *state) error {
		staged := &Mutations{st: next, rels: &Relationships{st: next}, guardian: m.guardian}
		if guard != nil {
			view := &decisionView{m: staged, snapshotReads: &snapshotReads{st: next, ctx: ctx}}
			err := func() error { defer view.closed.Store(true); return guard(ctx, view) }()
			if err != nil {
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
		before := []tuples.Tuple{}
		for t := range next.facts {
			if cmd.Target.Contains(t) {
				before = append(before, t)
			}
		}
		delta, outcome, err := mutation.Plan(cmd, before, m.guardian)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		next.applyLocked(delta)
		result = &mutation.Result{Outcome: outcome}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type decisionView struct {
	*snapshotReads
	m *Mutations
}

var _ mutation.StoreDecisionView = (*decisionView)(nil)

func (v *decisionView) CheckRelation(ctx context.Context, t mutation.Target, relation, st, id string) (bool, error) {
	return v.CheckRelationBounded(ctx, t, relation, st, id, 0)
}
func (v *decisionView) CheckRelationBounded(ctx context.Context, t mutation.Target, relation, st, id string, limit int) (bool, error) {
	if err := v.check(ctx); err != nil {
		return false, err
	}
	if err := t.Validate(); err != nil {
		return false, err
	}
	if t.Kind != mutation.TargetResource {
		return false, mutation.ErrInvalidCommand
	}
	return v.m.rels.CheckRelationWithGroupExpansion(ctx, t.Type, t.ID, relation, st, id, limit)
}
func (v *decisionView) RelationTargets(ctx context.Context, t mutation.Target, relation string) ([]relationships.RelationTarget, error) {
	if err := v.check(ctx); err != nil {
		return nil, err
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if t.Kind != mutation.TargetResource {
		return nil, mutation.ErrInvalidCommand
	}
	return v.m.rels.GetRelationTargets(ctx, t.Type, t.ID, relation)
}
