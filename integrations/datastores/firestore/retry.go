package firestore

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// defaultMinBackoff is retry's backoff floor and initial cap when
// RetryPolicy.MinBackoff is unset (<= 0).
const defaultMinBackoff = 100 * time.Millisecond

// RetryPolicy configures Open's boot-connectivity retry. The zero value means no
// retries — Open constructs the client and returns without a round-trip; set
// Attempts > 1 to opt into eager boot validation. It governs ONLY the boot
// connectivity check: no read or write is ever auto-retried by the connector.
type RetryPolicy struct {
	// Attempts is the maximum number of tries. <= 1 means a single try (no retry).
	Attempts int
	// MinBackoff is the backoff floor and the initial cap. <= 0 defaults to 100ms.
	MinBackoff time.Duration
	// MaxBackoff caps the backoff's exponential growth. < MinBackoff collapses to
	// MinBackoff (a fixed backoff, no growth).
	MaxBackoff time.Duration
}

// retry runs fn up to policy.Attempts times, sleeping a full-jitter backoff —
// a uniform random duration in [MinBackoff, cap] — between failures. The cap
// starts at MinBackoff and doubles each retry up to MaxBackoff. A cancelled ctx
// aborts immediately with ctx.Err(); otherwise fn's last error is returned once
// Attempts is exhausted. This helper is duplicated from the turso/pgxdb
// connectors by design: sdk is stdlib-vocabulary, not connector plumbing.
//
// One deliberate difference from those two: a PERMANENT error stops the loop
// immediately (see permanent). The SQL connectors retry every boot failure
// because a DSN mistake and an unreachable host are hard to tell apart in a
// driver error, but Firestore classifies its own failures — a denied service
// account or a malformed project answers PermissionDenied/Unauthenticated/
// InvalidArgument, and MapError has already turned that into an sdk sentinel by
// the time it reaches here. Sleeping five backoffs over a credential that will
// never work delays the boot failure and buries its cause under a retry count.
func retry(ctx context.Context, policy RetryPolicy, fn func(context.Context) error) error {
	attempts := policy.Attempts
	if attempts < 1 {
		attempts = 1
	}
	lo := policy.MinBackoff
	if lo <= 0 {
		lo = defaultMinBackoff
	}
	hi := policy.MaxBackoff
	if hi < lo {
		hi = lo
	}

	window := lo
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = fn(ctx); err == nil {
			return nil
		}
		if permanent(err) {
			return err
		}
		if attempt == attempts-1 {
			break
		}
		wait := lo + time.Duration(rand.Int64N(int64(window-lo)+1))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if window < hi {
			if window *= 2; window > hi {
				window = hi
			}
		}
	}
	return err
}

// permanent reports whether err is a condition no amount of waiting fixes: a
// credential the database refuses (sdk.ErrForbidden, sdk.ErrUnauthorized) or a
// request it will never accept (sdk.ErrInvalidInput — a bad project or database
// id). Everything else, including sdk.ErrUnavailable and a deadline, is the
// orchestration race the retry exists for.
func permanent(err error) bool {
	return errors.Is(err, sdk.ErrForbidden) ||
		errors.Is(err, sdk.ErrUnauthorized) ||
		errors.Is(err, sdk.ErrInvalidInput)
}
