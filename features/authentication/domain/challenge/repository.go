package challenge

import (
	"context"
	"time"
)

// Repository persists challenges with atomic replace and consume operations
// (design §3.2). Implemented by feature store adapters (features/authentication/
// stores/turso, .../pgx) or any host-provided implementation (see the storetest
// reference). The port doc comments are the spec; the storetest conformance suite
// is their executable form.
//
// The redemption operations are the whole security surface: each is ONE atomic
// step so exactly one correct concurrent request can win.
//
//   - Replace atomically deletes the prior (user, purpose) row and inserts the
//     new one, returning the stored challenge (with its assigned ID). Two active
//     rows never coexist for a (user, purpose); a colliding (purpose,
//     secret_digest) is sdk.ErrAlreadyExists.
//   - ConsumeCode resolves the live (user, purpose) row, decides expiry, selects
//     the DigestCandidate whose KeyID matches the row's ProtectorKeyID, compares
//     digests in constant time, and then EITHER counts a wrong attempt (deleting
//     the row at maxAttempts) OR consumes the row on success — all inside one
//     atomic operation. The disposition is the ConsumeOutcome; the error is
//     reserved for infrastructure failures. A correct code whose bound context
//     does not match expectedContextDigest is consumed anyway (OutcomeContext-
//     Mismatch). An empty candidate digest never matches.
//   - ConsumeToken is an atomic delete-returning by (purpose, presentedDigest):
//     an empty digest never matches (sdk.ErrNotFound), an expired row is deleted
//     and returns sdk.ErrExpired, a live row is deleted and returned. It mirrors
//     the oauthstate.Consume single-use get-and-delete contract.
//   - PurgeExpired deletes up to limit rows at or past before, returning the
//     count removed (bounded batching).
type Repository interface {
	// Replace atomically replaces the prior (user, purpose) challenge with c and
	// returns the stored row. A colliding (purpose, secret_digest) →
	// sdk.ErrAlreadyExists.
	Replace(ctx context.Context, c Challenge) (Challenge, error)
	// ConsumeCode atomically evaluates the (userID, purpose) code challenge
	// against candidates, binding expectedContextDigest, counting a wrong attempt
	// up to maxAttempts, and consuming on success. The ConsumeOutcome is
	// authoritative; error is infrastructure-only.
	ConsumeCode(ctx context.Context, userID, purpose string, candidates []DigestCandidate,
		expectedContextDigest string, maxAttempts int, now time.Time) (Consumed, ConsumeOutcome, error)
	// ConsumeToken atomically deletes and returns the (purpose, presentedDigest)
	// token row: empty → sdk.ErrNotFound, expired → sdk.ErrExpired (row deleted),
	// live → the Consumed row.
	ConsumeToken(ctx context.Context, purpose, presentedDigest string, now time.Time) (Consumed, error)
	// PurgeExpired deletes up to limit rows at or past before and returns the
	// number removed.
	PurgeExpired(ctx context.Context, before time.Time, limit int) (int, error)
}
