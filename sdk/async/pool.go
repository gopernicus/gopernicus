// Package async provides a bounded goroutine pool with panic recovery.
//
// Use Pool for fire-and-forget async tasks where you want to limit concurrency
// without the overhead of a full worker system.
//
// Example:
//
//	pool := async.NewPool(
//	    async.WithLogger(log),
//	    async.WithMaxConcurrency(50),
//	    async.WithDropOnFull(true),
//	)
//	defer pool.Close(ctx)
//
//	pool.Go(func() {
//	    invalidateCache(...)
//	})
package async

import (
	"context"
	"log/slog"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// PoolOption configures a Pool.
type PoolOption func(*poolConfig)

type poolConfig struct {
	maxConcurrency  int
	dropOnFull      bool
	shutdownTimeout time.Duration
	logger          *slog.Logger
}

// WithMaxConcurrency sets the maximum number of concurrent goroutines.
// Default: 100.
func WithMaxConcurrency(n int) PoolOption {
	return func(c *poolConfig) { c.maxConcurrency = n }
}

// WithDropOnFull controls behavior when at max concurrency.
// If true, new tasks are dropped immediately (non-blocking).
// If false, Go() blocks until a slot is available.
// Default: false (blocking).
func WithDropOnFull(drop bool) PoolOption {
	return func(c *poolConfig) { c.dropOnFull = drop }
}

// WithShutdownTimeout sets how long Close() waits for in-flight tasks.
// Default: 30s.
func WithShutdownTimeout(d time.Duration) PoolOption {
	return func(c *poolConfig) { c.shutdownTimeout = d }
}

// WithLogger sets the logger for the pool.
// Default: slog.Default().
func WithLogger(log *slog.Logger) PoolOption {
	return func(c *poolConfig) { c.logger = log }
}

// IOPreset returns options optimized for I/O-bound tasks.
// High concurrency (1000), drop on full, 30s shutdown.
//
// Best for: HTTP calls, database queries, cache operations, file I/O.
func IOPreset() []PoolOption {
	return []PoolOption{
		WithMaxConcurrency(1000),
		WithDropOnFull(true),
		WithShutdownTimeout(30 * time.Second),
	}
}

// CPUPreset returns options optimized for CPU-bound tasks.
// Concurrency matches GOMAXPROCS, blocking mode, 60s shutdown.
//
// Best for: PDF generation, image resizing, compression, encoding.
func CPUPreset() []PoolOption {
	return []PoolOption{
		WithMaxConcurrency(runtime.GOMAXPROCS(0)),
		WithDropOnFull(false),
		WithShutdownTimeout(60 * time.Second),
	}
}

// Stats holds runtime statistics.
type Stats struct {
	Active  int64 // Currently running goroutines
	Total   int64 // Total tasks started
	Dropped int64 // Tasks dropped (when DropOnFull=true)
	Panics  int64 // Recovered panics
}

// Pool manages a bounded pool of goroutines with panic recovery.
type Pool struct {
	log       *slog.Logger
	semaphore chan struct{}
	wg        sync.WaitGroup
	config    poolConfig

	active  int64
	total   int64
	dropped int64
	panics  int64

	closed  atomic.Bool
	closeMu sync.Mutex
}

// NewPool creates a new Pool with the given options.
//
//	pool := async.NewPool(async.WithMaxConcurrency(50))
//	pool := async.NewPool(async.IOPreset()...)
//	pool := async.NewPool(async.CPUPreset()..., async.WithLogger(log))
func NewPool(opts ...PoolOption) *Pool {
	cfg := poolConfig{
		maxConcurrency:  100,
		dropOnFull:      false,
		shutdownTimeout: 30 * time.Second,
		logger:          slog.Default(),
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.maxConcurrency <= 0 {
		cfg.maxConcurrency = 100
	}

	return &Pool{
		log:       cfg.logger,
		semaphore: make(chan struct{}, cfg.maxConcurrency),
		config:    cfg,
	}
}

// Go executes fn in a goroutine with panic recovery.
//
// When at max concurrency:
//   - DropOnFull=false (default): blocks until a slot is available
//   - DropOnFull=true: returns false immediately (task dropped)
//
// Returns true if the task was started, false if dropped or pool is closed.
func (p *Pool) Go(fn func()) bool {
	if p.closed.Load() {
		return false
	}

	if p.config.dropOnFull {
		select {
		case p.semaphore <- struct{}{}:
		default:
			atomic.AddInt64(&p.dropped, 1)
			p.log.Warn("async pool: task dropped",
				"max_concurrency", p.config.maxConcurrency,
				"dropped_total", atomic.LoadInt64(&p.dropped),
			)
			return false
		}
	} else {
		p.semaphore <- struct{}{}
	}

	atomic.AddInt64(&p.active, 1)
	atomic.AddInt64(&p.total, 1)
	p.wg.Add(1)

	go p.execute(fn)
	return true
}

// GoContext executes fn with context awareness.
// If ctx is cancelled before a slot is available, returns false.
func (p *Pool) GoContext(ctx context.Context, fn func()) bool {
	if p.closed.Load() {
		return false
	}

	if p.config.dropOnFull {
		select {
		case p.semaphore <- struct{}{}:
		case <-ctx.Done():
			return false
		default:
			atomic.AddInt64(&p.dropped, 1)
			p.log.Warn("async pool: task dropped",
				"max_concurrency", p.config.maxConcurrency,
				"dropped_total", atomic.LoadInt64(&p.dropped),
			)
			return false
		}
	} else {
		select {
		case p.semaphore <- struct{}{}:
		case <-ctx.Done():
			return false
		}
	}

	atomic.AddInt64(&p.active, 1)
	atomic.AddInt64(&p.total, 1)
	p.wg.Add(1)

	go p.execute(fn)
	return true
}

func (p *Pool) execute(fn func()) {
	defer func() {
		<-p.semaphore
		atomic.AddInt64(&p.active, -1)
		p.wg.Done()

		if r := recover(); r != nil {
			atomic.AddInt64(&p.panics, 1)
			p.log.Error("async pool: panic recovered",
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()

	fn()
}

// Stats returns current runtime statistics.
func (p *Pool) Stats() Stats {
	return Stats{
		Active:  atomic.LoadInt64(&p.active),
		Total:   atomic.LoadInt64(&p.total),
		Dropped: atomic.LoadInt64(&p.dropped),
		Panics:  atomic.LoadInt64(&p.panics),
	}
}

// Wait blocks until all in-flight tasks complete.
// Does not prevent new tasks from being submitted.
func (p *Pool) Wait() {
	p.wg.Wait()
}

// Close gracefully shuts down the pool.
// After Close(), Go() and GoContext() return false immediately.
// Waits for in-flight tasks up to ShutdownTimeout.
func (p *Pool) Close(ctx context.Context) error {
	p.closeMu.Lock()
	if p.closed.Load() {
		p.closeMu.Unlock()
		return nil
	}
	p.closed.Store(true)
	p.closeMu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	timeout := p.config.shutdownTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}

	select {
	case <-done:
		p.log.Info("async pool: closed gracefully",
			"total_tasks", atomic.LoadInt64(&p.total),
			"dropped", atomic.LoadInt64(&p.dropped),
			"panics", atomic.LoadInt64(&p.panics),
		)
		return nil
	case <-time.After(timeout):
		p.log.Warn("async pool: close timed out",
			"timeout", timeout,
			"active", atomic.LoadInt64(&p.active),
		)
		return nil
	case <-ctx.Done():
		p.log.Warn("async pool: close cancelled",
			"active", atomic.LoadInt64(&p.active),
		)
		return ctx.Err()
	}
}
