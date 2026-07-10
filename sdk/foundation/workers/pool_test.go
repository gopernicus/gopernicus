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
