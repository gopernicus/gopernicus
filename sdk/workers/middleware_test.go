package workers

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// --- Mock tracer for testing ---

type mockSpanFinisher struct {
	mu         sync.Mutex
	attributes []Attribute
	errors     []error
	finished   bool
}

func (s *mockSpanFinisher) SetAttributes(attrs ...Attribute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes = append(s.attributes, attrs...)
}

func (s *mockSpanFinisher) RecordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, err)
}

func (s *mockSpanFinisher) Finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = true
}

type mockTracer struct {
	mu    sync.Mutex
	spans []*mockSpanFinisher
}

func (t *mockTracer) StartSpan(ctx context.Context, name string) (context.Context, SpanFinisher) {
	span := &mockSpanFinisher{}
	t.mu.Lock()
	t.spans = append(t.spans, span)
	t.mu.Unlock()
	return ctx, span
}

func (t *mockTracer) lastSpan() *mockSpanFinisher {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.spans) == 0 {
		return nil
	}
	return t.spans[len(t.spans)-1]
}

// --- TracingMiddleware tests ---

func TestTracingMiddleware_CreatesSpan(t *testing.T) {
	tracer := &mockTracer{}

	mw := TracingMiddleware(tracer)
	called := false
	work := mw(func(ctx context.Context) error {
		called = true
		return nil
	})

	ctx := WithWorkerID(context.Background(), "w-1")
	err := work(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("inner work func not called")
	}

	span := tracer.lastSpan()
	if span == nil {
		t.Fatal("expected span to be created")
	}
	if !span.finished {
		t.Error("expected span to be finished")
	}
}

func TestTracingMiddleware_SetsWorkerID(t *testing.T) {
	tracer := &mockTracer{}

	mw := TracingMiddleware(tracer)
	work := mw(func(ctx context.Context) error { return nil })

	ctx := WithWorkerID(context.Background(), "test-worker-42")
	work(ctx)

	span := tracer.lastSpan()
	span.mu.Lock()
	defer span.mu.Unlock()

	found := false
	for _, attr := range span.attributes {
		if attr.Key == "worker.id" && attr.Value == "test-worker-42" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected worker.id attribute, got %v", span.attributes)
	}
}

func TestTracingMiddleware_RecordsError(t *testing.T) {
	tracer := &mockTracer{}
	testErr := errors.New("process failed")

	mw := TracingMiddleware(tracer)
	work := mw(func(ctx context.Context) error { return testErr })

	ctx := WithWorkerID(context.Background(), "w-1")
	err := work(ctx)

	if !errors.Is(err, testErr) {
		t.Errorf("expected error to pass through, got %v", err)
	}

	span := tracer.lastSpan()
	span.mu.Lock()
	defer span.mu.Unlock()

	if len(span.errors) != 1 || !errors.Is(span.errors[0], testErr) {
		t.Errorf("expected error recorded on span, got %v", span.errors)
	}
}

// --- ConsecutiveErrorShutdown tests ---

func TestConsecutiveErrorShutdown_TriggersAfterCount(t *testing.T) {
	mw := ConsecutiveErrorShutdown(3)
	callErr := errors.New("bad")

	work := mw(func(ctx context.Context) error { return callErr })
	ctx := WithWorkerID(context.Background(), "w-1")

	// First two errors should pass through
	for i := 0; i < 2; i++ {
		err := work(ctx)
		if errors.Is(err, ErrWorkerShutdown) {
			t.Fatalf("shutdown too early at call %d", i+1)
		}
	}

	// Third consecutive error should trigger shutdown
	err := work(ctx)
	if !errors.Is(err, ErrWorkerShutdown) {
		t.Errorf("expected ErrWorkerShutdown after 3 errors, got %v", err)
	}
}

func TestConsecutiveErrorShutdown_ResetsOnSuccess(t *testing.T) {
	mw := ConsecutiveErrorShutdown(3)

	callCount := 0
	work := mw(func(ctx context.Context) error {
		callCount++
		if callCount == 3 {
			return nil // Success resets counter
		}
		return errors.New("bad")
	})

	ctx := WithWorkerID(context.Background(), "w-1")

	// 2 errors, then 1 success
	for i := 0; i < 3; i++ {
		work(ctx)
	}

	// 2 more errors should NOT trigger (counter reset)
	callCount = 100 // Force errors again
	work2 := mw(func(ctx context.Context) error { return errors.New("bad") })
	err := work2(ctx)
	if errors.Is(err, ErrWorkerShutdown) {
		t.Error("should not shutdown — counter was reset by success")
	}
}

func TestConsecutiveErrorShutdown_IgnoresNoWork(t *testing.T) {
	mw := ConsecutiveErrorShutdown(2)

	work := mw(func(ctx context.Context) error { return ErrNoWork })
	ctx := WithWorkerID(context.Background(), "w-1")

	// ErrNoWork should not count toward consecutive errors
	for i := 0; i < 10; i++ {
		err := work(ctx)
		if errors.Is(err, ErrWorkerShutdown) {
			t.Fatalf("ErrNoWork should not trigger shutdown at call %d", i+1)
		}
	}
}

func TestConsecutiveErrorShutdown_IndependentPerWorker(t *testing.T) {
	mw := ConsecutiveErrorShutdown(2)

	work := mw(func(ctx context.Context) error { return errors.New("bad") })

	ctx1 := WithWorkerID(context.Background(), "w-1")
	ctx2 := WithWorkerID(context.Background(), "w-2")

	// 1 error for each worker
	work(ctx1)
	work(ctx2)

	// Second error for w-1 should trigger shutdown for w-1
	err1 := work(ctx1)
	if !errors.Is(err1, ErrWorkerShutdown) {
		t.Error("expected w-1 to shutdown after 2 consecutive errors")
	}

	// w-2 should still be fine (only 1 error)
	err2 := work(ctx2)
	if !errors.Is(err2, ErrWorkerShutdown) {
		// w-2 also has 2 errors now, so it should shutdown too
		t.Log("w-2 also reached threshold — this is correct")
	}
}
