// Package schedulesvc holds the jobs feature's schedule use cases: EnsureSchedule
// validation/upsert and the fire engine. It is internal so it is not part of the
// feature's public SemVer surface; the host-facing surface is package jobs.
//
// The fire engine, per tick:
//
//	ListDue -> for each: compute next (from now) -> ClaimDue value-CAS ->
//	deterministic job ID sched_<scheduleID>_<slotUnix> -> EnqueueJob
//	(sdk.ErrAlreadyExists swallowed) -> SetLastJob
//
// Computing next from now (not from the missed slot) makes a missed window fire
// exactly once. The CAS + deterministic idempotency key let N runtime instances
// fire each (schedule, slot) pair exactly once with no leader election.
package schedulesvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// ErrCronRequired is returned by EnsureSchedule when a Spec sets Cron but no
// CronNext function was configured (the host passed no CronParser). It degrades
// loudly rather than silently dropping the schedule.
var ErrCronRequired = errors.New("jobs: a cron schedule needs a CronParser (Config.Cron)")

// ErrInvalidSpec is returned when a Spec does not set exactly one of Cron/Every.
var ErrInvalidSpec = fmt.Errorf("jobs: schedule Spec must set exactly one of Cron/Every: %w", sdk.ErrInvalidInput)

// CronNextFunc computes the next fire time at or after after for a cron
// expression, returning an error for an invalid expression. jobs.go adapts its
// CronParser port into this stdlib-typed function so this package needs no cron
// vocabulary of its own.
type CronNextFunc func(expr string, after time.Time) (time.Time, error)

// Enqueuer is the narrow enqueue port the fire engine drives. queuesvc.Service
// satisfies it.
type Enqueuer interface {
	EnqueueJob(ctx context.Context, in job.Enqueue) (job.Job, error)
}

// Deps are the fire engine's collaborators.
type Deps struct {
	Schedules schedule.Repository
	Enqueuer  Enqueuer
	CronNext  CronNextFunc // nil = no cron support; a Spec.Cron then errors loudly
	Batch     int          // due schedules per tick
	Clock     func() time.Time
	Logger    *slog.Logger
}

// Service implements the schedule use cases over the schedule repository port.
type Service struct {
	repo     schedule.Repository
	enqueuer Enqueuer
	cronNext CronNextFunc
	batch    int
	now      func() time.Time
	log      *slog.Logger
}

// NewService builds a schedule Service from its dependencies, applying a
// time.Now UTC clock and a default batch when unset.
func NewService(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	batch := d.Batch
	if batch <= 0 {
		batch = 20
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		repo:     d.Schedules,
		enqueuer: d.Enqueuer,
		cronNext: d.CronNext,
		batch:    batch,
		now:      clock,
		log:      log,
	}
}

// EnsureSchedule validates the spec (cron via CronNext; Every > 0), computes the
// first NextRunAt, and upserts the schedule by Name.
func (s *Service) EnsureSchedule(ctx context.Context, in schedule.Ensure) (schedule.Schedule, error) {
	if err := validateSpec(in.Spec); err != nil {
		return schedule.Schedule{}, err
	}
	next, err := s.nextRun(in.Spec, s.now())
	if err != nil {
		return schedule.Schedule{}, err
	}
	return s.repo.Ensure(ctx, in, next)
}

// WorkFunc returns the fire engine as a workers.WorkFunc for a single-worker
// pool. A tick with no due schedules returns workers.ErrNoWork so the pool backs
// off to its idle interval; a tick that saw due schedules returns nil so the
// pool keeps polling actively.
func (s *Service) WorkFunc() workers.WorkFunc {
	return func(ctx context.Context) error {
		now := s.now()
		due, err := s.repo.ListDue(ctx, now, s.batch)
		if err != nil {
			return err
		}
		if len(due) == 0 {
			return workers.ErrNoWork
		}
		for _, sch := range due {
			s.fire(ctx, sch, now)
		}
		return nil
	}
}

// fire attempts to fire one due schedule: it computes the next slot from now,
// wins-or-loses the value-CAS on the current slot, and on a win enqueues a job
// with a deterministic ID (swallowing an already-fired duplicate) and records it.
func (s *Service) fire(ctx context.Context, sch schedule.Schedule, now time.Time) {
	next, err := s.nextRun(sch.Spec, now)
	if err != nil {
		s.log.ErrorContext(ctx, "schedule: compute next run failed", "schedule_id", sch.ID, "name", sch.Name, "error", err)
		return
	}

	won, err := s.repo.ClaimDue(ctx, sch.ID, sch.NextRunAt, next, now)
	if err != nil {
		s.log.ErrorContext(ctx, "schedule: claim due failed", "schedule_id", sch.ID, "error", err)
		return
	}
	if !won {
		// Another runtime instance won this (schedule, slot) pair.
		return
	}

	jobID := fmt.Sprintf("sched_%s_%d", sch.ID, sch.NextRunAt.Unix())
	if _, err := s.enqueuer.EnqueueJob(ctx, job.Enqueue{
		ID:      jobID,
		Kind:    sch.Kind,
		Payload: sch.Payload,
	}); err != nil && !errors.Is(err, sdk.ErrAlreadyExists) {
		s.log.ErrorContext(ctx, "schedule: enqueue job failed", "schedule_id", sch.ID, "job_id", jobID, "error", err)
		return
	}

	if err := s.repo.SetLastJob(ctx, sch.ID, jobID, now); err != nil {
		s.log.ErrorContext(ctx, "schedule: set last job failed", "schedule_id", sch.ID, "job_id", jobID, "error", err)
	}
}

// nextRun computes the next fire time. Every is the parser-free path
// (now.Add(Every)); Cron requires the CronNext function and errors loudly when
// it is nil.
func (s *Service) nextRun(spec schedule.Spec, now time.Time) (time.Time, error) {
	if spec.Every > 0 {
		return now.Add(spec.Every), nil
	}
	if spec.Cron == "" {
		return time.Time{}, ErrInvalidSpec
	}
	if s.cronNext == nil {
		return time.Time{}, ErrCronRequired
	}
	return s.cronNext(spec.Cron, now)
}

// validateSpec enforces that exactly one of Cron/Every is set.
func validateSpec(spec schedule.Spec) error {
	hasCron := spec.Cron != ""
	hasEvery := spec.Every > 0
	if hasCron == hasEvery {
		return ErrInvalidSpec
	}
	return nil
}
