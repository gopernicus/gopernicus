package workers

import "context"

// WorkFunc is the function signature for one iteration of work.
// The pool calls this in a loop. Return ErrNoWork to signal idle,
// ErrWorkerShutdown to stop one worker, or ErrPoolShutdown to stop all.
type WorkFunc func(ctx context.Context) error

// Middleware wraps a WorkFunc with additional behavior.
type Middleware func(WorkFunc) WorkFunc

type contextKey string

const workerIDKey contextKey = "worker_id"

// WithWorkerID returns a context carrying the given worker ID.
func WithWorkerID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, workerIDKey, id)
}

// GetWorkerID extracts the worker ID from the context.
// Returns an empty string if no worker ID is set.
func GetWorkerID(ctx context.Context) string {
	id, _ := ctx.Value(workerIDKey).(string)
	return id
}
