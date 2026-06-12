package goredisbus

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// Broadcast support: redis pub/sub alongside the durable streams. Pub/sub's
// fire-and-forget fan-out is exactly right for ephemeral consumers (SSE) —
// every process receives every event, nothing is replayed, and a message
// published while a subscriber is disconnected is simply gone (clients
// reconnect and re-fetch state). The streams path keeps its
// competing-consumer semantics untouched.

var _ events.Broadcaster = (*Bus)(nil)

// broadcastChannel is the pub/sub channel; one channel for all event types
// keeps SUBSCRIBE management trivial — handlers filter by topic locally.
func (b *Bus) broadcastChannel() string {
	return b.cfg.StreamPrefix + "broadcast"
}

// broadcastEnvelope is the pub/sub wire format.
type broadcastEnvelope struct {
	Type          string          `json:"type"`
	CorrelationID string          `json:"correlation_id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}

// publishBroadcast mirrors an emitted event onto the pub/sub channel on
// EVERY Emit: a process can't know whether some other instance holds
// broadcast subscribers, so publishing is unconditional. Cost: one
// PUBLISH per event; fire-and-forget.
func (b *Bus) publishBroadcast(ctx context.Context, event events.Event, encoded []byte) {
	env := broadcastEnvelope{
		Type:          event.Type(),
		CorrelationID: event.CorrelationID(),
		OccurredAt:    event.OccurredAt(),
		Payload:       encoded,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		b.log.Error("broadcast encode failed", "event_type", event.Type(), "error", err)
		return
	}
	if err := b.rdb.Publish(ctx, b.broadcastChannel(), raw).Err(); err != nil {
		b.log.Error("broadcast publish failed", "event_type", event.Type(), "error", err)
	}
}

// broadcastSub is one SubscribeBroadcast registration.
type broadcastSub struct {
	id      uint64
	topic   string
	handler events.Handler
	bus     *Bus
	once    sync.Once
}

func (s *broadcastSub) Unsubscribe() error {
	s.once.Do(func() {
		s.bus.broadcastMu.Lock()
		defer s.bus.broadcastMu.Unlock()
		delete(s.bus.broadcastSubs, s.id)
	})
	return nil
}

// SubscribeBroadcast implements events.Broadcaster: the handler receives
// every matching event emitted by ANY process, as an events.RemoteEvent.
// Topic "*" matches all; otherwise exact-match on event type.
func (b *Bus) SubscribeBroadcast(topic string, handler events.Handler) (events.Subscription, error) {
	b.broadcastMu.Lock()
	defer b.broadcastMu.Unlock()

	if b.broadcastSubs == nil {
		b.broadcastSubs = make(map[uint64]*broadcastSub)
	}
	id := atomic.AddUint64(&b.nextBroadcastID, 1)
	sub := &broadcastSub{id: id, topic: topic, handler: handler, bus: b}
	b.broadcastSubs[id] = sub

	if !b.broadcastStarted {
		b.broadcastStarted = true
		pubsub := b.rdb.Subscribe(context.Background(), b.broadcastChannel())
		b.broadcastPubsub = pubsub
		b.wg.Add(1)
		go b.broadcastLoop(pubsub)
	}
	return sub, nil
}

func (b *Bus) broadcastLoop(pubsub *redis.PubSub) {
	defer b.wg.Done()
	ch := pubsub.Channel()
	for {
		select {
		case <-b.stopCh:
			_ = pubsub.Close()
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var env broadcastEnvelope
			if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
				b.log.Error("broadcast decode failed", "error", err)
				continue
			}
			tenant, aggType, aggID := events.DecodeRemoteMetadata(env.Payload)
			event := events.RemoteEvent{
				EventType:   env.Type,
				Occurred:    env.OccurredAt,
				Correlation: env.CorrelationID,
				Payload:     env.Payload,
				Tenant:      tenant,
				AggType:     aggType,
				AggID:       aggID,
			}
			b.dispatchBroadcast(event)
		}
	}
}

func (b *Bus) dispatchBroadcast(event events.RemoteEvent) {
	b.broadcastMu.RLock()
	subs := make([]*broadcastSub, 0, len(b.broadcastSubs))
	for _, s := range b.broadcastSubs {
		if s.topic == "*" || s.topic == event.EventType || matchesPrefixTopic(s.topic, event.EventType) {
			subs = append(subs, s)
		}
	}
	b.broadcastMu.RUnlock()

	for _, s := range subs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.handler(ctx, event); err != nil {
			b.log.Error("broadcast handler failed", "event_type", event.EventType, "error", err)
		}
		cancel()
	}
}

// matchesPrefixTopic supports "domain.*" prefix subscriptions, mirroring
// the stream path's topic conventions.
func matchesPrefixTopic(topic, eventType string) bool {
	prefix, ok := strings.CutSuffix(topic, ".*")
	if !ok {
		return false
	}
	return strings.HasPrefix(eventType, prefix+".")
}
