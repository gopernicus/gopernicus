package jobs_test

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/jobs"
	"github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
)

func TestOptionalAssemblyDoesNotStartWorkers(t *testing.T) {
	ordinary := memory.NewQueue()
	components, err := jobs.New(jobs.Repositories{Queue: ordinary})
	if err != nil {
		t.Fatal(err)
	}
	if components.Schedules != nil {
		t.Fatal("queue-only assembly unexpectedly built schedules")
	}
	inserted, err := components.Queue.EnqueueJob(context.Background(), queue.Enqueue{Kind: "owned"})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := queue.NewRuntime(components.Queue, map[string]queue.HandlerFunc{"owned": func(context.Context, queue.Job) error { t.Error("runtime construction started a handler"); return nil }}, queue.WithScheduler(components.Schedules))
	if err != nil || runtime == nil {
		t.Fatalf("runtime construction: %v", err)
	}
	stored, err := ordinary.Get(context.Background(), inserted.JobID)
	if err != nil || stored.JobStatus != queue.StatusPending {
		t.Fatalf("constructor consumed queued work: %+v %v", stored, err)
	}
	fenced, err := jobs.New(jobs.Repositories{FencedQueue: memory.NewFencedQueue()})
	if err != nil {
		t.Fatal(err)
	}
	if fenced.Schedules != nil {
		t.Fatal("fenced-only assembly unexpectedly built schedules")
	}
	if _, err := queue.NewFencedRuntime(fenced.Queue, map[string]queue.FencedHandlerFunc{"owned": func(context.Context, queue.FencedClaim) error { return nil }}); err != nil {
		t.Fatal(err)
	}
}
