// gopernicus:bootstrap kind=repository/repository.go template=4de4ee11ec4e
// This file is created once by gopernicus and will NOT be overwritten.
// Add custom repository methods, store methods, and configuration below.
//
// To customize a generated method: remove its @func from queries.sql,
// then define your version here.

package jobschedules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/core/jobs/scheduler"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Storer
// =============================================================================

// Storer defines the job_schedule data access contract.
// Add custom store methods above the markers. Generated methods between
// the markers are updated automatically by 'gopernicus generate'.
type Storer interface {
	// ListDue returns enabled schedules whose next_run_at has passed,
	// by the database clock.
	ListDue(ctx context.Context, limit int) ([]JobSchedule, error)

	// ClaimDue advances next_run_at with a compare-and-set — exactly one
	// concurrent claimer wins each (schedule, slot) pair.
	ClaimDue(ctx context.Context, scheduleID string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error)

	// SetLastJob records the audit pointer for the last fired job.
	SetLastJob(ctx context.Context, scheduleID, jobID string, now time.Time) error

	// gopernicus:start (DO NOT EDIT between markers)
	List(ctx context.Context, filter FilterList, orderBy fop.Order, page fop.PageStringCursor, forPrevious bool) ([]JobSchedule, error)
	Get(ctx context.Context, scheduleID string) (JobSchedule, error)
	GetByName(ctx context.Context, name string) (JobSchedule, error)
	Create(ctx context.Context, input CreateJobSchedule) (JobSchedule, error)
	Update(ctx context.Context, scheduleID string, input UpdateJobSchedule) (JobSchedule, error)
	Delete(ctx context.Context, scheduleID string) error
	// gopernicus:end
}

// =============================================================================
// Repository
// =============================================================================

// Repository provides business logic for JobSchedules.
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

// NewRepository creates a new JobSchedule repository.
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

// ─── scheduler ergonomics (hand-written) ────────────────────────────────────

// compile-time check: Repository satisfies the engine's claim-path port.
var _ scheduler.ScheduleStore[JobSchedule] = (*Repository)(nil)

// ListDue exposes the store's due-schedule listing to the scheduler engine.
func (r *Repository) ListDue(ctx context.Context, limit int) ([]JobSchedule, error) {
	return r.store.ListDue(ctx, limit)
}

// ClaimDue exposes the store's compare-and-set claim to the scheduler engine.
func (r *Repository) ClaimDue(ctx context.Context, scheduleID string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error) {
	return r.store.ClaimDue(ctx, scheduleID, prevNextRunAt, newNextRunAt, now)
}

// SetLastJob exposes the store's audit-pointer update to the scheduler engine.
func (r *Repository) SetLastJob(ctx context.Context, scheduleID, jobID string, now time.Time) error {
	return r.store.SetLastJob(ctx, scheduleID, jobID, now)
}

// EnsureSchedule upserts a schedule by its unique name — idempotent at
// boot, so apps declare recurring jobs in the composition root while
// operators retain runtime control (enabled, cron_expr) through the
// repository. A changed cron_expr recomputes next_run_at; an unchanged
// schedule is left untouched (preserving its slot).
func (r *Repository) EnsureSchedule(ctx context.Context, name, cronExpr, eventType string, payload json.RawMessage) error {
	sched, err := scheduler.ParseCron(cronExpr)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	now := time.Now().UTC()

	existing, err := r.GetByName(ctx, name)
	switch {
	case err == nil:
		if existing.CronExpr == cronExpr && existing.EventType == eventType && string(existing.Payload) == string(payload) {
			return nil
		}
		next := existing.NextRunAt
		if existing.CronExpr != cronExpr {
			next = sched.Next(now)
		}
		_, err = r.Update(ctx, existing.ScheduleID, UpdateJobSchedule{
			CronExpr:  &cronExpr,
			EventType: &eventType,
			Payload:   &payload,
			NextRunAt: &next,
		})
		return err
	case errors.Is(err, ErrJobScheduleNotFound):
		id, err := cryptids.GenerateID()
		if err != nil {
			return fmt.Errorf("jobschedules: mint id: %w", err)
		}
		_, err = r.Create(ctx, CreateJobSchedule{
			ScheduleID: id,
			Name:       name,
			EventType:  eventType,
			CronExpr:   cronExpr,
			Payload:    payload,
			Enabled:    true,
			NextRunAt:  sched.Next(now),
		})
		if errors.Is(err, ErrJobScheduleAlreadyExists) {
			// Another instance won the boot race on the unique name; both
			// declared the same schedule, so theirs stands.
			return nil
		}
		return err
	default:
		return err
	}
}
