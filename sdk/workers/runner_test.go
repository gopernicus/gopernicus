package workers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Test helpers for Runner ---

type mockJob struct {
	id         string
	status     string
	retryCount int
}

func (j mockJob) GetID() string       { return j.id }
func (j mockJob) GetStatus() string   { return j.status }
func (j mockJob) GetRetryCount() int  { return j.retryCount }

type mockJobStore struct {
	checkoutFn func(ctx context.Context, workerID string, now time.Time) (mockJob, error)
	completeFn func(ctx context.Context, jobID string, now time.Time) error
	failFn     func(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error
}

func (s *mockJobStore) Checkout(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
	if s.checkoutFn != nil {
		return s.checkoutFn(ctx, workerID, now)
	}
	return mockJob{}, ErrNoWork
}

func (s *mockJobStore) Complete(ctx context.Context, jobID string, now time.Time) error {
	if s.completeFn != nil {
		return s.completeFn(ctx, jobID, now)
	}
	return nil
}

func (s *mockJobStore) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	if s.failFn != nil {
		return s.failFn(ctx, jobID, now, reason, maxAttempts)
	}
	return nil
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// --- Runner tests ---

func TestRunner_BasicLifecycle(t *testing.T) {
	var completed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 3 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "job-1", status: "pending"}, nil
		},
		completeFn: func(ctx context.Context, jobID string, now time.Time) error {
			completed.Add(1)
			return nil
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		job.status = "done"
		return job, nil
	}

	runner := NewRunner(store, process, silentLogger(), WithMaxAttempts(1))

	// Run via pool
	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for completed.Load() < 3 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if completed.Load() != 3 {
		t.Errorf("expected 3 completed, got %d", completed.Load())
	}
}

func TestRunner_ProcessFailure(t *testing.T) {
	var failed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 1 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "job-fail", status: "pending"}, nil
		},
		failFn: func(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
			failed.Add(1)
			return nil
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		return job, errors.New("process error")
	}

	runner := NewRunner(store, process, silentLogger(), WithMaxAttempts(1))
	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for failed.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if failed.Load() != 1 {
		t.Errorf("expected 1 failed, got %d", failed.Load())
	}
}

func TestRunner_RetryLogic(t *testing.T) {
	var attempts atomic.Int64
	var completed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 1 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "job-retry", status: "pending"}, nil
		},
		completeFn: func(ctx context.Context, jobID string, now time.Time) error {
			completed.Add(1)
			return nil
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		n := attempts.Add(1)
		if n < 3 {
			return job, errors.New("transient error")
		}
		return job, nil
	}

	runner := NewRunner(store, process, silentLogger(), WithMaxAttempts(3))
	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go func() {
		for completed.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
	if completed.Load() != 1 {
		t.Errorf("expected 1 completed, got %d", completed.Load())
	}
}

func TestRunner_RetryExhaustion(t *testing.T) {
	var failed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 1 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "job-exhaust", status: "pending"}, nil
		},
		failFn: func(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
			failed.Add(1)
			return nil
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		return job, errors.New("persistent error")
	}

	runner := NewRunner(store, process, silentLogger(), WithMaxAttempts(3))
	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go func() {
		for failed.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if failed.Load() != 1 {
		t.Errorf("expected 1 failed, got %d", failed.Load())
	}
}

func TestRunner_PanicRecovery(t *testing.T) {
	var failed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 1 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "job-panic", status: "pending"}, nil
		},
		failFn: func(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
			failed.Add(1)
			return nil
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		panic("test panic in process")
	}

	runner := NewRunner(store, process, silentLogger(), WithMaxAttempts(1))
	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for failed.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if failed.Load() != 1 {
		t.Errorf("expected 1 failed from panic, got %d", failed.Load())
	}
}

func TestRunner_PreProcessHooks(t *testing.T) {
	var hookCalled atomic.Bool
	var completed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 1 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "job-hook", status: "pending"}, nil
		},
		completeFn: func(ctx context.Context, jobID string, now time.Time) error {
			completed.Add(1)
			return nil
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		return job, nil
	}

	runner := NewRunner(store, process, silentLogger(), WithMaxAttempts(1))
	runner.AddPreProcessHooks(func(ctx context.Context, job mockJob) error {
		hookCalled.Store(true)
		return nil
	})

	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for completed.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if !hookCalled.Load() {
		t.Error("pre-process hook was not called")
	}
}

func TestRunner_PostProcessHooks(t *testing.T) {
	var hookErr error
	var hookMu sync.Mutex
	var hookCalled atomic.Bool
	var completed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 1 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "job-hook", status: "pending"}, nil
		},
		completeFn: func(ctx context.Context, jobID string, now time.Time) error {
			completed.Add(1)
			return nil
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		return job, nil
	}

	runner := NewRunner(store, process, silentLogger(), WithMaxAttempts(1))
	runner.AddPostProcessHooks(func(ctx context.Context, job mockJob, err error) error {
		hookMu.Lock()
		hookErr = err
		hookMu.Unlock()
		hookCalled.Store(true)
		return nil
	})

	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for completed.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if !hookCalled.Load() {
		t.Error("post-process hook was not called")
	}

	hookMu.Lock()
	if hookErr != nil {
		t.Errorf("expected nil error in post hook, got %v", hookErr)
	}
	hookMu.Unlock()
}

func TestRunner_PostProcessHookReceivesError(t *testing.T) {
	var capturedErr error
	var hookMu sync.Mutex
	var hookCalled atomic.Bool
	var failed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 1 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "job-err", status: "pending"}, nil
		},
		failFn: func(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
			failed.Add(1)
			return nil
		},
	}

	processErr := errors.New("process failed")
	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		return job, processErr
	}

	runner := NewRunner(store, process, silentLogger(), WithMaxAttempts(1))
	runner.AddPostProcessHooks(func(ctx context.Context, job mockJob, err error) error {
		hookMu.Lock()
		capturedErr = err
		hookMu.Unlock()
		hookCalled.Store(true)
		return nil
	})

	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for failed.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	if !hookCalled.Load() {
		t.Error("post-process hook was not called")
	}

	hookMu.Lock()
	if !errors.Is(capturedErr, processErr) {
		t.Errorf("expected process error in post hook, got %v", capturedErr)
	}
	hookMu.Unlock()
}

func TestRunner_WorkerIDPassedToCheckout(t *testing.T) {
	var capturedWorkerID string
	var mu sync.Mutex
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n == 1 {
				mu.Lock()
				capturedWorkerID = workerID
				mu.Unlock()
			}
			return mockJob{}, ErrNoWork
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		return job, nil
	}

	runner := NewRunner(store, process, silentLogger())
	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		for checkoutCount.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	mu.Lock()
	id := capturedWorkerID
	mu.Unlock()

	if id != "test-worker-1" {
		t.Errorf("expected workerID 'test-worker-1', got %q", id)
	}
}

func TestRunner_CustomClock(t *testing.T) {
	fixedTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var capturedTime time.Time
	var mu sync.Mutex
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n == 1 {
				mu.Lock()
				capturedTime = now
				mu.Unlock()
			}
			return mockJob{}, ErrNoWork
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		return job, nil
	}

	runner := NewRunner(store, process, silentLogger(), WithClock(fixedClock(fixedTime)))
	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		for checkoutCount.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	mu.Lock()
	got := capturedTime
	mu.Unlock()

	if !got.Equal(fixedTime) {
		t.Errorf("expected fixed time %v, got %v", fixedTime, got)
	}
}

func TestRunner_WithTracer(t *testing.T) {
	tracer := &mockTracer{}
	var completed atomic.Int64
	var checkoutCount atomic.Int64

	store := &mockJobStore{
		checkoutFn: func(ctx context.Context, workerID string, now time.Time) (mockJob, error) {
			n := checkoutCount.Add(1)
			if n > 1 {
				return mockJob{}, ErrNoWork
			}
			return mockJob{id: "traced-job", status: "pending"}, nil
		},
		completeFn: func(ctx context.Context, jobID string, now time.Time) error {
			completed.Add(1)
			return nil
		},
	}

	process := func(ctx context.Context, job mockJob) (mockJob, error) {
		return job, nil
	}

	runner := NewRunner(store, process, silentLogger(),
		WithMaxAttempts(1),
		WithTracer(tracer),
	)

	pool := NewPool(runner.WorkFunc(), defaultPoolOpts(), WithLogger(silentLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		for completed.Load() < 1 {
			time.Sleep(10 * time.Millisecond)
		}
		pool.Stop()
	}()

	pool.Start(ctx)

	tracer.mu.Lock()
	spanCount := len(tracer.spans)
	tracer.mu.Unlock()

	// Should have at least: checkout, process, complete spans
	if spanCount < 3 {
		t.Errorf("expected at least 3 spans, got %d", spanCount)
	}
}

// Compile-time check that mockJobStore satisfies the interface
var _ JobStore[mockJob] = (*mockJobStore)(nil)
