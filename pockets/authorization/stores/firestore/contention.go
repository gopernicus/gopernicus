package firestore

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

// Only definite aborted transaction attempts may retry. Transport failures can
// hide a successful commit and must be returned to the caller without replay.
const (
	contentionMaxRetries = 6
	contentionBaseDelay  = 5 * time.Millisecond
	contentionMaxDelay   = 250 * time.Millisecond
)

// retryContention runs fn until it stops reporting a retryable contention
// conflict, with a bounded, backing-off, JITTERED wait.
//
// terminal distinguishes a policy refusal from a transaction read failure that
// propagated through a guard. Classification happens before vendor retry identity
// is removed; policy errors retain their original identity.
//
// It stops on success, on a terminal or non-retryable error, on exhausting the
// budget (the caller then sees sdk.ErrConflict and may retry the workflow
// itself), or on cancellation. Policy errors return unchanged; store errors go
// through firestoredb.MapError to expose the sdk sentinel vocabulary.
func retryContention(ctx context.Context, fn func() (err error, terminal bool)) error {
	delay := contentionBaseDelay
	for attempt := 0; ; attempt++ {
		err, terminal := fn()
		if err == nil {
			return nil
		}
		if terminal {
			return err
		}
		if !retryableConflict(err) || attempt >= contentionMaxRetries {
			mapped := firestoredb.MapError(err)
			if retryableConflict(err) {
				return fmt.Errorf("%w: %w", mutations.ErrConcurrentMutation, mapped)
			}
			return mapped
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

// retryableConflict excludes stable domain refusals from the conflict class.
// A shared SDK conflict sentinel alone does not prove a transaction aborted.
func retryableConflict(err error) bool {
	if !errors.Is(err, sdk.ErrConflict) {
		return false
	}
	switch {
	case errors.Is(err, mutations.ErrInvalidCommand),
		errors.Is(err, mutations.ErrSemanticConflict),
		errors.Is(err, mutations.ErrInvariantBlocked),
		errors.Is(err, sdk.ErrForbidden),
		errors.Is(err, sdk.ErrUnauthorized),
		errors.Is(err, errTargetRelationConflict):
		return false
	}
	var detached *abortedTransaction
	return firestoredb.IsTransactionAborted(err) || errors.As(err, &detached)
}

// guardReadError identifies contention returned by a view read. A host's own
// conflict refusal is not retryable merely because it shares sdk.ErrConflict.
type guardReadError struct{ error }

func (e *guardReadError) Unwrap() error { return e.error }

func markGuardReadError(err error) error {
	if !retryableConflict(err) {
		return err
	}
	return &guardReadError{err}
}

func retryableGuardRead(err error) bool {
	var readErr *guardReadError
	return retryableConflict(err) && errors.As(err, &readErr)
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

// Detach the status only after identifying a definite abort: callback-visible
// contention retries in the outer jitter loop, while commit aborts remain the
// vendor's responsibility. The private marker preserves origin across mapping.
type abortedTransaction struct{ error }

func (e *abortedTransaction) Unwrap() error { return e.error }

func detachVendorRetry(err error) error {
	if err == nil || !retryableConflict(err) {
		return err
	}
	return &abortedTransaction{fmt.Errorf("authorization firestore store: contention (%s): %w", err, sdk.ErrConflict)}
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
