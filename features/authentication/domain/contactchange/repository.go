package contactchange

import (
	"context"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
)

// Repository persists the pending-value flow state of an identifier add/change
// (design §2.4). Implemented by feature store adapters (features/authentication/
// stores/turso, .../pgx) or any host-provided implementation (see the storetest
// reference). The port doc comments are the spec; the storetest conformance suite
// is their executable form.
//
// Contract (pinned, design §2.4 — the storetest conformance suite executes these):
//   - Create is an atomic replace for (UserID, Kind): it deletes any existing
//     pending change for that pair before inserting the new one, so at most one is
//     active per (user, kind). It returns the stored row (with its assigned ID).
//   - Consume is a single-use get-and-delete keyed by (userID, kind). The row is
//     deleted REGARDLESS of expiry, so:
//       - Consume of a live pending change returns it and deletes the row.
//       - Consume of an expired pending change DELETES the row and returns
//         sdk.ErrExpired.
//       - A missing or already-consumed pending change → sdk.ErrNotFound.
//     Stores implement it as DELETE … RETURNING so the delete and the expiry
//     decision are one atomic step (the oauthstate.Consume precedent).
type Repository interface {
	// Create atomically replaces any prior (UserID, Kind) pending change with p and
	// returns the stored row.
	Create(ctx context.Context, p PendingChange) (PendingChange, error)
	// Consume atomically deletes and returns the (userID, kind) pending change:
	// live → the PendingChange, expired → sdk.ErrExpired (row deleted), missing or
	// already-consumed → sdk.ErrNotFound.
	Consume(ctx context.Context, userID string, kind identifier.Kind) (PendingChange, error)
}
