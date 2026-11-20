package eventstest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

const (
	// TaskHandlerTimeout is the HandlerTimeout every RunTask case uses unless
	// it states otherwise. Adapters should keep their abandoned-delivery
	// reclaim floor just above it (for example HandlerTimeout+50ms) so the
	// suite runs quickly.
	TaskHandlerTimeout = 100 * time.Millisecond

	taskWait   = 10 * time.Second
	taskSettle = 600 * time.Millisecond
	// timerSlack tolerates clock granularity when asserting minimum delays.
	timerSlack = 10 * time.Millisecond
)

var errTaskFailure = errors.New("eventstest: deliberate task failure")

// TaskPeer is one competing consumer of a task backend, plus the
// publish side the suite needs to feed it.
type TaskPeer interface {
	Publish(context.Context, events.Event) error
	events.TaskSubscriber
	events.TaskAdmin
	Close(context.Context) error
}

// RunTask exercises the task delivery contract. For each case it calls
// newPeers once with the case's policy; every peer the returned constructor
// makes must share one backend, namespace and consumer group, and the factory
// owns closing them (t.Cleanup). Backends may retry later than the policy's
// minimum delays; the suite asserts lower bounds and ordering only.
func RunTask(t *testing.T, newPeers func(t *testing.T, policy events.DeliveryPolicy) func() TaskPeer) {
	t.Helper()
	cases := []struct {
		name string
		run  func(*testing.T, func(*testing.T, events.DeliveryPolicy) func() TaskPeer)
	}{
		{"InvalidInput", testTaskInvalidInput},
		{"AcksOnce", testTaskAcksOnce},
		{"RetriesCarryDeliveryAttempt", testTaskRetriesCarryAttempt},
		{"ZeroPolicyNeverDeadLetters", testTaskZeroPolicyNeverDeadLetters},
		{"PanicCountsAsFailure", testTaskPanicCountsAsFailure},
		{"TimeoutCountsAsFailure", testTaskTimeoutCountsAsFailure},
		{"ExhaustionDeadLettersOnceAcrossPeers", testTaskExhaustionOnceAcrossPeers},
		{"BackoffOrdering", testTaskBackoffOrdering},
		{"Redeliver", testTaskRedeliver},
		{"DiscardDeadLetter", testTaskDiscard},
		{"TrimKeepsUnacknowledgedEntries", testTaskTrimKeepsUnacknowledged},
		{"TrimWithoutGroupKeepsEntries", testTaskTrimWithoutGroup},
		{"StartLatestSkipsEarlierEntries", testTaskStartLatest},
		{"BacklogAgeGrowsWhileStalled", testTaskBacklogAgeGrows},
		{"ClosedPeerRefuses", testTaskClosedPeerRefuses},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newPeers) })
	}
}

func taskPolicy(maxDeliveries int) events.DeliveryPolicy {
	return events.DeliveryPolicy{MaxDeliveries: maxDeliveries, HandlerTimeout: TaskHandlerTimeout}
}

func taskCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), taskWait)
	t.Cleanup(cancel)
	return ctx
}

func eventually(t *testing.T, ctx context.Context, what string, cond func() bool) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

func subscribeTask(t *testing.T, ctx context.Context, p TaskPeer, topic string, h events.Handler) events.Subscription {
	t.Helper()
	sub, err := p.SubscribeTask(ctx, topic, h)
	if err != nil {
		t.Fatalf("SubscribeTask(%q): %v", topic, err)
	}
	return sub
}

func publishTask(t *testing.T, ctx context.Context, p TaskPeer, topic, data string) {
	t.Helper()
	if err := p.Publish(ctx, newSuiteEvent(topic, data)); err != nil {
		t.Fatalf("Publish(%q): %v", topic, err)
	}
}

func eventData(t *testing.T, e events.Event) string {
	t.Helper()
	var decoded suiteEvent
	if u, ok := e.(events.Unmarshaler); ok {
		if err := u.Unmarshal(&decoded); err == nil {
			return decoded.Data
		}
	}
	if typed, ok := e.(suiteEvent); ok {
		return typed.Data
	}
	return ""
}

func eventID(e events.Event) string {
	if identified, ok := e.(events.Identified); ok {
		return identified.EventID()
	}
	return ""
}

func backlog(t *testing.T, ctx context.Context, p TaskPeer, topic string) events.BacklogStats {
	t.Helper()
	stats, err := p.BacklogStats(ctx, topic)
	if err != nil {
		t.Fatalf("BacklogStats(%q): %v", topic, err)
	}
	return stats
}

func deadLetters(t *testing.T, ctx context.Context, p TaskPeer, topic string) []events.DeadLetter {
	t.Helper()
	dls, err := p.DeadLetters(ctx, topic, 100)
	if err != nil {
		t.Fatalf("DeadLetters(%q): %v", topic, err)
	}
	return dls
}

func waitDeadLetter(t *testing.T, ctx context.Context, p TaskPeer, topic string) events.DeadLetter {
	t.Helper()
	var dls []events.DeadLetter
	eventually(t, ctx, "one dead letter", func() bool {
		dls = deadLetters(t, ctx, p, topic)
		return len(dls) > 0
	})
	if len(dls) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(dls))
	}
	return dls[0]
}

func testTaskInvalidInput(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(0))()
	h := func(context.Context, events.Event) error { return nil }
	for _, topic := range []string{"", "*"} {
		if _, err := p.SubscribeTask(ctx, topic, h); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("SubscribeTask(%q) = %v, want ErrInvalidInput", topic, err)
		}
	}
	if _, err := p.SubscribeTask(ctx, "task.invalid", nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("SubscribeTask(nil handler) = %v, want ErrInvalidInput", err)
	}
	if _, err := p.DeadLetters(ctx, "task.invalid", 0); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("DeadLetters(limit 0) = %v, want ErrInvalidInput", err)
	}
	if _, err := p.Trim(ctx, "*", 0); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("Trim(*) = %v, want ErrInvalidInput", err)
	}
}

func testTaskAcksOnce(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(0))()
	const topic = "task.ack"
	var calls, attempt atomic.Int32
	subscribeTask(t, ctx, p, topic, func(ctx context.Context, _ events.Event) error {
		attempt.Store(int32(events.DeliveryAttempt(ctx)))
		calls.Add(1)
		return nil
	})
	publishTask(t, ctx, p, topic, "once")
	eventually(t, ctx, "delivery", func() bool { return calls.Load() == 1 })
	eventually(t, ctx, "acknowledgment", func() bool {
		s := backlog(t, ctx, p, topic)
		return s.Pending == 0 && s.OldestAge == 0
	})
	time.Sleep(taskSettle)
	if got := calls.Load(); got != 1 {
		t.Fatalf("acknowledged entry delivered %d times, want 1", got)
	}
	if got := attempt.Load(); got != 1 {
		t.Fatalf("DeliveryAttempt = %d, want 1", got)
	}
}

func testTaskRetriesCarryAttempt(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(0))()
	const topic = "task.retry"
	var mu sync.Mutex
	var attempts []int
	subscribeTask(t, ctx, p, topic, func(ctx context.Context, _ events.Event) error {
		n := events.DeliveryAttempt(ctx)
		mu.Lock()
		attempts = append(attempts, n)
		mu.Unlock()
		if n < 3 {
			return errTaskFailure
		}
		return nil
	})
	publishTask(t, ctx, p, topic, "retry")
	eventually(t, ctx, "three attempts and an ack", func() bool {
		mu.Lock()
		n := len(attempts)
		mu.Unlock()
		return n >= 3 && backlog(t, ctx, p, topic).Pending == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(attempts, []int{1, 2, 3}) {
		t.Fatalf("DeliveryAttempt sequence = %v, want [1 2 3]", attempts)
	}
}

func testTaskZeroPolicyNeverDeadLetters(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(0))()
	const topic = "task.unbounded"
	var calls atomic.Int32
	subscribeTask(t, ctx, p, topic, func(context.Context, events.Event) error {
		calls.Add(1)
		return errTaskFailure
	})
	publishTask(t, ctx, p, topic, "forever")
	eventually(t, ctx, "four failed deliveries", func() bool { return calls.Load() >= 4 })
	if dls := deadLetters(t, ctx, p, topic); len(dls) != 0 {
		t.Fatalf("unbounded policy dead-lettered %d entries", len(dls))
	}
	if s := backlog(t, ctx, p, topic); s.Pending != 1 || s.DeadLetters != 0 {
		t.Fatalf("backlog = %+v, want 1 pending and no dead letters", s)
	}
}

func testTaskPanicCountsAsFailure(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	var hooked atomic.Int32
	policy := taskPolicy(2)
	policy.OnDeadLetter = func(context.Context, events.DeadLetter) { hooked.Add(1) }
	p := newPeers(t, policy)()
	const topic = "task.panic"
	subscribeTask(t, ctx, p, topic, func(context.Context, events.Event) error { panic("boom") })
	publishTask(t, ctx, p, topic, "panic")
	dl := waitDeadLetter(t, ctx, p, topic)
	if dl.Deliveries != 2 || !strings.Contains(dl.Error, "panic") {
		t.Fatalf("dead letter = deliveries %d error %q, want 2 and a panic error", dl.Deliveries, dl.Error)
	}
	eventually(t, ctx, "dead-letter hook", func() bool { return hooked.Load() == 1 })
}

func testTaskTimeoutCountsAsFailure(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(1))()
	const topic = "task.timeout"
	subscribeTask(t, ctx, p, topic, func(ctx context.Context, _ events.Event) error {
		<-ctx.Done()
		return nil // a handler that swallows its deadline still failed
	})
	publishTask(t, ctx, p, topic, "slow")
	if dl := waitDeadLetter(t, ctx, p, topic); dl.Deliveries != 1 {
		t.Fatalf("dead letter deliveries = %d, want 1", dl.Deliveries)
	}
}

func testTaskExhaustionOnceAcrossPeers(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	var hooked, calls atomic.Int32
	policy := taskPolicy(3)
	policy.OnDeadLetter = func(context.Context, events.DeadLetter) { hooked.Add(1) }
	newPeer := newPeers(t, policy)
	a, b := newPeer(), newPeer()
	const topic = "task.exhaust"
	fail := func(context.Context, events.Event) error {
		calls.Add(1)
		return errTaskFailure
	}
	subscribeTask(t, ctx, a, topic, fail)
	subscribeTask(t, ctx, b, topic, fail)
	publishTask(t, ctx, a, topic, "poison")
	eventually(t, ctx, "dead-letter hook", func() bool { return hooked.Load() >= 1 })
	time.Sleep(taskSettle)
	if got := hooked.Load(); got != 1 {
		t.Fatalf("dead-letter hook ran %d times, want 1", got)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("deliveries = %d, want 3", got)
	}
	for _, p := range []TaskPeer{a, b} {
		dls := deadLetters(t, ctx, p, topic)
		if len(dls) != 1 {
			t.Fatalf("dead letters = %d, want 1", len(dls))
		}
		dl := dls[0]
		if dl.Deliveries != 3 || dl.Topic != topic || dl.Record == nil || dl.Record.Type != topic || len(dl.Raw) == 0 {
			t.Fatalf("dead letter = %+v", dl)
		}
		if s := backlog(t, ctx, p, topic); s.Pending != 0 || s.OldestAge != 0 || s.DeadLetters != 1 {
			t.Fatalf("backlog after dead-lettering = %+v", s)
		}
	}
}

func testTaskBackoffOrdering(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	policy := taskPolicy(4)
	policy.BackoffBase, policy.BackoffMax = 150*time.Millisecond, 600*time.Millisecond
	p := newPeers(t, policy)()
	const topic = "task.backoff"
	var mu sync.Mutex
	var at []time.Time
	subscribeTask(t, ctx, p, topic, func(context.Context, events.Event) error {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		return errTaskFailure
	})
	publishTask(t, ctx, p, topic, "backoff")
	waitDeadLetter(t, ctx, p, topic)
	mu.Lock()
	defer mu.Unlock()
	if len(at) != 4 {
		t.Fatalf("deliveries = %d, want 4", len(at))
	}
	var previous time.Duration
	for i := 1; i < len(at); i++ {
		gap, floor := at[i].Sub(at[i-1]), policy.RetryDelay(i)
		if gap+timerSlack < floor {
			t.Errorf("retry %d came after %v, want at least %v", i, gap, floor)
		}
		if gap+timerSlack < previous {
			t.Errorf("retry %d gap %v shrank below previous %v", i, gap, previous)
		}
		previous = gap
	}
}

func testTaskRedeliver(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(1))()
	const topic = "task.redeliver"
	var failing atomic.Bool
	failing.Store(true)
	type seen struct {
		attempt int
		id      string
	}
	got := make(chan seen, 4)
	subscribeTask(t, ctx, p, topic, func(ctx context.Context, e events.Event) error {
		got <- seen{events.DeliveryAttempt(ctx), eventID(e)}
		if failing.Load() {
			return errTaskFailure
		}
		return nil
	})
	publishTask(t, ctx, p, topic, "fix-me")
	first := <-got
	dl := waitDeadLetter(t, ctx, p, topic)
	failing.Store(false)
	if err := p.Redeliver(ctx, topic, dl.ID); err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	select {
	case again := <-got:
		if again.attempt != 1 || again.id != first.id || first.id == "" {
			t.Fatalf("redelivery = %+v, want attempt 1 and EventID %q", again, first.id)
		}
	case <-ctx.Done():
		t.Fatal("redelivered entry was not handled")
	}
	eventually(t, ctx, "redelivered ack", func() bool {
		s := backlog(t, ctx, p, topic)
		return s.Pending == 0 && s.OldestAge == 0 && s.DeadLetters == 0
	})
	if err := p.Redeliver(ctx, topic, dl.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second Redeliver = %v, want ErrNotFound", err)
	}
}

func testTaskDiscard(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(1))()
	const topic = "task.discard"
	subscribeTask(t, ctx, p, topic, func(context.Context, events.Event) error { return errTaskFailure })
	publishTask(t, ctx, p, topic, "drop")
	dl := waitDeadLetter(t, ctx, p, topic)
	if err := p.DiscardDeadLetter(ctx, topic, dl.ID); err != nil {
		t.Fatalf("DiscardDeadLetter: %v", err)
	}
	if dls := deadLetters(t, ctx, p, topic); len(dls) != 0 {
		t.Fatalf("dead letters after discard = %d", len(dls))
	}
	if err := p.DiscardDeadLetter(ctx, topic, dl.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second DiscardDeadLetter = %v, want ErrNotFound", err)
	}
}

func testTaskTrimKeepsUnacknowledged(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(0))()
	const topic = "task.trim"
	var mu sync.Mutex
	handled := map[string]int{}
	var healed atomic.Bool
	sub := subscribeTask(t, ctx, p, topic, func(_ context.Context, e events.Event) error {
		data := eventData(t, e)
		mu.Lock()
		handled[data]++
		mu.Unlock()
		if data == "poison" && !healed.Load() {
			return errTaskFailure
		}
		return nil
	})
	count := func(data string) int {
		mu.Lock()
		defer mu.Unlock()
		return handled[data]
	}
	publishTask(t, ctx, p, topic, "done")
	eventually(t, ctx, "first entry acknowledged", func() bool {
		return count("done") == 1 && backlog(t, ctx, p, topic).OldestAge == 0
	})
	publishTask(t, ctx, p, topic, "poison")
	eventually(t, ctx, "poison delivered", func() bool { return count("poison") >= 1 })
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	publishTask(t, ctx, p, topic, "unread")
	if n, err := p.Trim(ctx, topic, time.Hour); err != nil || n != 0 {
		t.Fatalf("Trim(1h) = %d, %v; want nothing that young removed", n, err)
	}
	time.Sleep(5 * time.Millisecond)
	if n, err := p.Trim(ctx, topic, 0); err != nil || n != 1 {
		t.Fatalf("Trim(0) = %d, %v; want only the acknowledged entry removed", n, err)
	}
	healed.Store(true)
	subscribeTask(t, ctx, p, topic, func(ctx context.Context, e events.Event) error {
		data := eventData(t, e)
		mu.Lock()
		handled[data]++
		mu.Unlock()
		return nil
	})
	eventually(t, ctx, "pending and unread entries survive trim", func() bool {
		return count("poison") >= 2 && count("unread") == 1 && backlog(t, ctx, p, topic).OldestAge == 0
	})
}

func testTaskTrimWithoutGroup(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(0))()
	const topic = "task.nogroup"
	publishTask(t, ctx, p, topic, "early")
	time.Sleep(5 * time.Millisecond)
	if n, err := p.Trim(ctx, topic, 0); err != nil || n != 0 {
		t.Fatalf("Trim without a group = %d, %v; want 0", n, err)
	}
	got := make(chan string, 1)
	subscribeTask(t, ctx, p, topic, func(_ context.Context, e events.Event) error {
		got <- eventData(t, e)
		return nil
	})
	select {
	case data := <-got:
		if data != "early" {
			t.Fatalf("delivered %q, want early", data)
		}
	case <-ctx.Done():
		t.Fatal("StartEarliest group did not receive the entry published before it")
	}
}

func testTaskStartLatest(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	policy := taskPolicy(0)
	policy.Start = events.StartLatest
	p := newPeers(t, policy)()
	const topic = "task.latest"
	publishTask(t, ctx, p, topic, "before")
	var mu sync.Mutex
	var seen []string
	subscribeTask(t, ctx, p, topic, func(_ context.Context, e events.Event) error {
		mu.Lock()
		seen = append(seen, eventData(t, e))
		mu.Unlock()
		return nil
	})
	publishTask(t, ctx, p, topic, "after")
	eventually(t, ctx, "the later entry", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(seen, "after")
	})
	time.Sleep(taskSettle)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, []string{"after"}) {
		t.Fatalf("StartLatest delivered %v, want [after]", seen)
	}
}

func testTaskBacklogAgeGrows(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(0))()
	const topic = "task.stalled"
	sub := subscribeTask(t, ctx, p, topic, func(context.Context, events.Event) error { return nil })
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	publishTask(t, ctx, p, topic, "stuck")
	time.Sleep(100 * time.Millisecond)
	first := backlog(t, ctx, p, topic)
	if first.Pending != 0 || first.OldestAge < 100*time.Millisecond-timerSlack {
		t.Fatalf("stalled backlog = %+v, want unread age ≥ 100ms and none pending", first)
	}
	time.Sleep(100 * time.Millisecond)
	if second := backlog(t, ctx, p, topic); second.OldestAge <= first.OldestAge {
		t.Fatalf("backlog age did not grow: %v then %v", first.OldestAge, second.OldestAge)
	}
}

func testTaskClosedPeerRefuses(t *testing.T, newPeers func(*testing.T, events.DeliveryPolicy) func() TaskPeer) {
	ctx := taskCtx(t)
	p := newPeers(t, taskPolicy(0))()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	h := func(context.Context, events.Event) error { return nil }
	if _, err := p.SubscribeTask(ctx, "task.closed", h); !errors.Is(err, events.ErrClosed) {
		t.Fatalf("SubscribeTask after Close = %v, want ErrClosed", err)
	}
	if err := p.Publish(ctx, newSuiteEvent("task.closed", "x")); !errors.Is(err, events.ErrClosed) {
		t.Fatalf("Publish after Close = %v, want ErrClosed", err)
	}
}
