package workers

import (
	"context"
	"errors"
	"testing"
)

func TestTracingMiddleware_StartsAndFinishesSpan(t *testing.T) {
	tr := &fakeTracer{}
	work := TracingMiddleware(tr)(func(ctx context.Context) error { return nil })

	if err := work(WithWorkerID(context.Background(), "w-7")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.started) != 1 || tr.started[0] != "worker.process" {
		t.Errorf("expected one worker.process span, got %v", tr.started)
	}
	if tr.finished != 1 {
		t.Errorf("expected the span finished once, got %d", tr.finished)
	}
	got := tr.attrs["worker.process"]
	if len(got) != 1 || got[0].Key != "worker.id" || got[0].Value != "w-7" {
		t.Errorf("expected a worker.id=w-7 attribute, got %v", got)
	}
	if len(tr.errs) != 0 {
		t.Errorf("expected no recorded errors, got %v", tr.errs)
	}
}

func TestTracingMiddleware_RecordsError(t *testing.T) {
	tr := &fakeTracer{}
	boom := errors.New("boom")
	work := TracingMiddleware(tr)(func(ctx context.Context) error { return boom })

	if err := work(WithWorkerID(context.Background(), "w-1")); !errors.Is(err, boom) {
		t.Fatalf("expected boom to propagate, got %v", err)
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.errs) != 1 || !errors.Is(tr.errs[0], boom) {
		t.Errorf("expected boom recorded on the span, got %v", tr.errs)
	}
	if tr.finished != 1 {
		t.Errorf("expected the span finished once, got %d", tr.finished)
	}
}

func TestConsecutiveErrorShutdown_TriggersAfterCount(t *testing.T) {
	mw := ConsecutiveErrorShutdown(3)
	work := mw(func(ctx context.Context) error { return errors.New("bad") })
	ctx := WithWorkerID(context.Background(), "w-1")

	for i := 0; i < 2; i++ {
		if err := work(ctx); errors.Is(err, ErrWorkerShutdown) {
			t.Fatalf("shutdown too early at call %d", i+1)
		}
	}
	if err := work(ctx); !errors.Is(err, ErrWorkerShutdown) {
		t.Errorf("expected ErrWorkerShutdown after 3 consecutive errors, got %v", err)
	}
}

func TestConsecutiveErrorShutdown_ResetsOnSuccess(t *testing.T) {
	mw := ConsecutiveErrorShutdown(3)
	calls := 0
	work := mw(func(ctx context.Context) error {
		calls++
		if calls == 3 {
			return nil // success resets the counter
		}
		return errors.New("bad")
	})
	ctx := WithWorkerID(context.Background(), "w-1")

	for i := 0; i < 5; i++ {
		if err := work(ctx); errors.Is(err, ErrWorkerShutdown) {
			t.Fatalf("unexpected shutdown at call %d — counter should have reset on success", i+1)
		}
	}
}

func TestConsecutiveErrorShutdown_IgnoresNoWork(t *testing.T) {
	mw := ConsecutiveErrorShutdown(2)
	work := mw(func(ctx context.Context) error { return ErrNoWork })
	ctx := WithWorkerID(context.Background(), "w-1")

	for i := 0; i < 10; i++ {
		if err := work(ctx); errors.Is(err, ErrWorkerShutdown) {
			t.Fatalf("ErrNoWork should not count toward shutdown (call %d)", i+1)
		}
	}
}

func TestConsecutiveErrorShutdown_IndependentPerWorker(t *testing.T) {
	mw := ConsecutiveErrorShutdown(2)
	work := mw(func(ctx context.Context) error { return errors.New("bad") })

	ctx1 := WithWorkerID(context.Background(), "w-1")
	ctx2 := WithWorkerID(context.Background(), "w-2")

	work(ctx1) // w-1: 1 error
	work(ctx2) // w-2: 1 error

	if err := work(ctx1); !errors.Is(err, ErrWorkerShutdown) {
		t.Error("expected w-1 to shut down on its 2nd consecutive error")
	}
}
