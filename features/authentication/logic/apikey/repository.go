package apikey

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// APIKeyRepository persists hashed machine credentials. Implemented by feature
// store adapters (features/authentication/stores/turso) or any host-provided
// implementation (see the storetest reference).
//
// THE PINNED GetByHash CONTRACT (design §4.1, plan-cut amendment): the store
// selects by key_hash ALONE and returns the record for ANY present row —
// revoked and expired rows INCLUDED; a zero/NULL ExpiresAt means never-expires.
// errs.ErrNotFound is returned ONLY for a genuinely-unknown hash. There is NO
// ErrExpired port sentinel: revocation and expiry are SERVICE-layer branches in
// AuthenticateAPIKey, because the service needs the record to attribute the
// blocked/failure audit event to its service account.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Create whose KeyHash collides with an existing key → errs.ErrAlreadyExists.
//   - GetByHash for an unknown hash → errs.ErrNotFound; any present row (revoked
//     or expired included) → the record.
//   - Revoke / TouchLastUsed for an unknown id → errs.ErrNotFound.
//
// ListByServiceAccount is crud-typed (design §9); ordering is pinned to
// ORDER BY created_at DESC, id DESC (the id tiebreak is contractual).
type APIKeyRepository interface {
	// Create persists a new key; a colliding KeyHash → errs.ErrAlreadyExists.
	Create(ctx context.Context, k APIKey) (APIKey, error)
	// GetByHash returns the key for keyHash regardless of revocation/expiry;
	// unknown hash → errs.ErrNotFound (see the pinned contract above).
	GetByHash(ctx context.Context, keyHash string) (APIKey, error)
	// ListByServiceAccount returns a cursor-paginated page of a service account's
	// keys, ordered created_at DESC, id DESC.
	ListByServiceAccount(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[APIKey], error)
	// Revoke marks the key revoked as of revokedAt; unknown id → errs.ErrNotFound.
	Revoke(ctx context.Context, id string, revokedAt time.Time) error
	// TouchLastUsed records that the key authenticated at usedAt; unknown id →
	// errs.ErrNotFound. Callers treat it as best-effort (a failure never fails
	// authentication).
	TouchLastUsed(ctx context.Context, id string, usedAt time.Time) error
}
