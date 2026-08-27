package media

import "context"

// AssetRepository is the app port for persisting asset metadata. Binary content
// is handled separately by a BlobStore. Implemented by feature store adapters
// (features/cms/stores/turso) or any host-provided implementation (see
// examples/minimal's memstore).
type AssetRepository interface {
	// Create persists asset metadata.
	Create(ctx context.Context, a Asset) (Asset, error)
	// Get returns the asset with the given id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Asset, error)
	// List returns all assets, newest first.
	List(ctx context.Context) ([]Asset, error)
	// Delete removes asset metadata by id.
	Delete(ctx context.Context, id string) error
}
