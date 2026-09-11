package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

const (
	defaultWorkerCount = 4
	defaultQueueSize   = 1000
)

// Memory provides bounded, nonpersistent notifications and checked local
// Dispatch. Construct it with NewMemory and close it after its producers stop.
// Accepted async work retains context values but detaches request cancellation.
// Callbacks must support concurrent calls and treat events as immutable.
type Memory struct {
	log           *slog.Logger
	mu            sync.Mutex
	subscriptions map[string][]*memorySubscription
	nextID        uint64
	closed        bool
	queue         chan asyncEvent
	wg            sync.WaitGroup
	done          chan struct{}
}

type asyncEvent struct {
	ctx   context.Context
	event Event
}
type memorySubscription struct {
	id      uint64
	topic   string
	handler Handler
	bus     *Memory
	once    sync.Once
}

func (s *memorySubscription) Unsubscribe() error {
	s.once.Do(func() { s.bus.removeSubscription(s.id, s.topic) })
	return nil
}

type MemoryOption func(*memoryConfig)
type memoryConfig struct {
	logger      *slog.Logger
	workerCount int
	queueSize   int
}

// WithLogger selects the handler-error logger; nil uses slog.Default.
func WithLogger(logger *slog.Logger) MemoryOption { return func(c *memoryConfig) { c.logger = logger } }

// WithWorkerCount bounds concurrent asynchronous dispatches.
func WithWorkerCount(n int) MemoryOption { return func(c *memoryConfig) { c.workerCount = n } }

// WithQueueSize bounds waiting asynchronous notifications.
func WithQueueSize(n int) MemoryOption { return func(c *memoryConfig) { c.queueSize = n } }

// NewMemory defaults to four workers and a queue of 1000; nonpositive options
// select those defaults. A nil logger selects slog.Default. A nil option panics.
func NewMemory(options ...MemoryOption) *Memory {
	cfg := memoryConfig{logger: slog.Default(), workerCount: defaultWorkerCount, queueSize: defaultQueueSize}
	for _, option := range options {
		if option == nil {
			panic("events.NewMemory: nil option")
		}
		option(&cfg)
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
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
		queue:         make(chan asyncEvent, cfg.queueSize),
		done:          make(chan struct{}),
	}
	b.wg.Add(cfg.workerCount)
	for range cfg.workerCount {
		go b.worker()
	}
	return b
}

var (
	_ Bus         = (*Memory)(nil)
	_ Broadcaster = (*Memory)(nil)
)

// Emit accepts notification without waiting for a handler. It returns ErrCapacity
// if full, ErrClosed after shutdown, or the validation/caller cancellation error.
func (b *Memory) Emit(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateEvent(event); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed {
		return ErrClosed
	}
	select {
	case b.queue <- asyncEvent{ctx: context.WithoutCancel(ctx), event: event}:
		return nil
	default:
		return ErrCapacity
	}
}

// Dispatch invokes each selected local handler, isolating panics and returning
// the first error. Cancellation stops later callbacks and is joined with any
// earlier failure. Host wiring must register
// required handlers before using Dispatch as an outbox handoff.
func (b *Memory) Dispatch(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateEvent(event); err != nil {
		return err
	}
	b.mu.Lock()
	if err := ctx.Err(); err != nil {
		b.mu.Unlock()
		return err
	}
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	b.wg.Add(1)
	b.mu.Unlock()
	defer b.wg.Done()
	return b.dispatch(ctx, event, false)
}

func (b *Memory) worker() {
	defer b.wg.Done()
	for item := range b.queue {
		b.dispatch(item.ctx, item.event, true)
	}
}

func (b *Memory) dispatch(ctx context.Context, event Event, report bool) error {
	b.mu.Lock()
	var handlers []Handler
	for _, sub := range b.subscriptions[event.Type()] {
		handlers = append(handlers, sub.handler)
	}
	for _, sub := range b.subscriptions["*"] {
		handlers = append(handlers, sub.handler)
	}
	b.mu.Unlock()
	var firstErr error
	for _, handler := range handlers {
		if err := ctx.Err(); err != nil {
			return errors.Join(firstErr, err)
		}
		err := invoke(ctx, event, handler)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if report {
				b.log.ErrorContext(ctx, "events: handler failed", "event_type", event.Type(), "correlation_id", event.CorrelationID(), "error", err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(firstErr, err)
	}
	return firstErr
}

func invoke(ctx context.Context, event Event, handler Handler) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("%w: %v", ErrHandlerPanic, value)
		}
	}()
	return handler(ctx, event)
}

func (b *Memory) Subscribe(topic string, handler Handler) (Subscription, error) {
	if err := ValidateSubscription(topic, handler); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	b.nextID++
	sub := &memorySubscription{id: b.nextID, topic: topic, handler: handler, bus: b}
	b.subscriptions[topic] = append(b.subscriptions[topic], sub)
	return sub, nil
}
func (b *Memory) SubscribeBroadcast(topic string, handler Handler) (Subscription, error) {
	return b.Subscribe(topic, handler)
}

func (b *Memory) removeSubscription(id uint64, topic string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subscriptions[topic]
	for i, sub := range subs {
		if sub.id != id {
			continue
		}
		copy(subs[i:], subs[i+1:])
		subs[len(subs)-1] = nil
		sub.handler = nil
		subs = subs[:len(subs)-1]
		if len(subs) == 0 {
			delete(b.subscriptions, topic)
		} else {
			b.subscriptions[topic] = subs
		}
		return
	}
}

// Close initiates shutdown once. Every caller waits for the same admitted work;
// a deadline reports incomplete drain and does not pretend callbacks have stopped.
func (b *Memory) Close(ctx context.Context) error {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		close(b.queue)
		go func() {
			b.wg.Wait()
			b.mu.Lock()
			for _, subs := range b.subscriptions {
				for _, sub := range subs {
					sub.handler = nil
				}
			}
			clear(b.subscriptions)
			b.mu.Unlock()
			close(b.done)
		}()
	}
	b.mu.Unlock()
	select {
	case <-b.done:
		return nil
	default:
	}
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
