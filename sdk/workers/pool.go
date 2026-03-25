package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// Options holds configuration parsed from environment variables.
type Options struct {
	Name         string        `env:"WORKER_NAME" default:"worker"`
	WorkerCount  int           `env:"WORKER_COUNT" default:"5"`
	PollInterval time.Duration `env:"WORKER_POLL_INTERVAL" default:"5s"`
	IdleInterval time.Duration `env:"WORKER_IDLE_INTERVAL" default:"30s"`
}

// PoolOption configures a WorkerPool programmatically.
type PoolOption func(*poolConfig)

type poolConfig struct {
	name         string
	workerCount  int
	pollInterval time.Duration
	idleInterval time.Duration
	middlewares  []Middleware
	logger       *slog.Logger
}

// WithName sets the worker pool name.
func WithName(name string) PoolOption {
	return func(c *poolConfig) { c.name = name }
}

// WithWorkerCount sets the number of workers.
func WithWorkerCount(count int) PoolOption {
	return func(c *poolConfig) { c.workerCount = count }
}

// WithPollInterval sets how often to poll for new work.
func WithPollInterval(interval time.Duration) PoolOption {
	return func(c *poolConfig) { c.pollInterval = interval }
}

// WithIdleInterval sets how long to wait when no work is available.
func WithIdleInterval(interval time.Duration) PoolOption {
	return func(c *poolConfig) { c.idleInterval = interval }
}

// WithMiddleware adds middleware to the worker pool.
func WithMiddleware(middlewares ...Middleware) PoolOption {
	return func(c *poolConfig) { c.middlewares = append(c.middlewares, middlewares...) }
}

// WithLogger sets a custom logger.
func WithLogger(logger *slog.Logger) PoolOption {
	return func(c *poolConfig) { c.logger = logger }
}

// WorkerPool manages N worker goroutines that call a WorkFunc in a polling loop.
// The pool handles adaptive polling, panic recovery, and graceful shutdown.
// It knows nothing about jobs or tasks — that's the Runner's concern.
type WorkerPool struct {
	baseWork     WorkFunc
	name         string
	workerCount  int
	pollInterval time.Duration
	idleInterval time.Duration
	log          *slog.Logger

	workFunc    WorkFunc // Final middleware-wrapped work function
	middlewares []Middleware
	stats       stats

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopMu  sync.Mutex
	startMu sync.Mutex
	running bool
	errors  chan error
}

// NewPool creates a new WorkerPool. The workFunc is called in a loop by each
// worker goroutine. Options provides env-based config, PoolOption funcs override.
func NewPool(workFunc WorkFunc, cfg Options, opts ...PoolOption) *WorkerPool {
	c := &poolConfig{
		name:         cfg.Name,
		workerCount:  cfg.WorkerCount,
		pollInterval: cfg.PollInterval,
		idleInterval: cfg.IdleInterval,
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.logger == nil {
		c.logger = slog.Default()
	}
	if c.workerCount <= 0 {
		c.workerCount = 1
	}
	if c.pollInterval <= 0 {
		c.pollInterval = 5 * time.Second
	}
	if c.idleInterval <= 0 {
		c.idleInterval = 30 * time.Second
	}

	pool := &WorkerPool{
		baseWork:     workFunc,
		name:         c.name,
		workerCount:  c.workerCount,
		pollInterval: c.pollInterval,
		idleInterval: c.idleInterval,
		log:          c.logger,
		middlewares:  c.middlewares,
		errors:       make(chan error, c.workerCount),
	}
	pool.buildMiddlewareChain()

	return pool
}

// Start launches worker goroutines and blocks until all workers exit.
// Cancel the context or call Stop() to initiate graceful shutdown.
func (wp *WorkerPool) Start(ctx context.Context) error {
	wp.startMu.Lock()
	defer wp.startMu.Unlock()

	startTime := time.Now()

	wp.log.InfoContext(ctx, "worker pool starting",
		"pool", wp.name,
		"worker_count", wp.workerCount,
		"poll_interval", wp.pollInterval,
		"idle_interval", wp.idleInterval,
	)

	wp.stopMu.Lock()
	wp.ctx, wp.cancel = context.WithCancel(ctx)
	wp.running = true
	wp.stopMu.Unlock()

	for i := 0; i < wp.workerCount; i++ {
		workerID := fmt.Sprintf("%s-worker-%d", wp.name, i+1)
		wp.wg.Add(1)
		go wp.worker(workerID)
	}

	wp.wg.Wait()
	close(wp.errors)

	wp.log.InfoContext(ctx, "worker pool stopped",
		"pool", wp.name,
		"runtime", time.Since(startTime),
		"stats", wp.Stats(),
	)

	wp.stopMu.Lock()
	wp.running = false
	wp.stopMu.Unlock()

	return nil
}

// Stop initiates graceful shutdown. Safe to call multiple times.
func (wp *WorkerPool) Stop() {
	wp.stopMu.Lock()
	defer wp.stopMu.Unlock()

	if !wp.running {
		return
	}

	wp.log.InfoContext(wp.ctx, "stopping worker pool", "pool", wp.name)
	if wp.cancel != nil {
		wp.cancel()
	}
}

// Stats returns a point-in-time snapshot of pool statistics.
func (wp *WorkerPool) Stats() Stats {
	return wp.stats.snapshot()
}

// Errors returns a channel of critical errors that require pool shutdown.
func (wp *WorkerPool) Errors() <-chan error {
	return wp.errors
}

// buildMiddlewareChain wraps the base work function with middlewares.
// First added middleware is outermost.
func (wp *WorkerPool) buildMiddlewareChain() {
	wp.workFunc = wp.baseWork

	for i := len(wp.middlewares) - 1; i >= 0; i-- {
		wp.workFunc = wp.middlewares[i](wp.workFunc)
	}
}

// worker is the main goroutine loop for a single worker.
func (wp *WorkerPool) worker(workerID string) {
	defer wp.wg.Done()
	defer wp.stats.activeWorkers.Add(-1)

	wp.stats.activeWorkers.Add(1)

	wp.log.InfoContext(wp.ctx, "worker started",
		"worker_id", workerID,
		"pool", wp.name,
	)
	defer wp.log.InfoContext(context.Background(), "worker stopped",
		"worker_id", workerID,
		"pool", wp.name,
	)

	currentInterval := 1 * time.Millisecond
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	for {
		select {
		case <-wp.ctx.Done():
			return

		case <-ticker.C:
			ctx := WithWorkerID(wp.ctx, workerID)
			err := wp.workWithPanicRecovery(ctx)

			wp.stats.iterations.Add(1)
			if err != nil && !errors.Is(err, ErrNoWork) {
				wp.stats.errors.Add(1)
			}

			var newInterval time.Duration

			if err != nil {
				if errors.Is(err, ErrWorkerShutdown) {
					wp.log.InfoContext(ctx, "worker shutting down as requested",
						"worker_id", workerID)
					return
				}

				if errors.Is(err, ErrPoolShutdown) {
					wp.log.ErrorContext(ctx, "worker requesting pool shutdown",
						"worker_id", workerID,
						"error", err)
					select {
					case wp.errors <- fmt.Errorf("worker %s: %w", workerID, err):
					default:
						wp.log.ErrorContext(ctx, "error channel full",
							"worker_id", workerID)
					}
					return
				}

				if errors.Is(err, ErrNoWork) {
					newInterval = wp.idleInterval
					if currentInterval != wp.idleInterval {
						wp.log.InfoContext(ctx, "no work available, switching to idle",
							"worker_id", workerID,
							"idle_interval", wp.idleInterval)
					}
				} else {
					newInterval = wp.pollInterval
				}
			} else {
				newInterval = wp.pollInterval
				if currentInterval != wp.pollInterval {
					wp.log.InfoContext(ctx, "work completed, switching to active",
						"worker_id", workerID,
						"poll_interval", wp.pollInterval)
				}
			}

			if newInterval != currentInterval {
				currentInterval = newInterval
				ticker.Reset(newInterval)
			}
		}
	}
}

// workWithPanicRecovery wraps the middleware chain with panic recovery.
func (wp *WorkerPool) workWithPanicRecovery(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			wp.log.ErrorContext(context.Background(), "panic recovered in worker",
				"worker_id", GetWorkerID(ctx),
				"panic", r,
				"stack", string(stack))

			wp.stats.panics.Add(1)
			err = fmt.Errorf("panic recovered: %v", r)
		}
	}()

	return wp.workFunc(ctx)
}
