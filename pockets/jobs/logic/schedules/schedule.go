// Package schedules is the recurring-schedule domain of the jobs pocket: the
// Schedule entity, its Spec (cron or fixed interval), the Ensure input, and the
// Repository outbound port a store adapter or host fills.
package schedules

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// Spec is a schedule's recurrence. Exactly one of Cron/Every is set (validated
// at Ensure): Cron is a 5-field cron expression or @descriptor and requires the
// host's CronParser; Every is a whole-second interval >= one second, requiring no parser.
type Spec struct {
	Cron  string
	Every time.Duration
}

// Schedule is a recurring job template.
type Schedule struct {
	ID   string
	Name string // unique; the Ensure upsert key
	Kind string // job kind fired into the queue
	// TenantID is the OPTIONAL host-defined boundary the schedule belongs to. It
	// is vocabulary only — the pocket attaches no semantics to it, but it IS
	// copied onto each job the schedule fires so tenant-scoped ops queries see
	// fired work; stores map "" to NULL.
	TenantID  string
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
	Name string
	Kind string
	// TenantID is the OPTIONAL host-defined boundary to stamp on the schedule
	// (see Schedule.TenantID). Empty = no tenant.
	TenantID string
	Spec     Spec
	Payload  json.RawMessage
}

// Repository is the schedule store outbound port. A store adapter or host fills
// it; the pocket core stays dialect-blind.
type Repository interface {
	// Ensure upserts by Name: it creates the schedule or updates its kind, spec,
	// and payload, setting NextRunAt = next on create and on a spec change.
	Ensure(ctx context.Context, in Ensure, next time.Time) (Schedule, error)
	// ListDue returns up to limit enabled schedules whose NextRunAt <= now.
	// kinds restricts the scan to schedules of those kinds (applied in the query,
	// before limit) so a runtime lists only the schedules it can fire; a
	// nil/empty kinds applies no filter.
	ListDue(ctx context.Context, now time.Time, limit int, kinds []string) ([]Schedule, error)
	// ClaimDue atomically checks the expected ID, slot, kind and spec, advances
	// NextRunAt, and persists one immutable occurrence using the current payload
	// and tenant. Disabled, stale, not-yet-due or already-pending schedules lose.
	ClaimDue(ctx context.Context, expected Schedule, next, now time.Time) (bool, error)
	// ListPending returns admitted occurrences in slot/JobID order, filtered by
	// kind before limit and ordered by attempts, then slot/ID. They survive edits, disabling and deletion of schedules.
	ListPending(ctx context.Context, limit int, kinds []string) ([]Occurrence, error)
	// RecordAttempt increments the pending occurrence attempt count before delivery.
	// Missing/already-acknowledged IDs return false; true admits a delivery attempt.
	RecordAttempt(ctx context.Context, jobID string) (bool, error)
	// AckOccurrence removes a delivered occurrence and records its job/run metadata
	// atomically. Missing/already-acknowledged IDs are successful no-ops.
	AckOccurrence(ctx context.Context, jobID string, now time.Time) error
	// Get returns the schedule with the given id, or sdk.ErrNotFound.
	Get(ctx context.Context, id string) (Schedule, error)
	// List returns a cursor-paginated page of schedules.
	List(ctx context.Context, req list.Request) (list.Page[Schedule], error)
	// SetEnabled toggles a schedule's enabled flag.
	SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error
	// Delete removes a schedule; missing id → sdk.ErrNotFound.
	Delete(ctx context.Context, id string) error
}

// Occurrence is an immutable job admitted by a schedule claim. Stores retain it
// until enqueue succeeds and AckOccurrence commits. Queue IDs must be retained
// until acknowledgement; replay after an uncertain enqueue uses the same JobID.
type Occurrence struct {
	JobID      string
	ScheduleID string
	Kind       string
	TenantID   string
	Payload    json.RawMessage
	Slot       time.Time
	ClaimedAt  time.Time
	// Attempts counts admitted delivery attempts, including a crash before enqueue.
	Attempts int
}

// OccurrenceID identifies a schedule slot without truncating fractional seconds.
func OccurrenceID(id string, slot time.Time) string {
	return fmt.Sprintf("sched_%s_%d_%09d", id, slot.Unix(), slot.Nanosecond())
}
