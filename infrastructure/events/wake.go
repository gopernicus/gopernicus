package events

import "context"

// WakeChannel returns a buffered channel that receives a value every time
// the bus emits an event matching topic. Designed to feed
// workers.WithWakeChannel.
//
// Sends are coalesced — bursts collapse into a single wake — and never block
// the bus subscriber. Lost wakes are tolerated; the consumer (typically a
// polling worker pool) will catch up on its next interval tick.
//
// The returned Subscription's lifetime is the caller's to manage. Calling
// Unsubscribe stops further wakes from firing. Closing the bus has the same
// effect for all subscriptions it owns.
func WakeChannel(bus Bus, topic string) (<-chan struct{}, Subscription, error) {
	wake := make(chan struct{}, 1)
	sub, err := bus.Subscribe(topic, func(_ context.Context, _ Event) error {
		select {
		case wake <- struct{}{}:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return wake, sub, nil
}
