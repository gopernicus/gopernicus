package workers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestPoolDelayAfterSlowWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		var ended time.Time
		p := NewPool(func(context.Context) error {
			calls++
			if calls > 1 && time.Since(ended) != 10*time.Millisecond {
				t.Errorf("gap=%v, want 10ms", time.Since(ended))
			}
			time.Sleep(30 * time.Millisecond)
			ended = time.Now()
			if calls == 3 {
				return ErrWorkerShutdown
			}
			return nil
		}, WithLogger(silentLogger()), WithPollInterval(10*time.Millisecond))
		if err := p.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPoolReturnsFatalDuringParentCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		cause := errors.New("fatal storage failure")
		p := NewPool(func(context.Context) error {
			close(started)
			<-release
			return errors.Join(ErrWorkerShutdown, ErrPoolShutdown, cause)
		}, WithLogger(silentLogger()))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- p.Run(ctx) }()
		<-started
		cancel()
		close(release)
		if err := <-done; !errors.Is(err, ErrPoolShutdown) || !errors.Is(err, cause) {
			t.Fatalf("Run=%v", err)
		}
		if err := p.Run(context.Background()); !errors.Is(err, ErrAlreadyRun) {
			t.Fatalf("second Run=%v", err)
		}
	})
}

func TestPoolConcurrentRunRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		p := NewPool(func(context.Context) error { close(started); <-release; return ErrWorkerShutdown }, WithLogger(silentLogger()))
		done := make(chan error, 1)
		go func() { done <- p.Run(context.Background()) }()
		<-started
		if err := p.Run(context.Background()); !errors.Is(err, ErrAlreadyRun) {
			t.Fatalf("concurrent Run=%v", err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestPoolLogsOrdinaryErrorsAndRetainsFatalAfterPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		calls := 0
		p := NewPool(func(context.Context) error {
			calls++
			switch calls {
			case 1:
				panic("recoverable panic")
			case 2:
				return errors.New("outbox unavailable")
			default:
				return ErrPoolShutdown
			}
		}, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
		if err := p.Run(context.Background()); !errors.Is(err, ErrPoolShutdown) {
			t.Fatal(err)
		}
		if !strings.Contains(logs.String(), "outbox unavailable") {
			t.Fatal("missing ordinary failure cause")
		}
		if p.Stats().Panics != 1 || calls != 3 {
			t.Fatalf("stats=%+v calls=%d", p.Stats(), calls)
		}
	})
}

func TestShutdownMiddlewarePreservesScopeAndOwnsCounters(t *testing.T) {
	ctx := WithWorkerID(context.Background(), "same-worker")
	failure := errors.New("failure")
	mw := ConsecutiveErrorShutdown(2)
	first := mw(func(context.Context) error { return failure })
	second := mw(func(context.Context) error { return failure })
	if err := first(ctx); !errors.Is(err, failure) || errors.Is(err, ErrWorkerShutdown) {
		t.Fatal(err)
	}
	if err := second(ctx); errors.Is(err, ErrWorkerShutdown) {
		t.Fatal("independent wrapper inherited counter")
	}
	if err := first(ctx); !errors.Is(err, failure) || !errors.Is(err, ErrWorkerShutdown) {
		t.Fatal(err)
	}
	fatal := errors.Join(ErrPoolShutdown, failure)
	gated := ConsecutiveErrorShutdown(1)(func(context.Context) error { return fatal })
	if err := gated(ctx); err != fatal {
		t.Fatalf("control error replaced: %v", err)
	}
}

func TestWorkerGateSkipsWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		gate := func(next WorkFunc) WorkFunc { return func(context.Context) error { cancel(); return ErrNoWork } }
		p := NewPool(func(context.Context) error { calls++; return nil }, WithMiddleware(gate), WithLogger(silentLogger()))
		if err := p.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if calls != 0 {
			t.Fatal("closed gate invoked work")
		}
	})
}

func TestPoolLogsFailureJoinedWithParentCancellation(t *testing.T) {
	for _, joined := range []bool{false, true} {
		name := "exact cancellation"
		if joined {
			name = "cancellation with failure"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var logs bytes.Buffer
				failure := errors.New("storage write failed during shutdown")
				calls := 0
				pool := NewPool(func(workCtx context.Context) error {
					calls++
					cancel()
					if joined {
						return errors.Join(workCtx.Err(), failure)
					}
					return workCtx.Err()
				}, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
				if err := pool.Run(ctx); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("calls = %d, want one before shutdown", calls)
				}
				logged := strings.Contains(logs.String(), "worker iteration failed")
				if logged != joined {
					t.Fatalf("failure logged = %v, want %v: %s", logged, joined, logs.String())
				}
				if joined && !strings.Contains(logs.String(), failure.Error()) {
					t.Fatal("joined storage failure was lost from the log")
				}
			})
		})
	}
}
