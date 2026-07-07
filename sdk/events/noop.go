package events

import "context"

// Noop is the disabled-bus default: Emit does nothing, Subscribe returns a
// no-op subscription. Wire it (or leave Mount.Events nil and guard) when a host
// does not use events, so call sites can Emit unconditionally.
type Noop struct{}

var (
	_ Bus         = Noop{}
	_ Broadcaster = Noop{}
)

// Emit discards the event.
func (Noop) Emit(context.Context, Event, ...EmitOption) error { return nil }

// Subscribe returns a subscription whose Unsubscribe is a no-op.
func (Noop) Subscribe(string, Handler) (Subscription, error) {
	return noopSubscription{}, nil
}

// SubscribeBroadcast returns a subscription whose Unsubscribe is a no-op.
func (Noop) SubscribeBroadcast(string, Handler) (Subscription, error) {
	return noopSubscription{}, nil
}

// Close is a no-op.
func (Noop) Close(context.Context) error { return nil }

type noopSubscription struct{}

func (noopSubscription) Unsubscribe() error { return nil }
