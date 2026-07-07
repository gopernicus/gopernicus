package workers

import "errors"

var (
	// ErrNoWork reports that the queue is currently empty. A WorkFunc (or a
	// JobStore.Claim) returns it to tell the pool there is nothing to do; the
	// pool then backs the calling worker off to its idle interval until the
	// next tick or wake signal.
	ErrNoWork = errors.New("workers: no work available")

	// ErrWorkerShutdown stops the single worker that returns it. The rest of
	// the pool keeps running.
	ErrWorkerShutdown = errors.New("workers: worker shutdown requested")

	// ErrPoolShutdown stops the whole pool. The worker that returns it surfaces
	// it on Errors() and cancels the pool; every other worker drains its
	// in-flight iteration and exits, then Run returns.
	ErrPoolShutdown = errors.New("workers: pool shutdown requested")
)
