package events

import (
	"context"
	"testing"
)

func TestMemory_Retention_UnsubscribeReleasesReferences(t *testing.T) {
	bus := NewMemory(WithWorkerCount(1))
	defer bus.Close(context.Background())
	const topic = "retention.event"
	handler := func(context.Context, Event) error { return nil }
	first, err := bus.Subscribe(topic, handler)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bus.Subscribe(topic, handler)
	if err != nil {
		t.Fatal(err)
	}
	removed := first.(*memorySubscription)
	remaining := second.(*memorySubscription)
	bus.mu.Lock()
	backing := bus.subscriptions[topic]
	bus.mu.Unlock()

	if err := first.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	bus.mu.Lock()
	if subscriptions := bus.subscriptions[topic]; len(subscriptions) != 1 || subscriptions[0] != remaining {
		t.Error("removing the first subscription did not preserve the remaining registration")
	}
	if backing[1] != nil {
		t.Error("vacated backing-array slot retains a subscription")
	}
	if removed.handler != nil {
		t.Error("unsubscribed token retains its handler closure")
	}
	bus.mu.Unlock()

	if err := second.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	bus.mu.Lock()
	if _, exists := bus.subscriptions[topic]; exists {
		t.Error("unsubscribing the final handler retains the empty topic")
	}
	if backing[0] != nil || backing[1] != nil {
		t.Error("backing-array slots retain subscriptions after all handlers unsubscribe")
	}
	if remaining.handler != nil {
		t.Error("final unsubscribed token retains its handler closure")
	}
	bus.mu.Unlock()
}

func TestMemory_Retention_CloseReleasesHandlersHeldByTokens(t *testing.T) {
	bus := NewMemory()
	sub, err := bus.Subscribe("retention.closed", func(context.Context, Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	if sub.(*memorySubscription).handler != nil {
		t.Fatal("closed subscription token retains handler")
	}
}
