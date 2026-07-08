package oauthstate

import "context"

// StateRepository persists one-time OAuth flow secrets keyed by token.
// Implemented by feature store adapters (features/authentication/stores/turso) or any
// host-provided implementation (see the storetest reference).
//
// Consume contract (pinned, design §3 — the storetest conformance suite
// executes these): Consume is a single-use get-and-delete. The row is deleted
// REGARDLESS of expiry, so:
//   - Consume of a live token returns the State and deletes the row.
//   - Consume of an expired token DELETES the row and returns errs.ErrExpired.
//   - A second Consume of any token → errs.ErrNotFound (it is gone).
//   - Consume of an unknown token → errs.ErrNotFound.
//
// Stores implement it as DELETE … RETURNING (the jobs queue.go precedent), so
// the delete and the expiry decision are one atomic step.
type StateRepository interface {
	// Create persists a new flow state.
	Create(ctx context.Context, s State) (State, error)
	// Consume atomically deletes and returns the state for token. Expired →
	// errs.ErrExpired (row deleted); unknown or already-consumed →
	// errs.ErrNotFound.
	Consume(ctx context.Context, token string) (State, error)
}
