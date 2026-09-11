package queue

import (
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// Private fixture for existing staged-wiring and failure matrices.
type runtimeTestConfig struct {
	WorkerMiddleware []workers.Middleware
	JobMiddleware    []workers.JobMiddleware[Job]
	Handlers         map[string]HandlerFunc
	Workers          int           `env:"JOBS_WORKERS"`
	PollInterval     time.Duration `env:"JOBS_POLL_INTERVAL"`
	IdleInterval     time.Duration `env:"JOBS_IDLE_INTERVAL"`
	Heartbeat        time.Duration `env:"JOBS_HEARTBEAT_INTERVAL"`
	Logger           *slog.Logger
}

func runtimeFromTestConfig(svc *Service, scheduler Scheduler, cfg runtimeTestConfig) (*Runtime, error) {
	return NewRuntime(svc, cfg.Handlers, WithScheduler(scheduler),
		WithRuntimePolicy(RuntimePolicy{Workers: cfg.Workers, PollInterval: cfg.PollInterval, IdleInterval: cfg.IdleInterval, Heartbeat: cfg.Heartbeat}),
		WithRuntimeLogger(cfg.Logger), WithRuntimeWorkerMiddleware(cfg.WorkerMiddleware...), WithRuntimeJobMiddleware(cfg.JobMiddleware...))
}
