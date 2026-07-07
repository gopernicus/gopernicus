package goredis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/events"
)

const (
	defaultStreamPrefix  = "events:"
	defaultConsumerGroup = "default"
	defaultWorkers       = 4
	defaultBlockTimeout  = 5 * time.Second
	defaultBatchSize     = 10

	// idlePoll bounds how long a worker sleeps when there is nothing to read and
	// how long the error backoff waits, so shutdown and newly-relevant streams
	// are both picked up promptly.
	idlePoll = 100 * time.Millisecond

	// opTimeout bounds one-shot Redis calls (XGROUPCREATE, XACK) issued outside
	// the blocking read loop.
	opTimeout = 5 * time.Second
)

var (
	_ events.Bus         = (*Bus)(nil)
	_ events.Broadcaster = (*Bus)(nil)

	errBusClosed = errors.New("goredis: bus is closed")
)

// Options holds Redis Streams bus configuration. Populate it directly or with
// sdk/environment.ParseEnvTags; New fills any zero field with its default.
type Options struct {
	// StreamPrefix is prepended to an event type to form its stream name and
	// also names the pub/sub broadcast channel (StreamPrefix+"broadcast").
	StreamPrefix string `env:"EVENT_BUS_STREAM_PREFIX" default:"events:"`

	// ConsumerGroup names the shared consumer group. Instances that set the same
	// group compete for stream messages (load sharing + at-least-once).
	ConsumerGroup string `env:"EVENT_BUS_CONSUMER_GROUP" default:"default"`

	// Workers is the number of concurrent XReadGroup processors per instance.
	Workers int `env:"EVENT_BUS_WORKERS" default:"4"`

	// BlockTimeout is how long XReadGroup blocks waiting for messages.
	BlockTimeout time.Duration `env:"EVENT_BUS_BLOCK_TIMEOUT" default:"5s"`

	// BatchSize is the max messages read per XReadGroup call.
	BatchSize int64 `env:"EVENT_BUS_BATCH_SIZE" default:"10"`

	// MaxLen approximately caps entries per stream (Redis trims on XADD when > 0).
	// 0 means streams grow unbounded.
	MaxLen int64 `env:"EVENT_BUS_MAX_LEN" default:"0"`
}

// handlerEntry is one Subscribe registration on the streams path.
type handlerEntry struct {
	id      uint64
	handler events.Handler
}

// Bus is a Redis Streams-backed events.Bus (durable at-least-once) that also
// satisfies events.Broadcaster (best-effort pub/sub fan-out).
type Bus struct {
	rdb          *redis.Client
	log          *slog.Logger
	cfg          Options
	consumerName string

	// ctx is cancelled by Close; the consumer and broadcast loops derive their
	// blocking Redis calls from it so shutdown unblocks them promptly.
	ctx    context.Context
	cancel context.CancelFunc

	subsMu  sync.RWMutex
	subs    map[string][]handlerEntry
	nextID  uint64
	started bool

	// groups is the set of streams that have a live consumer group; the workers
	// read exactly these. A stream enters the set when an exact topic subscribes
	// or when a local wildcard subscriber makes an emitted stream relevant.
	groupsMu sync.RWMutex
	groups   map[string]bool

	wg sync.WaitGroup

	closeMu sync.Mutex
	closed  bool

	// Broadcast (pub/sub) support — see broadcast.go.
	broadcastMu      sync.RWMutex
	broadcastSubs    map[uint64]*broadcastSub
	nextBroadcastID  uint64
	broadcastStarted bool
	broadcastPubsub  *redis.PubSub
}

// New creates a Redis Streams event bus over the caller's client. The caller
// owns the client's lifecycle (Close closes the bus, not the client). Each
// process gets a distinct consumer name so a shared ConsumerGroup distributes
// load. A nil logger falls back to slog.Default().
func New(rdb *redis.Client, log *slog.Logger, opts Options) *Bus {
	if log == nil {
		log = slog.Default()
	}
	if opts.StreamPrefix == "" {
		opts.StreamPrefix = defaultStreamPrefix
	}
	if opts.ConsumerGroup == "" {
		opts.ConsumerGroup = defaultConsumerGroup
	}
	if opts.Workers <= 0 {
		opts.Workers = defaultWorkers
	}
	if opts.BlockTimeout <= 0 {
		opts.BlockTimeout = defaultBlockTimeout
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Bus{
		rdb:          rdb,
		log:          log,
		cfg:          opts,
		consumerName: fmt.Sprintf("consumer-%d", time.Now().UnixNano()),
		ctx:          ctx,
		cancel:       cancel,
		subs:         make(map[string][]handlerEntry),
		groups:       make(map[string]bool),
	}
}

// Emit publishes an event: it PUBLISHes onto the broadcast channel (best-effort
// fan-out) and XADDs onto the event type's stream (durable at-least-once).
// Async by default (fire-and-forget XADD on a tracked goroutine); WithSync
// blocks until Redis acknowledges the XADD and then dispatches to local handlers
// so a same-request caller sees the handler run before Emit returns. Because the
// synchronously-dispatched copy is also on the stream, a competing consumer may
// process it again — handlers must be idempotent.
func (b *Bus) Emit(ctx context.Context, event events.Event, opts ...events.EmitOption) error {
	cfg := events.ApplyOptions(opts...)

	data, err := events.EncodeEvent(event)
	if err != nil {
		return fmt.Errorf("goredis: encoding event: %w", err)
	}

	// Mirror every emit onto pub/sub: a process cannot know whether some other
	// instance holds broadcast subscribers, so publishing is unconditional.
	b.publishBroadcast(ctx, event, data)

	stream := b.cfg.StreamPrefix + event.Type()

	// A local "*" subscriber only receives this event if our workers read its
	// stream, so ensure the group exists for wildcard delivery.
	if b.hasWildcardSubscriber() {
		b.ensureGroup(stream)
	}

	values := map[string]any{
		"type":           event.Type(),
		"correlation_id": event.CorrelationID(),
		"occurred_at":    event.OccurredAt().Format(time.RFC3339Nano),
		"payload":        string(data),
	}

	if cfg.Sync {
		b.closeMu.Lock()
		closed := b.closed
		b.closeMu.Unlock()
		if closed {
			b.warnDropped(event, "bus closed")
			return nil
		}
		if err := b.rdb.XAdd(ctx, b.xAddArgs(stream, values)).Err(); err != nil {
			return fmt.Errorf("goredis: xadd: %w", err)
		}
		return b.dispatchLocal(ctx, event)
	}

	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		b.warnDropped(event, "bus closed")
		return nil
	}
	b.wg.Add(1)
	b.closeMu.Unlock()

	go func() {
		defer b.wg.Done()
		publishCtx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		if err := b.rdb.XAdd(publishCtx, b.xAddArgs(stream, values)).Err(); err != nil {
			b.log.Error("events: publish failed",
				"event_type", event.Type(),
				"correlation_id", event.CorrelationID(),
				"error", err,
			)
		}
	}()
	return nil
}

func (b *Bus) xAddArgs(stream string, values map[string]any) *redis.XAddArgs {
	return &redis.XAddArgs{
		Stream: stream,
		MaxLen: b.cfg.MaxLen,
		Approx: b.cfg.MaxLen > 0,
		Values: values,
	}
}

// Subscribe registers a handler for an exact topic, or "*" for every event.
// Lazily creates the topic's consumer group and starts the worker pool on the
// first subscription. Returns errBusClosed on a closed bus.
func (b *Bus) Subscribe(topic string, handler events.Handler) (events.Subscription, error) {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return nil, errBusClosed
	}

	b.subsMu.Lock()
	b.nextID++
	id := b.nextID
	b.subs[topic] = append(b.subs[topic], handlerEntry{id: id, handler: handler})
	start := !b.started
	b.started = true
	b.subsMu.Unlock()

	if start {
		// wg.Add happens under closeMu so a concurrent Close cannot race wg.Wait.
		b.startConsumers()
	}
	b.closeMu.Unlock()

	// The consumer-group creation is a Redis round-trip; keep it out of the locks.
	if topic != "*" {
		b.ensureGroup(b.cfg.StreamPrefix + topic)
	}

	return &subscription{id: id, topic: topic, bus: b}, nil
}

// Close stops the bus, draining in-flight workers and publish goroutines up to
// the context deadline. It is idempotent and does not close the caller's client.
func (b *Bus) Close(ctx context.Context) error {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return nil
	}
	b.closed = true
	b.cancel()
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
		return ctx.Err()
	}
}

func (b *Bus) hasWildcardSubscriber() bool {
	b.subsMu.RLock()
	defer b.subsMu.RUnlock()
	return len(b.subs["*"]) > 0
}

// ensureGroup creates the consumer group on stream (MKSTREAM) if this process
// has not already, tolerating BUSYGROUP for the concurrent/already-exists case.
func (b *Bus) ensureGroup(stream string) {
	b.groupsMu.RLock()
	exists := b.groups[stream]
	b.groupsMu.RUnlock()
	if exists {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	err := b.rdb.XGroupCreateMkStream(ctx, stream, b.cfg.ConsumerGroup, "0").Err()
	if err != nil && !isBusyGroup(err) {
		b.log.Warn("events: ensure consumer group failed",
			"stream", stream,
			"group", b.cfg.ConsumerGroup,
			"error", err,
		)
		return
	}

	b.groupsMu.Lock()
	b.groups[stream] = true
	b.groupsMu.Unlock()
}

func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}

// startConsumers launches the XReadGroup worker pool. Called once, under
// closeMu, so its wg.Add cannot race Close's wg.Wait.
func (b *Bus) startConsumers() {
	for i := 0; i < b.cfg.Workers; i++ {
		b.wg.Add(1)
		go b.consumeLoop(i)
	}
}

// consumeLoop runs the read-dispatch-ack cycle until Close cancels b.ctx.
func (b *Bus) consumeLoop(workerID int) {
	defer b.wg.Done()
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
			b.consumeBatch(workerID)
		}
	}
}

// consumeBatch reads one XReadGroup batch across every stream with a live group,
// dispatching and acking each message.
func (b *Bus) consumeBatch(workerID int) {
	b.groupsMu.RLock()
	streamNames := make([]string, 0, len(b.groups))
	for s := range b.groups {
		streamNames = append(streamNames, s)
	}
	b.groupsMu.RUnlock()

	if len(streamNames) == 0 {
		select {
		case <-b.ctx.Done():
		case <-time.After(idlePoll):
		}
		return
	}

	// XReadGroup wants streams as [name...] followed by [id...]; ">" means
	// "messages never delivered to this group".
	streamArgs := make([]string, 0, len(streamNames)*2)
	streamArgs = append(streamArgs, streamNames...)
	for range streamNames {
		streamArgs = append(streamArgs, ">")
	}

	ctx, cancel := context.WithTimeout(b.ctx, b.cfg.BlockTimeout+time.Second)
	defer cancel()

	consumer := fmt.Sprintf("%s-%d", b.consumerName, workerID)
	results, err := b.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    b.cfg.ConsumerGroup,
		Consumer: consumer,
		Streams:  streamArgs,
		Count:    b.cfg.BatchSize,
		Block:    b.cfg.BlockTimeout,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return // block timed out with no messages
		}
		if b.ctx.Err() != nil {
			return // shutting down; the cancel above unblocked the read
		}
		b.log.Error("events: xreadgroup failed", "worker", workerID, "error", err)
		select {
		case <-b.ctx.Done():
		case <-time.After(idlePoll):
		}
		return
	}

	for _, stream := range results {
		for _, msg := range stream.Messages {
			b.processMessage(stream.Stream, msg.ID, msg.Values)
		}
	}
}

// processMessage parses, dispatches, and unconditionally acks one message. The
// deferred ack is the XACK-always poison-pill policy: a message leaves the
// pending list even when parsing panics or a handler fails, so one bad message
// never blocks the group.
func (b *Bus) processMessage(stream, msgID string, values map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("events: panic recovered in stream handler",
				"stream", stream,
				"message_id", msgID,
				"panic", r,
			)
		}
		b.ack(stream, msgID)
	}()

	event, err := parseMessage(values)
	if err != nil {
		b.log.Error("events: parse stream message failed",
			"stream", stream,
			"message_id", msgID,
			"error", err,
		)
		return
	}

	if err := b.dispatchLocal(context.Background(), event); err != nil {
		b.log.Error("events: stream handler returned error",
			"event_type", event.Type(),
			"correlation_id", event.CorrelationID(),
			"error", err,
		)
	}
}

// parseMessage rehydrates a stream message into an events.RemoteEvent, decoding
// aggregate/tenant metadata from the payload. RemoteEvent satisfies Event and
// Metadata and, via Unmarshaler, TypedHandler's slow path.
func parseMessage(values map[string]any) (events.Event, error) {
	payload, ok := values["payload"].(string)
	if !ok {
		return nil, fmt.Errorf("stream message missing payload field")
	}

	eventType, _ := values["type"].(string)
	correlationID, _ := values["correlation_id"].(string)
	occurredAtStr, _ := values["occurred_at"].(string)
	occurredAt, _ := time.Parse(time.RFC3339Nano, occurredAtStr)

	tenant, aggType, aggID := events.DecodeRemoteMetadata([]byte(payload))
	return events.RemoteEvent{
		EventType:   eventType,
		Occurred:    occurredAt,
		Correlation: correlationID,
		Payload:     []byte(payload),
		Tenant:      tenant,
		AggType:     aggType,
		AggID:       aggID,
	}, nil
}

func (b *Bus) ack(stream, msgID string) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	if err := b.rdb.XAck(ctx, stream, b.cfg.ConsumerGroup, msgID).Err(); err != nil {
		b.log.Error("events: ack stream message failed",
			"stream", stream,
			"message_id", msgID,
			"error", err,
		)
	}
}

// dispatchLocal calls every handler subscribed to the event's exact topic and to
// the "*" wildcard, returning the first handler error.
func (b *Bus) dispatchLocal(ctx context.Context, event events.Event) error {
	b.subsMu.RLock()
	var handlers []events.Handler
	for _, e := range b.subs[event.Type()] {
		handlers = append(handlers, e.handler)
	}
	for _, e := range b.subs["*"] {
		handlers = append(handlers, e.handler)
	}
	b.subsMu.RUnlock()

	var firstErr error
	for _, h := range handlers {
		if err := h(ctx, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (b *Bus) warnDropped(event events.Event, reason string) {
	b.log.Warn("events: event dropped",
		"reason", reason,
		"event_type", event.Type(),
		"correlation_id", event.CorrelationID(),
	)
}

func (b *Bus) unsubscribe(topic string, id uint64) {
	b.subsMu.Lock()
	defer b.subsMu.Unlock()
	entries := b.subs[topic]
	for i, e := range entries {
		if e.id == id {
			b.subs[topic] = append(entries[:i], entries[i+1:]...)
			return
		}
	}
}

// subscription is an active streams-path subscription.
type subscription struct {
	id    uint64
	topic string
	bus   *Bus
	once  sync.Once
}

// Unsubscribe removes this subscription. Safe to call more than once.
func (s *subscription) Unsubscribe() error {
	s.once.Do(func() { s.bus.unsubscribe(s.topic, s.id) })
	return nil
}
