// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.
//
// To customize a generated method: remove its @func from queries.sql,
// then define your version here.

package jobqueue

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// =============================================================================
// Storer
// =============================================================================

// Storer defines the job_queue data access contract.
// Add custom store methods above the markers. Generated methods between
// the markers are updated automatically by 'gopernicus generate'.
type Storer interface {
	// Checkout atomically claims the next available job.
	// Returns workers.ErrNoWork if no jobs are available.
	Checkout(ctx context.Context, workerID string, now time.Time) (JobQueue, error)

	// Complete marks a job as successfully completed.
	Complete(ctx context.Context, jobID string, now time.Time) error

	// Fail marks a job as failed. Increments retry_count, dead-letters if exhausted.
	Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error

	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]JobQueue, error)
	Get(ctx context.Context, jobID string) (JobQueue, error)
	Create(ctx context.Context, input CreateJobQueue) (JobQueue, error)
	Update(ctx context.Context, jobID string, input UpdateJobQueue) (JobQueue, error)
	Delete(ctx context.Context, jobID string) error
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for JobQueues.
type Repository struct {
	store      Storer
	generateID func() (string, error)
	bus        events.Bus
}

// Option configures a Repository.
type Option func(*Repository)

// WithGenerateID overrides the default ID generator (cryptids.GenerateID).
func WithGenerateID(fn func() (string, error)) Option {
	return func(r *Repository) { r.generateID = fn }
}

// WithEventBus configures the event bus for emitting domain events.
func WithEventBus(bus events.Bus) Option {
	return func(r *Repository) { r.bus = bus }
}

// NewRepository creates a new JobQueue repository.
func NewRepository(store Storer, opts ...Option) *Repository {
	r := &Repository{
		store:      store,
		generateID: cryptids.GenerateID,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Ensure imports are used.
var _ context.Context
var _ fop.Order

// ─── worker-pool ergonomics (hand-written) ───────────────────────────────────

// compile-time check: Repository satisfies workers.JobStore, so it plugs
// straight into workers.NewRunner.
var _ workers.JobStore[JobQueue] = (*Repository)(nil)

// Checkout exposes the store's atomic claim to the worker runner.
func (r *Repository) Checkout(ctx context.Context, workerID string, now time.Time) (JobQueue, error) {
	return r.store.Checkout(ctx, workerID, now)
}

// Complete exposes the store's completion update to the worker runner.
func (r *Repository) Complete(ctx context.Context, jobID string, now time.Time) error {
	return r.store.Complete(ctx, jobID, now)
}

// Fail exposes the store's failure/retry update to the worker runner.
func (r *Repository) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	return r.store.Fail(ctx, jobID, now, reason, maxAttempts)
}
