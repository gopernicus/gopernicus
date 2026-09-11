package queue_test

import (
	"log/slog"
	"time"

	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// Private fixture for existing staged-wiring and failure matrices.
type runtimeTestConfig struct {
	WorkerMiddleware []workers.Middleware
	JobMiddleware    []workers.JobMiddleware[queueapi.Job]
	Handlers         map[string]queueapi.HandlerFunc
	Workers          int           `env:"JOBS_WORKERS"`
	PollInterval     time.Duration `env:"JOBS_POLL_INTERVAL"`
	IdleInterval     time.Duration `env:"JOBS_IDLE_INTERVAL"`
	Heartbeat        time.Duration `env:"JOBS_HEARTBEAT_INTERVAL"`
	Logger           *slog.Logger
}
type fencedRuntimeTestConfig struct {
	WorkerMiddleware []workers.Middleware
	JobMiddleware    []workers.JobMiddleware[queueapi.FencedClaim]
	Handlers         map[string]queueapi.FencedHandlerFunc
	DeadLetters      map[string]queueapi.DeadLetterFunc
	Workers          int           `env:"JOBS_WORKERS"`
	PollInterval     time.Duration `env:"JOBS_POLL_INTERVAL"`
	IdleInterval     time.Duration `env:"JOBS_IDLE_INTERVAL"`
	LeaseFor         time.Duration `env:"JOBS_LEASE_FOR"`
	ProcessTimeout   time.Duration `env:"JOBS_PROCESS_TIMEOUT"`
	MaxAttempts      int           `env:"JOBS_MAX_ATTEMPTS"`
	Backoff          func(attempt int) time.Duration
	Clock            func() time.Time
	Logger           *slog.Logger
}

func runtimeFromTestConfig(svc *queueapi.Service, scheduler queueapi.Scheduler, cfg runtimeTestConfig) (*queueapi.Runtime, error) {
	return queueapi.NewRuntime(svc, cfg.Handlers, queueapi.WithScheduler(scheduler),
		queueapi.WithRuntimePolicy(queueapi.RuntimePolicy{Workers: cfg.Workers, PollInterval: cfg.PollInterval, IdleInterval: cfg.IdleInterval, Heartbeat: cfg.Heartbeat}),
		queueapi.WithRuntimeLogger(cfg.Logger), queueapi.WithRuntimeWorkerMiddleware(cfg.WorkerMiddleware...), queueapi.WithRuntimeJobMiddleware(cfg.JobMiddleware...))
}
func fencedRuntimeFromTestConfig(svc *queueapi.Service, cfg fencedRuntimeTestConfig) (*queueapi.FencedRuntime, error) {
	return queueapi.NewFencedRuntime(svc, cfg.Handlers,
		queueapi.WithFencedRuntimePolicy(queueapi.FencedRuntimePolicy{Workers: cfg.Workers, PollInterval: cfg.PollInterval, IdleInterval: cfg.IdleInterval, LeaseFor: cfg.LeaseFor, ProcessTimeout: cfg.ProcessTimeout, MaxAttempts: cfg.MaxAttempts, Backoff: cfg.Backoff}),
		queueapi.WithFencedRuntimeLogger(cfg.Logger), queueapi.WithFencedRuntimeClock(cfg.Clock), queueapi.WithDeadLetters(cfg.DeadLetters),
		queueapi.WithFencedWorkerMiddleware(cfg.WorkerMiddleware...), queueapi.WithFencedJobMiddleware(cfg.JobMiddleware...))
}
