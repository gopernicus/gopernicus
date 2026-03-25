package async

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPool_BasicExecution(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(10))
	defer pool.Close(context.Background())

	var executed atomic.Bool
	if !pool.Go(func() { executed.Store(true) }) {
		t.Fatal("expected task to start")
	}

	pool.Wait()

	if !executed.Load() {
		t.Fatal("expected task to be executed")
	}

	stats := pool.Stats()
	if stats.Total != 1 {
		t.Errorf("Total = %d, want 1", stats.Total)
	}
	if stats.Active != 0 {
		t.Errorf("Active = %d, want 0", stats.Active)
	}
}

func TestPool_ConcurrencyLimit(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(3))
	defer pool.Close(context.Background())

	var maxSeen atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		pool.Go(func() {
			defer wg.Done()
			current := pool.Stats().Active
			for {
				old := maxSeen.Load()
				if current <= old || maxSeen.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		})
	}

	wg.Wait()
	pool.Wait()

	if maxSeen.Load() > 3 {
		t.Errorf("exceeded max concurrency: saw %d, max is 3", maxSeen.Load())
	}
}

func TestPool_DropOnFull(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(1), WithDropOnFull(true))
	defer pool.Close(context.Background())

	blocker := make(chan struct{})
	pool.Go(func() { <-blocker })

	var dropped int
	for i := 0; i < 5; i++ {
		if !pool.Go(func() {}) {
			dropped++
		}
	}

	close(blocker)
	pool.Wait()

	if dropped != 5 {
		t.Errorf("dropped = %d, want 5", dropped)
	}
	if stats := pool.Stats(); stats.Dropped != 5 {
		t.Errorf("Stats.Dropped = %d, want 5", stats.Dropped)
	}
}

func TestPool_PanicRecovery(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(10))
	defer pool.Close(context.Background())

	pool.Go(func() { panic("test panic") })
	pool.Wait()

	if stats := pool.Stats(); stats.Panics != 1 {
		t.Errorf("Panics = %d, want 1", stats.Panics)
	}
}

func TestPool_MultiplePanics(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(10))
	defer pool.Close(context.Background())

	for i := 0; i < 5; i++ {
		pool.Go(func() { panic("test panic") })
	}
	pool.Wait()

	if stats := pool.Stats(); stats.Panics != 5 {
		t.Errorf("Panics = %d, want 5", stats.Panics)
	}
}

func TestPool_GoContext_Cancellation(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(1))
	defer pool.Close(context.Background())

	blocker := make(chan struct{})
	pool.Go(func() { <-blocker })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := pool.GoContext(ctx, func() {
		t.Error("task should not have executed")
	})

	close(blocker)
	pool.Wait()

	if started {
		t.Error("expected GoContext to return false for cancelled context")
	}
}

func TestPool_GoContext_Success(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(10))
	defer pool.Close(context.Background())

	var executed atomic.Bool
	if !pool.GoContext(context.Background(), func() { executed.Store(true) }) {
		t.Fatal("expected task to start")
	}

	pool.Wait()

	if !executed.Load() {
		t.Fatal("expected task to execute")
	}
}

func TestPool_Close_RejectsNewTasks(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(10))
	pool.Close(context.Background())

	if pool.Go(func() { t.Error("should not execute") }) {
		t.Error("expected Go() to return false after Close()")
	}
}

func TestPool_Close_WaitsForInFlight(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(10), WithShutdownTimeout(5*time.Second))

	var completed atomic.Bool
	pool.Go(func() {
		time.Sleep(100 * time.Millisecond)
		completed.Store(true)
	})

	pool.Close(context.Background())

	if !completed.Load() {
		t.Error("Close() should wait for in-flight tasks")
	}
}

func TestPool_Stats(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(2), WithDropOnFull(true))
	defer pool.Close(context.Background())

	blocker := make(chan struct{})
	pool.Go(func() { <-blocker })
	pool.Go(func() { <-blocker })

	pool.Go(func() {})
	pool.Go(func() {})

	stats := pool.Stats()
	if stats.Active != 2 {
		t.Errorf("Active = %d, want 2", stats.Active)
	}
	if stats.Dropped != 2 {
		t.Errorf("Dropped = %d, want 2", stats.Dropped)
	}

	close(blocker)
	pool.Wait()

	if stats := pool.Stats(); stats.Active != 0 {
		t.Errorf("Active after wait = %d, want 0", stats.Active)
	}
}

func TestPool_IOPreset(t *testing.T) {
	pool := NewPool(append(IOPreset(), WithLogger(quietLogger()))...)
	defer pool.Close(context.Background())

	var executed atomic.Bool
	pool.Go(func() { executed.Store(true) })
	pool.Wait()

	if !executed.Load() {
		t.Error("IO pool should execute tasks")
	}
}

func TestPool_CPUPreset(t *testing.T) {
	pool := NewPool(append(CPUPreset(), WithLogger(quietLogger()))...)
	defer pool.Close(context.Background())

	var executed atomic.Bool
	pool.Go(func() { executed.Store(true) })
	pool.Wait()

	if !executed.Load() {
		t.Error("CPU pool should execute tasks")
	}
}

func TestPool_Defaults(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()))
	defer pool.Close(context.Background())

	if pool.config.maxConcurrency != 100 {
		t.Errorf("default maxConcurrency = %d, want 100", pool.config.maxConcurrency)
	}
	if pool.config.dropOnFull {
		t.Error("default dropOnFull should be false")
	}
	if pool.config.shutdownTimeout != 30*time.Second {
		t.Errorf("default shutdownTimeout = %v, want 30s", pool.config.shutdownTimeout)
	}
}
