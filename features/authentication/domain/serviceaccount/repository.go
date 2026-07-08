package serviceaccount

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// ServiceAccountRepository persists machine identities. Implemented by feature
// store adapters (features/authentication/stores/turso) or any host-provided
// implementation (see the storetest reference).
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for an unknown id → errs.ErrNotFound.
//   - Update for an unknown id → errs.ErrNotFound.
//   - Delete for an unknown id → errs.ErrNotFound.
//
// List is crud-typed (design §9). Ordering is pinned (plan-cut amendment): a
// zero-value ListRequest.Order means the store's default, which is
// ORDER BY created_at DESC, id DESC — the id tiebreak is contractual, so pages
// stay stable when several accounts share a created_at (the storetest collision
// case asserts identical order AND NextCursor across implementations).
type ServiceAccountRepository interface {
	// Create persists a new service account.
	Create(ctx context.Context, sa ServiceAccount) (ServiceAccount, error)
	// Get returns the account for id, or errs.ErrNotFound.
	Get(ctx context.Context, id string) (ServiceAccount, error)
	// List returns a cursor-paginated page ordered created_at DESC, id DESC.
	List(ctx context.Context, req crud.ListRequest) (crud.Page[ServiceAccount], error)
	// Update replaces the account for id; unknown → errs.ErrNotFound.
	Update(ctx context.Context, id string, sa ServiceAccount) (ServiceAccount, error)
	// Delete removes the account for id; unknown → errs.ErrNotFound.
	Delete(ctx context.Context, id string) error
}
