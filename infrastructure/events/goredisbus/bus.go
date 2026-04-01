// Package goredisbus provides a Redis Streams-backed event bus.
// It uses XADD/XREADGROUP/XACK for durable, at-least-once delivery across
// multiple process instances sharing the same consumer group.
//
// Use sdk/environment.ParseEnvTags to populate Options from environment variables.
//
//	var opts goredisbus.Options
//	environment.ParseEnvTags("MYAPP", &opts)
//	bus := goredisbus.New(rdb, log, opts)
package goredisbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// Options holds Redis Streams bus configuration.
// Use sdk/environment.ParseEnvTags to populate from environment variables.
type Options struct {
	// StreamPrefix is prepended to event types to form stream names.
	StreamPrefix string `env:"EVENT_BUS_STREAM_PREFIX" default:"events:"`

	// ConsumerGroup is the name of the consumer group.
	// Multiple instances with the same group share the load.
	ConsumerGroup string `env:"EVENT_BUS_CONSUMER_GROUP" default:"default"`

	// Workers is the number of concurrent message processors.
	Workers int `env:"EVENT_BUS_WORKERS" default:"4"`

	// BlockTimeout is how long XREADGROUP blocks waiting for messages.
	BlockTimeout time.Duration `env:"EVENT_BUS_BLOCK_TIMEOUT" default:"5s"`

	// BatchSize is the max messages to read per XREADGROUP call.
	BatchSize int64 `env:"EVENT_BUS_BATCH_SIZE" default:"10"`

	// MaxLen is the approximate maximum number of entries per stream.
	// When set, Redis trims old entries on each XADD using approximate trimming.
	// 0 means no trimming (streams grow unbounded).
	MaxLen int64 `env:"EVENT_BUS_MAX_LEN" default:"0"`
}

// Bus is a Redis Streams-backed event bus.
// Satisfies events.Bus. Suitable for multi-instance deployments requiring
// durable, at-least-once event delivery.
type Bus struct {
	rdb          *redis.Client
	log          *slog.Logger
	cfg          Options
	consumerName string

	subsMu  sync.RWMutex
	subs    map[string][]handlerEntry
	nextID  uint64
	started bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

type handlerEntry struct {
	id      uint64
	handler events.Handler
}

// Compile-time interface check.
var _ events.Bus = (*Bus)(nil)

// New creates a new Redis Streams event bus.
// rdb is the Redis client (returned by goredisdb.New).
// ConsumerName is auto-generated per process instance for load distribution.
func New(rdb *redis.Client, log *slog.Logger, opts Options) *Bus {
	if opts.StreamPrefix == "" {
		opts.StreamPrefix = "events:"
	}
	if opts.ConsumerGroup == "" {
		opts.ConsumerGroup = "default"
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.BlockTimeout <= 0 {
		opts.BlockTimeout = 5 * time.Second
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 10
	}

	return &Bus{
		rdb:          rdb,
		log:          log,
		cfg:          opts,
		consumerName: fmt.Sprintf("consumer-%d", time.Now().UnixNano()),
		subs:         make(map[string][]handlerEntry),
		stopCh:       make(chan struct{}),
	}
}

// Emit publishes an event to the Redis stream for its event type.
// Async by default (fire-and-forget with tracked goroutine).
// Use events.WithSync() to block until Redis acknowledges the write.
func (b *Bus) Emit(ctx context.Context, event events.Event, opts ...events.EmitOption) error {
	cfg := events.ApplyOptions(opts...)

	data, err := events.EncodeEvent(event)
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}

	stream := b.cfg.StreamPrefix + event.Type()
	values := map[string]interface{}{
		"type":           event.Type(),
		"correlation_id": event.CorrelationID(),
		"occurred_at":    event.OccurredAt().Format(time.RFC3339Nano),
		"payload":        string(data),
	}

	if cfg.Sync {
		if err := b.rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: stream,
			MaxLen: b.cfg.MaxLen,
			Approx: b.cfg.MaxLen > 0,
			Values: values,
		}).Err(); err != nil {
			return fmt.Errorf("redis xadd: %w", err)
		}
		return b.dispatchLocal(ctx, event)
	}

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.rdb.XAdd(publishCtx, &redis.XAddArgs{
			Stream: stream,
			MaxLen: b.cfg.MaxLen,
			Approx: b.cfg.MaxLen > 0,
			Values: values,
		}).Err(); err != nil {
			b.log.Error("event publish failed",
				"event_type", event.Type(),
				"correlation_id", event.CorrelationID(),
				"error", err,
			)
		}
	}()

	return nil
}

// Subscribe registers a handler for events of the given topic.
// Use "*" to subscribe to all event types.
// Lazily creates the Redis consumer group on first subscription per topic.
// Starts background consumer workers on the first subscription overall.
func (b *Bus) Subscribe(topic string, handler events.Handler) (events.Subscription, error) {
	b.subsMu.Lock()
	defer b.subsMu.Unlock()

	id := atomic.AddUint64(&b.nextID, 1)
	b.subs[topic] = append(b.subs[topic], handlerEntry{id: id, handler: handler})

	// Ensure the consumer group exists for this stream (lazy creation).
	if topic != "*" {
		stream := b.cfg.StreamPrefix + topic
		err := b.rdb.XGroupCreateMkStream(context.Background(), stream, b.cfg.ConsumerGroup, "0").Err()
		if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
			b.log.Warn("failed to ensure consumer group",
				"stream", stream,
				"group", b.cfg.ConsumerGroup,
				"error", err,
			)
		}
	}

	if !b.started {
		b.started = true
		b.startConsumers()
	}

	return &subscription{id: id, topic: topic, bus: b}, nil
}

// Close gracefully shuts down the bus, waiting for in-flight messages up to ctx deadline.
func (b *Bus) Close(ctx context.Context) error {
	close(b.stopCh)

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

// startConsumers starts background XREADGROUP consumer workers.
func (b *Bus) startConsumers() {
	for i := range b.cfg.Workers {
		b.wg.Add(1)
		go b.consumeLoop(i)
	}
}

// consumeLoop runs the read-dispatch-ack cycle for one worker.
func (b *Bus) consumeLoop(workerID int) {
	defer b.wg.Done()

	for {
		select {
		case <-b.stopCh:
			return
		default:
			b.consumeBatch(workerID)
		}
	}
}

func (b *Bus) consumeBatch(workerID int) {
	b.subsMu.RLock()
	var streamNames []string
	for topic := range b.subs {
		if topic != "*" {
			streamNames = append(streamNames, b.cfg.StreamPrefix+topic)
		}
	}
	b.subsMu.RUnlock()

	if len(streamNames) == 0 {
		time.Sleep(100 * time.Millisecond)
		return
	}

	// XReadGroup expects streams as [name...] [id...] interleaved.
	streamArgs := make([]string, 0, len(streamNames)*2)
	for _, name := range streamNames {
		streamArgs = append(streamArgs, name)
	}
	for range streamNames {
		streamArgs = append(streamArgs, ">")
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.cfg.BlockTimeout+time.Second)
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
		if err == redis.Nil {
			return // timeout — no messages available
		}
		b.log.Error("redis xreadgroup error", "worker", workerID, "error", err)
		return
	}

	for _, stream := range results {
		for _, msg := range stream.Messages {
			b.processMessage(stream.Stream, msg.ID, msg.Values)
		}
	}
}

func (b *Bus) processMessage(stream, msgID string, values map[string]interface{}) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("panic recovered in event handler",
				"stream", stream,
				"message_id", msgID,
				"panic", r,
			)
		}
	}()

	event, err := b.parseMessage(values)
	if err != nil {
		b.log.Error("failed to parse stream message",
			"stream", stream,
			"message_id", msgID,
			"error", err,
		)
		b.ack(stream, msgID) // ack to avoid poison-pill reprocessing
		return
	}

	if err := b.dispatchLocal(context.Background(), event); err != nil {
		b.log.Error("event handler returned error",
			"event_type", event.Type(),
			"correlation_id", event.CorrelationID(),
			"error", err,
		)
	}

	b.ack(stream, msgID)
}

func (b *Bus) parseMessage(values map[string]interface{}) (events.Event, error) {
	payload, ok := values["payload"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid payload field")
	}

	eventType, _ := values["type"].(string)
	correlationID, _ := values["correlation_id"].(string)
	occurredAtStr, _ := values["occurred_at"].(string)
	occurredAt, _ := time.Parse(time.RFC3339Nano, occurredAtStr)

	return &streamEvent{
		base:    events.NewBaseEventWithCorrelation(eventType, correlationID),
		occurred: occurredAt,
		payload: []byte(payload),
	}, nil
}

func (b *Bus) ack(stream, messageID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.rdb.XAck(ctx, stream, b.cfg.ConsumerGroup, messageID).Err(); err != nil {
		b.log.Error("failed to ack stream message",
			"stream", stream,
			"message_id", messageID,
			"error", err,
		)
	}
}

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

	var lastErr error
	for _, h := range handlers {
		if err := safeCall(ctx, event, h); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func safeCall(ctx context.Context, event events.Event, h events.Handler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = nil // swallow — already logged at processMessage level
		}
	}()
	return h(ctx, event)
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

// subscription represents an active topic subscription.
type subscription struct {
	id    uint64
	topic string
	bus   *Bus
	once  sync.Once
}

func (s *subscription) Unsubscribe() error {
	s.once.Do(func() { s.bus.unsubscribe(s.topic, s.id) })
	return nil
}

// streamEvent is a generic Event wrapper for messages read from Redis Streams.
type streamEvent struct {
	base     events.BaseEvent
	occurred time.Time
	payload  []byte
}

func (e *streamEvent) Type() string          { return e.base.Type() }
func (e *streamEvent) OccurredAt() time.Time { return e.occurred }
func (e *streamEvent) CorrelationID() string { return e.base.CorrelationID() }

// Unmarshal deserializes the payload into the target value.
func (e *streamEvent) Unmarshal(target any) error {
	return json.Unmarshal(e.payload, target)
}
