// Package workers provides generic polling pools, claim-based runners and
// composable middleware. A Pool runs arbitrary WorkFuncs; Runner and FencedRunner
// additionally drive user-supplied queue stores. The jobs pocket is one consumer,
// not a requirement. Durability depends on the store and application behavior.
// Worker middleware wraps polling iterations; JobMiddleware wraps processing
// after claim. Gates before claim may return ErrNoWork to back off. Job gates
// return DeferUntil or Reject so unprocessed work is not marked complete.
package workers

import "context"

// WorkFunc runs one iteration of work. The pool calls it in a loop on each
// worker goroutine. Returning ErrNoWork backs the worker off to its idle
// interval; ErrWorkerShutdown stops that worker; ErrPoolShutdown stops the
// whole pool. Any other error is counted and logged and the worker keeps
// polling at its active interval; a nil return does the same without counting
// an error.
type WorkFunc func(ctx context.Context) error

// Middleware wraps a WorkFunc with additional behavior. The first middleware
// passed to WithMiddleware becomes the outermost wrapper. It may be called by
// multiple workers concurrently; synchronize shared mutable state. A wrapper
// may pass a derived context or return without calling next. Nil means a
// successful iteration, ErrNoWork means idle, and shutdown errors keep their scope.
type Middleware func(WorkFunc) WorkFunc

type contextKey string

const workerIDKey contextKey = "worker_id"

// WithWorkerID returns a context carrying the given worker ID. The pool sets it
// on the context handed to every WorkFunc call.
func WithWorkerID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, workerIDKey, id)
}

// WorkerIDFromContext returns the worker ID carried by ctx, or "" if none is set.
func WorkerIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(workerIDKey).(string)
	return id
}
