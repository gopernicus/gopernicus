// Package jobs is the public surface of the jobs feature module: a durable job
// queue (enqueue with idempotency, atomic claim, retry, dead-letter, stale-claim
// recovery) and cron/interval recurring schedules, plus the runtime the host
// runs to process them.
//
// The feature is datastore-free and view-free: it depends on its repository
// ports (logic/job, logic/schedule) and sdk facilities only, never on a concrete
// store, an integration, or a view library. Cron parsing lives behind the
// CronParser port (integrations/scheduling/robfig-cron satisfies it); the
// stdlib-only Spec.Every path needs no parser at all.
//
// Host-facing surface, all in this file per the feature charter:
//
//   - Repositories — the outbound ports a store adapter or host fills (Schedules
//     nil = a queue-only host; the Runtime then skips the scheduler).
//   - HandlerFunc — a host-supplied per-kind job handler.
//   - CronParser / CronSchedule — the feature-owned cron ports (UTC by contract).
//   - Config — Handlers (required non-empty to build a Runtime), optional Cron,
//     and sizing/cadence with safe defaults.
//   - NewService / Service.Enqueue / EnqueueJob / EnsureSchedule — the enqueue
//     and scheduling surface, including the cross-feature primitive-typed
//     Enqueue.
//   - NewRuntime(svc) / Runtime.Run — the runtime the host explicitly runs.
//   - Service.Register — validates the built Service carries handlers and logs;
//     registers no routes and starts no goroutines.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/internal/logic/queuesvc"
	"github.com/gopernicus/gopernicus/features/jobs/internal/logic/runtime"
	"github.com/gopernicus/gopernicus/features/jobs/internal/logic/schedulesvc"
	"github.com/gopernicus/gopernicus/features/jobs/logic/job"
	"github.com/gopernicus/gopernicus/features/jobs/logic/schedule"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

const (
	// defaultWorkers is the queue pool size when Config.Workers is 0.
	defaultWorkers = 4
	// defaultMaxAttempts is the per-job attempt ceiling when unset.
	defaultMaxAttempts = 3
	// defaultScheduleBatch is the number of due schedules handled per tick when
	// Config.ScheduleBatch is 0.
	defaultScheduleBatch = 20
)

// Validation errors from NewService/NewRuntime/Register. A misconfigured host
// fails at construction/registration, not at first enqueue.
var (
	// ErrQueueRequired is returned when Repositories.Queue is nil.
	ErrQueueRequired = errors.New("jobs: Repositories.Queue is required")
	// ErrHandlersRequired is returned when a Runtime is built (or the feature is
	// registered) with no handlers — a jobs runtime with nothing to run.
	ErrHandlersRequired = errors.New("jobs: Config.Handlers must be non-empty")
	// ErrInvalidHandler is returned when Config.Handlers has an empty kind key or
	// a nil handler value.
	ErrInvalidHandler = errors.New("jobs: Config.Handlers has an empty kind or a nil handler")
	// ErrSchedulesNotConfigured is returned by EnsureSchedule when the host wired
	// no Schedules repository (a queue-only host).
	ErrSchedulesNotConfigured = errors.New("jobs: Repositories.Schedules is nil; scheduling is disabled")
	// ErrCronRequired is returned by EnsureSchedule for a Spec.Cron when no
	// CronParser was configured. Re-exported from the schedule service so hosts
	// check one symbol.
	ErrCronRequired = schedulesvc.ErrCronRequired
)

// CronSchedule is a parsed cron expression that yields fire times. It is a type
// alias to the interface literal (not a defined type) so any conforming parser —
// including integrations/scheduling/robfig-cron, whose Parse returns the identical
// aliased shape — satisfies CronParser directly at the composition root, with no
// adapter and zero import in either direction.
type CronSchedule = interface {
	// Next returns the next fire time strictly after (per the adapter) the given
	// time. A zero Time means the schedule never fires again. Evaluation is UTC.
	Next(after time.Time) time.Time
}

// CronParser parses cron expressions into schedules. It is feature-owned and
// consumer-declared; integrations/scheduling/robfig-cron satisfies it
// structurally with zero import in either direction.
type CronParser interface {
	// Parse validates a 5-field cron expression (plus @hourly-style descriptors)
	// and returns its schedule. Evaluation is UTC — v1 has no timezone support;
	// the contract states it so an adapter cannot silently localize.
	Parse(expr string) (CronSchedule, error)
}

// HandlerFunc executes one job of a registered kind. Handlers are host-supplied
// data: closures over whatever services the host built (including other
// features' services), wired at the composition root with zero ports.
type HandlerFunc func(ctx context.Context, j job.Job) error

// Repositories is the set of outbound ports the feature needs. A store adapter
// (features/jobs/stores/turso, the in-core memstore) or a host fills it.
type Repositories struct {
	// Queue is required.
	Queue job.QueueRepository
	// Schedules is optional; nil = a queue-only host, and the Runtime skips the
	// scheduler pool.
	Schedules schedule.Repository
}

// Config carries host-provided collaborators and tuning. Handlers is required
// non-empty to build a Runtime; Cron is optional until a Spec.Cron schedule
// appears (then EnsureSchedule errors loudly); the sizing/cadence fields all
// have safe defaults at 0.
type Config struct {
	// Handlers maps a job kind to its handler; required non-empty for a Runtime.
	Handlers map[string]HandlerFunc
	// Cron parses cron expressions; nil is fine until a Spec.Cron schedule is
	// ensured, which then returns ErrCronRequired.
	Cron CronParser
	// Workers is the queue pool size; 0 → defaultWorkers.
	Workers int
	// PollInterval is the delay between iterations while work flows; 0 → the pool
	// default.
	PollInterval time.Duration
	// IdleInterval is the delay after an empty poll; 0 → the pool default.
	IdleInterval time.Duration
	// MaxAttempts is the default per-job attempt ceiling; 0 → defaultMaxAttempts.
	MaxAttempts int
	// ScheduleBatch is the number of due schedules handled per tick; 0 →
	// defaultScheduleBatch.
	ScheduleBatch int
	// Logger is the operational logger for the runtime pools (queue and
	// scheduler); nil → slog.Default(). It is distinct from feature.Mount.Logger:
	// Config.Logger is the runtime pools' operational logger, while Mount.Logger
	// is registration-time logging — do not unify them by threading Mount into
	// NewService.
	Logger *slog.Logger
}

// resolvedConfig is Config with defaults applied.
type resolvedConfig struct {
	workers      int
	pollInterval time.Duration
	idleInterval time.Duration
	maxAttempts  int
	logger       *slog.Logger // nil → the seams fall back to slog.Default()
}

// Service is the jobs feature's enqueue + scheduling capability, minus the run
// loop. It owns the wake channel (through the internal queue service); NewRuntime
// shares that channel with the queue pool by construction.
type Service struct {
	repos     Repositories
	queue     *queuesvc.Service
	scheduler *schedulesvc.Service // nil when Repositories.Schedules is nil
	handlers  map[string]HandlerFunc
	cfg       resolvedConfig
}

// NewService validates the (repos, cfg) pair, applies defaults, and builds the
// enqueue and (when Schedules is set) scheduling services. It does not build or
// start the runtime (see NewRuntime).
func NewService(repos Repositories, cfg Config) (*Service, error) {
	if repos.Queue == nil {
		return nil, ErrQueueRequired
	}
	for kind, h := range cfg.Handlers {
		if kind == "" || h == nil {
			return nil, ErrInvalidHandler
		}
	}

	rc := resolvedConfig{
		workers:      cfg.Workers,
		pollInterval: cfg.PollInterval,
		idleInterval: cfg.IdleInterval,
		maxAttempts:  cfg.MaxAttempts,
		logger:       cfg.Logger,
	}
	if rc.workers <= 0 {
		rc.workers = defaultWorkers
	}
	if rc.maxAttempts <= 0 {
		rc.maxAttempts = defaultMaxAttempts
	}

	svc := &Service{
		repos:    repos,
		queue:    queuesvc.NewService(repos.Queue, rc.maxAttempts, nil),
		handlers: cfg.Handlers,
		cfg:      rc,
	}

	if repos.Schedules != nil {
		svc.scheduler = schedulesvc.NewService(schedulesvc.Deps{
			Schedules: repos.Schedules,
			Enqueuer:  svc.queue,
			CronNext:  cronNextFunc(cfg.Cron),
			Batch:     cfg.ScheduleBatch,
			Logger:    cfg.Logger,
		})
	}

	return svc, nil
}

// Enqueue is the primitive-typed entry point. Its signature is a HARD
// compatibility contract: stdlib types only (string, json.RawMessage), so a
// consuming feature's own narrow enqueuer port matches it structurally with zero
// import of features/jobs (constitution rule 6). Do not widen it.
func (s *Service) Enqueue(ctx context.Context, kind string, payload json.RawMessage) (string, error) {
	return s.queue.Enqueue(ctx, kind, payload)
}

// EnqueueJob is the full-fidelity variant (idempotency ID, ScheduledFor,
// Priority) for hosts and internal use.
func (s *Service) EnqueueJob(ctx context.Context, in job.Enqueue) (job.Job, error) {
	return s.queue.EnqueueJob(ctx, in)
}

// EnsureSchedule validates in.Spec (cron via Config.Cron; Every > 0) and upserts
// the schedule by Name. It returns ErrSchedulesNotConfigured on a queue-only
// host and ErrCronRequired for a Spec.Cron with no configured parser.
func (s *Service) EnsureSchedule(ctx context.Context, in schedule.Ensure) (schedule.Schedule, error) {
	if s.scheduler == nil {
		return schedule.Schedule{}, ErrSchedulesNotConfigured
	}
	return s.scheduler.EnsureSchedule(ctx, in)
}

// wakeChan exposes the Service's wake channel for the wiring assertion in tests.
func (s *Service) wakeChan() <-chan struct{} { return s.queue.Wake() }

// Runtime runs the queue and (optional) scheduler pools. Build it from a
// constructed Service so the wake channel is shared by construction.
type Runtime struct {
	rt   *runtime.Runtime
	wake <-chan struct{}
}

// NewRuntime takes the BUILT Service — never (repos, cfg) a second time — so the
// wake channel and dependencies are shared by construction. It requires the
// Service to carry at least one handler.
func NewRuntime(svc *Service) (*Runtime, error) {
	if len(svc.handlers) == 0 {
		return nil, ErrHandlersRequired
	}

	handlers := make(map[string]runtime.HandlerFunc, len(svc.handlers))
	for kind, h := range svc.handlers {
		handlers[kind] = runtime.HandlerFunc(h)
	}

	var scheduler workers.WorkFunc
	if svc.scheduler != nil {
		scheduler = svc.scheduler.WorkFunc()
	}

	rt := runtime.New(runtime.Deps{
		Queue:        svc.repos.Queue,
		Handlers:     handlers,
		Scheduler:    scheduler,
		Wake:         svc.queue.Wake(),
		Workers:      svc.cfg.workers,
		PollInterval: svc.cfg.pollInterval,
		IdleInterval: svc.cfg.idleInterval,
		MaxAttempts:  svc.cfg.maxAttempts,
		Logger:       svc.cfg.logger,
	})

	return &Runtime{rt: rt, wake: svc.queue.Wake()}, nil
}

// Run blocks running the pools; cancel ctx to drain gracefully. See
// runtime.Runtime.Run.
func (r *Runtime) Run(ctx context.Context) error { return r.rt.Run(ctx) }

// wakeChan exposes the Runtime's wake channel for the wiring assertion in tests.
func (r *Runtime) wakeChan() <-chan struct{} { return r.wake }

// Register mounts the already-built Service into the host: it requires the
// Service to carry at least one handler and logs the registration. It registers
// NO routes (the /jobs/* namespace is a documentation reservation until the v2
// admin surface) and starts NO goroutines: the host owns the run loop and calls
// Runtime.Run explicitly. Migrations are the store adapter's concern, not this
// feature core's.
func (s *Service) Register(m feature.Mount) error {
	if len(s.handlers) == 0 {
		return ErrHandlersRequired
	}
	if m.Logger != nil {
		m.Logger.Info("registered jobs feature",
			"handlers", len(s.handlers),
			"scheduler", s.repos.Schedules != nil,
		)
	}
	return nil
}

// cronNextFunc adapts the CronParser port into the internal schedule service's
// stdlib-typed CronNextFunc. A nil parser yields a nil func, which the service
// treats as "no cron support".
func cronNextFunc(parser CronParser) schedulesvc.CronNextFunc {
	if parser == nil {
		return nil
	}
	return func(expr string, after time.Time) (time.Time, error) {
		cs, err := parser.Parse(expr)
		if err != nil {
			return time.Time{}, err
		}
		return cs.Next(after), nil
	}
}
