package queue_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestNewRuntime_ValidatesStagedHandlers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		update func(map[string]queueapi.HandlerFunc)
		want   error
	}{
		{"nil handler", func(h map[string]queueapi.HandlerFunc) { h["later"] = nil }, queueapi.ErrInvalidHandler},
		{"empty kind", func(h map[string]queueapi.HandlerFunc) { h[""] = h["demo"] }, queueapi.ErrInvalidHandler},
		{"removed all handlers", func(h map[string]queueapi.HandlerFunc) { clear(h) }, queueapi.ErrHandlersRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handlers := demoHandlers()
			svcRuntimeConfig := runtimeTestConfig{Handlers: handlers}
			svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: memory.NewQueue()})
			if err != nil {
				t.Fatal(err)
			}
			tc.update(handlers)
			if _, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig); !errors.Is(err, tc.want) {
				t.Fatalf("NewRuntime after staged change: %v, want %v", err, tc.want)
			}
		})
	}
}

// A host can create the enqueue service before the services its handlers use.
// Each later runtime must own a matching handler/queue/scheduler snapshot, even
// when the same service builds another runtime from a different map contents.
func TestRuntime_StagedHandlersSnapshot(t *testing.T) {
	for _, initial := range []string{"empty", "nonempty"} {
		t.Run(initial, func(t *testing.T) {
			ctx := context.Background()
			queue := memory.NewQueue()
			schedules := memory.NewSchedules()
			handlers := make(map[string]queueapi.HandlerFunc)
			if initial == "nonempty" {
				handlers["removed"] = func(context.Context, queueapi.Job) error { return nil }
			}
			svcRuntimeConfig := runtimeTestConfig{
				Handlers:     handlers,
				Workers:      1,
				PollInterval: time.Millisecond,
				IdleInterval: time.Millisecond,
				Logger:       discardLogger(),
			}
			svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: queue, Schedules: schedules})
			if err != nil {
				t.Fatal(err)
			}
			// Schedule management does not require a completed handler registry.
			if _, err := svc.Schedules.EnsureSchedule(ctx, schedule.Ensure{
				Name: "future", Kind: "owned", Spec: schedule.Spec{Every: time.Hour},
			}); err != nil {
				t.Fatalf("EnsureSchedule before handlers: %v", err)
			}

			var oldCalls, newCalls, foreignCalls atomic.Int32
			clear(handlers)
			handlers["owned"] = func(context.Context, queueapi.Job) error { oldCalls.Add(1); return nil }
			first, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig)
			if err != nil {
				t.Fatal(err)
			}
			handlers["owned"] = func(context.Context, queueapi.Job) error { newCalls.Add(1); return nil }
			handlers["foreign"] = func(context.Context, queueapi.Job) error { foreignCalls.Add(1); return nil }
			second, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig)
			if err != nil {
				t.Fatal(err)
			}
			clear(handlers) // Neither runtime may keep reading the host map.

			past := time.Now().UTC().Add(-time.Minute)
			seeded := make(map[string]schedule.Schedule)
			for _, kind := range []string{"owned", "foreign", "removed"} {
				sch, err := schedules.Ensure(ctx, schedule.Ensure{
					Name: kind, Kind: kind, Spec: schedule.Spec{Every: time.Hour},
				}, past)
				if err != nil {
					t.Fatal(err)
				}
				seeded[kind] = sch
				if _, err := svc.Queue.EnqueueJob(ctx, queueapi.Enqueue{ID: "direct-" + kind, Kind: kind}); err != nil {
					t.Fatal(err)
				}
			}
			completed := func(id string) bool {
				j, err := queue.Get(ctx, id)
				return err == nil && j.JobStatus == queueapi.StatusCompleted
			}
			scheduleCompleted := func(kind string) bool {
				sch, err := schedules.Get(ctx, seeded[kind].ID)
				return err == nil && sch.LastJobID != "" && completed(sch.LastJobID)
			}
			untouched := func(kind string) {
				t.Helper()
				sch, err := schedules.Get(ctx, seeded[kind].ID)
				if err != nil {
					t.Fatal(err)
				}
				if !sch.NextRunAt.Equal(past) || sch.LastRunAt != nil || sch.LastJobID != "" {
					t.Fatalf("%s schedule advanced without a handler: %+v", kind, sch)
				}
				page, err := queue.List(ctx, queueapi.ListFilter{Kind: kind}, list.Request{Limit: 10})
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 1 || page.Items[0].JobStatus != queueapi.StatusPending || page.Items[0].RetryCount() != 0 {
					t.Fatalf("%s jobs changed without a handler: %+v", kind, page.Items)
				}
			}

			runSnapshotUntil(t, first, func() bool { return completed("direct-owned") && scheduleCompleted("owned") })
			untouched("foreign")
			untouched("removed")
			if oldCalls.Load() != 2 || newCalls.Load() != 0 || foreignCalls.Load() != 0 {
				t.Fatalf("first runtime handlers: old=%d new=%d foreign=%d", oldCalls.Load(), newCalls.Load(), foreignCalls.Load())
			}

			if _, err := svc.Queue.EnqueueJob(ctx, queueapi.Enqueue{ID: "later-owned", Kind: "owned"}); err != nil {
				t.Fatal(err)
			}
			runSnapshotUntil(t, second, func() bool {
				return completed("later-owned") && completed("direct-foreign") && scheduleCompleted("foreign")
			})
			untouched("removed")
			if oldCalls.Load() != 2 || newCalls.Load() != 1 || foreignCalls.Load() != 2 {
				t.Fatalf("second runtime handlers: old=%d new=%d foreign=%d", oldCalls.Load(), newCalls.Load(), foreignCalls.Load())
			}
		})
	}
}

// Drain even when the condition fails, so a regression cannot leave a poller
// running for the rest of the suite.
func runSnapshotUntil(t *testing.T, rt *queueapi.Runtime, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("queueapi.Runtime.Run: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("runtime did not drain")
		}
	}()
	waitFor(t, 3*time.Second, cond)
}
