// Package memorybus provides an in-memory event bus for development and single-instance deployments.
package memorybus

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// asyncEvent wraps an event for async processing.
type asyncEvent struct {
	ctx      context.Context
	event    events.Event
	priority int
}

// subscription tracks a single handler registration.
type subscription struct {
	id        uint64
	topic     string
	handler   events.Handler
	bus       *Bus
	once      sync.Once
	cancelled bool
}

// Unsubscribe removes this subscription from the bus.
// Safe to call multiple times.
func (s *subscription) Unsubscribe() error {
	s.once.Do(func() {
		s.bus.removeSubscription(s.id, s.topic)
		s.cancelled = true
	})
	return nil
}

// Bus is an in-memory event bus with async worker support.
type Bus struct {
	log           *slog.Logger
	subscriptions map[string][]*subscription
	mu            sync.RWMutex
	asyncChan     chan asyncEvent
	wg            sync.WaitGroup
	closed        bool
	closeMu       sync.Mutex
	nextID        uint64
}

// Option configures the memory bus.
type Option func(*Bus)

// WithWorkerCount sets the number of async worker goroutines. Default is 4.
func WithWorkerCount(n int) Option {
	return func(b *Bus) {
		if n > 0 {
			b.asyncChan = make(chan asyncEvent, cap(b.asyncChan))
			// Will be applied during New.
		}
	}
}

// WithQueueSize sets the buffer size for the async event queue. Default is 1000.
func WithQueueSize(n int) Option {
	return func(b *Bus) {
		if n > 0 {
			b.asyncChan = make(chan asyncEvent, n)
		}
	}
}

// New creates a new in-memory event bus.
func New(log *slog.Logger, opts ...Option) *Bus {
	b := &Bus{
		log:           log,
		subscriptions: make(map[string][]*subscription),
		asyncChan:     make(chan asyncEvent, 1000),
	}

	workers := 4
	for _, opt := range opts {
		opt(b)
	}

	// Start async workers.
	for i := 0; i < workers; i++ {
		b.wg.Add(1)
		go b.worker(i)
	}

	log.Info("memory event bus started", "workers", workers, "queue_size", cap(b.asyncChan))
	return b
}

// worker processes async events with panic recovery.
func (b *Bus) worker(id int) {
	defer b.wg.Done()

	for ae := range b.asyncChan {
		b.dispatchWithRecovery(id, ae.ctx, ae.event)
	}
}

// dispatchWithRecovery wraps dispatch with panic recovery.
func (b *Bus) dispatchWithRecovery(workerID int, ctx context.Context, event events.Event) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("panic in event handler recovered",
				"worker", workerID,
				"event_type", event.Type(),
				"correlation_id", event.CorrelationID(),
				"panic", r,
			)
		}
	}()

	if err := b.dispatch(ctx, event); err != nil {
		b.log.Error("async event handler failed",
			"worker", workerID,
			"event_type", event.Type(),
			"correlation_id", event.CorrelationID(),
			"error", err,
		)
	}
}

// Emit publishes an event to all registered handlers.
func (b *Bus) Emit(ctx context.Context, event events.Event, opts ...events.EmitOption) error {
	cfg := events.ApplyOptions(opts...)

	if cfg.Sync {
		return b.dispatch(ctx, event)
	}
	return b.emitAsync(ctx, event, cfg)
}

// emitAsync queues the event for background processing.
func (b *Bus) emitAsync(ctx context.Context, event events.Event, cfg events.EmitConfig) error {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		b.log.Warn("event dropped - bus closed",
			"event_type", event.Type(),
			"correlation_id", event.CorrelationID(),
		)
		return nil
	}
	b.closeMu.Unlock()

	// Detach from request context cancellation while preserving values.
	asyncCtx := context.WithoutCancel(ctx)

	select {
	case b.asyncChan <- asyncEvent{ctx: asyncCtx, event: event, priority: cfg.Priority}:
		// Queued successfully.
	default:
		b.log.Warn("event dropped - queue full",
			"event_type", event.Type(),
			"correlation_id", event.CorrelationID(),
		)
	}

	return nil
}

// dispatch sends the event to all matching handlers.
func (b *Bus) dispatch(ctx context.Context, event events.Event) error {
	b.mu.RLock()
	var handlers []events.Handler
	if subs, ok := b.subscriptions[event.Type()]; ok {
		for _, sub := range subs {
			if !sub.cancelled {
				handlers = append(handlers, sub.handler)
			}
		}
	}
	if subs, ok := b.subscriptions["*"]; ok {
		for _, sub := range subs {
			if !sub.cancelled {
				handlers = append(handlers, sub.handler)
			}
		}
	}
	b.mu.RUnlock()

	if len(handlers) == 0 {
		return nil
	}

	var firstErr error
	for _, h := range handlers {
		if err := h(ctx, event); err != nil {
			b.log.Error("event handler error",
				"event_type", event.Type(),
				"correlation_id", event.CorrelationID(),
				"error", err,
			)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// Subscribe registers a handler for events of the given topic.
func (b *Bus) Subscribe(topic string, handler events.Handler) (events.Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := &subscription{
		id:      atomic.AddUint64(&b.nextID, 1),
		topic:   topic,
		handler: handler,
		bus:     b,
	}

	b.subscriptions[topic] = append(b.subscriptions[topic], sub)
	return sub, nil
}

// removeSubscription removes a subscription by ID.
func (b *Bus) removeSubscription(id uint64, topic string) {
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

// Close gracefully shuts down the bus.
func (b *Bus) Close(ctx context.Context) error {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return nil
	}
	b.closed = true
	b.closeMu.Unlock()

	close(b.asyncChan)

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		b.log.Info("memory event bus closed gracefully")
	case <-ctx.Done():
		b.log.Warn("memory event bus close timed out - some events may be lost")
	}

	return nil
}

// Compile-time interface check.
var _ events.Bus = (*Bus)(nil)
