package workers

import (
	"context"
	"errors"
	"sync"
)

// ConsecutiveErrorShutdown returns middleware that stops a worker after it sees
// count consecutive errors (ErrNoWork does not count). The per-worker counter
// resets on any successful iteration or ErrNoWork. Each worker is tracked
// independently by its worker ID.
func ConsecutiveErrorShutdown(count int) Middleware {
	if count < 1 {
		count = 1
	}
	return func(next WorkFunc) WorkFunc {
		var mu sync.Mutex
		failures := make(map[string]int)
		return func(ctx context.Context) error {
			err := next(ctx)
			if errors.Is(err, ErrPoolShutdown) || errors.Is(err, ErrWorkerShutdown) {
				return err
			}

			workerID := WorkerIDFromContext(ctx)

			mu.Lock()
			defer mu.Unlock()

			if err != nil && !errors.Is(err, ErrNoWork) {
				failures[workerID]++
				if failures[workerID] >= count {
					return errors.Join(ErrWorkerShutdown, err)
				}
				return err
			}

			failures[workerID] = 0
			return err
		}
	}
}
