package firestore

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/sdk"
)

// The contention-retry discipline for EVERY write path in this store — the
// Firestore twin of the turso store's busy retry and of pgx's serialization
// retry, and it exists for the same reason: the shared conformance suite's
// concurrency cases (ConcurrentReplayStorm, ConcurrentSingleWinner,
// ConcurrentReceiptRevisionForensics) assert ZERO spurious errors, so contention
// must surface as WAITING, never as a failure the caller has to interpret.
//
// A Firestore read-write transaction locks the documents and query ranges it
// reads. Every command on one scope reads that scope's anchor and its resource's
// rows, so N concurrent commands on ONE resource genuinely serialize; the vendor
// re-runs the callback up to Config.MaxAttempts times (5 by default) with its own
// exponential backoff, and past that the server's Aborted — or, on the emulator,
// a "Transaction lock timeout" — arrives as sdk.ErrConflict. That is an
// infrastructure conflict, not a domain outcome: nothing committed, and re-running
// the whole operation is safe because every callback here re-READS before it
// decides (Apply is additionally idempotent by MutationID).
//
// Retryability is decided by the error's IDENTITY, never by where it surfaced.
// The earlier rule — "retry only a failure the callback did not return" — treated
// a mapped Aborted raised by a transactional READ as terminal, because it reached
// the loop through the callback; the same condition reported by the COMMIT was
// retried. One condition, two answers. The classification below asks what the
// error IS instead.
const (
	contentionMaxRetries = 6
	contentionBaseDelay  = 5 * time.Millisecond
	contentionMaxDelay   = 250 * time.Millisecond
)

// retryContention runs fn until it stops reporting a retryable contention
// conflict, with a bounded, backing-off, JITTERED wait.
//
// fn reports (err, terminal). terminal is the caller's own classification for an
// error whose identity cannot carry it — today only "the guard returned this",
// which is an authorization ANSWER even when it happens to wrap sdk.ErrConflict.
// Everything else is judged by retryableConflict.
//
// It stops on success, on a terminal or non-retryable error, on exhausting the
// budget (the caller then sees sdk.ErrConflict and may retry the workflow
// itself), or on cancellation. The final error goes through firestoredb.MapError
// once — idempotent for anything already mapped, and the guarantee that no raw
// vendor status leaves this store without an sdk sentinel.
func retryContention(ctx context.Context, fn func() (err error, terminal bool)) error {
	delay := contentionBaseDelay
	for attempt := 0; ; attempt++ {
		err, terminal := fn()
		if err == nil {
			return nil
		}
		if terminal || !retryableConflict(err) || attempt >= contentionMaxRetries {
			return firestoredb.MapError(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jitter(delay)):
		}
		if delay *= 2; delay > contentionMaxDelay {
			delay = contentionMaxDelay
		}
	}
}

// retryableConflict reports whether err is a contention conflict a re-run can
// clear. Two rules, in this order:
//
//   - It must be sdk.ErrConflict. Anything else — a guard denial, a validator
//     refusal, a cancelled context, an unavailable database — is an answer or a
//     failure, and re-running it would only repeat it.
//   - It must not be one of the port's STABLE domain sentinels. Three of them
//     live in the conflict class: mutation.ErrStaleRevision (the caller's
//     ExpectedRevision does not match — it never will on a re-read),
//     mutation.ErrPayloadMismatch (the MutationID is taken by a different
//     payload — permanently), and mutation.ErrInvalidCommand (a shape refusal).
//     Retrying any of them would turn a deterministic refusal into a spin. The
//     raw write path adds errTargetRelationConflict, the reconciliation's
//     one-relation-per-subject refusal, for the same reason.
//
// Everything left is genuine contention: a mapped Aborted from a transactional
// read, a lost commit race, an emulator lock timeout, or the store's own
// receipt-race signal.
func retryableConflict(err error) bool {
	if !errors.Is(err, sdk.ErrConflict) {
		return false
	}
	switch {
	case errors.Is(err, mutation.ErrStaleRevision),
		errors.Is(err, mutation.ErrPayloadMismatch),
		errors.Is(err, mutation.ErrInvalidCommand),
		errors.Is(err, errTargetRelationConflict):
		return false
	}
	return true
}

// retryTransact is the shape every RAW write path takes: one db.Transact under
// the contention retry, with no caller-owned terminal classification (a raw
// write has no guard). Each callback re-reads before it decides, so re-running
// it is safe.
func retryTransact(ctx context.Context, db *firestoredb.DB, fn func(ctx context.Context) error) error {
	return retryContention(ctx, func() (error, bool) {
		return db.Transact(ctx, func(ctx context.Context) error {
			return detachVendorRetry(fn(ctx))
		}), false
	})
}

// detachVendorRetry re-roots a retryable contention conflict on sdk.ErrConflict
// ALONE, dropping the gRPC status the connector's MapError keeps in the chain.
// The message is preserved; only the status leaves.
//
// Without it BOTH retry loops fire on the same error: the vendor's, whose gate
// asks status.FromError about the value a callback returned (transaction.go:245,
// and MapError preserves that status precisely so a host's own callback gets
// that behavior), and this store's. Nesting them is not "more retries", it is
// WORSE retries — the vendor's backoff is NOT jittered, so N contenders on one
// scope re-run in the same order and the losers of round one lose round two.
// That is the effect A4b measured and fixed: a fixed backoff starved a
// twenty-way ConcurrentReceiptRevisionForensics storm past every budget, and
// jitter drained it. Re-introducing an un-jittered inner loop under the jittered
// outer one reproduced exactly that starvation on the emulator.
//
// So the rule is: a conflict this store's callback can SEE ends the vendor's
// loop and is re-run HERE, with jitter and with the whole apply reset. A
// conflict the callback cannot see — a losing COMMIT — is still the vendor's,
// and it retries that as it always did before falling through to this loop.
func detachVendorRetry(err error) error {
	if err == nil || !retryableConflict(err) {
		return err
	}
	return fmt.Errorf("authorization firestore store: contention (%s): %w", err, sdk.ErrConflict)
}

// jitter spreads a backoff over [d/2, d). Without it every contender on one
// scope waits the SAME interval and they collide again in the same order, which
// is how a wide fan-out starves one writer for good instead of draining the
// queue: the losers of round one are the losers of round two. Randomizing the
// wait breaks that lockstep, which is the whole reason this loop can drain a
// twenty-way storm at all.
func jitter(d time.Duration) time.Duration {
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(rand.Int64N(int64(half)))
}
