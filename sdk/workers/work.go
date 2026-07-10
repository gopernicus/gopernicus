// Package workers is the sdk facility for background job processing: a Pool of
// worker goroutines, a generic Runner[T] that drives a durable JobStore through
// the claim -> process -> complete/fail lifecycle, and composable Middleware.
//
// Deliberately no tracing. An earlier Runner WithTracer option and a pool-level
// TracingMiddleware were removed at sdk-layering P2 (2026-07-10) as YAGNI: they
// had zero callers repo-wide, and the local-interface alternative is
// unimplementable because interface satisfaction compares SpanFinisher return
// types by identity, so an otel tracer could never satisfy a workers-local
// tracer port. Traced workers reintroduce as a workers-decorator in the tracing
// CAPABILITY (tracing importing workers is a legal capability -> foundation
// edge); the trigger is the first host that actually wants them.
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
