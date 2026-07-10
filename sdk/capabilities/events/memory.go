package events

import (
	"context"
	"log/slog"
	"sync"
)

const (
	defaultWorkerCount = 4
	defaultQueueSize   = 1000
)

// Memory is the in-process pub/sub Bus that ships with sdk — no external
// dependency, handy for development and single-instance deployments.
//
// Delivery guarantee: at-most-once, in-process, no persistence, no replay.
// Emit dispatches asynchronously by default (it returns immediately; handler
// errors and panics are recovered and logged, never returned) so an emitter's
// latency never depends on its slowest subscriber. WithSync forces synchronous
// delivery for deterministic tests and same-request flows. The async queue is
// bounded: when it is full, events are dropped with a warning rather than
// blocking the emitter. Async handlers run under context.WithoutCancel(ctx) so
// request cancellation does not abort side work while values (request/trace
// IDs) survive. Close drains in-flight handlers up to the context deadline.
//
// Memory satisfies Broadcaster trivially: a single process already fans every
// event out to all subscribers, so SubscribeBroadcast is plain Subscribe.
type Memory struct {
	log *slog.Logger

	mu            sync.RWMutex
	subscriptions map[string][]*memorySubscription
	nextID        uint64

	asyncChan chan asyncEvent
	wg        sync.WaitGroup

	closeMu sync.Mutex
	closed  bool
}

var (
	_ Bus         = (*Memory)(nil)
	_ Broadcaster = (*Memory)(nil)
)

// asyncEvent carries an event queued for background dispatch.
type asyncEvent struct {
	ctx   context.Context
	event Event
}

// memorySubscription tracks a single handler registration.
type memorySubscription struct {
	id      uint64
	topic   string
	handler Handler
	bus     *Memory
	once    sync.Once
}

// Unsubscribe removes this subscription from the bus. Safe to call more than
// once.
func (s *memorySubscription) Unsubscribe() error {
	s.once.Do(func() {
		s.bus.removeSubscription(s.id, s.topic)
	})
	return nil
}

// MemoryOption configures a Memory bus at construction.
type MemoryOption func(*memoryConfig)

type memoryConfig struct {
	logger      *slog.Logger
	workerCount int
	queueSize   int
}

// WithLogger sets the logger. Default: slog.Default().
func WithLogger(log *slog.Logger) MemoryOption {
	return func(c *memoryConfig) { c.logger = log }
}

// WithWorkerCount sets the number of async dispatch workers. Default: 4.
func WithWorkerCount(n int) MemoryOption {
	return func(c *memoryConfig) { c.workerCount = n }
}

// WithQueueSize sets the bounded async queue depth. Default: 1000.
func WithQueueSize(n int) MemoryOption {
	return func(c *memoryConfig) { c.queueSize = n }
}

// NewMemory creates an in-process Memory bus and starts its async workers.
func NewMemory(opts ...MemoryOption) *Memory {
	cfg := memoryConfig{
		logger:      slog.Default(),
		workerCount: defaultWorkerCount,
		queueSize:   defaultQueueSize,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.workerCount <= 0 {
		cfg.workerCount = defaultWorkerCount
	}
	if cfg.queueSize <= 0 {
		cfg.queueSize = defaultQueueSize
	}

	b := &Memory{
		log:           cfg.logger,
		subscriptions: make(map[string][]*memorySubscription),
		asyncChan:     make(chan asyncEvent, cfg.queueSize),
	}

	for i := 0; i < cfg.workerCount; i++ {
		b.wg.Add(1)
		go b.worker(i)
	}
	return b
}

// worker drains the async queue with panic recovery.
func (b *Memory) worker(id int) {
	defer b.wg.Done()
	for ae := range b.asyncChan {
		b.dispatchRecovered(id, ae.ctx, ae.event)
	}
}

// dispatchRecovered dispatches one event, recovering panics so a bad handler
// never takes down a worker goroutine.
func (b *Memory) dispatchRecovered(workerID int, ctx context.Context, event Event) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("events: panic in async handler recovered",
				"worker", workerID,
				"event_type", event.Type(),
				"correlation_id", event.CorrelationID(),
				"panic", r,
			)
		}
	}()

	if err := b.dispatch(ctx, event); err != nil {
		b.log.Error("events: async handler failed",
			"worker", workerID,
			"event_type", event.Type(),
			"correlation_id", event.CorrelationID(),
			"error", err,
		)
	}
}

// Emit publishes an event. Async by default; WithSync dispatches synchronously
// and returns the first handler error.
func (b *Memory) Emit(ctx context.Context, event Event, opts ...EmitOption) error {
	cfg := ApplyOptions(opts...)

	if cfg.Sync {
		b.closeMu.Lock()
		closed := b.closed
		b.closeMu.Unlock()
		if closed {
			b.warnDropped(event, "bus closed")
			return nil
		}
		return b.dispatch(ctx, event)
	}
	return b.emitAsync(ctx, event)
}

// emitAsync enqueues the event for background dispatch. It holds closeMu across
// the enqueue so a concurrent Close cannot close the channel mid-send; the
// non-blocking select means the lock is never held while waiting.
func (b *Memory) emitAsync(ctx context.Context, event Event) error {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()

	if b.closed {
		b.warnDropped(event, "bus closed")
		return nil
	}

	// Detach from request cancellation while preserving context values.
	asyncCtx := context.WithoutCancel(ctx)

	select {
	case b.asyncChan <- asyncEvent{ctx: asyncCtx, event: event}:
	default:
		b.warnDropped(event, "queue full")
	}
	return nil
}

func (b *Memory) warnDropped(event Event, reason string) {
	b.log.Warn("events: event dropped",
		"reason", reason,
		"event_type", event.Type(),
		"correlation_id", event.CorrelationID(),
	)
}

// dispatch calls every handler subscribed to the event's exact topic and to the
// "*" wildcard, returning the first handler error.
func (b *Memory) dispatch(ctx context.Context, event Event) error {
	b.mu.RLock()
	var handlers []Handler
	for _, sub := range b.subscriptions[event.Type()] {
		handlers = append(handlers, sub.handler)
	}
	for _, sub := range b.subscriptions["*"] {
		handlers = append(handlers, sub.handler)
	}
	b.mu.RUnlock()

	var firstErr error
	for _, h := range handlers {
		if err := h(ctx, event); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			b.log.Error("events: handler error",
				"event_type", event.Type(),
				"correlation_id", event.CorrelationID(),
				"error", err,
			)
		}
	}
	return firstErr
}

// Subscribe registers a handler for an exact topic or "*".
func (b *Memory) Subscribe(topic string, handler Handler) (Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	sub := &memorySubscription{
		id:      b.nextID,
		topic:   topic,
		handler: handler,
		bus:     b,
	}
	b.subscriptions[topic] = append(b.subscriptions[topic], sub)
	return sub, nil
}

// SubscribeBroadcast implements Broadcaster. In one process broadcast is plain
// Subscribe.
func (b *Memory) SubscribeBroadcast(topic string, handler Handler) (Subscription, error) {
	return b.Subscribe(topic, handler)
}

func (b *Memory) removeSubscription(id uint64, topic string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subs := b.subscriptions[topic]
	for i, sub := range subs {
		if sub.id == id {
			b.subscriptions[topic] = append(subs[:i], subs[i+1:]...)
			return
		}
	}
}

// Close stops the bus, draining in-flight async handlers up to the context
// deadline. It is idempotent; a later Emit is dropped with a warning.
func (b *Memory) Close(ctx context.Context) error {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return nil
	}
	b.closed = true
	close(b.asyncChan)
	b.closeMu.Unlock()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		b.log.Warn("events: memory bus close timed out; some async handlers may not have run")
		return nil
	}
}
