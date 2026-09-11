package goredis

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/redis/go-redis/v9"
)

const (
	defaultStreamPrefix   = "events:"
	busKeyVersion         = "v2:"
	defaultConsumerGroup  = "default"
	defaultWorkers        = 4
	defaultQueueSize      = 1000
	defaultBlockTimeout   = 5 * time.Second
	defaultRetryAfter     = time.Minute
	defaultHandlerTimeout = 30 * time.Second
	idlePoll              = 100 * time.Millisecond
	opTimeout             = 5 * time.Second
)

var (
	_ events.Bus         = (*Bus)(nil)
	_ events.Broadcaster = (*Bus)(nil)
)

// BusOption configures construction of a Bus. Options apply in order; the last
// value for each setting wins. Defaults resolve before publishers start.
type BusOption func(*busConfig)

type busConfig struct {
	log            *slog.Logger
	StreamPrefix   string
	ConsumerGroup  string
	Workers        int
	QueueSize      int
	BlockTimeout   time.Duration
	RetryAfter     time.Duration
	HandlerTimeout time.Duration
}

// WithLogger selects the bus diagnostic logger. Nil uses slog.Default().
func WithLogger(log *slog.Logger) BusOption { return func(cfg *busConfig) { cfg.log = log } }

// WithStreamPrefix selects the host namespace. Empty uses "events:"; New always
// appends the internal "v2:" suffix.
func WithStreamPrefix(prefix string) BusOption {
	return func(cfg *busConfig) { cfg.StreamPrefix = prefix }
}

// WithConsumerGroup selects the work group. Empty uses "default". Members of
// the group must share the same handler responsibilities.
func WithConsumerGroup(group string) BusOption {
	return func(cfg *busConfig) { cfg.ConsumerGroup = group }
}

// WithWorkers bounds each publisher and work-consumer pool. Nonpositive uses 4.
func WithWorkers(workers int) BusOption { return func(cfg *busConfig) { cfg.Workers = workers } }

// WithQueueSize bounds asynchronous Emit admissions waiting for a publisher.
// Nonpositive uses 1000.
func WithQueueSize(size int) BusOption { return func(cfg *busConfig) { cfg.QueueSize = size } }

// WithBlockTimeout bounds each XREADGROUP wait for fresh work. Zero uses 5s.
// SubscribeWork requires a positive whole-millisecond duration.
func WithBlockTimeout(timeout time.Duration) BusOption {
	return func(cfg *busConfig) { cfg.BlockTimeout = timeout }
}

// WithRetryAfter selects the minimum pending idle time before reclaim. Zero
// uses 1m. SubscribeWork requires a positive whole-millisecond duration greater
// than HandlerTimeout.
func WithRetryAfter(delay time.Duration) BusOption {
	return func(cfg *busConfig) { cfg.RetryAfter = delay }
}

// WithHandlerTimeout bounds a selected-handler attempt's context. Zero uses
// 30s. SubscribeWork requires positive whole milliseconds. A callback ignoring
// its context may keep running and must tolerate redelivery.
func WithHandlerTimeout(timeout time.Duration) BusOption {
	return func(cfg *busConfig) { cfg.HandlerTimeout = timeout }
}

type publication struct {
	ctx   context.Context
	event events.Event
}

type handlerEntry struct {
	id      uint64
	handler events.Handler
}

// Bus provides notification fanout and explicit reliable work subscriptions.
// Emit queues a bounded publication; Publish confirms Redis acceptance. The
// caller owns the Redis client and must close the bus before closing that client.
type Bus struct {
	rdb          *redis.Client
	log          *slog.Logger
	cfg          busConfig
	consumerName string
	ctx          context.Context
	cancel       context.CancelFunc
	queue        chan publication
	done         chan struct{}
	wg           sync.WaitGroup

	// One gate protects lifecycle admission and local registrations. Network
	// operations and user callbacks always run outside it.
	mu              sync.Mutex
	closed          bool
	nextID          uint64
	workStarted     bool
	workSubs        map[string][]handlerEntry
	broadcastSubs   map[uint64]*broadcastSub
	broadcastPubsub *redis.PubSub
	broadcastSetup  *broadcastSetup
}

// New wraps a caller-owned client. It starts a fixed publisher pool; work and
// pub/sub readers start after their first successful subscription. A nil option
// panics before any goroutines start.
func New(rdb *redis.Client, opts ...BusOption) *Bus {
	var cfg busConfig
	for _, opt := range opts {
		if opt == nil {
			panic("goredis: nil BusOption")
		}
		opt(&cfg)
	}
	if cfg.log == nil {
		cfg.log = slog.Default()
	}
	if cfg.StreamPrefix == "" {
		cfg.StreamPrefix = defaultStreamPrefix
	}
	cfg.StreamPrefix += busKeyVersion
	if cfg.ConsumerGroup == "" {
		cfg.ConsumerGroup = defaultConsumerGroup
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.BlockTimeout == 0 {
		cfg.BlockTimeout = defaultBlockTimeout
	}
	if cfg.RetryAfter == 0 {
		cfg.RetryAfter = defaultRetryAfter
	}
	if cfg.HandlerTimeout == 0 {
		cfg.HandlerTimeout = defaultHandlerTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bus{rdb: rdb, log: cfg.log, cfg: cfg,
		consumerName: "consumer-" + rand.Text(),
		ctx:          ctx, cancel: cancel, queue: make(chan publication, cfg.QueueSize), done: make(chan struct{}),
		workSubs: make(map[string][]handlerEntry), broadcastSubs: make(map[uint64]*broadcastSub)}
	for range cfg.Workers {
		b.wg.Add(1)
		go b.publishLoop()
	}
	return b
}

func (b *Bus) begin(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed {
		return events.ErrClosed
	}
	b.wg.Add(1)
	return nil
}

// Emit admits bounded asynchronous publication. The caller must keep the event
// graph immutable. Its context values survive admission; cancellation does not.
// Encoding and remote failures are reported through the bus logger.
func (b *Bus) Emit(ctx context.Context, event events.Event) error {
	if err := b.begin(ctx); err != nil {
		return err
	}
	defer b.wg.Done()
	if err := events.ValidateEvent(event); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed {
		return events.ErrClosed
	}
	select {
	case b.queue <- publication{ctx: context.WithoutCancel(ctx), event: event}:
		return nil
	default:
		return events.ErrCapacity
	}
}

// Publish waits for XADD acceptance, then attempts best-effort pub/sub fanout.
// It never forces local handler execution. Cancellation cannot undo an accepted
// publication; replay must retain the event ID and tolerate duplicate delivery.
func (b *Bus) Publish(ctx context.Context, event events.Event) error {
	if err := b.begin(ctx); err != nil {
		return err
	}
	defer b.wg.Done()
	return b.publish(ctx, event)
}

func (b *Bus) publishLoop() {
	defer b.wg.Done()
	for p := range b.queue {
		ctx, cancel := context.WithTimeout(p.ctx, opTimeout)
		err := b.publish(ctx, p.event)
		cancel()
		if err != nil {
			b.log.Error("events: asynchronous publication failed", "error", err)
		}
	}
}

func (b *Bus) publish(ctx context.Context, event events.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := events.NewRecord(event)
	if err != nil {
		return fmt.Errorf("goredis: encoding event record: %w", err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("goredis: encoding event envelope: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = b.rdb.XAdd(ctx, &redis.XAddArgs{Stream: b.cfg.StreamPrefix + record.Type,
		Values: map[string]any{"record": string(raw)}}).Err()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("goredis: publishing event record: %w", err)
	}
	if err := b.rdb.Publish(ctx, b.broadcastChannel(), raw).Err(); err != nil {
		b.log.Error("events: best-effort broadcast failed", "event_type", record.Type, "error", err)
	}
	return ctx.Err()
}

// Subscribe receives best-effort notifications for an exact topic or "*".
// Use SubscribeWork for reliable competing-consumer processing.
func (b *Bus) Subscribe(topic string, handler events.Handler) (events.Subscription, error) {
	return b.SubscribeBroadcast(topic, handler)
}

// Close refuses new admissions, stops readers and drains admitted publications
// and callbacks. Every caller waits for the same completion using its own context.
// Callbacks must not call Close themselves; the host owns shutdown.
func (b *Bus) Close(ctx context.Context) error {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		close(b.queue)
		b.cancel()
		pubsub := b.broadcastPubsub
		go func() {
			if pubsub != nil {
				_ = pubsub.Close()
			}
			b.wg.Wait()
			b.mu.Lock()
			clear(b.workSubs)
			for _, sub := range b.broadcastSubs {
				sub.handler = nil
			}
			clear(b.broadcastSubs)
			b.broadcastPubsub = nil
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

func callEventHandler(ctx context.Context, handler events.Handler, event events.Event) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("%w: %v", events.ErrHandlerPanic, value)
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = handler(ctx, event); err != nil {
		return err
	}
	return ctx.Err()
}

func decodeRecord(raw string) (events.RemoteEvent, error) {
	var record events.Record
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return events.RemoteEvent{}, fmt.Errorf("decoding event record: %w", err)
	}
	if err := record.Validate(); err != nil {
		return events.RemoteEvent{}, err
	}
	return record.Event(), nil
}

func parseMessage(values map[string]any) (events.RemoteEvent, error) {
	raw, ok := values["record"].(string)
	if !ok {
		return events.RemoteEvent{}, fmt.Errorf("stream entry missing record envelope")
	}
	return decodeRecord(raw)
}
