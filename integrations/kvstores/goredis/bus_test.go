package goredis

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/redis/go-redis/v9"
)

type testEvent struct {
	events.BaseEvent
	Data string `json:"data"`
}

// Also used by the module's cache/limiter hook tests; it performs no startup I/O.
func dummyClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:6390"})
}

type busCommandHook struct {
	run func(context.Context, redis.Cmder) error
}

func (h busCommandHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h busCommandHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return h.run
}
func (h busCommandHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func newTestBus(t *testing.T, opts ...BusOption) *Bus {
	t.Helper()
	rdb := dummyClient()
	t.Cleanup(func() { _ = rdb.Close() })
	b := New(rdb, append([]BusOption{WithLogger(slog.New(slog.DiscardHandler))}, opts...)...)
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

func TestBusDefaultsAndExplicitOptions(t *testing.T) {
	b := newTestBus(t)
	want := busConfig{log: b.log, StreamPrefix: "events:v2:", ConsumerGroup: "default", Workers: 4,
		QueueSize: 1000, BlockTimeout: 5 * time.Second, RetryAfter: time.Minute, HandlerTimeout: 30 * time.Second}
	if b.cfg != want || b.consumerName == "" {
		t.Fatalf("defaults = %+v, consumer = %q", b.cfg, b.consumerName)
	}
	explicit := newTestBus(t, WithStreamPrefix("ignored:"), WithStreamPrefix("host:v2:"), WithConsumerGroup("workers"),
		WithWorkers(2), WithQueueSize(3), WithBlockTimeout(time.Millisecond), WithRetryAfter(time.Second), WithHandlerTimeout(100*time.Millisecond))
	want = busConfig{log: explicit.log, StreamPrefix: "host:v2:v2:", ConsumerGroup: "workers", Workers: 2,
		QueueSize: 3, BlockTimeout: time.Millisecond, RetryAfter: time.Second, HandlerTimeout: 100 * time.Millisecond}
	if explicit.cfg != want {
		t.Fatalf("explicit = %+v, want %+v", explicit.cfg, want)
	}
	nilLogger := New(nil, WithLogger(nil))
	defer nilLogger.Close(context.Background())
	if nilLogger.log != slog.Default() {
		t.Fatal("nil logger was not defaulted")
	}
}

func TestBusRecordRoundTripAndTypedHandler(t *testing.T) {
	src := testEvent{BaseEvent: events.NewBaseEvent("widget.created").WithTenant("t1").WithAggregate("widget", "w1"), Data: "hello"}
	record, err := events.NewRecord(src)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := parseMessage(map[string]any{"record": string(raw)})
	if err != nil || !reflect.DeepEqual(remote.Record, record) {
		t.Fatalf("record = %+v, %v; want %+v", remote, err, record)
	}
	var received testEvent
	if err := events.TypedHandler(func(_ context.Context, e testEvent) error { received = e; return nil })(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
	if received.Data != src.Data || received.Type() != src.Type() {
		t.Fatalf("typed payload = %+v", received)
	}
	for _, values := range []map[string]any{{"payload": "old format"}, {"record": "{}"}, {"record": "not json"}, {"record": `{"event_id":"id","type":"*"}`}} {
		if _, err := parseMessage(values); err == nil {
			t.Fatalf("accepted invalid envelope %v", values)
		}
	}
}

func TestBusPublishPreservesOpaqueRecordWithoutLocalDispatch(t *testing.T) {
	b := newTestBus(t)
	var localCalls atomic.Int64
	b.broadcastSubs[1] = &broadcastSub{topic: "binary", handler: func(context.Context, events.Event) error { localCalls.Add(1); return nil }}
	b.workSubs["binary"] = []handlerEntry{{handler: func(context.Context, events.Event) error { localCalls.Add(1); return nil }}}
	tenant := "tenant"
	record := events.Record{EventID: "stable", Type: "binary", Payload: []byte{0, 0xff, 'x'}, TenantID: &tenant}
	var streamRaw, broadcastRaw string
	var commands []string
	b.rdb.AddHook(busCommandHook{run: func(_ context.Context, cmd redis.Cmder) error {
		commands = append(commands, cmd.Name())
		switch cmd.Name() {
		case "xadd":
			args := cmd.Args()
			if len(args) != 5 || args[1] != "events:v2:binary" || args[3] != "record" {
				t.Fatalf("XADD must contain one untrimmed envelope: %#v", args)
			}
			streamRaw = args[4].(string)
			cmd.(*redis.StringCmd).SetVal("1-0")
		case "publish":
			broadcastRaw = string(cmd.Args()[2].([]byte))
			cmd.(*redis.IntCmd).SetVal(0)
		default:
			t.Fatalf("unexpected command %s", cmd.Name())
		}
		return nil
	}})
	if err := b.Publish(context.Background(), record.Event()); err != nil {
		t.Fatal(err)
	}
	got, err := decodeRecord(streamRaw)
	if err != nil || !reflect.DeepEqual(got.Record, record) || streamRaw != broadcastRaw || localCalls.Load() != 0 || !reflect.DeepEqual(commands, []string{"xadd", "publish"}) {
		t.Fatalf("publication lost envelope or dispatched locally: record=%+v err=%v commands=%v local=%d", got.Record, err, commands, localCalls.Load())
	}
}

func TestBusPublishFailureAndCancellationDoNotBroadcast(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancellation"}[canceled], func(t *testing.T) {
			b := newTestBus(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("XADD failed")
			calls := 0
			b.rdb.AddHook(busCommandHook{run: func(_ context.Context, cmd redis.Cmder) error {
				calls++
				if cmd.Name() != "xadd" {
					t.Fatal("broadcast ran after failed/canceled acceptance")
				}
				if canceled {
					cmd.(*redis.StringCmd).SetVal("1-0")
					cancel()
					return nil
				}
				return failure
			}})
			want := failure
			if canceled {
				want = context.Canceled
			}
			if err := b.Publish(ctx, events.NewBaseEvent("topic")); !errors.Is(err, want) || calls != 1 {
				t.Fatalf("Publish = %v, calls=%d", err, calls)
			}
		})
	}
}

func TestBusBoundedAsyncAdmissionAndSharedDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newTestBus(t, WithWorkers(1), WithQueueSize(1))
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		var writes atomic.Int64
		b.rdb.AddHook(busCommandHook{run: func(ctx context.Context, cmd redis.Cmder) error {
			if cmd.Name() == "xadd" {
				if writes.Add(1) == 1 {
					entered <- struct{}{}
					<-release
				}
				cmd.(*redis.StringCmd).SetVal("1-0")
			} else {
				cmd.(*redis.IntCmd).SetVal(0)
			}
			return ctx.Err()
		}})
		ctx, cancel := context.WithCancel(context.Background())
		if err := b.Emit(ctx, events.NewBaseEvent("topic")); err != nil {
			t.Fatal(err)
		}
		<-entered
		cancel() // Admitted async publication keeps its independent context.
		if err := b.Emit(context.Background(), events.NewBaseEvent("topic")); err != nil {
			t.Fatal(err)
		}
		if err := b.Emit(context.Background(), events.NewBaseEvent("topic")); !errors.Is(err, events.ErrCapacity) {
			t.Fatalf("full queue = %v", err)
		}
		for range 2 {
			closeCtx, stop := context.WithTimeout(context.Background(), time.Millisecond)
			if err := b.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("undrained Close = %v", err)
			}
			stop()
		}
		if err := b.Publish(context.Background(), events.NewBaseEvent("topic")); !errors.Is(err, events.ErrClosed) {
			t.Fatalf("closed publication = %v", err)
		}
		close(release)
		if err := b.Close(context.Background()); err != nil || writes.Load() != 2 {
			t.Fatalf("drain = %v, writes=%d", err, writes.Load())
		}
	})
}

func TestBusValidationAndClosedGating(t *testing.T) {
	b := newTestBus(t)
	for _, event := range []events.Event{nil, events.BaseEvent{}, events.NewBaseEvent("*")} {
		if err := b.Emit(context.Background(), event); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid Emit = %v", err)
		}
		if err := b.Publish(context.Background(), event); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid Publish = %v", err)
		}
	}
	if _, err := b.Subscribe("", func(context.Context, events.Event) error { return nil }); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := b.Subscribe("topic", nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, subscribe := range []func() (events.Subscription, error){
		func() (events.Subscription, error) { return b.Subscribe("topic", nil) },
		func() (events.Subscription, error) { return b.SubscribeBroadcast("topic", nil) },
		func() (events.Subscription, error) { return b.SubscribeWork(context.Background(), "topic", nil) },
	} {
		if _, err := subscribe(); !errors.Is(err, events.ErrClosed) {
			t.Fatalf("closed subscription = %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Emit(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBusWorkAttemptRecoversEachCallbackWithoutAck(t *testing.T) {
	b := newTestBus(t)
	var calls, acks int
	b.workSubs["topic"] = []handlerEntry{
		{handler: func(context.Context, events.Event) error { calls++; panic("failed") }},
		{handler: func(context.Context, events.Event) error { calls++; return errors.New("failed") }},
		{handler: func(context.Context, events.Event) error { calls++; return nil }},
	}
	b.rdb.AddHook(busCommandHook{run: func(context.Context, redis.Cmder) error { acks++; return nil }})
	raw, err := json.Marshal(events.Record{EventID: "id", Type: "topic"})
	if err != nil {
		t.Fatal(err)
	}
	b.processMessage(b.cfg.StreamPrefix+"topic", "1-0", map[string]any{"record": string(raw)})
	if calls != 3 || acks != 0 {
		t.Fatalf("callbacks=%d, ACKs=%d", calls, acks)
	}
	if err := callEventHandler(context.Background(), func(context.Context, events.Event) error { panic("x") }, events.NewBaseEvent("topic")); !errors.Is(err, events.ErrHandlerPanic) {
		t.Fatalf("panic classification = %v", err)
	}
}

func TestBusWorkTimeoutCoversWholeAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newTestBus(t, WithHandlerTimeout(30*time.Millisecond), WithRetryAfter(time.Second))
		var deadlines []time.Time
		var acks int
		handler := func(ctx context.Context, _ events.Event) error {
			deadline, _ := ctx.Deadline()
			deadlines = append(deadlines, deadline)
			time.Sleep(20 * time.Millisecond)
			return nil
		}
		b.workSubs["topic"] = []handlerEntry{{handler: handler}, {handler: handler}, {handler: handler}}
		b.rdb.AddHook(busCommandHook{run: func(context.Context, redis.Cmder) error { acks++; return nil }})
		raw, err := json.Marshal(events.Record{EventID: "id", Type: "topic"})
		if err != nil {
			t.Fatal(err)
		}
		b.processMessage(b.cfg.StreamPrefix+"topic", "1-0", map[string]any{"record": string(raw)})
		if len(deadlines) != 2 || deadlines[0] != deadlines[1] || acks != 0 {
			t.Fatalf("deadlines=%v ACKs=%d; want one deadline, no third callback or ACK", deadlines, acks)
		}
	})
}

func TestBusInvalidWorkConfigNeverCreatesGroup(t *testing.T) {
	for _, opt := range []BusOption{
		WithHandlerTimeout(-time.Second), WithRetryAfter(-time.Second),
		WithHandlerTimeout(time.Millisecond + 1), WithRetryAfter(30 * time.Second),
		WithBlockTimeout(-time.Second), WithBlockTimeout(time.Millisecond + 1),
	} {
		b := newTestBus(t, opt)
		if _, err := b.SubscribeWork(context.Background(), "topic", func(context.Context, events.Event) error { return nil }); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid config %+v = %v", b.cfg, err)
		}
	}
	b := newTestBus(t)
	if _, err := b.SubscribeWork(context.Background(), "*", func(context.Context, events.Event) error { return nil }); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("wildcard work = %v", err)
	}
}

func TestBusWorkReclaimCursorAndSingleEntryReads(t *testing.T) {
	b := newTestBus(t, WithWorkers(1), WithBlockTimeout(time.Millisecond))
	raw, err := json.Marshal(events.Record{EventID: "later-id", Type: "topic"})
	if err != nil {
		t.Fatal(err)
	}
	var scans, reads, handled, acks atomic.Int64
	b.workSubs["topic"] = []handlerEntry{{handler: func(context.Context, events.Event) error { handled.Add(1); return nil }}}
	b.rdb.AddHook(busCommandHook{run: func(_ context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		switch cmd.Name() {
		case "xautoclaim":
			if args[len(args)-1] != int64(1) {
				t.Errorf("claimed beyond available capacity: %#v", args)
			}
			if scans.Add(1) == 1 {
				cmd.(*redis.XAutoClaimCmd).SetVal(nil, "123-0")
			} else {
				if args[5] != "123-0" {
					t.Errorf("lost nonterminal cursor after empty scan: %#v", args)
				}
				cmd.(*redis.XAutoClaimCmd).SetVal([]redis.XMessage{{ID: "124-0", Values: map[string]any{"record": string(raw)}}}, "0-0")
			}
		case "xreadgroup":
			reads.Add(1)
			if args[len(args)-2] != b.cfg.StreamPrefix+"topic" || args[len(args)-1] != ">" {
				t.Errorf("read must select one stream: %#v", args)
			}
			return redis.Nil
		case "xack":
			acks.Add(1)
			cmd.(*redis.IntCmd).SetVal(1)
			b.cancel()
		default:
			t.Errorf("unexpected command: %s", cmd.Name())
		}
		return nil
	}})
	b.wg.Add(1)
	finished := make(chan struct{})
	go func() { b.consumeLoop(0); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker did not progress beyond empty nonterminal reclaim scan")
	}
	if scans.Load() != 2 || reads.Load() != 1 || handled.Load() != 1 || acks.Load() != 1 {
		t.Fatalf("scans=%d reads=%d handlers=%d ACKs=%d", scans.Load(), reads.Load(), handled.Load(), acks.Load())
	}
}

func TestBusWorkSubscriptionFailureIsRetryable(t *testing.T) {
	b := newTestBus(t, WithWorkers(1))
	var creates atomic.Int64
	failure := errors.New("group setup failed")
	b.rdb.AddHook(busCommandHook{run: func(_ context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "xgroup" {
			if creates.Add(1) == 1 {
				return failure
			}
			cmd.(*redis.StatusCmd).SetVal("OK")
			return nil
		}
		return redis.Nil
	}})
	handler := func(context.Context, events.Event) error { return nil }
	if _, err := b.SubscribeWork(context.Background(), "topic", handler); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if b.workStarted || len(b.workSubs) != 0 {
		t.Fatal("failed setup installed a registration or permanently started workers")
	}
	sub, err := b.SubscribeWork(context.Background(), "topic", handler)
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	if topics := b.activeTopics(); len(topics) != 0 {
		t.Fatalf("unsubscribed work remains active: %v", topics)
	}
}

func TestBusWorkSubscribeCloseRaceRejectsRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newTestBus(t, WithWorkers(1))
		entered, release := make(chan struct{}), make(chan struct{})
		b.rdb.AddHook(busCommandHook{run: func(_ context.Context, cmd redis.Cmder) error {
			close(entered)
			<-release
			cmd.(*redis.StatusCmd).SetVal("OK")
			return nil
		}})
		done := make(chan error, 1)
		go func() {
			_, err := b.SubscribeWork(context.Background(), "topic", func(context.Context, events.Event) error { return nil })
			done <- err
		}()
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		if err := b.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close while admitted setup runs = %v", err)
		}
		cancel()
		close(release)
		if err := <-done; !errors.Is(err, events.ErrClosed) {
			t.Fatalf("registration after Close = %v", err)
		}
		if err := b.Close(context.Background()); err != nil || len(b.activeTopics()) != 0 {
			t.Fatalf("close cleanup = %v", err)
		}
	})
}

func TestBusUnhandledAndUnsubscribedWorkNeverAcknowledges(t *testing.T) {
	b := newTestBus(t)
	var calls, acks int
	entries := []handlerEntry{{id: 1, handler: func(context.Context, events.Event) error { calls++; return nil }}}
	b.workSubs["topic"] = entries
	b.rdb.AddHook(busCommandHook{run: func(context.Context, redis.Cmder) error { acks++; return nil }})
	if err := (&workSubscription{id: 1, topic: "topic", bus: b}).Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	if entries[0].handler != nil || len(b.activeTopics()) != 0 {
		t.Fatal("unsubscribe retained its handler graph or active topic")
	}
	b.processMessage(b.cfg.StreamPrefix+"topic", "1-0", map[string]any{"record": `{"event_id":"id","type":"topic"}`})
	if calls != 0 || acks != 0 {
		t.Fatalf("unhandled entry: callbacks=%d ACKs=%d", calls, acks)
	}
}

func TestBusCloseWaitsForAdmittedCheckedPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newTestBus(t)
		entered, release := make(chan struct{}), make(chan struct{})
		b.rdb.AddHook(busCommandHook{run: func(_ context.Context, cmd redis.Cmder) error {
			if cmd.Name() == "xadd" {
				close(entered)
				<-release
				cmd.(*redis.StringCmd).SetVal("1-0")
			} else {
				cmd.(*redis.IntCmd).SetVal(0)
			}
			return nil
		}})
		done := make(chan error, 1)
		go func() { done <- b.Publish(context.Background(), events.NewBaseEvent("topic")) }()
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		if err := b.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close completed before accepted Publish: %v", err)
		}
		cancel()
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := b.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
