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

// SubscribeTask registers reliable competing-consumer work for one exact topic.
// It verifies timing and creates the group before returning success. Every
// member of a group must deploy the same responsibilities for that topic.
func (b *Bus) SubscribeTask(ctx context.Context, topic string, handler events.Handler) (events.Subscription, error) {
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
	start := "0"
	if b.cfg.policy.Start == events.StartLatest {
		start = "$"
	}
	if err := b.ensureGroup(ctx, b.cfg.StreamPrefix+topic, start); err != nil {
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
	if err := b.cfg.policy.Validate(); err != nil {
		return err
	}
	if b.cfg.policy.BackoffBase%time.Millisecond != 0 || b.cfg.policy.BackoffMax%time.Millisecond != 0 {
		return fmt.Errorf("goredis: backoff durations must be whole milliseconds: %w", sdk.ErrInvalidInput)
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

// ensureGroup creates the group at start ("0" or "$") unless it exists.
// Recovery of a lost group always passes "0" so no unread entry is skipped.
func (b *Bus) ensureGroup(ctx context.Context, stream, start string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	err := b.rdb.XGroupCreateMkStream(ctx, stream, b.cfg.ConsumerGroup, start).Err()
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
		claimed, cursor, err := b.reclaim(stream, consumer, cursors[topic])
		if err != nil {
			b.workReadError(stream, err)
			continue
		}
		// Empty scans can have a nonzero cursor. Keep it to reach later pending
		// entries rather than repeatedly inspecting the start of the list.
		cursors[topic] = cursor
		for _, c := range claimed {
			b.processMessage(stream, consumer, c.message.ID, c.message.Values, c.attempt)
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
		ctx, cancel := context.WithTimeout(b.ctx, block)
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
				b.processMessage(result.Stream, consumer, message.ID, message.Values, 1)
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
			err = b.ensureGroup(b.ctx, stream, "0")
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

type claimedEntry struct {
	message redis.XMessage
	attempt int
}

// reclaim claims at most one idle pending entry, so a batch tail cannot
// exhaust its retry interval before its first callback even starts. Without
// backoff it uses XAUTOCLAIM with RetryAfter. With backoff the required idle
// time depends on each entry's delivery count, so it scans XPENDING and then
// XCLAIMs with that entry's own minimum idle, which also guards against a
// peer claiming it first.
func (b *Bus) reclaim(stream, consumer, cursor string) ([]claimedEntry, string, error) {
	ctx, cancel := context.WithTimeout(b.ctx, opTimeout)
	defer cancel()
	if b.cfg.policy.BackoffBase == 0 {
		if cursor == "" {
			cursor = "0-0"
		}
		messages, next, err := b.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: stream, Group: b.cfg.ConsumerGroup, Consumer: consumer,
			MinIdle: b.cfg.RetryAfter, Start: cursor, Count: 1}).Result()
		if err != nil {
			return nil, cursor, err
		}
		claimed := make([]claimedEntry, 0, len(messages))
		for _, message := range messages {
			claimed = append(claimed, claimedEntry{message: message, attempt: b.deliveryCount(ctx, stream, message.ID)})
		}
		return claimed, next, nil
	}
	if cursor == "" {
		cursor = "-"
	}
	const scan = 8
	pending, err := b.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: b.cfg.ConsumerGroup,
		Idle: b.cfg.RetryAfter, Start: cursor, End: "+", Count: scan}).Result()
	if err != nil {
		return nil, cursor, err
	}
	next := "-"
	if len(pending) == scan {
		next = "(" + pending[len(pending)-1].ID
	}
	for _, p := range pending {
		minIdle := max(b.cfg.RetryAfter, b.cfg.policy.RetryDelay(int(p.RetryCount)))
		if p.Idle < minIdle {
			continue
		}
		messages, err := b.rdb.XClaim(ctx, &redis.XClaimArgs{Stream: stream, Group: b.cfg.ConsumerGroup,
			Consumer: consumer, MinIdle: minIdle, Messages: []string{p.ID}}).Result()
		if err != nil {
			return nil, cursor, err
		}
		if len(messages) == 1 {
			return []claimedEntry{{message: messages[0], attempt: int(p.RetryCount) + 1}}, "(" + p.ID, nil
		}
	}
	return nil, next, nil
}

// deliveryCount reads a claimed entry's delivery count. Zero means unknown,
// which never dead-letters.
func (b *Bus) deliveryCount(ctx context.Context, stream, messageID string) int {
	rows, err := b.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: b.cfg.ConsumerGroup,
		Start: messageID, End: messageID, Count: 1}).Result()
	if err != nil || len(rows) != 1 {
		if err != nil && b.ctx.Err() == nil {
			b.log.Error("events: reading work delivery count failed", "stream", stream, "message_id", messageID, "error", err)
		}
		return 0
	}
	return int(rows[0].RetryCount)
}

func (b *Bus) processMessage(stream, consumer, messageID string, values map[string]any, attempt int) {
	if b.ctx.Err() != nil {
		return
	}
	event, err := parseMessage(values)
	if err != nil || b.cfg.StreamPrefix+event.Type() != stream {
		if err == nil {
			err = fmt.Errorf("event type does not match its stream")
		}
		if b.cfg.policy.Exhausted(attempt) {
			b.deadLetter(stream, messageID, consumer, values, err)
			return
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
	ctx, cancel := context.WithTimeout(events.WithDeliveryAttempt(b.ctx, attempt), b.cfg.HandlerTimeout)
	defer cancel()
	var failure error
	for _, entry := range handlers {
		if err := ctx.Err(); err != nil {
			failure = err
			break
		}
		if err := callEventHandler(ctx, entry.handler, event); err != nil {
			if failure == nil {
				failure = err
			}
			b.log.Error("events: work handler failed", "stream", stream, "message_id", messageID, "attempt", attempt, "error", err)
		}
	}
	if failure == nil {
		failure = ctx.Err()
	}
	if b.ctx.Err() != nil {
		return // shutdown is not a failed delivery; the entry stays pending
	}
	if failure != nil {
		if b.cfg.policy.Exhausted(attempt) {
			b.deadLetter(stream, messageID, consumer, values, failure)
		}
		return
	}
	if err := b.rdb.XAck(ctx, stream, b.cfg.ConsumerGroup, messageID).Err(); err != nil {
		b.log.Error("events: acknowledging work failed", "stream", stream, "message_id", messageID, "error", err)
	}
}
