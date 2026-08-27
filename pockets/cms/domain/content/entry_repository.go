package content

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// EntryQuery is a type-scoped, optionally status-filtered list query over
// entries. It embeds the generic cursor ListRequest. Type is required for
// type-scoped lists (the spine is shared, so an unfiltered list mixes types);
// an empty Status matches any status.
type EntryQuery struct {
	Type   string // content type slug; required for type-scoped lists
	Status Status // "" = any status
	crud.ListRequest
}

// EntryRepository is the single generic port for persisting all content
// (plan §4). One port replaces the former per-type PostRepository/PageRepository
// because every type rides the shared Entry + EAV rail. Implemented by
// stores/turso (entries + entry_fields) and the in-memory example store.
type EntryRepository interface {
	// Create persists a new entry and its custom fields. A (type, slug)
	// collision returns sdk.ErrAlreadyExists.
	Create(ctx context.Context, e Entry) (Entry, error)

	// Update persists changes to the entry with the given id, replacing its
	// custom fields. A missing id returns sdk.ErrNotFound.
	Update(ctx context.Context, id string, e Entry) (Entry, error)

	// Get returns the entry with the given id — spine, custom fields, and term
	// IDs — or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Entry, error)

	// GetBySlug returns the entry of type typ with the given slug, or
	// sdk.ErrNotFound.
	GetBySlug(ctx context.Context, typ, slug string) (Entry, error)

	// Delete removes the entry (and its fields/terms via cascade), or returns
	// sdk.ErrNotFound.
	Delete(ctx context.Context, id string) error

	// List returns a cursor-paginated page of entries matching q.
	List(ctx context.Context, q EntryQuery) (crud.Page[Entry], error)

	// ListByTerm returns a cursor-paginated page of entries matching q that are
	// associated with termID.
	ListByTerm(ctx context.Context, termID string, q EntryQuery) (crud.Page[Entry], error)

	// SetTerms replaces the entry's taxonomy associations with termIDs.
	SetTerms(ctx context.Context, entryID string, termIDs []string) error
}
