package workers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- shared helpers ---

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// captureHandler is a mutex-guarded slog.Handler that retains a clone of every
// record it handles. The pool logs from several goroutines, so a shared
// bytes.Buffer would race; tests read the records back after Run has returned.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelDebug
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(_ string) slog.Handler { return h }

// captured returns a snapshot of the records seen so far.
func (h *captureHandler) captured() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

// capturingLogger returns a logger enabled at Debug and the handler holding its
// records.
func capturingLogger() (*slog.Logger, *captureHandler) {
	h := &captureHandler{}
	return slog.New(h), h
}

func recordsWithMessage(records []slog.Record, msg string) []slog.Record {
	var out []slog.Record
	for _, r := range records {
		if r.Message == msg {
			out = append(out, r)
		}
	}
	return out
}

func attrValue(r slog.Record, key string) (slog.Value, bool) {
	var (
		value slog.Value
		found bool
	)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			value, found = a.Value, true
			return false
		}
		return true
	})
	return value, found
}

// testPool builds a fast pool: 1 worker, 10ms poll, 50ms idle, silent logger,
// plus any extra options (which override the defaults above).
func testPool(work WorkFunc, extra ...PoolOption) *Pool {
	opts := []PoolOption{
		WithName("test"),
		WithWorkerCount(1),
		WithPollInterval(10 * time.Millisecond),
		WithIdleInterval(50 * time.Millisecond),
		WithLogger(silentLogger()),
	}
	opts = append(opts, extra...)
	return NewPool(work, opts...)
}

// runPool starts p.Run in a goroutine and returns a channel that receives once
// Run has returned.
func runPool(p *Pool, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	return done
}

func assertReturns(t *testing.T, done <-chan error, within time.Duration, msg string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatal(msg)
	}
}

// --- pool tests ---

func TestPool_CallsWorkFunc(t *testing.T) {
	var calls atomic.Int64
	work := func(ctx context.Context) error {
		if calls.Add(1) >= 5 {
			return ErrNoWork
		}
		return nil
	}

	pool := testPool(work)
	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	for calls.Load() < 5 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return after cancel")

	if got := calls.Load(); got < 5 {
		t.Errorf("expected at least 5 calls, got %d", got)
	}
}

func TestPool_StopsOnContextCancel(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }
	pool := testPool(work)

	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	time.Sleep(30 * time.Millisecond)
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not stop after context cancel")
}

func TestPool_WorkerIDInContext(t *testing.T) {
	var mu sync.Mutex
	var captured string
	var done atomic.Bool

	work := func(ctx context.Context) error {
		if !done.Load() {
			mu.Lock()
			captured = WorkerIDFromContext(ctx)
			mu.Unlock()
			done.Store(true)
		}
		return ErrNoWork
	}

	pool := testPool(work)
	ctx, cancel := context.WithCancel(context.Background())
	ch := runPool(pool, ctx)

	for !done.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	assertReturns(t, ch, 2*time.Second, "pool did not return")

	mu.Lock()
	got := captured
	mu.Unlock()
	if got != "test-worker-1" {
		t.Errorf("expected worker ID 'test-worker-1', got %q", got)
	}
}

func TestPool_Defaults(t *testing.T) {
	pool := NewPool(func(ctx context.Context) error { return ErrNoWork }, WithLogger(silentLogger()))
	if pool.workerCount != 1 {
		t.Errorf("expected default workerCount 1, got %d", pool.workerCount)
	}
	if pool.pollInterval != 5*time.Second {
		t.Errorf("expected default pollInterval 5s, got %v", pool.pollInterval)
	}
	if pool.idleInterval != 30*time.Second {
		t.Errorf("expected default idleInterval 30s, got %v", pool.idleInterval)
	}
}

func TestPool_ErrWorkerShutdown(t *testing.T) {
	work := func(ctx context.Context) error { return ErrWorkerShutdown }
	pool := testPool(work)
	done := runPool(pool, context.Background())
	assertReturns(t, done, 2*time.Second, "pool did not stop after ErrWorkerShutdown")
}

func TestPool_ErrPoolShutdown(t *testing.T) {
	work := func(ctx context.Context) error { return ErrPoolShutdown }
	pool := testPool(work, WithWorkerCount(3))
	done := runPool(pool, context.Background())

	select {
	case err := <-pool.Errors():
		if !errors.Is(err, ErrPoolShutdown) {
			t.Errorf("expected ErrPoolShutdown on Errors(), got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected an error on Errors()")
	}

	// One worker signalling ErrPoolShutdown cancels the whole pool.
	assertReturns(t, done, 2*time.Second, "pool did not stop after ErrPoolShutdown")
}

func TestPool_PanicRecoveryContained(t *testing.T) {
	var panicked atomic.Bool
	var after atomic.Int64

	work := func(ctx context.Context) error {
		if panicked.CompareAndSwap(false, true) {
			panic("boom")
		}
		after.Add(1)
		return ErrNoWork
	}

	pool := testPool(work)
	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	// The panic is surfaced on Errors().
	select {
	case err := <-pool.Errors():
		if err == nil {
			t.Fatal("expected a non-nil panic error on Errors()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the recovered panic on Errors()")
	}

	// The worker keeps going after the panic.
	for after.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if got := pool.Stats().Panics; got < 1 {
		t.Errorf("expected at least 1 recovered panic in stats, got %d", got)
	}
}

func TestPool_MiddlewareChainOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	var done atomic.Bool

	work := func(ctx context.Context) error {
		if done.CompareAndSwap(false, true) {
			return nil
		}
		return ErrNoWork
	}

	mw := func(name string) Middleware {
		return func(next WorkFunc) WorkFunc {
			return func(ctx context.Context) error {
				mu.Lock()
				order = append(order, name+"-before")
				mu.Unlock()
				err := next(ctx)
				mu.Lock()
				order = append(order, name+"-after")
				mu.Unlock()
				return err
			}
		}
	}

	pool := testPool(work, WithMiddleware(mw("outer"), mw("inner")))
	ctx, cancel := context.WithCancel(context.Background())
	ch := runPool(pool, ctx)

	for !done.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	cancel()
	assertReturns(t, ch, 2*time.Second, "pool did not return")

	mu.Lock()
	defer mu.Unlock()
	want := []string{"outer-before", "inner-before", "inner-after", "outer-after"}
	if len(order) < len(want) {
		t.Fatalf("expected at least %d middleware entries, got %v", len(want), order)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("order[%d] = %q, want %q", i, order[i], w)
		}
	}
}

func TestPool_IdleBackoffEngagesOnNoWork(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time

	work := func(ctx context.Context) error {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		return ErrNoWork
	}

	// Poll is tiny; idle is large. If idle backoff engages, the gap between
	// consecutive iterations jumps to ~idleInterval after the first ErrNoWork.
	pool := testPool(work, WithPollInterval(5*time.Millisecond), WithIdleInterval(120*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	for {
		mu.Lock()
		n := len(times)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	mu.Lock()
	gap := times[2].Sub(times[1])
	mu.Unlock()
	if gap < 80*time.Millisecond {
		t.Errorf("expected idle backoff (~120ms) between iterations, got %v", gap)
	}
}

func TestPool_WakeChannelCutsIdleLatency(t *testing.T) {
	var noWork atomic.Int64
	claimed := make(chan struct{}, 1)
	returnWork := atomic.Bool{}

	work := func(ctx context.Context) error {
		if returnWork.Load() {
			select {
			case claimed <- struct{}{}:
			default:
			}
			returnWork.Store(false)
			return nil
		}
		noWork.Add(1)
		return ErrNoWork
	}

	wake := make(chan struct{}, 1)
	// Huge poll and idle intervals: without a wake, the next iteration would be
	// seconds away.
	pool := testPool(work,
		WithPollInterval(10*time.Second),
		WithIdleInterval(10*time.Second),
		WithWakeChannel(wake),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	// Let the worker take its first iteration and drop into the long idle wait.
	for noWork.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)

	returnWork.Store(true)
	wake <- struct{}{}

	select {
	case <-claimed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("wake did not cut idle latency: no prompt claim within 200ms")
	}

	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")
}

func TestPool_WakeChannelNilIsSafe(t *testing.T) {
	var calls atomic.Int64
	work := func(ctx context.Context) error {
		calls.Add(1)
		return ErrNoWork
	}

	pool := testPool(work, WithWakeChannel(nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	<-runPool(pool, ctx)

	if calls.Load() == 0 {
		t.Error("expected the pool to run with a nil wake channel")
	}
}

func TestPool_WakeChannelCancelTakesPrecedence(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }
	wake := make(chan struct{}, 1)
	pool := testPool(work, WithWakeChannel(wake))

	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	time.Sleep(30 * time.Millisecond)
	cancel()
	wake <- struct{}{}

	assertReturns(t, done, 2*time.Second, "pool did not exit after cancel despite a wake signal")
}

func TestPool_DrainsInFlightOnCancel(t *testing.T) {
	before := runtime.NumGoroutine()

	started := make(chan struct{})
	var once sync.Once
	var finished atomic.Bool

	work := func(ctx context.Context) error {
		once.Do(func() { close(started) })
		// A slow in-flight iteration: it must run to completion after cancel.
		time.Sleep(120 * time.Millisecond)
		finished.Store(true)
		return ErrNoWork
	}

	pool := testPool(work)
	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	<-started
	cancel()
	assertReturns(t, done, 2*time.Second, "Run did not return after cancel")

	if !finished.Load() {
		t.Error("in-flight WorkFunc call did not finish before drain returned")
	}

	// No goroutine leak: workers exit before Run returns.
	assertNoLeak(t, before)
}

func TestPool_MultipleWorkersRun(t *testing.T) {
	var calls atomic.Int64
	work := func(ctx context.Context) error {
		if calls.Add(1) > 20 {
			return ErrNoWork
		}
		time.Sleep(2 * time.Millisecond)
		return nil
	}

	pool := testPool(work, WithWorkerCount(4))
	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	for calls.Load() < 20 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if calls.Load() < 20 {
		t.Errorf("expected at least 20 calls, got %d", calls.Load())
	}
}

func TestPool_StatsActiveWorkersZeroAfterStop(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }
	pool := testPool(work, WithWorkerCount(3))

	if got := pool.Stats().ActiveWorkers; got != 0 {
		t.Errorf("expected 0 active workers before Run, got %d", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if got := pool.Stats().ActiveWorkers; got != 0 {
		t.Errorf("expected 0 active workers after stop, got %d", got)
	}
}

func TestPool_LogsNoWorkIterationAtDebug(t *testing.T) {
	logger, capture := capturingLogger()
	work := func(ctx context.Context) error { return ErrNoWork }

	pool := testPool(work, WithIdleInterval(10*time.Millisecond), WithLogger(logger))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	<-runPool(pool, ctx)

	noWork := recordsWithMessage(capture.captured(), "iteration: no work")
	if len(noWork) == 0 {
		t.Fatal("expected at least one \"iteration: no work\" record")
	}
	for i, r := range noWork {
		if r.Level != slog.LevelDebug {
			t.Errorf("record %d: level = %v, want %v", i, r.Level, slog.LevelDebug)
		}
		if v, ok := attrValue(r, "pool"); !ok || v.String() != "test" {
			t.Errorf("record %d: pool attr = %v (present %v), want \"test\"", i, v, ok)
		}
		if v, ok := attrValue(r, "worker_id"); !ok || v.String() != "test-worker-1" {
			t.Errorf("record %d: worker_id attr = %v (present %v), want \"test-worker-1\"", i, v, ok)
		}
	}
}

func TestPool_NoWorkLineAbsentWhenWorkSucceeds(t *testing.T) {
	logger, capture := capturingLogger()
	work := func(ctx context.Context) error { return nil }

	pool := testPool(work, WithPollInterval(5*time.Millisecond), WithLogger(logger))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	<-runPool(pool, ctx)

	if got := len(recordsWithMessage(capture.captured(), "iteration: no work")); got != 0 {
		t.Errorf("expected no \"iteration: no work\" records from a succeeding WorkFunc, got %d", got)
	}
}

// --- heartbeat tests ---

func beatCount(capture *captureHandler) int {
	return len(recordsWithMessage(capture.captured(), "pool alive"))
}

func waitForBeats(t *testing.T, capture *captureHandler, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if beatCount(capture) >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("expected at least %d \"pool alive\" records within %v, got %d", want, within, beatCount(capture))
}

func intAttr(t *testing.T, r slog.Record, key string) int64 {
	t.Helper()
	v, ok := attrValue(r, key)
	if !ok {
		t.Fatalf("record %q is missing attr %q", r.Message, key)
	}
	if v.Kind() != slog.KindInt64 {
		t.Fatalf("record %q attr %q kind = %v, want Int64", r.Message, key, v.Kind())
	}
	return v.Int64()
}

// assertBeat checks the shape every beat must have and returns its deltas.
func assertBeat(t *testing.T, r slog.Record) (iterations, claims, errs int64) {
	t.Helper()
	if r.Level != slog.LevelInfo {
		t.Errorf("beat level = %v, want %v", r.Level, slog.LevelInfo)
	}
	if v, ok := attrValue(r, "pool"); !ok || v.String() != "test" {
		t.Errorf("beat pool attr = %v (present %v), want \"test\"", v, ok)
	}
	if got := intAttr(t, r, "active_workers"); got < 0 {
		t.Errorf("beat active_workers = %d, want >= 0", got)
	}
	iterations = intAttr(t, r, "iterations")
	claims = intAttr(t, r, "claims")
	errs = intAttr(t, r, "errors")
	if iterations < 0 || claims < 0 || errs < 0 {
		t.Errorf("beat deltas must be non-negative, got iterations=%d claims=%d errors=%d", iterations, claims, errs)
	}
	return iterations, claims, errs
}

func TestPool_HeartbeatOffByDefault(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }

	for _, tc := range []struct {
		name  string
		extra []PoolOption
	}{
		{name: "default"},
		{name: "explicit zero", extra: []PoolOption{WithHeartbeat(0)}},
		{name: "negative", extra: []PoolOption{WithHeartbeat(-time.Second)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, capture := capturingLogger()
			opts := append([]PoolOption{WithIdleInterval(10 * time.Millisecond)}, tc.extra...)
			opts = append(opts, WithLogger(logger))

			pool := testPool(work, opts...)
			if pool.heartbeat > 0 {
				t.Fatalf("expected the heartbeat to be off, got %v", pool.heartbeat)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
			defer cancel()
			<-runPool(pool, ctx)

			if got := beatCount(capture); got != 0 {
				t.Errorf("expected no \"pool alive\" records with the heartbeat off, got %d", got)
			}
		})
	}
}

func TestPool_HeartbeatBeatsWhileIdle(t *testing.T) {
	logger, capture := capturingLogger()
	work := func(ctx context.Context) error { return ErrNoWork }

	pool := testPool(work,
		WithIdleInterval(10*time.Millisecond),
		WithHeartbeat(20*time.Millisecond),
		WithLogger(logger),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	<-runPool(pool, ctx)

	beats := recordsWithMessage(capture.captured(), "pool alive")
	if len(beats) == 0 {
		t.Fatal("expected at least one \"pool alive\" record from an idle pool")
	}
	for _, r := range beats {
		if _, claims, _ := assertBeat(t, r); claims != 0 {
			t.Errorf("an always-ErrNoWork pool reported claims = %d, want 0", claims)
		}
	}
}

func TestPool_HeartbeatCountsErrorsNotClaims(t *testing.T) {
	logger, capture := capturingLogger()
	work := func(ctx context.Context) error { return errors.New("boom") }

	pool := testPool(work,
		WithPollInterval(5*time.Millisecond),
		WithHeartbeat(20*time.Millisecond),
		WithLogger(logger),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	<-runPool(pool, ctx)

	beats := recordsWithMessage(capture.captured(), "pool alive")
	if len(beats) == 0 {
		t.Fatal("expected at least one \"pool alive\" record")
	}

	var totalClaims, totalErrors int64
	for _, r := range beats {
		_, claims, errs := assertBeat(t, r)
		totalClaims += claims
		totalErrors += errs
	}
	if totalClaims != 0 {
		t.Errorf("a failing WorkFunc reported %d claims, want 0", totalClaims)
	}
	if totalErrors == 0 {
		t.Error("a failing WorkFunc reported no errors across any beat")
	}
}

func TestPool_HeartbeatBaselineCapturesStartupClaim(t *testing.T) {
	logger, capture := capturingLogger()

	var once atomic.Bool
	work := func(ctx context.Context) error {
		if once.CompareAndSwap(false, true) {
			return nil
		}
		return ErrNoWork
	}

	pool := testPool(work,
		WithPollInterval(5*time.Millisecond),
		WithIdleInterval(10*time.Millisecond),
		WithHeartbeat(20*time.Millisecond),
		WithLogger(logger),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	<-runPool(pool, ctx)

	beats := recordsWithMessage(capture.captured(), "pool alive")
	if len(beats) == 0 {
		t.Fatal("expected at least one \"pool alive\" record")
	}

	var totalClaims int64
	for _, r := range beats {
		_, claims, _ := assertBeat(t, r)
		totalClaims += claims
	}
	// The baseline is read synchronously before the workers launch, so the one
	// successful iteration cannot be lost to scheduling before the first beat.
	if totalClaims != 1 {
		t.Errorf("total claims across beats = %d, want 1", totalClaims)
	}
}

// TestPool_HeartbeatDoesNotDeadlockOnWorkerShutdown is the decision-5
// regression: every worker exits via ErrWorkerShutdown, so ctx is never
// cancelled from outside. A heartbeat that joined p.wg would tick forever with
// Run blocked on Wait.
func TestPool_HeartbeatDoesNotDeadlockOnWorkerShutdown(t *testing.T) {
	work := func(ctx context.Context) error { return ErrWorkerShutdown }

	pool := testPool(work,
		WithWorkerCount(3),
		WithHeartbeat(20*time.Millisecond),
		WithLogger(silentLogger()),
	)
	done := runPool(pool, context.Background())
	assertReturns(t, done, 2*time.Second, "Run did not return with the heartbeat enabled and every worker shut down")
}

// TestPool_HeartbeatSilentDuringDrain is the decision-6 regression: once ctx is
// cancelled the heartbeat stops, even while an in-flight iteration keeps the
// drain open past several beat intervals.
func TestPool_HeartbeatSilentDuringDrain(t *testing.T) {
	const heartbeat = 50 * time.Millisecond

	logger, capture := capturingLogger()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	work := func(ctx context.Context) error {
		once.Do(func() { close(started) })
		<-release
		return ErrNoWork
	}

	pool := testPool(work, WithHeartbeat(heartbeat), WithLogger(logger))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runPool(pool, ctx)

	<-started
	// Cancel right after a beat lands, so the next tick is still ~a full
	// interval away and cancellation is the only reason no further beat fires.
	waitForBeats(t, capture, 1, 2*time.Second)
	atCancel := beatCount(capture)
	cancel()

	// Hold the drain open across several intervals' worth of ticks.
	time.Sleep(4 * heartbeat)
	if got := beatCount(capture); got != atCancel {
		t.Errorf("heartbeat beat during drain: %d records at cancel, %d after", atCancel, got)
	}

	close(release)
	assertReturns(t, done, 2*time.Second, "Run did not return after the in-flight iteration finished")

	records := capture.captured()
	if got := beatCount(capture); got != atCancel {
		t.Errorf("heartbeat beat after cancel: %d records at cancel, %d once Run returned", atCancel, got)
	}
	if last := records[len(records)-1]; last.Message != "worker pool stopped" {
		t.Errorf("last record = %q, want \"worker pool stopped\"", last.Message)
	}
}

func assertNoLeak(t *testing.T, before int) {
	t.Helper()
	// Allow scheduler settle; goroutine teardown is asynchronous.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutine leak: started with %d, still %d after Run returned", before, runtime.NumGoroutine())
}
