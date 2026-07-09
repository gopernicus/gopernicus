// Package taxonomysvc holds the taxonomy use-case service, kept internal so it is
// not part of the feature's public SemVer surface (plan §5/B3). The public
// domain types and TermRepository interface stay in package taxonomy.
package taxonomysvc

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
)

// Clock returns the current time. Injected so tests can pin timestamps.
type Clock func() time.Time

// Service implements the taxonomy use cases over a taxonomy.TermRepository.
type Service struct {
	terms taxonomy.TermRepository
	// ids is the app-chosen entity-ID strategy (cms.Config.IDs); zero value →
	// default nanoids.
	ids   cryptids.IDGenerator
	clock Clock
}

// NewService constructs a Service. A nil clock defaults to time.Now. ids is the
// app's entity-ID strategy (cms.Config.IDs).
func NewService(terms taxonomy.TermRepository, ids cryptids.IDGenerator, clock Clock) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{terms: terms, ids: ids, clock: clock}
}

// CreateTerm validates and persists a new term.
func (s *Service) CreateTerm(ctx context.Context, kind taxonomy.Kind, name, parentID string) (taxonomy.Term, error) {
	t, err := taxonomy.NewTerm(s.ids, kind, name, parentID, s.clock())
	if err != nil {
		return taxonomy.Term{}, err
	}
	return s.terms.Create(ctx, t)
}

// GetTerm returns the term with the given id, or errs.ErrNotFound.
func (s *Service) GetTerm(ctx context.Context, id string) (taxonomy.Term, error) {
	return s.terms.Get(ctx, id)
}

// GetTermBySlug returns the term with the given kind+slug, or errs.ErrNotFound.
func (s *Service) GetTermBySlug(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	return s.terms.GetBySlug(ctx, kind, slug)
}

// ListTerms returns all terms of a kind, ordered by name.
func (s *Service) ListTerms(ctx context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error) {
	return s.terms.ListByKind(ctx, kind)
}

// EditTerm loads the term, applies the edit, and persists it.
func (s *Service) EditTerm(ctx context.Context, id, name, parentID string) (taxonomy.Term, error) {
	t, err := s.terms.Get(ctx, id)
	if err != nil {
		return taxonomy.Term{}, err
	}
	if err := t.ApplyEdit(name, parentID, s.clock()); err != nil {
		return taxonomy.Term{}, err
	}
	return s.terms.Update(ctx, id, t)
}

// DeleteTerm removes a term by id.
func (s *Service) DeleteTerm(ctx context.Context, id string) error {
	return s.terms.Delete(ctx, id)
}
