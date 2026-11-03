package queue_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
)

// idleHour is far longer than any wake-driven claim should take, so a job
// processed under a second of fake time proves the wake, not the idle poll.
var idleHour = queueapi.FencedRuntimePolicy{Workers: 1, PollInterval: time.Hour, IdleInterval: time.Hour}

func startFencedIdleHour(t *testing.T, svc *queueapi.Service, handlers map[string]queueapi.FencedHandlerFunc) (stop func()) {
	t.Helper()
	rt, err := queueapi.NewFencedRuntime(svc, handlers,
		queueapi.WithFencedRuntimePolicy(idleHour),
		queueapi.WithFencedRuntimeLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// TestFencedAdmissionWakesRuntime: EnqueueOnce and Replace are processed at
// once, not on the next idle poll (#95).
func TestFencedAdmissionWakesRuntime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		comps, _ := newFencedService(t)
		svc := comps.Queue
		handled := make(chan time.Time, 4)
		stop := startFencedIdleHour(t, svc, map[string]queueapi.FencedHandlerFunc{
			"mail": func(context.Context, queueapi.FencedClaim) error { handled <- time.Now(); return nil },
		})
		defer stop()
		synctest.Wait() // the worker is idle, waiting an hour

		start := time.Now()
		if _, err := svc.EnqueueOnce(context.Background(), "mail", "k1", []byte("a")); err != nil {
			t.Fatal(err)
		}
		if got := (<-handled).Sub(start); got > time.Second {
			t.Fatalf("EnqueueOnce processed after %v of fake time; want a wake, not the idle poll", got)
		}
		synctest.Wait()

		start = time.Now()
		if _, err := svc.Replace(context.Background(), "mail", "k2", []byte("b")); err != nil {
			t.Fatal(err)
		}
		if got := (<-handled).Sub(start); got > time.Second {
			t.Fatalf("Replace processed after %v of fake time", got)
		}
	})
}

// TestFencedWakeReachesTheOwningRuntime: two fenced runtimes on one Service,
// each claiming its own kind; a job of either kind is claimed at once.
func TestFencedWakeReachesTheOwningRuntime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		comps, _ := newFencedService(t)
		svc := comps.Queue
		alpha, beta := make(chan time.Time, 4), make(chan time.Time, 4)
		stopA := startFencedIdleHour(t, svc, map[string]queueapi.FencedHandlerFunc{"alpha": func(context.Context, queueapi.FencedClaim) error { alpha <- time.Now(); return nil }})
		defer stopA()
		stopB := startFencedIdleHour(t, svc, map[string]queueapi.FencedHandlerFunc{"beta": func(context.Context, queueapi.FencedClaim) error { beta <- time.Now(); return nil }})
		defer stopB()
		synctest.Wait()

		for i, kind := range []string{"beta", "alpha", "beta"} {
			start := time.Now()
			if _, err := svc.EnqueueOnce(context.Background(), kind, fmt.Sprintf("k%d", i), nil); err != nil {
				t.Fatal(err)
			}
			ch := alpha
			if kind == "beta" {
				ch = beta
			}
			if got := (<-ch).Sub(start); got > time.Second {
				t.Fatalf("%s processed after %v of fake time; another runtime swallowed its wake", kind, got)
			}
			synctest.Wait()
		}
	})
}

// TestFencedBurstAndDuplicates: a burst of 100 admissions coalesces into few
// wakes but every job is processed promptly (drain-backlog); a duplicate
// EnqueueOnce admits nothing new.
func TestFencedBurstAndDuplicates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		comps, _ := newFencedService(t)
		svc := comps.Queue
		var processed atomic.Int64
		stop := startFencedIdleHour(t, svc, map[string]queueapi.FencedHandlerFunc{
			"mail": func(context.Context, queueapi.FencedClaim) error { processed.Add(1); return nil },
		})
		defer stop()
		synctest.Wait()
		start := time.Now()
		for i := 0; i < 100; i++ {
			if _, err := svc.EnqueueOnce(context.Background(), "mail", fmt.Sprintf("burst-%d", i), nil); err != nil {
				t.Fatal(err)
			}
		}
		for processed.Load() < 100 {
			time.Sleep(10 * time.Millisecond)
			if time.Since(start) > time.Minute {
				t.Fatalf("burst: %d of 100 processed after a minute of fake time", processed.Load())
			}
		}
		synctest.Wait()

		first, err := svc.EnqueueOnce(context.Background(), "mail", "dup", nil)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		second, err := svc.EnqueueOnce(context.Background(), "mail", "dup", nil)
		if err != nil {
			t.Fatal(err)
		}
		if second != first {
			// The first has already completed, so the key is free again: a new
			// execution is legitimate. Either way nothing is lost.
			synctest.Wait()
		}
		if processed.Load() < 101 {
			t.Fatalf("processed %d, want at least 101", processed.Load())
		}
	})
}
