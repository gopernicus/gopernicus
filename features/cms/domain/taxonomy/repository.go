package taxonomy

import "context"

// TermRepository is the app port for persisting terms. Slug uniqueness is
// per-kind (a category and a tag may share a slug). Implemented by feature
// store adapters (features/cms/stores/turso) or any host-provided
// implementation (see examples/minimal's memstore).
type TermRepository interface {
	// Get returns the term with the given id, or errs.ErrNotFound.
	Get(ctx context.Context, id string) (Term, error)
	// GetBySlug returns the term with the given kind+slug, or errs.ErrNotFound.
	GetBySlug(ctx context.Context, kind Kind, slug string) (Term, error)
	// ListByKind returns all terms of a kind, ordered by name.
	ListByKind(ctx context.Context, kind Kind) ([]Term, error)
	// Create persists a new term; (kind,slug) collision → errs.ErrAlreadyExists.
	Create(ctx context.Context, t Term) (Term, error)
	// Update persists changes; missing id → errs.ErrNotFound.
	Update(ctx context.Context, id string, t Term) (Term, error)
	// Delete removes the term (and any post associations) by id.
	Delete(ctx context.Context, id string) error
}
