package workers

import (
	"context"
	"errors"
	"sync"

	"github.com/gopernicus/gopernicus/sdk/tracing"
)

// TracingMiddleware wraps each pool iteration in a worker.process span on the
// given Tracer, tagging it with the worker.id attribute (when set) and recording
// any error the iteration returns. ErrNoWork is an error like any other here; a
// caller that does not want backoff signals traced can order this middleware
// accordingly. It is a pool-level Middleware, distinct from the Runner's
// WithTracer (which traces the claim/process/complete/fail lifecycle).
func TracingMiddleware(tracer tracing.Tracer) Middleware {
	return func(next WorkFunc) WorkFunc {
		return func(ctx context.Context) error {
			ctx, span := tracer.StartSpan(ctx, "worker.process")
			defer span.Finish()

			if id := WorkerIDFromContext(ctx); id != "" {
				span.SetAttributes(tracing.StringAttribute("worker.id", id))
			}

			err := next(ctx)
			if err != nil {
				span.RecordError(err)
			}

			return err
		}
	}
}

// ConsecutiveErrorShutdown returns middleware that stops a worker after it sees
// count consecutive errors (ErrNoWork does not count). The per-worker counter
// resets on any successful iteration or ErrNoWork. Each worker is tracked
// independently by its worker ID.
func ConsecutiveErrorShutdown(count int) Middleware {
	var mu sync.Mutex
	failures := make(map[string]int)

	return func(next WorkFunc) WorkFunc {
		return func(ctx context.Context) error {
			err := next(ctx)

			workerID := WorkerIDFromContext(ctx)

			mu.Lock()
			defer mu.Unlock()

			if err != nil && !errors.Is(err, ErrNoWork) {
				failures[workerID]++
				if failures[workerID] >= count {
					return ErrWorkerShutdown
				}
				return err
			}

			failures[workerID] = 0
			return err
		}
	}
}
