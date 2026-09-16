package memory

import (
	"context"
	"slices"

	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// Store bundles Tuples, Relationships and Mutations over one canonical fact set.
// Every facade shares the same mutex and write boundary. Wire repositories from
// one Store so atomic mutations, raw writes and reads share that authority.
type Store struct {
	rel *Relationships
	tup *Tuples
	mut *Mutations
}

// Option configures a [Store] at construction.
type Option func(*storeConfig)

type storeConfig struct {
	integrity   mutation.IntegrityPolicy
	recordAudit bool
}

// WithIntegrityPolicy installs the host's relationship invariants. The option
// snapshots its input; the store defaults to an empty policy. NewService checks
// the repository's policy against the host relationship model.
func WithIntegrityPolicy(p mutation.IntegrityPolicy) Option {
	p.Rules = slices.Clone(p.Rules)
	return func(c *storeConfig) { c.integrity = mutation.IntegrityPolicy{Rules: slices.Clone(p.Rules)} }
}

// WithAudit enables atomic recording on every writer in this bundle.
func WithAudit() Option { return func(c *storeConfig) { c.recordAudit = true } }

// New builds tuple, relationship and mutation repositories sharing one state.
// Integrity protection is opt-in through WithIntegrityPolicy.
func New(opts ...Option) *Store {
	cfg := storeConfig{}
	for _, o := range opts {
		if o == nil {
			panic("authorization memory: New received a nil option")
		}
		o(&cfg)
	}
	st := newState()
	if err := cfg.integrity.Validate(); err != nil {
		panic(err)
	}
	st.recordAudit = cfg.recordAudit
	st.integrity = cfg.integrity

	s := &Store{rel: &Relationships{st: st}, tup: &Tuples{st: st}}
	s.mut = &Mutations{st: st, integrity: cfg.integrity}
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

// Mutations applies integrity checks and changes within the shared write boundary.
type Mutations struct {
	st        *state
	integrity mutation.IntegrityPolicy
}

var _ mutation.MutationRepository = (*Mutations)(nil)

func (m *Mutations) IntegrityPolicy() mutation.IntegrityPolicy {
	return mutation.IntegrityPolicy{Rules: slices.Clone(m.integrity.Rules)}
}
func (m *Mutations) Apply(ctx context.Context, cmd mutation.Command, validate mutation.SemanticValidator) (*mutation.Result, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var result *mutation.Result
	err := m.st.write(ctx, func(next *state) error {
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
		delta, outcome, err := mutation.Plan(cmd, before, m.integrity)
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
