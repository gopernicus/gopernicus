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

const (
	defaultWorkerCount  = 1
	defaultPollInterval = 5 * time.Second
	defaultIdleInterval = 30 * time.Second

	// initialInterval is the first tick delay so a fresh worker runs its first
	// iteration almost immediately, then adapts to poll/idle.
	initialInterval = time.Millisecond
)

// PoolOption configures a Pool at construction.
type PoolOption func(*poolConfig)

type poolConfig struct {
	name         string
	workerCount  int
	pollInterval time.Duration
	idleInterval time.Duration
	heartbeat    time.Duration
	middlewares  []Middleware
	logger       *slog.Logger
	wake         <-chan struct{}
}

// WithName sets the pool name used in logs and worker IDs.
func WithName(name string) PoolOption {
	return func(c *poolConfig) { c.name = name }
}

// WithWorkerCount sets how many worker goroutines the pool runs. Values below 1
// are clamped to 1.
func WithWorkerCount(count int) PoolOption {
	return func(c *poolConfig) { c.workerCount = count }
}

// WithPollInterval sets the delay between iterations while work is flowing.
func WithPollInterval(interval time.Duration) PoolOption {
	return func(c *poolConfig) { c.pollInterval = interval }
}

// WithIdleInterval sets the delay between iterations after a worker sees
// ErrNoWork. The idle interval is the cross-process backstop that guarantees
// eventual pickup even when no wake signal arrives.
func WithIdleInterval(interval time.Duration) PoolOption {
	return func(c *poolConfig) { c.idleInterval = interval }
}

// WithHeartbeat turns on a periodic INFO "pool alive" line carrying the pool
// name, the active worker count, and the iteration, claim, and error counts
// since the previous beat. The pool beats even when every delta is zero, so a
// MISSING heartbeat is the alarm: an idle pool proves it is alive and polling
// instead of leaving silence as the steady state.
//
// An interval of zero or less disables the heartbeat, which is the default.
// Unlike the poll and idle intervals there is no fallback clamp — an unset
// value has to keep meaning "no heartbeat". There is no minimum either; a
// sub-second interval is operator noise, not an error.
func WithHeartbeat(interval time.Duration) PoolOption {
	return func(c *poolConfig) { c.heartbeat = interval }
}

// WithMiddleware adds middleware around the WorkFunc. The first middleware
// passed is the outermost wrapper.
func WithMiddleware(middlewares ...Middleware) PoolOption {
	return func(c *poolConfig) { c.middlewares = append(c.middlewares, middlewares...) }
}

// WithLogger sets the pool's logger. Defaults to slog.Default().
func WithLogger(logger *slog.Logger) PoolOption {
	return func(c *poolConfig) { c.logger = logger }
}

// WithWakeChannel registers a signal channel the pool selects on alongside its
// interval timer. Receiving a value runs the next iteration immediately instead
// of waiting out the current poll or idle interval — the latency coupling that
// lets an enqueue run promptly.
//
// The channel is a non-durable hint. Producers must use a buffered cap-1
// channel and a non-blocking send so coalesced signals never block:
//
//	wake := make(chan struct{}, 1)
//	select {
//	case wake <- struct{}{}:
//	default:
//	}
//
// Lost signals are tolerated: many sends collapse into at most one buffered
// wake, and the idle/poll interval is the backstop that catches any work a
// dropped signal missed. A nil channel disables the pocket — the pool behaves
// exactly as if the option were not passed. Do not close the wake channel: a
// closed channel reads ready forever and would spin iterations until ctx is
// cancelled. Producers tear down without closing it.
func WithWakeChannel(wake <-chan struct{}) PoolOption {
	return func(c *poolConfig) { c.wake = wake }
}

// Pool runs N worker goroutines that call a WorkFunc in an adaptive polling
// loop. It owns polling cadence, panic recovery, and graceful drain; it knows
// nothing about jobs or tasks — that is the Runner's concern.
type Pool struct {
	name         string
	workerCount  int
	pollInterval time.Duration
	idleInterval time.Duration
	heartbeat    time.Duration
	log          *slog.Logger
	wake         <-chan struct{}

	workFunc WorkFunc
	stats    stats
	wg       sync.WaitGroup
	errors   chan error
}

// heartbeatCounters is the heartbeat's local view of the pool's counters: the
// public snapshot plus the private claims total it reports as a delta.
type heartbeatCounters struct {
	stats  Stats
	claims int64
}

// NewPool builds a Pool that calls work in a loop on each of its workers.
// Options set sizing and cadence; unset values fall back to sensible defaults
// (1 worker, 5s poll, 30s idle).
func NewPool(work WorkFunc, opts ...PoolOption) *Pool {
	c := &poolConfig{
		name:         "worker",
		workerCount:  defaultWorkerCount,
		pollInterval: defaultPollInterval,
		idleInterval: defaultIdleInterval,
	}
	for _, opt := range opts {
		opt(c)
	}

	if c.logger == nil {
		c.logger = slog.Default()
	}
	if c.workerCount < 1 {
		c.workerCount = 1
	}
	if c.pollInterval <= 0 {
		c.pollInterval = defaultPollInterval
	}
	if c.idleInterval <= 0 {
		c.idleInterval = defaultIdleInterval
	}

	p := &Pool{
		name:         c.name,
		workerCount:  c.workerCount,
		pollInterval: c.pollInterval,
		idleInterval: c.idleInterval,
		heartbeat:    c.heartbeat,
		log:          c.logger,
		wake:         c.wake,
		errors:       make(chan error, c.workerCount),
	}

	p.workFunc = work
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		p.workFunc = c.middlewares[i](p.workFunc)
	}

	return p
}

// Run launches the workers and blocks until they all exit. Cancelling ctx
// initiates a graceful drain: no worker starts a new iteration once ctx is
// done, but any in-flight WorkFunc call runs to completion. Run returns nil
// after the last worker has drained and stopped. Critical failures
// (ErrPoolShutdown, recovered panics) are surfaced on Errors() as they happen.
func (p *Pool) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	start := time.Now()
	p.log.InfoContext(ctx, "worker pool starting",
		"pool", p.name,
		"worker_count", p.workerCount,
		"poll_interval", p.pollInterval,
		"idle_interval", p.idleInterval,
	)

	// The heartbeat baseline is read synchronously, before anything is
	// launched, so goroutine scheduling cannot hide startup work from the first
	// beat.
	var heartbeatDone chan struct{}
	if p.heartbeat > 0 {
		baseline := p.heartbeatSnapshot()
		heartbeatDone = make(chan struct{})
		go p.runHeartbeat(ctx, baseline, heartbeatDone)
	}

	for i := 0; i < p.workerCount; i++ {
		workerID := fmt.Sprintf("%s-worker-%d", p.name, i+1)
		p.wg.Add(1)
		go p.worker(ctx, cancel, workerID)
	}

	p.wg.Wait()

	// The heartbeat is not in p.wg: workers can all exit via ErrWorkerShutdown
	// without ctx ever being cancelled, and a heartbeat parked in the WaitGroup
	// would then tick forever with Run blocked on Wait. Cancel once the workers
	// are done, then wait for the heartbeat on its own channel.
	cancel()
	if heartbeatDone != nil {
		<-heartbeatDone
	}

	close(p.errors)

	p.log.InfoContext(context.Background(), "worker pool stopped",
		"pool", p.name,
		"runtime", time.Since(start),
		"stats", p.Stats(),
	)
	return nil
}

// Stats returns a point-in-time snapshot of the pool's counters.
func (p *Pool) Stats() Stats {
	return p.stats.snapshot()
}

// Errors returns the channel of critical errors (ErrPoolShutdown and recovered
// panics). The pool closes it once Run returns, so callers may range over it.
func (p *Pool) Errors() <-chan error {
	return p.errors
}

// worker is the loop for a single worker goroutine.
func (p *Pool) worker(ctx context.Context, cancel context.CancelFunc, workerID string) {
	defer p.wg.Done()

	p.stats.activeWorkers.Add(1)
	defer p.stats.activeWorkers.Add(-1)

	p.log.InfoContext(ctx, "worker started", "pool", p.name, "worker_id", workerID)
	defer p.log.InfoContext(context.Background(), "worker stopped", "pool", p.name, "worker_id", workerID)

	interval := initialInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.wake:
		}

		// Cancellation takes precedence over a coincident tick or wake: once
		// ctx is done a worker never begins a fresh iteration, which is what
		// makes the drain graceful (in-flight calls already past this point
		// still finish).
		if ctx.Err() != nil {
			return
		}

		err := p.runOnce(WithWorkerID(ctx, workerID), workerID)

		p.stats.iterations.Add(1)
		if err == nil {
			p.stats.claims.Add(1)
		}
		if err != nil && !errors.Is(err, ErrNoWork) {
			p.stats.errors.Add(1)
		}

		switch {
		case errors.Is(err, ErrWorkerShutdown):
			p.log.InfoContext(ctx, "worker shutting down as requested", "worker_id", workerID)
			return
		case errors.Is(err, ErrPoolShutdown):
			p.log.ErrorContext(ctx, "worker requesting pool shutdown", "worker_id", workerID, "error", err)
			select {
			case p.errors <- fmt.Errorf("worker %s: %w", workerID, err):
			default:
			}
			cancel()
			return
		}

		next := p.pollInterval
		if errors.Is(err, ErrNoWork) {
			p.log.DebugContext(ctx, "iteration: no work", "pool", p.name, "worker_id", workerID)
			next = p.idleInterval
		}
		if next != interval {
			interval = next
			ticker.Reset(next)
		}
	}
}

// heartbeatSnapshot reads the counters the heartbeat reports: the public
// snapshot plus the private claims total.
func (p *Pool) heartbeatSnapshot() heartbeatCounters {
	return heartbeatCounters{stats: p.stats.snapshot(), claims: p.stats.claims.Load()}
}

// runHeartbeat emits one INFO line per interval with the counter deltas since
// the previous beat, and closes done when it stops. It is the pool's single
// reader of previous, so the running totals need no extra synchronization.
// Counters are independent atomics rather than a transactional tuple, so an
// iteration completing on a tick may land on either adjacent beat; no delta can
// go negative.
func (p *Pool) runHeartbeat(ctx context.Context, previous heartbeatCounters, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(p.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Cancellation takes precedence over a coincident tick, mirroring the
		// worker loop: a draining pool is narrated by the runner and summarized
		// by the shutdown stats line, so a drain-time beat adds nothing.
		if ctx.Err() != nil {
			return
		}

		current := p.heartbeatSnapshot()
		p.log.InfoContext(ctx, "pool alive",
			"pool", p.name,
			"active_workers", current.stats.ActiveWorkers,
			"iterations", current.stats.Iterations-previous.stats.Iterations,
			"claims", current.claims-previous.claims,
			"errors", current.stats.Errors-previous.stats.Errors,
		)
		previous = current
	}
}

// runOnce runs the middleware-wrapped WorkFunc with panic recovery. A recovered
// panic is counted, logged, surfaced on Errors(), and returned as an error so
// the worker keeps polling.
func (p *Pool) runOnce(ctx context.Context, workerID string) (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		p.stats.panics.Add(1)
		err = fmt.Errorf("workers: panic recovered: %v", r)
		p.log.ErrorContext(context.Background(), "panic recovered in worker",
			"pool", p.name,
			"worker_id", workerID,
			"panic", r,
			"stack", string(debug.Stack()),
		)
		select {
		case p.errors <- fmt.Errorf("worker %s: %w", workerID, err):
		default:
		}
	}()

	return p.workFunc(ctx)
}
