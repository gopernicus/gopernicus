package firestore

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The contention-retry discipline for EVERY write path in this store — the
// Firestore twin of the turso store's busy retry and of pgx's serialization
// retry, and it exists for the same reason: the shared conformance suite's
// concurrency family (ConcurrentClaimArbitration and its seven siblings) asserts
// that contention resolves to exactly one WINNER and one DOMAIN answer, never to
// an infrastructure error the caller has to interpret.
//
// A Firestore read-write transaction locks the documents it reads. Every write
// path here reads the aggregate it is about to change — the users document for a
// revision CAS, the identifier row it retires — so concurrent operations on ONE
// user genuinely serialize; the vendor re-runs the callback up to
// Config.MaxAttempts times (5 by default) with its own exponential backoff, and
// past that the server's Aborted — or, on the emulator, a "Transaction lock
// timeout" — arrives as sdk.ErrConflict. That is an infrastructure conflict, not
// a domain outcome: nothing committed, and re-running the whole operation is
// safe because every callback here re-READS before it decides.
//
// Retryability is decided by the error's IDENTITY, never by where it surfaced: a
// mapped Aborted raised by a transactional READ and the same condition reported
// by the COMMIT are one condition and get one answer.
const (
	contentionMaxRetries = 6
	contentionBaseDelay  = 5 * time.Millisecond
	contentionMaxDelay   = 250 * time.Millisecond
)

// errStaleAuthRevision is the revision-CAS refusal every credential-policy
// mutation shares: the user's auth_revision is not the value the caller expected,
// so the safe-looking method set it decided against is out of date. The port
// contract is sdk.ErrConflict (storetest's ApplyVerifiedChangeRevisionConflict
// checks exactly that), and it is a NAMED sentinel rather than a bare
// sdk.ErrConflict so retryableConflict can tell it apart from a lost race:
// re-reading would find the same advanced revision, so retrying would turn a
// deterministic refusal into a spin.
var errStaleAuthRevision = fmt.Errorf("authentication firestore store: the user's auth_revision advanced since the caller read it: %w", sdk.ErrConflict)

// errClaimLost is what a write path reports when a uniqueness key it tried to
// TAKE was taken by someone else between its read phase and its commit — a lost
// claim, a duplicate document id, either way nothing was written.
//
// It exists to keep the vendor's own AlreadyExists text OUT of the answer. That
// message names the document that lost: "…/identifier_claims/<64 hex>". The
// collection layout is an implementation detail no caller should couple to, and
// the id is a SHA-256 fingerprint of the very address, token, or secret digest
// the operation was about — an attacker-supplied value, echoed back through a
// host's error log at exactly the rate a probe can drive it. retryTransact maps
// every sdk.ErrAlreadyExists leaving a transaction onto this, so the sentinel
// the port contract names still matches and the fingerprint stays inside the
// store.
var errClaimLost = fmt.Errorf("authentication firestore store: a uniqueness key this operation had to take is already held; nothing was written: %w", sdk.ErrAlreadyExists)

// stableConflicts are the conflict-shaped answers that are DECISIONS, not races
// (see retryableConflict). Adding a domain sentinel that wraps sdk.ErrConflict
// here is not optional.
var stableConflicts = []error{
	errStaleAuthRevision,
	session.ErrRotationConflict,
	identifier.ErrVerificationRequired,
	credential.ErrNoLoginMethod,
	credential.ErrNoRecoveryMethod,
	credential.ErrInsufficientRecovery,
	credential.ErrRecoveryRequiresNonPSTN,
	credential.ErrInsufficientAssurance,
}

// retryContention runs fn until it stops reporting a retryable contention
// conflict, with a bounded, backing-off, JITTERED wait. It stops on success, on
// a non-retryable error, on exhausting the budget (the caller then sees
// sdk.ErrConflict and may retry the workflow itself), or on cancellation. The
// final error goes through firestoredb.MapError once — idempotent for anything
// already mapped, and the guarantee that no raw vendor status leaves this store
// without an sdk sentinel.
func retryContention(ctx context.Context, fn func() error) error {
	delay := contentionBaseDelay
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if !retryableConflict(err) || attempt >= contentionMaxRetries {
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
//   - It must be sdk.ErrConflict. Anything else — a lost claim
//     (sdk.ErrAlreadyExists), a missing row, a validator refusal, a cancelled
//     context, an unavailable database — is an ANSWER or a failure, and
//     re-running it would only repeat it.
//   - It must not be one of the STABLE conflict sentinels — a deterministic
//     refusal that a re-read would only reproduce, so retrying it turns an
//     answer into a spin and then into a mangled error (detachVendorRetry
//     re-roots a retryable conflict on sdk.ErrConflict ALONE, which would drop
//     the sentinel the caller is meant to branch on).
//
// The rule for that list, stated once because it is the one a future path gets
// wrong: EVERY domain sentinel that wraps sdk.ErrConflict belongs in it. Most of
// this pocket's domain answers wrap a different sentinel and are already
// excluded by the first rule — passwordless.ErrRedemption (Unauthorized),
// session.ErrUserNotActive (Forbidden), sdk.ErrExpired, sdk.ErrNotFound,
// sdk.ErrAlreadyExists — but the credential POLICY errors and
// identifier.ErrVerificationRequired do wrap sdk.ErrConflict, and
// session.ErrRotationConflict is a bare sentinel today that reads like one.
// They are named below whether or not a callback in this store returns them
// today, because the cost of naming a sentinel that never arrives is zero and
// the cost of missing one is a domain refusal reported as infrastructure
// contention. contention_test.go enumerates them one at a time.
//
// Everything left is genuine contention: a mapped Aborted from a transactional
// read, a lost commit race, or an emulator lock timeout.
func retryableConflict(err error) bool {
	if !errors.Is(err, sdk.ErrConflict) {
		return false
	}
	for _, stable := range stableConflicts {
		if errors.Is(err, stable) {
			return false
		}
	}
	return true
}

// retryTransact is the shape every write path takes: ONE db.Transact under the
// contention retry. Each callback re-reads before it decides, so re-running it
// is safe.
func retryTransact(ctx context.Context, db *firestoredb.DB, fn func(ctx context.Context) error) error {
	err := retryContention(ctx, func() error {
		return db.Transact(ctx, func(ctx context.Context) error {
			return detachVendorRetry(fn(ctx))
		})
	})
	// The last error path, and the one place a lost claim can be recognized:
	// Firestore evaluates every Create precondition at COMMIT, so a claim lost
	// to a concurrent writer arrives here as the vendor's AlreadyExists with the
	// losing document's path in its message. errClaimLost keeps the sentinel and
	// drops the fingerprint.
	if err != nil && errors.Is(err, sdk.ErrAlreadyExists) {
		return errClaimLost
	}
	return err
}

// detachVendorRetry re-roots a retryable contention conflict on sdk.ErrConflict
// ALONE, dropping the gRPC status the connector's MapError keeps in the chain.
// The message is preserved; only the status leaves.
//
// Without it BOTH retry loops fire on the same error: the vendor's, whose gate
// asks status.FromError about the value a callback returned (and MapError
// preserves that status precisely so a host's own callback gets that behavior),
// and this store's. Nesting them is not "more retries", it is WORSE retries —
// the vendor's backoff is NOT jittered, so N contenders re-run in the same order
// and the losers of round one lose round two. The authorization train measured
// exactly that starvation on the emulator and fixed it with this seam.
//
// So the rule is: a conflict this store's callback can SEE ends the vendor's
// loop and is re-run HERE, with jitter and with the whole operation reset. A
// conflict the callback cannot see — a losing COMMIT — is still the vendor's,
// and it retries that as it always did before falling through to this loop.
//
// The cause is formatted with %s and NOT %w, and that is the whole mechanism
// rather than a formatting slip: %w would put the mapped gRPC status back in
// the chain, the vendor's isAborted (status.FromError → errors.As, which walks
// Unwrap) would find it again, and both loops would fire on one error exactly
// as before. TestDetachVendorRetryDropsTheAbortedStatus pins that property, so
// a later "fix" to %w fails loudly instead of quietly restoring the starvation.
// The message is preserved verbatim, which is what a log needs.
func detachVendorRetry(err error) error {
	if err == nil || !retryableConflict(err) {
		return err
	}
	return fmt.Errorf("authentication firestore store: contention (%s): %w", err, sdk.ErrConflict)
}

// jitter spreads a backoff over [d/2, d). Without it every contender waits the
// SAME interval and they collide again in the same order, which is how a wide
// fan-out starves one writer for good instead of draining the queue.
func jitter(d time.Duration) time.Duration {
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(rand.Int64N(int64(half)))
}
