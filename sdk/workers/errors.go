package workers

import "errors"

var (
	// ErrWorkerShutdown signals that an individual worker should stop.
	// Return this from middleware or a WorkFunc to gracefully stop a single worker.
	ErrWorkerShutdown = errors.New("worker shutdown requested")

	// ErrPoolShutdown signals that the entire pool should stop.
	// Return this from middleware or a WorkFunc to trigger pool-wide shutdown.
	ErrPoolShutdown = errors.New("pool shutdown requested")

	// ErrNoWork indicates no tasks are available for processing.
	// Return this from a WorkFunc (or JobStore.Checkout) when the queue is empty.
	// This triggers the pool's adaptive polling to switch to idle interval.
	ErrNoWork = errors.New("no work available")
)
