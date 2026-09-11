package events

import "context"

// Noop explicitly disables notifications. It validates admission but does not
// provide Dispatch or any checked outbox handoff. It owns no lifecycle state.
type Noop struct{}

var (
	_ Bus         = Noop{}
	_ Broadcaster = Noop{}
)

func (Noop) Emit(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ValidateEvent(event)
}
func (Noop) Subscribe(topic string, handler Handler) (Subscription, error) {
	if err := ValidateSubscription(topic, handler); err != nil {
		return nil, err
	}
	return noopSubscription{}, nil
}
func (n Noop) SubscribeBroadcast(topic string, handler Handler) (Subscription, error) {
	return n.Subscribe(topic, handler)
}
func (Noop) Close(context.Context) error { return nil }

type noopSubscription struct{}

func (noopSubscription) Unsubscribe() error { return nil }
