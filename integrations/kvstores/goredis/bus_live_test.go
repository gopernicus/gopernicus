package goredis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/redis/go-redis/v9"
)

func liveBusClient(t *testing.T) (*redis.Client, context.Context, []BusOption) {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set — live event delivery and work recovery not verified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	rdb, err := Open(ctx, Config{Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	prefix := fmt.Sprintf("bustest:%d:", time.Now().UnixNano())
	opts := []BusOption{WithStreamPrefix(prefix), WithConsumerGroup("work"), WithWorkers(1), WithQueueSize(4),
		WithBlockTimeout(20 * time.Millisecond), WithRetryAfter(150 * time.Millisecond), WithHandlerTimeout(50 * time.Millisecond)}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := rdb.Scan(cleanupCtx, cursor, prefix+"*", 100).Result()
			if err != nil {
				return
			}
			if len(keys) != 0 {
				_ = rdb.Del(cleanupCtx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	return rdb, ctx, opts
}

func newLiveBus(t *testing.T, rdb *redis.Client, opts []BusOption) *Bus {
	t.Helper()
	b := New(rdb, append([]BusOption{WithLogger(slog.New(slog.DiscardHandler))}, opts...)...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return b
}

func waitBusCondition(t *testing.T, ctx context.Context, message string, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v", message, ctx.Err())
		case <-ticker.C:
		}
	}
}

func receiveBusRecord(t *testing.T, ctx context.Context, ch <-chan events.Record) events.Record {
	t.Helper()
	select {
	case record := <-ch:
		return record
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return events.Record{}
	}
}

func TestBusLiveRecordPreservedAcrossBothPaths(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	receiver := newLiveBus(t, rdb, opts)
	publisher := newLiveBus(t, rdb, opts)
	notifications, work := make(chan events.Record, 2), make(chan events.Record, 2)
	if _, err := receiver.Subscribe("*", func(_ context.Context, e events.Event) error {
		notifications <- e.(events.RemoteEvent).Record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.SubscribeWork(ctx, "opaque", func(_ context.Context, e events.Event) error {
		work <- e.(events.RemoteEvent).Record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tenant, aggregate, aggregateID := "t1", "widget", "w1"
	record := events.Record{EventID: "stable-id", Type: "opaque", OccurredAt: time.Now().UTC(),
		CorrelationID: "correlation", Payload: []byte{0xff, 0, 'x'}, TenantID: &tenant, AggregateType: &aggregate, AggregateID: &aggregateID}
	// No readiness sleep: Subscribe must have received the server's ACK.
	if err := publisher.Publish(ctx, record.Event()); err != nil {
		t.Fatal(err)
	}
	for name, ch := range map[string]<-chan events.Record{"notification": notifications, "work": work} {
		if got := receiveBusRecord(t, ctx, ch); !reflect.DeepEqual(got, record) {
			t.Fatalf("%s record = %+v, want %+v", name, got, record)
		}
	}
	stream := receiver.cfg.StreamPrefix + "opaque"
	waitBusCondition(t, ctx, "successful work ACK", func() bool {
		pending, err := rdb.XPending(ctx, stream, "work").Result()
		if err != nil {
			t.Fatal(err)
		}
		return pending.Count == 0
	})
	rows, err := rdb.XRange(ctx, stream, "-", "+").Result()
	if err != nil || len(rows) != 1 {
		t.Fatalf("retained stream entries = %v, %v", rows, err)
	}
	stored, err := parseMessage(rows[0].Values)
	if err != nil || !reflect.DeepEqual(stored.Record, record) {
		t.Fatalf("stored envelope = %+v, %v", stored, err)
	}
}

func TestBusLiveWorkFailuresRetryWithoutBlockingFreshWork(t *testing.T) {
	for _, failure := range []string{"error", "panic", "timeout returning nil"} {
		t.Run(failure, func(t *testing.T) {
			rdb, ctx, opts := liveBusClient(t)
			b := newLiveBus(t, rdb, opts)
			var attempts atomic.Int64
			first := make(chan struct{})
			delivered := make(chan events.Record, 4)
			if _, err := b.SubscribeWork(ctx, "topic", func(handlerCtx context.Context, event events.Event) error {
				record := event.(events.RemoteEvent).Record
				if record.EventID == "retry" && attempts.Add(1) == 1 {
					close(first)
					switch failure {
					case "error":
						return errors.New("retry me")
					case "panic":
						panic("retry me")
					default:
						<-handlerCtx.Done()
						return nil
					}
				}
				delivered <- record
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := b.Publish(ctx, (events.Record{EventID: "retry", Type: "topic", Payload: []byte("original")}).Event()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-first:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := b.Publish(ctx, (events.Record{EventID: "fresh", Type: "topic"}).Event()); err != nil {
				t.Fatal(err)
			}
			seen := make(map[string]events.Record)
			for len(seen) < 2 {
				record := receiveBusRecord(t, ctx, delivered)
				seen[record.EventID] = record
			}
			if attempts.Load() < 2 || string(seen["retry"].Payload) != "original" {
				t.Fatalf("retry lost identity/payload: attempts=%d record=%+v", attempts.Load(), seen["retry"])
			}
			waitBusCondition(t, ctx, "retried and fresh entries acknowledged", func() bool {
				pending, err := rdb.XPending(ctx, b.cfg.StreamPrefix+"topic", "work").Result()
				if err != nil {
					t.Fatal(err)
				}
				return pending.Count == 0
			})
		})
	}
}

func TestBusLiveMalformedRecordsRemainPending(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	b := newLiveBus(t, rdb, opts)
	stream := b.cfg.StreamPrefix + "topic"
	for _, raw := range []string{"not-json", `{"type":"topic"}`, `{"event_id":"wrong","type":"another-topic"}`} {
		if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"record": raw}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	delivered := make(chan events.Record, 1)
	if _, err := b.SubscribeWork(ctx, "topic", func(_ context.Context, event events.Event) error {
		delivered <- event.(events.RemoteEvent).Record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitBusCondition(t, ctx, "all malformed entries stay pending", func() bool {
		pending, err := rdb.XPending(ctx, stream, "work").Result()
		if err != nil {
			t.Fatal(err)
		}
		return pending.Count == 3
	})
	if err := b.Publish(ctx, (events.Record{EventID: "good", Type: "topic"}).Event()); err != nil {
		t.Fatal(err)
	}
	if got := receiveBusRecord(t, ctx, delivered); got.EventID != "good" {
		t.Fatalf("dispatched malformed record: %+v", got)
	}
	waitBusCondition(t, ctx, "only successful work leaves pending", func() bool {
		pending, err := rdb.XPending(ctx, stream, "work").Result()
		if err != nil {
			t.Fatal(err)
		}
		return pending.Count == 3
	})
}

func TestBusLiveReclaimsAnotherConsumerPendingEntry(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	b := newLiveBus(t, rdb, opts)
	stream := b.cfg.StreamPrefix + "topic"
	if err := b.Publish(ctx, (events.Record{EventID: "orphan", Type: "topic"}).Event()); err != nil {
		t.Fatal(err)
	}
	if err := rdb.XGroupCreate(ctx, stream, "work", "0").Err(); err != nil {
		t.Fatal(err)
	}
	rows, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "work", Consumer: "crashed", Streams: []string{stream, ">"}, Count: 1, Block: -1}).Result()
	if err != nil || len(rows) != 1 || len(rows[0].Messages) != 1 {
		t.Fatalf("seed pending = %v, %v", rows, err)
	}
	id := rows[0].Messages[0].ID
	// Set pending idle age directly, avoiding a timing sleep in this recovery proof.
	if err := rdb.Do(ctx, "XCLAIM", stream, "work", "crashed", 0, id, "IDLE", 1000).Err(); err != nil {
		t.Fatal(err)
	}
	delivered := make(chan events.Record, 1)
	if _, err := b.SubscribeWork(ctx, "topic", func(_ context.Context, event events.Event) error {
		delivered <- event.(events.RemoteEvent).Record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := receiveBusRecord(t, ctx, delivered); got.EventID != "orphan" {
		t.Fatalf("reclaim changed event identity: %+v", got)
	}
}

func TestBusLiveGroupLossRecreatesAndResumes(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	b := newLiveBus(t, rdb, opts)
	delivered := make(chan events.Record, 8)
	if _, err := b.SubscribeWork(ctx, "topic", func(_ context.Context, event events.Event) error {
		delivered <- event.(events.RemoteEvent).Record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, (events.Record{EventID: "before", Type: "topic"}).Event()); err != nil {
		t.Fatal(err)
	}
	_ = receiveBusRecord(t, ctx, delivered)
	if err := rdb.XGroupDestroy(ctx, b.cfg.StreamPrefix+"topic", "work").Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, (events.Record{EventID: "after", Type: "topic"}).Event()); err != nil {
		t.Fatal(err)
	}
	for {
		if got := receiveBusRecord(t, ctx, delivered); got.EventID == "after" {
			break
		}
	}
}

func TestBusLiveBroadcastPanicDoesNotSkipOtherHandlers(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	b := newLiveBus(t, rdb, opts)
	if _, err := b.Subscribe("topic", func(context.Context, events.Event) error { panic("notification panic") }); err != nil {
		t.Fatal(err)
	}
	delivered := make(chan events.Record, 2)
	if _, err := b.SubscribeBroadcast("topic", func(_ context.Context, event events.Event) error {
		delivered <- event.(events.RemoteEvent).Record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err := b.Publish(ctx, (events.Record{EventID: id, Type: "topic"}).Event()); err != nil {
			t.Fatal(err)
		}
		if got := receiveBusRecord(t, ctx, delivered); got.EventID != id {
			t.Fatalf("broadcast stopped after panic: %+v", got)
		}
	}
}

func TestBusLiveClosedBusHasNoRedisEffects(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	b := newLiveBus(t, rdb, opts)
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var notifications atomic.Int64
	receiver := newLiveBus(t, rdb, opts)
	if _, err := receiver.Subscribe("*", func(context.Context, events.Event) error { notifications.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, publish := range []func(context.Context, events.Event) error{b.Emit, b.Publish} {
		if err := publish(ctx, events.NewBaseEvent("closed")); !errors.Is(err, events.ErrClosed) {
			t.Fatal(err)
		}
	}
	if count, err := rdb.Exists(ctx, b.cfg.StreamPrefix+"closed").Result(); err != nil || count != 0 {
		t.Fatalf("closed bus wrote Redis state: %d, %v", count, err)
	}
	if notifications.Load() != 0 {
		t.Fatal("closed bus broadcast an event")
	}
}
