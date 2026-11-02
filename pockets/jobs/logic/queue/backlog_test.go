package queue_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
)

// TestFencedRuntimeDrainsBacklogUnderDefaultPoll pins #230: with the default
// 5s PollInterval, 50 queued fenced jobs settle in well under one interval.
func TestFencedRuntimeDrainsBacklogUnderDefaultPoll(t *testing.T) {
	svc, _ := newFencedService(t)
	var handled atomic.Int64
	rt, err := queueapi.NewFencedRuntime(svc.Queue, map[string]queueapi.FencedHandlerFunc{
		"delivery": func(context.Context, queueapi.FencedClaim) error { handled.Add(1); return nil },
	}, queueapi.WithFencedRuntimePolicy(queueapi.FencedRuntimePolicy{Workers: 2}))
	if err != nil {
		t.Fatalf("NewFencedRuntime: %v", err)
	}
	for i := range 50 {
		if _, err := svc.Queue.EnqueueOnce(context.Background(), "delivery", fmt.Sprintf("k-%d", i), json.RawMessage(`"x"`)); err != nil {
			t.Fatalf("EnqueueOnce: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(ctx) }()
	waitFor(t, 3*time.Second, func() bool { return handled.Load() == 50 })
	cancel()
	<-done
}

// TestRuntimeDrainsBacklogUnderDefaultPoll is the ordinary-queue twin.
func TestRuntimeDrainsBacklogUnderDefaultPoll(t *testing.T) {
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: memory.NewQueue()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var handled atomic.Int64
	rt, err := queueapi.NewRuntime(svc.Queue, map[string]queueapi.HandlerFunc{
		"work": func(context.Context, queueapi.Job) error { handled.Add(1); return nil },
	}, queueapi.WithRuntimePolicy(queueapi.RuntimePolicy{Workers: 2}))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	for range 50 {
		if _, err := svc.Queue.Enqueue(context.Background(), "work", json.RawMessage(`"x"`)); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(ctx) }()
	waitFor(t, 3*time.Second, func() bool { return handled.Load() == 50 })
	cancel()
	<-done
}
