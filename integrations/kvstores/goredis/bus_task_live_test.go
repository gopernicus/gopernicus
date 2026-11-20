package goredis

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events/eventstest"
	"github.com/redis/go-redis/v9"
)

// liveTaskBus returns peers sharing one live prefix and group, with RetryAfter
// just above the suite's handler timeout.
func liveTaskBus(t *testing.T, policy events.DeliveryPolicy) func() *Bus {
	t.Helper()
	rdb, _, opts := liveBusClient(t)
	opts = append(opts, WithRetryAfter(eventstest.TaskHandlerTimeout+50*time.Millisecond), WithWorkPolicy(policy))
	return func() *Bus { return newLiveBus(t, rdb, opts) }
}

func TestBusLiveTaskConformance(t *testing.T) {
	eventstest.RunTask(t, func(t *testing.T, policy events.DeliveryPolicy) func() eventstest.TaskPeer {
		newBus := liveTaskBus(t, policy)
		return func() eventstest.TaskPeer { return newBus() }
	})
}

func liveDeadLetterCount(t *testing.T, ctx context.Context, b *Bus, topic string) int {
	t.Helper()
	dls, err := b.DeadLetters(ctx, topic, 10)
	if err != nil {
		t.Fatal(err)
	}
	return len(dls)
}

func TestBusLiveGroupRecreationDoesNotResurrectDeadLetter(t *testing.T) {
	b := liveTaskBus(t, events.DeliveryPolicy{MaxDeliveries: 1, HandlerTimeout: eventstest.TaskHandlerTimeout})()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var calls atomic.Int32
	if _, err := b.SubscribeTask(ctx, "recreate", func(context.Context, events.Event) error {
		calls.Add(1)
		return errors.New("poison")
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, events.NewBaseEvent("recreate")); err != nil {
		t.Fatal(err)
	}
	waitBusCondition(t, ctx, "dead letter", func() bool { return liveDeadLetterCount(t, ctx, b, "recreate") == 1 })
	stream := b.cfg.StreamPrefix + "recreate"
	if n := b.rdb.XLen(ctx, stream).Val(); n != 0 {
		t.Fatalf("sole-group dead-lettering left %d source entries", n)
	}
	if err := b.rdb.XGroupDestroy(ctx, stream, b.cfg.ConsumerGroup).Err(); err != nil {
		t.Fatal(err)
	}
	// The reader recreates the group at 0 on NOGROUP; nothing may come back.
	waitBusCondition(t, ctx, "group recreation", func() bool {
		groups, err := b.rdb.XInfoGroups(ctx, stream).Result()
		return err == nil && len(groups) == 1
	})
	time.Sleep(400 * time.Millisecond)
	if calls.Load() != 1 || liveDeadLetterCount(t, ctx, b, "recreate") != 1 {
		t.Fatalf("after recreation: calls=%d dead letters=%d", calls.Load(), liveDeadLetterCount(t, ctx, b, "recreate"))
	}
}

func TestBusLiveMultiGroupDeadLetterKeepsEntryForOtherGroups(t *testing.T) {
	b := liveTaskBus(t, events.DeliveryPolicy{MaxDeliveries: 1, HandlerTimeout: eventstest.TaskHandlerTimeout})()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream := b.cfg.StreamPrefix + "shared"
	if err := b.rdb.XGroupCreateMkStream(ctx, stream, "other-service", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SubscribeTask(ctx, "shared", func(context.Context, events.Event) error { return errors.New("poison") }); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, events.NewBaseEvent("shared")); err != nil {
		t.Fatal(err)
	}
	waitBusCondition(t, ctx, "dead letter", func() bool { return liveDeadLetterCount(t, ctx, b, "shared") == 1 })
	pending, err := b.rdb.XPending(ctx, stream, b.cfg.ConsumerGroup).Result()
	if err != nil || pending.Count != 0 {
		t.Fatalf("our group pending = %+v, %v; want acknowledged", pending, err)
	}
	other, err := b.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "other-service", Consumer: "c",
		Streams: []string{stream, ">"}, Count: 10, Block: -1}).Result()
	if err != nil || len(other) != 1 || len(other[0].Messages) != 1 {
		t.Fatalf("other group lost the entry: %+v, %v", other, err)
	}
}

func TestBusLiveMalformedEntryDeadLettersRawBytes(t *testing.T) {
	b := liveTaskBus(t, events.DeliveryPolicy{MaxDeliveries: 2, HandlerTimeout: eventstest.TaskHandlerTimeout})()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := b.SubscribeTask(ctx, "broken", func(context.Context, events.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	stream := b.cfg.StreamPrefix + "broken"
	for _, values := range []map[string]any{{"record": "not-json"}, {"bogus": "x"}} {
		if err := b.rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	waitBusCondition(t, ctx, "two malformed dead letters", func() bool { return liveDeadLetterCount(t, ctx, b, "broken") == 2 })
	dls, err := b.DeadLetters(ctx, "broken", 10)
	if err != nil {
		t.Fatal(err)
	}
	if dls[0].Record != nil || string(dls[0].Raw) != "not-json" || dls[0].Deliveries != 2 {
		t.Fatalf("invalid JSON dead letter = %+v", dls[0])
	}
	if dls[1].Record != nil || !strings.Contains(string(dls[1].Raw), "bogus") {
		t.Fatalf("missing-envelope dead letter = %+v", dls[1])
	}
}

func TestBusLiveNoHandlerClaimNeverDeadLetters(t *testing.T) {
	b := liveTaskBus(t, events.DeliveryPolicy{MaxDeliveries: 1})()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream := b.cfg.StreamPrefix + "orphan"
	if err := b.ensureGroup(ctx, stream, "0"); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, events.NewBaseEvent("orphan")); err != nil {
		t.Fatal(err)
	}
	read, err := b.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: b.cfg.ConsumerGroup, Consumer: "c",
		Streams: []string{stream, ">"}, Count: 1, Block: -1}).Result()
	if err != nil {
		t.Fatal(err)
	}
	message := read[0].Messages[0]
	b.processMessage(stream, "c", message.ID, message.Values, 5)
	if n := liveDeadLetterCount(t, ctx, b, "orphan"); n != 0 {
		t.Fatalf("an unhandled claim dead-lettered %d entries", n)
	}
	if pending := b.rdb.XPending(ctx, stream, b.cfg.ConsumerGroup).Val(); pending.Count != 1 {
		t.Fatalf("unhandled entry pending = %d, want 1", pending.Count)
	}
}
