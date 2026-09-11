// Package schedules owns recurring schedule admission and durable occurrence recovery.
// Claims persist pending occurrences alongside schedule advancement. Successful
// enqueue acknowledges an occurrence; failures and restarts retry its stable ID.
package schedules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// ErrCronRequired is returned by EnsureSchedule when a Spec sets Cron but no
// parser was configured; fixed-interval schedules remain available.
var ErrCronRequired = errors.New("jobs: a cron schedule needs a CronParser (WithCronParser)")

// ErrInvalidSpec reports an invalid recurrence or unsupported interval precision.
var ErrInvalidSpec = fmt.Errorf("jobs: schedule needs Cron or whole-second Every >= 1s: %w", sdk.ErrInvalidInput)

// Enqueuer is the admission port the occurrence engine drives. queue.Service satisfies it.
type Enqueuer interface {
	EnqueueJob(ctx context.Context, in job.Enqueue) (job.Job, error)
}

// Option configures schedule management and occurrence processing.
type Option func(*serviceConfig)

type serviceConfig struct {
	enqueuer Enqueuer
	cron     CronParser
	batch    int
	clock    func() time.Time
}

// WithEnqueuer replaces the occurrence admission port. Nil selects management-only
// operation; WorkFunc then reports ErrInvalidInput.
func WithEnqueuer(enqueuer Enqueuer) Option {
	return func(c *serviceConfig) { c.enqueuer = enqueuer }
}

// WithCronParser replaces the cron parser. Nil disables cron expressions while
// fixed-interval schedules remain available.
func WithCronParser(parser CronParser) Option {
	return func(c *serviceConfig) { c.cron = parser }
}

// WithBatchSize replaces the due-schedule batch size. Nonpositive values select 20.
func WithBatchSize(n int) Option {
	return func(c *serviceConfig) { c.batch = n }
}

// WithClock replaces the schedule clock. Nil selects time.Now in UTC.
func WithClock(clock func() time.Time) Option {
	return func(c *serviceConfig) { c.clock = clock }
}

// Service implements the schedule use cases over the schedule repository port.
type Service struct {
	repo     Repository
	enqueuer Enqueuer
	cron     CronParser
	batch    int
	now      func() time.Time
}

// NewService builds a schedule Service from its dependencies, applying a
// time.Now UTC clock and a default batch when unset.
func NewService(repo Repository, opts ...Option) (*Service, error) {
	d := serviceConfig{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("jobs schedules: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&d)
	}
	if repo == nil {
		return nil, fmt.Errorf("jobs: schedule repository is required: %w", sdk.ErrInvalidInput)
	}
	clock := d.clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	batch := d.batch
	if batch <= 0 {
		batch = 20
	}
	return &Service{
		repo:     repo,
		enqueuer: d.enqueuer,
		cron:     d.cron,
		batch:    batch,
		now:      clock,
	}, nil
}

// EnsureSchedule validates the spec (cron via WithCronParser; Every > 0), computes the
// first NextRunAt, and upserts the schedule by Name.
func (s *Service) EnsureSchedule(ctx context.Context, in Ensure) (Schedule, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Kind) == "" || (len(in.Payload) > 0 && !json.Valid(in.Payload)) {
		return Schedule{}, fmt.Errorf("jobs: schedule needs a name, kind and valid JSON payload: %w", sdk.ErrInvalidInput)
	}
	if err := validateSpec(in.Spec); err != nil {
		return Schedule{}, err
	}
	if len(in.Payload) == 0 {
		in.Payload = json.RawMessage(`{}`)
	}
	next, err := s.nextRun(in.Spec, s.now())
	if err != nil {
		return Schedule{}, err
	}
	return s.repo.Ensure(ctx, in, next)
}

// WorkFunc prepares due schedules and delivers pending occurrences of the copied
// kind set. Errors are returned to the worker; one failure does not skip the rest
// of the batch. Pending work can be retried even when its schedule was removed.
func (s *Service) WorkFunc(kinds []string) workers.WorkFunc {
	if s == nil {
		return nil
	}
	kinds = append([]string(nil), kinds...)
	return func(ctx context.Context) error {
		if s.enqueuer == nil {
			return fmt.Errorf("jobs: schedule processing requires an enqueuer: %w", sdk.ErrInvalidInput)
		}
		now := s.now()
		due, listErr := s.repo.ListDue(ctx, now, s.batch, kinds)
		var failures []error
		if listErr != nil {
			failures = append(failures, listErr)
		}
		for _, sch := range due {
			next, err := s.nextRun(sch.Spec, now)
			if err == nil {
				_, err = s.repo.ClaimDue(ctx, sch, next, now)
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("schedule %s: %w", sch.ID, err))
			}
		}
		pending, err := s.repo.ListPending(ctx, s.batch, kinds)
		if err != nil {
			failures = append(failures, err)
		}
		for _, occurrence := range pending {
			admitted, err := s.repo.RecordAttempt(ctx, occurrence.JobID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if !admitted {
				continue
			}
			_, err = s.enqueuer.EnqueueJob(ctx, job.Enqueue{ID: occurrence.JobID, Kind: occurrence.Kind, TenantID: occurrence.TenantID, Payload: occurrence.Payload})
			if err == nil || errors.Is(err, sdk.ErrAlreadyExists) {
				err = s.repo.AckOccurrence(ctx, occurrence.JobID, s.now())
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("schedule occurrence %s: %w", occurrence.JobID, err))
			}
		}
		if err := errors.Join(failures...); err != nil {
			return err
		}
		if len(due) == 0 && len(pending) == 0 {
			return workers.ErrNoWork
		}
		return nil
	}
}

// nextRun computes the next fire time. Every is the parser-free path
// (now.Add(Every)); Cron requires a configured parser and errors loudly when
// it is nil.
func (s *Service) nextRun(spec Spec, now time.Time) (time.Time, error) {
	if err := validateSpec(spec); err != nil {
		return time.Time{}, err
	}
	if spec.Every > 0 {
		return now.Add(spec.Every), nil
	}
	if spec.Cron == "" {
		return time.Time{}, ErrInvalidSpec
	}
	if s.cron == nil {
		return time.Time{}, ErrCronRequired
	}
	parsed, err := s.cron.Parse(spec.Cron)
	if err != nil {
		return time.Time{}, err
	}
	if parsed == nil {
		return time.Time{}, fmt.Errorf("jobs: cron parser returned no schedule: %w", sdk.ErrInvalidInput)
	}
	next := parsed.Next(now)
	if next.IsZero() || !next.After(now) {
		return time.Time{}, fmt.Errorf("jobs: cron must advance time: %w", sdk.ErrInvalidInput)
	}
	return next, nil
}

// validateSpec enforces that exactly one of Cron/Every is set.
func validateSpec(spec Spec) error {
	hasCron := spec.Cron != ""
	hasEvery := spec.Every > 0
	if hasCron == hasEvery || spec.Every < 0 || (hasEvery && (spec.Every < time.Second || spec.Every%time.Second != 0)) {
		return ErrInvalidSpec
	}
	return nil
}
