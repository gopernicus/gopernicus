// Package schedule is the recurring-schedule domain of the jobs feature: the
// Schedule entity, its Spec (cron or fixed interval), the Ensure input, and the
// Repository outbound port a store adapter or host fills.
//
// The Repository exposes the compare-and-set primitives (ListDue + ClaimDue +
// SetLastJob) the fire engine uses to fire each (schedule, slot) pair exactly
// once across N runtime instances, with no leader election.
package schedule

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// Spec is a schedule's recurrence. Exactly one of Cron/Every is set (validated
// at Ensure): Cron is a 5-field cron expression or @descriptor and requires the
// host's CronParser; Every is a stdlib-only fixed interval that needs no parser.
type Spec struct {
	Cron  string
	Every time.Duration
}

// Schedule is a recurring job template.
type Schedule struct {
	ID        string
	Name      string // unique; the Ensure upsert key
	Kind      string // job kind fired into the queue
	Spec      Spec
	Payload   json.RawMessage
	Enabled   bool
	NextRunAt time.Time
	LastRunAt *time.Time
	LastJobID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Ensure is the input for creating or updating a schedule by Name.
type Ensure struct {
	Name    string
	Kind    string
	Spec    Spec
	Payload json.RawMessage
}

// Repository is the schedule store outbound port. A store adapter or host fills
// it; the feature core stays dialect-blind.
type Repository interface {
	// Ensure upserts by Name: it creates the schedule or updates its kind, spec,
	// and payload, setting NextRunAt = next on create and on a spec change.
	Ensure(ctx context.Context, in Ensure, next time.Time) (Schedule, error)
	// ListDue returns up to limit enabled schedules whose NextRunAt <= now.
	ListDue(ctx context.Context, now time.Time, limit int) ([]Schedule, error)
	// ClaimDue is a pure value compare-and-set on next_run_at: it advances
	// next_run_at to newNextRunAt (and last_run_at to now) only when the row's
	// current next_run_at still equals prevNextRunAt and the schedule is
	// enabled. It reports true when this caller won the (schedule, slot) pair.
	ClaimDue(ctx context.Context, id string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error)
	// SetLastJob records the id of the job fired for the most recent slot.
	SetLastJob(ctx context.Context, id, jobID string, now time.Time) error
	// Get returns the schedule with the given id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Schedule, error)
	// List returns a cursor-paginated page of schedules.
	List(ctx context.Context, req crud.ListRequest) (crud.Page[Schedule], error)
	// SetEnabled toggles a schedule's enabled flag.
	SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error
	// Delete removes a schedule; missing id → sdk.ErrNotFound.
	Delete(ctx context.Context, id string) error
}
