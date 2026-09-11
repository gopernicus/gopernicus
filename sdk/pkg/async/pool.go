// Package async provides a bounded fire-and-forget goroutine pool with panic
// recovery.
//
// Use Pool for best-effort work — cache invalidation, notifications, warmups —
// where you want to cap concurrency. Tasks are submitted with Go/GoContext and
// run once; the pool never polls, persists, or retries.
//
// GoContext's context bounds admission, not the task's lifetime. A task can
// outlive its submitting request; cancellation of running work belongs to the
// task. Work that must survive a restart needs a durable queue and executor,
// such as the jobs pocket, rather than a goroutine pool alone.
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
	"runtime/debug"
	"sync"
	"sync/atomic"
)

const defaultMaxConcurrency = 100

// PoolOption configures a Pool at construction.
type PoolOption func(*poolConfig)

type poolConfig struct {
	maxConcurrency int
	dropOnFull     bool
	logger         *slog.Logger
}

// WithMaxConcurrency sets the maximum number of concurrent goroutines.
// Default: 100.
func WithMaxConcurrency(n int) PoolOption {
	return func(c *poolConfig) { c.maxConcurrency = n }
}

// WithDropOnFull controls behavior when at max concurrency.
// If true, new tasks are dropped immediately (non-blocking).
// If false, Go blocks until a slot is available.
// Default: false (blocking).
func WithDropOnFull(drop bool) PoolOption {
	return func(c *poolConfig) { c.dropOnFull = drop }
}

// WithLogger sets the logger for the pool.
// A nil logger uses slog.Default().
func WithLogger(log *slog.Logger) PoolOption {
	return func(c *poolConfig) { c.logger = log }
}

// Stats holds a point-in-time snapshot of pool counters.
type Stats struct {
	Active  int64 // Currently running goroutines.
	Total   int64 // Total tasks started.
	Dropped int64 // Tasks dropped (when DropOnFull is true).
	Panics  int64 // Recovered panics.
}

// Pool manages a bounded set of goroutines with panic recovery.
type Pool struct {
	log       *slog.Logger
	semaphore chan struct{}
	wg        sync.WaitGroup
	config    poolConfig

	active  atomic.Int64
	total   atomic.Int64
	dropped atomic.Int64
	panics  atomic.Int64

	admissionMu sync.Mutex
	closed      bool
	closing     chan struct{}
	drained     chan struct{}
}

// NewPool creates a Pool with the given options. A nil option panics.
//
//	pool := async.NewPool(async.WithMaxConcurrency(50))
func NewPool(opts ...PoolOption) *Pool {
	cfg := poolConfig{
		maxConcurrency: defaultMaxConcurrency,
		logger:         slog.Default(),
	}

	for _, opt := range opts {
		if opt == nil {
			panic("async.NewPool: nil option")
		}
		opt(&cfg)
	}

	if cfg.maxConcurrency <= 0 {
		cfg.maxConcurrency = defaultMaxConcurrency
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}

	return &Pool{
		log:       cfg.logger,
		semaphore: make(chan struct{}, cfg.maxConcurrency),
		config:    cfg,
		closing:   make(chan struct{}),
		drained:   make(chan struct{}),
	}
}

// Go executes fn in a goroutine with panic recovery.
//
// When at max concurrency:
//   - DropOnFull=false (default): blocks until a slot is available.
//   - DropOnFull=true: returns false immediately (task dropped).
//
// Returns true if the task was started, false if dropped or the pool is closed.
func (p *Pool) Go(fn func()) bool {
	return p.GoContext(context.Background(), fn)
}

// GoContext admits fn unless ctx is canceled, the pool is closing, or a full
// pool is configured to drop work. Otherwise it waits for capacity. The context
// controls admission only; once admitted, fn runs independently of ctx.
func (p *Pool) GoContext(ctx context.Context, fn func()) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case <-p.closing:
		return false
	default:
	}

	if p.config.dropOnFull {
		select {
		case p.semaphore <- struct{}{}:
		case <-ctx.Done():
			return false
		case <-p.closing:
			return false
		default:
			p.dropped.Add(1)
			p.log.Warn("async pool: task dropped",
				"max_concurrency", p.config.maxConcurrency,
				"dropped_total", p.dropped.Load(),
			)
			return false
		}
	} else {
		select {
		case p.semaphore <- struct{}{}:
		case <-ctx.Done():
			return false
		case <-p.closing:
			return false
		}
	}

	// Capacity can become available alongside cancellation or closing. Register
	// admitted work under the same gate that starts Close's WaitGroup wait.
	p.admissionMu.Lock()
	if p.closed || ctx.Err() != nil {
		p.admissionMu.Unlock()
		<-p.semaphore
		return false
	}
	p.active.Add(1)
	p.total.Add(1)
	p.wg.Add(1)
	p.admissionMu.Unlock()

	go p.execute(fn)
	return true
}

func (p *Pool) execute(fn func()) {
	defer func() {
		// Panic accounting must complete before wg.Done — otherwise Wait can
		// return while the panic counter is still unwritten and a
		// Wait-then-Stats caller reads a torn count.
		if r := recover(); r != nil {
			p.panics.Add(1)
			p.log.Error("async pool: panic recovered",
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}

		p.active.Add(-1)
		<-p.semaphore
		p.wg.Done()
	}()

	fn()
}

// Stats returns a snapshot of the current counters.
func (p *Pool) Stats() Stats {
	return Stats{
		Active:  p.active.Load(),
		Total:   p.total.Load(),
		Dropped: p.dropped.Load(),
		Panics:  p.panics.Load(),
	}
}

// Wait waits for an admitted batch to finish. Callers must finish all Go and
// GoContext calls for that batch before calling Wait, and start the next batch
// only after Wait returns. Wait keeps the pool open; use Close when admission
// and shutdown may happen concurrently.
func (p *Pool) Wait() {
	p.wg.Wait()
}

// Close stops admission, wakes blocked submitters, and waits for admitted work
// to finish. It returns nil only after that work drains, or ctx.Err() if this
// caller stops waiting. Close does not cancel tasks. Concurrent and repeated
// calls share the drain operation and each respects its own context.
func (p *Pool) Close(ctx context.Context) error {
	p.admissionMu.Lock()
	if !p.closed {
		p.closed = true
		close(p.closing)
		go func() {
			p.wg.Wait()
			p.log.Info("async pool: closed gracefully",
				"total_tasks", p.total.Load(),
				"dropped", p.dropped.Load(),
				"panics", p.panics.Load(),
			)
			close(p.drained)
		}()
	}
	p.admissionMu.Unlock()

	select {
	case <-p.drained:
		return nil
	default:
	}
	select {
	case <-p.drained:
		return nil
	case <-ctx.Done():
		p.log.Warn("async pool: close cancelled",
			"active", p.active.Load(),
		)
		return ctx.Err()
	}
}
