package goredis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/redis/go-redis/v9"
)

type workSubscription struct {
	id    uint64
	topic string
	bus   *Bus
	once  sync.Once
}

// SubscribeWork registers reliable competing-consumer work for one exact topic.
// It verifies timing and creates the group before returning success. Every
// member of a group must deploy the same responsibilities for that topic.
func (b *Bus) SubscribeWork(ctx context.Context, topic string, handler events.Handler) (events.Subscription, error) {
	if err := b.begin(ctx); err != nil {
		return nil, err
	}
	defer b.wg.Done()
	if err := events.ValidateSubscription(topic, handler); err != nil {
		return nil, err
	}
	if topic == "*" {
		return nil, fmt.Errorf("goredis: work subscriptions require an exact topic: %w", sdk.ErrInvalidInput)
	}
	if err := b.validateWorkConfig(); err != nil {
		return nil, err
	}
	if err := b.ensureGroup(ctx, b.cfg.StreamPrefix+topic); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, events.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.nextID++
	entry := handlerEntry{id: b.nextID, handler: handler}
	b.workSubs[topic] = append(b.workSubs[topic], entry)
	if !b.workStarted {
		b.workStarted = true
		for worker := range b.cfg.Workers {
			b.wg.Add(1)
			go b.consumeLoop(worker)
		}
	}
	return &workSubscription{id: entry.id, topic: topic, bus: b}, nil
}

func (b *Bus) validateWorkConfig() error {
	if b.cfg.HandlerTimeout <= 0 || b.cfg.HandlerTimeout%time.Millisecond != 0 ||
		b.cfg.RetryAfter <= b.cfg.HandlerTimeout || b.cfg.RetryAfter%time.Millisecond != 0 ||
		b.cfg.BlockTimeout <= 0 || b.cfg.BlockTimeout%time.Millisecond != 0 {
		return fmt.Errorf("goredis: work durations must be positive whole milliseconds and RetryAfter must exceed HandlerTimeout: %w", sdk.ErrInvalidInput)
	}
	return nil
}

// Unsubscribe stops selecting this topic after its last local handler leaves.
// Already claimed entries without a remaining handler stay pending for a peer.
func (s *workSubscription) Unsubscribe() error {
	s.once.Do(func() {
		b := s.bus
		b.mu.Lock()
		defer b.mu.Unlock()
		entries := b.workSubs[s.topic]
		for i, entry := range entries {
			if entry.id == s.id {
				copy(entries[i:], entries[i+1:])
				entries[len(entries)-1] = handlerEntry{}
				entries = entries[:len(entries)-1]
				if len(entries) == 0 {
					delete(b.workSubs, s.topic)
				} else {
					b.workSubs[s.topic] = entries
				}
				return
			}
		}
	})
	return nil
}

func (b *Bus) ensureGroup(ctx context.Context, stream string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	err := b.rdb.XGroupCreateMkStream(ctx, stream, b.cfg.ConsumerGroup, "0").Err()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && !redis.HasErrorPrefix(err, "BUSYGROUP") {
		return fmt.Errorf("goredis: creating work consumer group: %w", err)
	}
	return nil
}

func (b *Bus) hasWorkTopic(topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.closed && len(b.workSubs[topic]) > 0
}

func (b *Bus) activeTopics() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	topics := make([]string, 0, len(b.workSubs))
	for topic := range b.workSubs {
		topics = append(topics, topic)
	}
	slices.Sort(topics)
	return topics
}

func (b *Bus) consumeLoop(worker int) {
	defer b.wg.Done()
	consumer := fmt.Sprintf("%s-%d", b.consumerName, worker)
	cursors := make(map[string]string)
	next := worker
	for b.ctx.Err() == nil {
		topics := b.activeTopics()
		for topic := range cursors {
			if !slices.Contains(topics, topic) {
				delete(cursors, topic)
			}
		}
		if len(topics) == 0 {
			b.pauseWork()
			continue
		}
		topic := topics[next%len(topics)]
		next = (next + 1) % len(topics)
		stream := b.cfg.StreamPrefix + topic
		if !b.hasWorkTopic(topic) {
			continue
		}
		cursor := cursors[topic]
		if cursor == "" {
			cursor = "0-0"
		}
		// Claim one entry per available worker, so a batch tail cannot exhaust
		// its retry interval before its first callback even starts.
		ctx, cancel := context.WithTimeout(b.ctx, opTimeout)
		messages, cursor, err := b.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: stream, Group: b.cfg.ConsumerGroup, Consumer: consumer,
			MinIdle: b.cfg.RetryAfter, Start: cursor, Count: 1}).Result()
		cancel()
		if err != nil {
			b.workReadError(stream, err)
			continue
		}
		// Empty scans can have a nonzero cursor. Keep it to reach later pending
		// entries rather than repeatedly inspecting the start of the list.
		cursors[topic] = cursor
		for _, message := range messages {
			b.processMessage(stream, message.ID, message.Values)
		}
		if b.ctx.Err() != nil {
			return
		}
		if !b.hasWorkTopic(topic) {
			continue
		}
		// Alternate reclaim with fresh reads; permanent poison cannot monopolize
		// the reader. With multiple topics, keep rotation responsive.
		block := b.cfg.BlockTimeout
		if len(topics) > 1 {
			block = min(block, idlePoll)
		}
		ctx, cancel = context.WithTimeout(b.ctx, block)
		results, err := b.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: b.cfg.ConsumerGroup, Consumer: consumer,
			Streams: []string{stream, ">"}, Count: 1, Block: block}).Result()
		cancel()
		if err != nil {
			if !errors.Is(err, redis.Nil) && !errors.Is(err, context.DeadlineExceeded) {
				b.workReadError(stream, err)
			}
			continue
		}
		for _, result := range results {
			for _, message := range result.Messages {
				b.processMessage(result.Stream, message.ID, message.Values)
			}
		}
	}
}

func (b *Bus) workReadError(stream string, err error) {
	if b.ctx.Err() != nil {
		return
	}
	if redis.HasErrorPrefix(err, "NOGROUP") {
		b.mu.Lock()
		active := len(b.workSubs[stream[len(b.cfg.StreamPrefix):]]) > 0
		b.mu.Unlock()
		if active {
			err = b.ensureGroup(b.ctx, stream)
			if err == nil {
				return
			}
		}
	}
	b.log.Error("events: work read failed", "stream", stream, "error", err)
	b.pauseWork()
}

func (b *Bus) pauseWork() {
	select {
	case <-b.ctx.Done():
	case <-time.After(idlePoll):
	}
}

func (b *Bus) processMessage(stream, messageID string, values map[string]any) {
	if b.ctx.Err() != nil {
		return
	}
	event, err := parseMessage(values)
	if err != nil || b.cfg.StreamPrefix+event.Type() != stream {
		if err == nil {
			err = fmt.Errorf("event type does not match its stream")
		}
		b.log.Error("events: invalid work record remains pending", "stream", stream, "message_id", messageID, "error", err)
		return
	}
	b.mu.Lock()
	handlers := append([]handlerEntry(nil), b.workSubs[event.Type()]...)
	b.mu.Unlock()
	if len(handlers) == 0 {
		b.log.Warn("events: unhandled work remains pending", "stream", stream, "message_id", messageID)
		return
	}
	// A single deadline covers the entire selected-handler attempt, so its
	// normal execution stays shorter than RetryAfter regardless of handler count.
	ctx, cancel := context.WithTimeout(b.ctx, b.cfg.HandlerTimeout)
	defer cancel()
	failed := false
	for _, entry := range handlers {
		if ctx.Err() != nil {
			return
		}
		if err := callEventHandler(ctx, entry.handler, event); err != nil {
			failed = true
			b.log.Error("events: work handler failed; entry remains pending", "stream", stream, "message_id", messageID, "error", err)
		}
	}
	if failed || ctx.Err() != nil {
		return
	}
	if err := b.rdb.XAck(ctx, stream, b.cfg.ConsumerGroup, messageID).Err(); err != nil {
		b.log.Error("events: acknowledging work failed", "stream", stream, "message_id", messageID, "error", err)
	}
}
