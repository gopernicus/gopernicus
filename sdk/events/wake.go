package events

import "context"

// WakeChannel returns a capacity-1 channel that receives a signal every time
// the bus emits an event matching topic — the low-latency bridge between Emit
// and a polling worker pool (feed it to workers.WithWakeChannel).
//
// Sends are coalesced: a burst of events collapses into a single pending wake,
// and the send never blocks the bus. Lost wakes are tolerated by design — a
// missed signal only means the consumer waits until its next interval tick, so
// the poller stays correct even under drops.
//
// The returned Subscription's lifetime is the caller's to manage. Unsubscribe
// stops further wakes; closing the bus has the same effect for all its
// subscriptions.
func WakeChannel(bus Bus, topic string) (<-chan struct{}, Subscription, error) {
	wake := make(chan struct{}, 1)
	sub, err := bus.Subscribe(topic, func(context.Context, Event) error {
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
