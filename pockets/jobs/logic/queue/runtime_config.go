package queue

import (
	"context"
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// HandlerFunc executes one job of a registered kind. Handlers are host-supplied
// data: closures over whatever services the host built (including other
// pockets' services), wired at the composition root with zero ports.
type HandlerFunc func(ctx context.Context, j Job) error

// RuntimePolicy controls the ordinary queue pool and its optional scheduler.
// Nonpositive Workers selects four workers; zero cadence values select pool defaults.
//
// Heartbeat zero disables liveness logging.
type RuntimePolicy struct {
	Workers      int           `env:"JOBS_WORKERS"`
	PollInterval time.Duration `env:"JOBS_POLL_INTERVAL"`
	IdleInterval time.Duration `env:"JOBS_IDLE_INTERVAL"`
	Heartbeat    time.Duration `env:"JOBS_HEARTBEAT_INTERVAL"`
}

// RuntimeOption configures an ordinary queue runtime before workers are built.
type RuntimeOption func(*runtimeConfig)

type runtimeConfig struct {
	RuntimePolicy
	scheduler        Scheduler
	Logger           *slog.Logger
	WorkerMiddleware []workers.Middleware
	JobMiddleware    []workers.JobMiddleware[Job]
}

// WithRuntimePolicy replaces all pool timing and sizing settings.
func WithRuntimePolicy(policy RuntimePolicy) RuntimeOption {
	return func(c *runtimeConfig) { c.RuntimePolicy = policy }
}

// WithScheduler replaces the optional occurrence processor. Nil omits the scheduler pool.
func WithScheduler(scheduler Scheduler) RuntimeOption {
	return func(c *runtimeConfig) { c.scheduler = scheduler }
}

// WithRuntimeLogger replaces the pools' logger. Nil selects slog.Default.
func WithRuntimeLogger(logger *slog.Logger) RuntimeOption {
	return func(c *runtimeConfig) { c.Logger = logger }
}

// WithRuntimeWorkerMiddleware replaces the queue polling middleware; first is outermost.
// The separate scheduler pool is not wrapped. The supplied slice is copied.
func WithRuntimeWorkerMiddleware(middleware ...workers.Middleware) RuntimeOption {
	snapshot := append([]workers.Middleware(nil), middleware...)
	return func(c *runtimeConfig) { c.WorkerMiddleware = append([]workers.Middleware(nil), snapshot...) }
}

// WithRuntimeJobMiddleware replaces claimed-job middleware; first is outermost.
// A temporary gate returns workers.DeferUntil; a permanent gate returns Reject.
// The supplied slice is copied.
func WithRuntimeJobMiddleware(middleware ...workers.JobMiddleware[Job]) RuntimeOption {
	snapshot := append([]workers.JobMiddleware[Job](nil), middleware...)
	return func(c *runtimeConfig) { c.JobMiddleware = append([]workers.JobMiddleware[Job](nil), snapshot...) }
}
