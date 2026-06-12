// Package scheduler fires recurring job_schedules into the job queue.
// One WorkFunc plugged into sdk/workers drives it; N instances are safe
// with no leader election — ClaimDue's compare-and-set on next_run_at
// means exactly one instance wins each (schedule, slot) pair, and the
// deterministic job id makes any crash-window refire collapse into
// ErrJobExists.
//
// Missed windows fire once: next_run_at advances from the current time,
// so a three-hour outage on an hourly job produces one job, not three.
//
// Like workers.Runner, the engine is generic over the project's schedule
// row type — projects own their jobschedules package, so the engine binds
// through the Schedule accessors and an EnqueueFunc rather than nominal
// repository types. The jobs domain composite ships both adapters:
// jobschedules.Repository satisfies ScheduleStore, and
// jobs.Repositories.EnqueueScheduled satisfies EnqueueFunc.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/gopernicus/gopernicus/sdk/workers"
)

// ErrJobExists signals the slot's job was already enqueued — the
// idempotent-refire case an EnqueueFunc adapter maps its domain's
// already-exists error onto, and the engine swallows.
var ErrJobExists = errors.New("scheduler: job already enqueued")

// Schedule is the minimal row contract the engine needs, satisfied by the
// project's jobschedules.JobSchedule via its accessor methods.
type Schedule interface {
	GetScheduleID() string
	GetName() string
	GetEventType() string
	GetCronExpr() string
	GetPayload() json.RawMessage
	GetNextRunAt() time.Time
}

// ScheduleStore is the claim-path port, satisfied by the project's
// jobschedules.Repository (backed by the hand-written pgx store methods).
type ScheduleStore[S Schedule] interface {
	ListDue(ctx context.Context, limit int) ([]S, error)
	ClaimDue(ctx context.Context, scheduleID string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error)
	SetLastJob(ctx context.Context, scheduleID, jobID string, now time.Time) error
}

// Job is one firing handed to the EnqueueFunc. JobID doubles as the
// idempotency key and the correlation id.
type Job struct {
	JobID      string
	EventType  string
	Payload    json.RawMessage
	OccurredAt time.Time
}

// EnqueueFunc inserts one fired job into the project's job queue. It must
// return ErrJobExists (wrapped is fine) when the JobID is already present,
// so crash-window refires stay silent.
type EnqueueFunc func(ctx context.Context, job Job) error

// Scheduler claims due schedules and enqueues their jobs.
type Scheduler[S Schedule] struct {
	schedules ScheduleStore[S]
	enqueue   EnqueueFunc
	log       *slog.Logger
	batch     int
}

// Option configures a Scheduler.
type Option func(*config)

type config struct {
	batch int
}

// WithBatchSize sets how many due schedules one tick claims (default 20).
func WithBatchSize(n int) Option {
	return func(c *config) { c.batch = n }
}

// New wires a Scheduler over the schedule store and the job queue.
func New[S Schedule](schedules ScheduleStore[S], enqueue EnqueueFunc, log *slog.Logger, opts ...Option) *Scheduler[S] {
	cfg := config{batch: 20}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Scheduler[S]{schedules: schedules, enqueue: enqueue, log: log, batch: cfg.batch}
}

// ParseCron validates a cron expression with the scheduler's parser
// (standard 5-field plus @hourly-style descriptors, evaluated in UTC) and
// returns the schedule for next-run computation. The jobschedules
// repository uses the same parser, so what EnsureSchedule accepts the
// engine can always evaluate.
func ParseCron(expr string) (cron.Schedule, error) {
	sched, err := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	).Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("scheduler: invalid cron %q: %w", expr, err)
	}
	return sched, nil
}

// JobID returns the deterministic id for one (schedule, slot) firing —
// the idempotency key that makes refires after crashes harmless.
func JobID(scheduleID string, slot time.Time) string {
	return fmt.Sprintf("sched_%s_%d", scheduleID, slot.UTC().Unix())
}

// WorkFunc returns the polling function for an sdk/workers pool. It
// returns workers.ErrNoWork when nothing is due, engaging the pool's idle
// backoff.
func (s *Scheduler[S]) WorkFunc() func(ctx context.Context) error {
	return func(ctx context.Context) error {
		due, err := s.schedules.ListDue(ctx, s.batch)
		if err != nil {
			return fmt.Errorf("scheduler: list due: %w", err)
		}
		if len(due) == 0 {
			return workers.ErrNoWork
		}

		fired := 0
		for _, sched := range due {
			ok, err := s.fire(ctx, sched)
			if err != nil {
				// One poisoned schedule must not wedge the rest.
				s.log.ErrorContext(ctx, "scheduler: fire failed",
					"schedule", sched.GetName(), "error", err)
				continue
			}
			if ok {
				fired++
			}
		}
		if fired == 0 {
			return workers.ErrNoWork
		}
		return nil
	}
}

// fire claims one schedule's slot and enqueues its job. ok reports
// whether THIS instance fired (a lost claim is not an error — another
// instance won the slot).
func (s *Scheduler[S]) fire(ctx context.Context, sched S) (bool, error) {
	cronSched, err := ParseCron(sched.GetCronExpr())
	if err != nil {
		// Invalid cron can only happen via direct DB edits — loud, skip.
		return false, err
	}

	now := time.Now().UTC()
	slot := sched.GetNextRunAt()
	next := cronSched.Next(now) // fire-once catch-up: advance from now

	claimed, err := s.schedules.ClaimDue(ctx, sched.GetScheduleID(), slot, next, now)
	if err != nil {
		return false, fmt.Errorf("claim: %w", err)
	}
	if !claimed {
		return false, nil // another instance won this slot
	}

	jobID := JobID(sched.GetScheduleID(), slot)
	payload := sched.GetPayload()
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	err = s.enqueue(ctx, Job{
		JobID:      jobID,
		EventType:  sched.GetEventType(),
		Payload:    payload,
		OccurredAt: now,
	})
	if err != nil && !errors.Is(err, ErrJobExists) {
		return false, fmt.Errorf("enqueue: %w", err)
	}

	if err := s.schedules.SetLastJob(ctx, sched.GetScheduleID(), jobID, now); err != nil {
		s.log.WarnContext(ctx, "scheduler: audit pointer update failed",
			"schedule", sched.GetName(), "job_id", jobID, "error", err)
	}
	s.log.InfoContext(ctx, "scheduler: fired",
		"schedule", sched.GetName(), "job_id", jobID, "next_run_at", next)
	return true, nil
}
