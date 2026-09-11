package queue_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	jobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queue "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

func TestRuntimeOptionsValidateTheFinalWholePolicy(t *testing.T) {
	svc, _ := newFencedService(t)
	handlers := map[string]queue.FencedHandlerFunc{"kind": func(context.Context, queue.FencedClaim) error { return nil }}
	original := queue.WithFencedRuntimePolicy(queue.FencedRuntimePolicy{LeaseFor: time.Minute, ProcessTimeout: 45 * time.Second})
	if _, err := queue.NewFencedRuntime(svc.Queue, handlers, original,
		queue.WithFencedRuntimePolicy(queue.FencedRuntimePolicy{ProcessTimeout: 35 * time.Second})); !errors.Is(err, queue.ErrProcessTimeoutExceedsLease) {
		t.Fatalf("final omitted lease must resolve to 30s, not inherit 1m: %v", err)
	}
	if _, err := queue.NewFencedRuntime(svc.Queue, handlers, original, queue.WithFencedRuntimePolicy(queue.FencedRuntimePolicy{})); err != nil {
		t.Fatalf("zero policy did not clear old timeout: %v", err)
	}
	if _, err := queue.NewFencedRuntime(svc.Queue, handlers, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil fenced option: %v", err)
	}
	ordinary, err := jobs.New(jobs.Repositories{Queue: memory.NewQueue()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.NewRuntime(ordinary.Queue, map[string]queue.HandlerFunc{"kind": func(context.Context, queue.Job) error { return nil }}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil runtime option: %v", err)
	}
	if _, err := jobs.New(jobs.Repositories{Queue: memory.NewQueue()}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil root option: %v", err)
	}
}

func TestFencedOptionsCaptureReuseAndClearMiddlewareAndHooks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var gates, hooks atomic.Int32
		middleware := []workers.JobMiddleware[queue.FencedClaim]{func(next workers.ProcessFunc[queue.FencedClaim]) workers.ProcessFunc[queue.FencedClaim] {
			return func(ctx context.Context, claim queue.FencedClaim) error { gates.Add(1); return next(ctx, claim) }
		}}
		deadLetters := map[string]queue.DeadLetterFunc{"kind": func(context.Context, queue.Job) error { hooks.Add(1); return nil }}
		middlewareOption := queue.WithFencedJobMiddleware(middleware...)
		hookOption := queue.WithDeadLetters(deadLetters)
		middleware[0] = nil
		clear(deadLetters)
		for run := range 3 {
			svc, store := newFencedService(t)
			id, err := svc.Queue.EnqueueOnce(context.Background(), "kind", "key", []byte("opaque"))
			if err != nil {
				t.Fatal(err)
			}
			options := []queue.FencedRuntimeOption{middlewareOption, hookOption,
				queue.WithFencedRuntimePolicy(queue.FencedRuntimePolicy{Workers: 1, PollInterval: time.Millisecond, IdleInterval: time.Millisecond}),
				queue.WithFencedRuntimeLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			}
			if run == 2 {
				options = append(options, queue.WithFencedJobMiddleware(), queue.WithDeadLetters(nil))
			}
			runtime, err := queue.NewFencedRuntime(svc.Queue, map[string]queue.FencedHandlerFunc{"kind": func(context.Context, queue.FencedClaim) error { return queue.Permanent("test rejection") }}, options...)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- runtime.Run(ctx) }()
			time.Sleep(5 * time.Millisecond)
			synctest.Wait()
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			stored, err := store.Get(context.Background(), id)
			if err != nil || stored.JobStatus != queue.StatusDeadLetter {
				t.Fatalf("job=%+v error=%v", stored, err)
			}
			expected := int32(run + 1)
			if run == 2 {
				expected = 2
			}
			if gates.Load() != expected || hooks.Load() != expected {
				t.Fatalf("run %d gates=%d hooks=%d want %d", run, gates.Load(), hooks.Load(), expected)
			}
		}
	})
}
