package queue_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

func TestRuntimeJobGateDefersThenProcesses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := memory.NewQueue()
		var ready atomic.Bool
		gated, processed := make(chan struct{}), make(chan struct{})
		gate := func(next workers.ProcessFunc[queueapi.Job]) workers.ProcessFunc[queueapi.Job] {
			return func(ctx context.Context, j queueapi.Job) error {
				if !ready.Load() {
					close(gated)
					return workers.DeferUntil(time.Now().Add(100*time.Millisecond), "tenant paused")
				}
				return next(ctx, j)
			}
		}
		svcRuntimeConfig := runtimeTestConfig{
			Handlers:      map[string]queueapi.HandlerFunc{"kind": func(context.Context, queueapi.Job) error { close(processed); return nil }},
			JobMiddleware: []workers.JobMiddleware[queueapi.Job]{gate},
			Workers:       1,
			PollInterval:  10 * time.Millisecond,
			IdleInterval:  10 * time.Millisecond,
			Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: queue})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := queue.Enqueue(context.Background(), queueapi.Enqueue{ID: "job", Kind: "kind", TenantID: "tenant", Payload: json.RawMessage("opaque")}); err != nil {
			t.Fatal(err)
		}
		rt, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- rt.Run(ctx) }()
		<-gated
		synctest.Wait()
		j, err := queue.Get(ctx, "job")
		if err != nil {
			t.Fatal(err)
		}
		if j.JobStatus != queueapi.StatusPending || j.Retries != 0 || j.ClaimedAt != nil || j.TenantID != "tenant" {
			t.Fatalf("deferred job=%+v", j)
		}
		select {
		case <-processed:
			t.Fatal("gate invoked handler")
		default:
		}
		ready.Store(true)
		time.Sleep(100 * time.Millisecond)
		<-processed
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		j, err = queue.Get(context.Background(), "job")
		if err != nil || j.JobStatus != queueapi.StatusCompleted || j.Retries != 0 {
			t.Fatalf("completed=%+v err=%v", j, err)
		}
	})
}

func TestFencedRuntimeJobGateRefundsClaimAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc, queue := newFencedService(t)
		id, err := svc.Queue.EnqueueOnceIn(context.Background(), queueapi.EnqueueOnceInput{Kind: "kind", LogicalKey: "key", TenantID: "tenant", Payload: []byte{0xff, 0, 1}})
		if err != nil {
			t.Fatal(err)
		}
		var ready atomic.Bool
		gated, processed := make(chan struct{}), make(chan struct{})
		gate := func(next workers.ProcessFunc[queueapi.FencedClaim]) workers.ProcessFunc[queueapi.FencedClaim] {
			return func(ctx context.Context, c queueapi.FencedClaim) error {
				if c.TenantID != "tenant" {
					t.Error("middleware did not receive tenant")
				}
				if !ready.Load() {
					close(gated)
					return workers.DeferUntil(time.Now().Add(100*time.Millisecond), "tenant paused")
				}
				return next(ctx, c)
			}
		}
		rt, err := queueapi.NewFencedRuntime(svc.Queue, map[string]queueapi.FencedHandlerFunc{"kind": func(_ context.Context, c queueapi.FencedClaim) error {
			if c.Attempt != 1 {
				t.Errorf("attempt=%d", c.Attempt)
			}
			close(processed)
			return nil
		}}, queueapi.WithFencedRuntimePolicy(queueapi.FencedRuntimePolicy{Workers: 1, PollInterval: 10 * time.Millisecond, IdleInterval: 10 * time.Millisecond}), queueapi.WithFencedRuntimeLogger(slog.New(slog.NewTextHandler(io.Discard, nil))), queueapi.WithFencedJobMiddleware([]workers.JobMiddleware[queueapi.FencedClaim]{gate}...))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- rt.Run(ctx) }()
		<-gated
		synctest.Wait()
		j, err := queue.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if j.JobStatus != queueapi.StatusPending || j.Retries != 0 || j.LeaseID != "" || !bytes.Equal(j.Payload, []byte{0xff, 0, 1}) {
			t.Fatalf("deferred=%+v", j)
		}
		ready.Store(true)
		time.Sleep(100 * time.Millisecond)
		<-processed
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		j, err = queue.Get(context.Background(), id)
		if err != nil || j.JobStatus != queueapi.StatusCompleted || j.Retries != 1 {
			t.Fatalf("completed=%+v err=%v", j, err)
		}
	})
}
