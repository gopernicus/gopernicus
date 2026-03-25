package workers

import (
	"context"
	"errors"
	"sync"
)

// TracingMiddleware creates spans for each work iteration using the provided Tracer.
// The span includes worker.id attribute and records any errors.
func TracingMiddleware(tracer Tracer) Middleware {
	return func(next WorkFunc) WorkFunc {
		return func(ctx context.Context) error {
			ctx, span := tracer.StartSpan(ctx, "worker.process")
			defer span.Finish()

			if id := GetWorkerID(ctx); id != "" {
				span.SetAttributes(StringAttribute("worker.id", id))
			}

			err := next(ctx)
			if err != nil {
				span.RecordError(err)
			}

			return err
		}
	}
}

// ConsecutiveErrorShutdown stops a worker after it encounters count consecutive
// errors (excluding ErrNoWork). The error counter resets on any successful
// iteration or ErrNoWork.
func ConsecutiveErrorShutdown(count int) Middleware {
	errorCounts := make(map[string]int)
	var mu sync.Mutex

	return func(next WorkFunc) WorkFunc {
		return func(ctx context.Context) error {
			err := next(ctx)

			workerID := GetWorkerID(ctx)
			mu.Lock()
			defer mu.Unlock()

			if err != nil && !errors.Is(err, ErrNoWork) {
				errorCounts[workerID]++
				if errorCounts[workerID] >= count {
					return ErrWorkerShutdown
				}
			} else {
				errorCounts[workerID] = 0
			}

			return err
		}
	}
}
