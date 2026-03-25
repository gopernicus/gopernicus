package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Test helpers ---

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func defaultPoolOpts() Options {
	return Options{
		Name:         "test",
		WorkerCount:  1,
		PollInterval: 10 * time.Millisecond,
		IdleInterval: 50 * time.Millisecond,
	}
}

// --- Pool tests ---

func TestPool_CallsWorkFunc(t *testing.T) {
	var calls atomic.Int64

	work := func(ctx context.Context) error {
		if calls.Add(1) >= 5 {
			return ErrNoWork
		}
		return nil
	}

	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		for calls.Load() < 5 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if got := calls.Load(); got < 5 {
		t.Errorf("expected at least 5 calls, got %d", got)
	}
}

func TestPool_StopsOnContextCancel(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }

	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		pool.Start(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not stop after context cancel")
	}
}

func TestPool_StopsOnStopCall(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }

	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	done := make(chan struct{})
	go func() {
		pool.Start(context.Background())
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	pool.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not stop after Stop()")
	}
}

func TestPool_ErrWorkerShutdown(t *testing.T) {
	work := func(ctx context.Context) error {
		return ErrWorkerShutdown
	}

	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	done := make(chan struct{})
	go func() {
		pool.Start(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not stop after ErrWorkerShutdown")
	}
}

func TestPool_ErrPoolShutdown(t *testing.T) {
	work := func(ctx context.Context) error {
		return ErrPoolShutdown
	}

	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	done := make(chan struct{})
	go func() {
		pool.Start(context.Background())
		close(done)
	}()

	select {
	case err := <-pool.Errors():
		if !errors.Is(err, ErrPoolShutdown) {
			t.Errorf("expected ErrPoolShutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected error on errors channel")
	}

	<-done
}

func TestPool_PanicRecovery(t *testing.T) {
	var panicked atomic.Bool
	work := func(ctx context.Context) error {
		if !panicked.Load() {
			panicked.Store(true)
			panic("test panic")
		}
		return ErrNoWork
	}

	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(200 * time.Millisecond)
		pool.Stop()
	}()

	pool.Start(ctx)

	stats := pool.Stats()
	if stats.Panics < 1 {
		t.Errorf("expected at least 1 panic in stats, got %d", stats.Panics)
	}
}

func TestPool_WorkerIDInContext(t *testing.T) {
	var capturedID string
	var mu sync.Mutex
	var done atomic.Bool

	work := func(ctx context.Context) error {
		if !done.Load() {
			mu.Lock()
			capturedID = GetWorkerID(ctx)
			mu.Unlock()
			done.Store(true)
		}
		return ErrNoWork
	}

	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		for !done.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	mu.Lock()
	id := capturedID
	mu.Unlock()

	if id != "test-worker-1" {
		t.Errorf("expected worker ID 'test-worker-1', got %q", id)
	}
}

func TestPool_MiddlewareChain(t *testing.T) {
	var order []string
	var mu sync.Mutex
	var done atomic.Bool

	work := func(ctx context.Context) error {
		if !done.Load() {
			done.Store(true)
			return nil
		}
		return ErrNoWork
	}

	makeMiddleware := func(name string) Middleware {
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

	pool := NewPool(work, defaultPoolOpts(),
		WithLogger(silentLogger()),
		WithMiddleware(makeMiddleware("outer"), makeMiddleware("inner")),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		for !done.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		pool.Stop()
	}()

	pool.Start(ctx)

	mu.Lock()
	defer mu.Unlock()

	expected := []string{"outer-before", "inner-before", "inner-after", "outer-after"}
	if len(order) < len(expected) {
		t.Fatalf("expected at least %d middleware calls, got %d: %v", len(expected), len(order), order)
	}
	for i, exp := range expected {
		if order[i] != exp {
			t.Errorf("middleware order[%d] = %q, want %q", i, order[i], exp)
		}
	}
}

func TestPool_Defaults(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }
	pool := NewPool(work, Options{})

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

func TestPool_MultipleWorkers(t *testing.T) {
	var calls atomic.Int64
	target := int64(10)

	work := func(ctx context.Context) error {
		if calls.Add(1) > target {
			return ErrNoWork
		}
		time.Sleep(5 * time.Millisecond)
		return nil
	}

	opts := defaultPoolOpts()
	opts.WorkerCount = 3
	pool := NewPool(work, opts, WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for calls.Load() < target {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if calls.Load() < target {
		t.Errorf("expected at least %d calls, got %d", target, calls.Load())
	}
}

func TestPool_AdaptivePolling(t *testing.T) {
	var noWorkSent atomic.Bool

	work := func(ctx context.Context) error {
		if !noWorkSent.Load() {
			noWorkSent.Store(true)
			return ErrNoWork
		}
		return nil
	}

	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(300 * time.Millisecond)
		pool.Stop()
	}()

	pool.Start(ctx)

	if !noWorkSent.Load() {
		t.Error("expected ErrNoWork to have been sent")
	}
}

func TestPool_StopIsIdempotent(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }
	pool := NewPool(work, defaultPoolOpts(), WithLogger(silentLogger()))

	done := make(chan struct{})
	go func() {
		pool.Start(context.Background())
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	pool.Stop()
	pool.Stop() // Should not panic

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not stop")
	}
}

func TestPool_Stats(t *testing.T) {
	work := func(ctx context.Context) error { return ErrNoWork }

	opts := defaultPoolOpts()
	opts.WorkerCount = 3
	pool := NewPool(work, opts, WithLogger(silentLogger()))

	stats := pool.Stats()
	if stats.ActiveWorkers != 0 {
		t.Errorf("expected 0 active workers before start, got %d", stats.ActiveWorkers)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(200 * time.Millisecond)
		pool.Stop()
	}()

	_ = fmt.Sprintf("") // use fmt
	pool.Start(ctx)

	stats = pool.Stats()
	if stats.ActiveWorkers != 0 {
		t.Errorf("expected 0 active workers after stop, got %d", stats.ActiveWorkers)
	}
}
