package queue

import (
	"errors"
	"time"
)

const defaultWorkers = 4
const defaultMaxAttempts = 3

// Validation errors from service/runtime construction and the enqueue
// methods that require optional repositories or collaborators.
var (
	// ErrQueueRequired is returned when neither Repositories.Queue nor
	// Repositories.FencedQueue is wired: a jobs Service needs at least one queue
	// surface. It is also returned by the unfenced Enqueue/EnqueueJob/NewRuntime
	// path when only the fenced queue is wired.
	ErrQueueRequired = errors.New("jobs: at least one of Repositories.Queue or Repositories.FencedQueue is required")
	// ErrFencedQueueRequired is returned by the fenced primitive methods
	// (EnqueueOnce/Replace/LatestStatusByKey/Checkpoint) and NewFencedRuntime when
	// Repositories.FencedQueue is nil.
	ErrFencedQueueRequired = errors.New("jobs: Repositories.FencedQueue is required for the fenced delivery surface")
	// ErrHandlersRequired is returned when a runtime is built with no handlers.
	ErrHandlersRequired = errors.New("jobs: handlers must be non-empty")
	// ErrInvalidHandler is returned when handlers has an empty kind key or
	// a nil handler value.
	ErrInvalidHandler = errors.New("jobs: handlers has an empty kind or a nil handler")
	// ErrProcessTimeoutExceedsLease is returned by NewFencedRuntime when the
	// per-attempt ProcessTimeout is not safely shorter than the claim LeaseFor: a
	// provider call bounded by a timeout at or beyond the lease could still be running
	// after the lease lapses and a second worker reclaims the job (AV3D-3.4).
	ErrProcessTimeoutExceedsLease = errors.New("jobs: FencedRuntimePolicy.ProcessTimeout must be shorter than LeaseFor")
)

// Repositories supplies either or both durable queue protocols.
type Repositories struct {
	Queue       QueueRepository
	FencedQueue FencedQueueRepository
}

// Option configures queue admission before the service is constructed.
type Option func(*serviceConfig)

type serviceConfig struct {
	maxAttempts int
	clock       func() time.Time
}

// WithMaxAttempts replaces the admission retry limit. Nonpositive values select three attempts.
func WithMaxAttempts(n int) Option {
	return func(c *serviceConfig) { c.maxAttempts = n }
}

// WithClock replaces the admission clock. Nil selects time.Now in UTC.
func WithClock(clock func() time.Time) Option {
	return func(c *serviceConfig) { c.clock = clock }
}
