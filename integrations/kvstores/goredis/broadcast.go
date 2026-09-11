package goredis

import (
	"context"
	"fmt"
	"sync"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/redis/go-redis/v9"
)

type broadcastSetup struct {
	done chan struct{}
	err  error
}

type broadcastSub struct {
	id      uint64
	topic   string
	handler events.Handler
	bus     *Bus
	once    sync.Once
}

func (b *Bus) broadcastChannel() string { return b.cfg.StreamPrefix + "broadcast" }

// Unsubscribe removes this registration. Already selected callbacks may finish.
func (s *broadcastSub) Unsubscribe() error {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.broadcastSubs, s.id)
		s.handler = nil
		s.bus.mu.Unlock()
	})
	return nil
}

// SubscribeBroadcast waits for Redis subscription acknowledgment before it
// registers an exact-topic or wildcard notification handler. Setup has a
// five-second context; caller-owned client timeouts govern underlying I/O.
// Setup failure returns an error instead of an inert subscription.
func (b *Bus) SubscribeBroadcast(topic string, handler events.Handler) (events.Subscription, error) {
	ctx, cancel := context.WithTimeout(b.ctx, opTimeout)
	defer cancel()
	if err := b.begin(context.Background()); err != nil {
		return nil, err
	}
	defer b.wg.Done()
	if err := events.ValidateSubscription(topic, handler); err != nil {
		return nil, err
	}
	if err := b.ensureBroadcast(ctx); err != nil {
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
	sub := &broadcastSub{id: b.nextID, topic: topic, handler: handler, bus: b}
	b.broadcastSubs[sub.id] = sub
	return sub, nil
}

func (b *Bus) ensureBroadcast(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return events.ErrClosed
	}
	if b.broadcastPubsub != nil {
		b.mu.Unlock()
		return nil
	}
	if setup := b.broadcastSetup; setup != nil {
		b.mu.Unlock()
		select {
		case <-setup.done:
			return setup.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	setup := &broadcastSetup{done: make(chan struct{})}
	b.broadcastSetup = setup
	b.mu.Unlock()

	pubsub := b.rdb.Subscribe(ctx, b.broadcastChannel())
	ack, err := pubsub.ReceiveTimeout(ctx, opTimeout)
	if err == nil {
		subscription, ok := ack.(*redis.Subscription)
		if !ok || subscription.Kind != "subscribe" || subscription.Channel != b.broadcastChannel() {
			err = fmt.Errorf("goredis: unexpected broadcast subscription acknowledgment")
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	b.mu.Lock()
	if b.closed {
		err = events.ErrClosed
	}
	if err == nil {
		b.broadcastPubsub = pubsub
		b.wg.Add(1)
		go b.broadcastLoop(pubsub)
	}
	setup.err = err
	b.broadcastSetup = nil
	close(setup.done)
	b.mu.Unlock()
	if err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("goredis: subscribing to broadcasts: %w", err)
	}
	return nil
}

func (b *Bus) broadcastLoop(pubsub *redis.PubSub) {
	defer b.wg.Done()
	ch := pubsub.Channel()
	for {
		select {
		case <-b.ctx.Done():
			return
		case message, ok := <-ch:
			if !ok {
				return
			}
			event, err := decodeRecord(message.Payload)
			if err != nil {
				b.log.Error("events: invalid broadcast record", "error", err)
				continue
			}
			b.dispatchBroadcast(event)
		}
	}
}

func (b *Bus) dispatchBroadcast(event events.RemoteEvent) {
	b.mu.Lock()
	var handlers []events.Handler
	if !b.closed {
		for _, sub := range b.broadcastSubs {
			if sub.topic == "*" || sub.topic == event.Type() {
				handlers = append(handlers, sub.handler)
			}
		}
	}
	b.mu.Unlock()
	for _, handler := range handlers {
		if b.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(b.ctx, opTimeout)
		err := callEventHandler(ctx, handler, event)
		cancel()
		if err != nil {
			b.log.Error("events: broadcast handler failed", "event_type", event.Type(), "error", err)
		}
	}
}
