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
// passed to WithMiddleware becomes the outermost wrapper.
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
