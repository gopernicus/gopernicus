package session

import (
	"context"
	"errors"
)

// ErrRotationConflict is the compare-and-swap sentinel returned by Rotate and
// ConsumeGrace when the row's state no longer matches the caller's expectation
// (zero rows affected): the current refresh hash has moved on, or the grace slot
// was already consumed. The service re-resolves the presented token once to tell
// a benign concurrent-rotation race from genuine token reuse (§1.3). Checked with
// errors.Is; it is distinct from sdk.ErrNotFound so a lost CAS never looks like a
// missing row.
var ErrRotationConflict = errors.New("session: refresh rotation conflict")

// SessionRepository persists revocable session anchors keyed by their app-minted
// ID, with compare-and-swap refresh-token rotation. Implemented by feature store
// adapters (features/authentication/stores/turso, .../pgx) or any host-provided
// implementation (see the storetest reference). The service hashes every refresh
// token (SHA-256) before it reaches this port, so the hashes the store persists
// and matches on are opaque to it; the store does no hashing itself.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - Get for an unknown id → sdk.ErrNotFound; for a present row whose ExpiresAt
//     is at or before the read time → sdk.ErrExpired (expired-at-read: the store
//     reports expiry rather than returning a dead session; it MAY delete the row).
//     Get backs RequireLiveSession.
//   - GetByRefreshHash is ONE current-or-previous scan
//     (WHERE refresh_token_hash = ? OR previous_refresh_token_hash = ?): it
//     returns the row VERBATIM — it applies NO expiry filter (expiry is a service
//     branch, mirroring the apikey.GetByHash verbatim-return rule) — and reports
//     which slot matched (RefreshMatchCurrent / RefreshMatchPrevious). An
//     empty/NULL hash NEVER matches (a fresh row's NULL previous slot must never
//     be returned for GetByRefreshHash("")). No match → sdk.ErrNotFound.
//   - Rotate is compare-and-swap: it updates the row ONLY when its current
//     refresh_token_hash still equals expectedCurrentHash, setting
//     previous_refresh_token_hash ← expectedCurrentHash, previous_used ← false,
//     rotation_count ← rotation_count + 1, and refresh_token_hash ← newHash. It
//     NEVER touches ExpiresAt (fixed horizon, D2). Zero rows affected →
//     ErrRotationConflict.
//   - ConsumeGrace is compare-and-swap on the previous slot: it flips
//     previous_used ← true ONLY when previous_refresh_token_hash equals
//     previousHash AND previous_used is still false. Zero rows affected →
//     ErrRotationConflict.
//   - Delete for an unknown id → sdk.ErrNotFound.
//   - DeleteByUser is bulk and idempotent: it removes every session for the user
//     and returns nil even when none exist (never sdk.ErrNotFound). It is the
//     logout-everywhere primitive (a password change revokes all sessions).
type SessionRepository interface {
	// Create persists a new session. A colliding refresh_token_hash surfaces as
	// sdk.ErrAlreadyExists (MapError-routed) via the store's unique index.
	Create(ctx context.Context, s Session) (Session, error)
	// Get returns the live session for id: unknown → sdk.ErrNotFound,
	// present-but-expired → sdk.ErrExpired.
	Get(ctx context.Context, id string) (Session, error)
	// GetByRefreshHash returns the row (verbatim, no expiry filter) whose current
	// or previous refresh-token hash equals hash, reporting which slot matched. An
	// empty hash never matches; no match → sdk.ErrNotFound.
	GetByRefreshHash(ctx context.Context, hash string) (Session, RefreshMatch, error)
	// Rotate compare-and-swaps the live refresh token (see the contract above);
	// zero rows → ErrRotationConflict.
	Rotate(ctx context.Context, id, expectedCurrentHash, newHash string) error
	// ConsumeGrace compare-and-swaps the previous slot's consumed flag (see the
	// contract above); zero rows → ErrRotationConflict.
	ConsumeGrace(ctx context.Context, id, previousHash string) error
	// Delete removes the session for id; unknown → sdk.ErrNotFound.
	Delete(ctx context.Context, id string) error
	// DeleteByUser removes every session belonging to userID. It is bulk and
	// idempotent: zero matching rows returns nil, never sdk.ErrNotFound.
	DeleteByUser(ctx context.Context, userID string) error
}
