package workers

import (
	"context"
	"errors"
	"testing"
)

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
