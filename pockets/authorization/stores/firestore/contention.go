package firestore

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// The contention-retry discipline for the atomic mutation path — the Firestore
// twin of the turso store's busy retry and of pgx's serialization retry, and it
// exists for the same reason: the shared conformance suite's concurrency cases
// (ConcurrentReplayStorm, ConcurrentSingleWinner, ConcurrentReceiptRevisionForensics)
// assert ZERO spurious errors, so contention must surface as WAITING, never as a
// failure the caller has to interpret.
//
// A Firestore read-write transaction locks the documents and query ranges it
// reads. Every command on one scope reads that scope's anchor and its resource's
// rows, so N concurrent commands on ONE resource genuinely serialize; the vendor
// re-runs the callback up to Config.MaxAttempts times (5 by default) with its own
// exponential backoff, and past that the server's Aborted — or, on the emulator,
// a "Transaction lock timeout" — arrives as sdk.ErrConflict. That is an
// infrastructure conflict, not a domain outcome: nothing committed, no receipt
// was minted, and re-running the WHOLE apply is safe because Apply is idempotent
// by MutationID (a re-run either replays a now-committed receipt or re-evaluates
// against current state).
//
// The loop only re-runs a VENDOR failure. A callback refusal — a guard denial, a
// payload mismatch, a stale revision, a cancelled context — is the answer, and
// mutation.ErrStaleRevision wraps sdk.ErrConflict too, so retrying on the
// sentinel alone would turn a deterministic refusal into a spin.
const (
	contentionMaxRetries = 6
	contentionBaseDelay  = 5 * time.Millisecond
	contentionMaxDelay   = 250 * time.Millisecond
)

// retryContention runs fn until it stops reporting a retryable vendor conflict,
// with a bounded, backing-off wait. fn reports (fromVendor, err): fromVendor is
// true only when the transaction failed WITHOUT the callback refusing it, which
// is the single case a retry can clear. It stops on success, on a callback
// refusal, on any non-conflict error, on exhausting the budget (the caller then
// sees sdk.ErrConflict and may retry the workflow itself), or on cancellation.
func retryContention(ctx context.Context, fn func() (fromVendor bool, err error)) error {
	delay := contentionBaseDelay
	for attempt := 0; ; attempt++ {
		fromVendor, err := fn()
		if err == nil || !fromVendor || !errors.Is(err, sdk.ErrConflict) || attempt >= contentionMaxRetries {
			return err
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
