package workers

import "errors"

var (
	// ErrDeferralUnsupported means a gate requested Defer on a store without it.
	ErrDeferralUnsupported = errors.New("workers: store does not support deferral")
	// ErrInvalidDeferral means a gate did not supply a future availability time.
	ErrInvalidDeferral = errors.New("workers: deferral time must be in the future")
	// ErrAlreadyRun reports a repeated or concurrent Run on the same Pool.
	ErrAlreadyRun = errors.New("workers: pool already run")

	// ErrNoWork reports that the queue is currently empty. A WorkFunc (or a
	// JobStore.Claim) returns it to tell the pool there is nothing to do; the
	// pool then backs the calling worker off to its idle interval until the
	// next tick or wake signal.
	ErrNoWork = errors.New("workers: no work available")

	// ErrWorkerShutdown stops the single worker that returns it. The rest of
	// the pool keeps running.
	ErrWorkerShutdown = errors.New("workers: worker shutdown requested")

	// ErrPoolShutdown stops the whole pool. The worker that returns it surfaces
	// it through Run and cancels the pool; every other worker drains its
	// in-flight iteration and exits before Run returns the error.
	ErrPoolShutdown = errors.New("workers: pool shutdown requested")
)
