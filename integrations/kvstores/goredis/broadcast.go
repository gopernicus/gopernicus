package goredis

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/events"
)

// Broadcast is the Redis pub/sub fan-out rail alongside the durable streams.
// Pub/sub's fire-and-forget fan-out is exactly right for ephemeral consumers
// (SSE, live metrics): EVERY process receives EVERY event, nothing is replayed,
// and a message published while a subscriber is disconnected is simply gone —
// clients reconnect and re-fetch state. The streams path keeps its
// competing-consumer semantics untouched.

// broadcastChannel is a single pub/sub channel for all event types; subscribers
// filter by topic locally, keeping channel management trivial.
func (b *Bus) broadcastChannel() string {
	return b.cfg.StreamPrefix + "broadcast"
}

// broadcastEnvelope is the pub/sub wire format. Payload is the EncodeEvent bytes
// so a receiver can rehydrate an events.RemoteEvent (and decode its metadata).
type broadcastEnvelope struct {
	Type          string          `json:"type"`
	CorrelationID string          `json:"correlation_id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}

// publishBroadcast mirrors an emitted event onto the pub/sub channel on every
// Emit. Best-effort: a publish error is logged, never returned.
func (b *Bus) publishBroadcast(ctx context.Context, event events.Event, encoded []byte) {
	env := broadcastEnvelope{
		Type:          event.Type(),
		CorrelationID: event.CorrelationID(),
		OccurredAt:    event.OccurredAt(),
		Payload:       encoded,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		b.log.Error("events: broadcast encode failed", "event_type", event.Type(), "error", err)
		return
	}
	if err := b.rdb.Publish(ctx, b.broadcastChannel(), raw).Err(); err != nil {
		b.log.Error("events: broadcast publish failed", "event_type", event.Type(), "error", err)
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

// Unsubscribe removes this broadcast subscription. Safe to call more than once.
func (s *broadcastSub) Unsubscribe() error {
	s.once.Do(func() {
		s.bus.broadcastMu.Lock()
		delete(s.bus.broadcastSubs, s.id)
		s.bus.broadcastMu.Unlock()
	})
	return nil
}

// SubscribeBroadcast implements events.Broadcaster: the handler receives every
// matching event emitted by ANY process, as an events.RemoteEvent. Topic "*"
// matches all events; otherwise the match is exact on event type. Lazily opens
// the pub/sub channel on the first broadcast subscription.
func (b *Bus) SubscribeBroadcast(topic string, handler events.Handler) (events.Subscription, error) {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return nil, errBusClosed
	}

	b.broadcastMu.Lock()
	if b.broadcastSubs == nil {
		b.broadcastSubs = make(map[uint64]*broadcastSub)
	}
	b.nextBroadcastID++
	id := b.nextBroadcastID
	sub := &broadcastSub{id: id, topic: topic, handler: handler, bus: b}
	b.broadcastSubs[id] = sub
	start := !b.broadcastStarted
	if start {
		b.broadcastStarted = true
	}
	b.broadcastMu.Unlock()

	if start {
		pubsub := b.rdb.Subscribe(context.Background(), b.broadcastChannel())
		b.broadcastMu.Lock()
		b.broadcastPubsub = pubsub
		b.broadcastMu.Unlock()
		// wg.Add under closeMu so a concurrent Close cannot race wg.Wait.
		b.wg.Add(1)
		go b.broadcastLoop(pubsub)
	}
	b.closeMu.Unlock()

	return sub, nil
}

// broadcastLoop decodes pub/sub messages into RemoteEvents and fans them out to
// local broadcast subscribers until Close cancels b.ctx.
func (b *Bus) broadcastLoop(pubsub *redis.PubSub) {
	defer b.wg.Done()
	ch := pubsub.Channel()
	for {
		select {
		case <-b.ctx.Done():
			_ = pubsub.Close()
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var env broadcastEnvelope
			if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
				b.log.Error("events: broadcast decode failed", "error", err)
				continue
			}
			tenant, aggType, aggID := events.DecodeRemoteMetadata(env.Payload)
			b.dispatchBroadcast(events.RemoteEvent{
				EventType:   env.Type,
				Occurred:    env.OccurredAt,
				Correlation: env.CorrelationID,
				Payload:     env.Payload,
				Tenant:      tenant,
				AggType:     aggType,
				AggID:       aggID,
			})
		}
	}
}

func (b *Bus) dispatchBroadcast(event events.RemoteEvent) {
	b.broadcastMu.RLock()
	subs := make([]*broadcastSub, 0, len(b.broadcastSubs))
	for _, s := range b.broadcastSubs {
		if s.topic == "*" || s.topic == event.EventType {
			subs = append(subs, s)
		}
	}
	b.broadcastMu.RUnlock()

	for _, s := range subs {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		if err := s.handler(ctx, event); err != nil {
			b.log.Error("events: broadcast handler failed", "event_type", event.EventType, "error", err)
		}
		cancel()
	}
}
