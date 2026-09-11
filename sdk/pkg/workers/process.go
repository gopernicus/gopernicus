package workers

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// ProcessFunc processes one claimed job. Nil means success, including when a
// wrapper short-circuits. Use DeferUntil or Reject to decline processing.
type ProcessFunc[T any] func(ctx context.Context, job T) error

// JobMiddleware wraps processing after claim and before persistence. It can
// inspect the job, pass a derived context, call next, or return a disposition.
// Runners invoke the chain inside panic recovery and any processing timeout.
// It may be called concurrently; synchronize mutable state. Post-persistence
// behavior belongs in a separate lifecycle hook.
type JobMiddleware[T any] func(ProcessFunc[T]) ProcessFunc[T]

// ChainJobMiddleware applies the first middleware as the outermost wrapper.
// Compose before starting the runner. Middleware must not alter job identity.
func ChainJobMiddleware[T any](process ProcessFunc[T], middleware ...JobMiddleware[T]) ProcessFunc[T] {
	for i := len(middleware) - 1; i >= 0; i-- {
		process = middleware[i](process)
	}
	return process
}

// DeferredError requests a future attempt without spending a failure attempt.
// Until must be after the runner clock when processing returns. Reason is stored
// by the queue and must be suitable for operational logs/storage.
type DeferredError struct {
	Until  time.Time
	Reason string
}

func (e *DeferredError) Error() string { return "workers: job deferred: " + e.Reason }

// DeferUntil declines processing until a future time. It requires the optional
// store deferral port. An unsupported/invalid deferral returns an iteration error
// and leaves the claim for recovery; it never completes or fails the job.
func DeferUntil(until time.Time, reason string) error {
	return &DeferredError{Until: until, Reason: reason}
}

// PermanentError rejects an execution without further processing retries.
type PermanentError struct{ Reason string }

func (e *PermanentError) Error() string { return e.Reason }

// Reject requests immediate permanent failure. It takes precedence over a
// DeferredError if an error tree contains both dispositions.
func Reject(reason string) error { return &PermanentError{Reason: reason} }

func runProcess[T any](ctx context.Context, job T, id string, process ProcessFunc[T], log *slog.Logger) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.ErrorContext(ctx, "panic recovered in job", "job_id", id, "panic", rec, "stack", string(debug.Stack()))
			err = fmt.Errorf("workers: panic in process: %v", rec)
		}
	}()
	return process(ctx, job)
}
