package queue_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The #37 regressions: N runtimes sharing ONE queue (and one schedules table)
// with disjoint handler registries. Before the fix, whichever runtime polled
// first claimed the other's job, failed it "no handler registered", and — at
// MaxAttempts 1, as configured here — dead-lettered it outright. Now the job of
// the other kind stays pending, uncharged, until its own runtime runs.

// kindService builds a pocketjobs.Components over shared stores handling exactly one kind.
func kindService(t *testing.T, queue queueapi.QueueRepository, sched schedule.Repository, kind string, logger *slog.Logger) (*pocketjobs.Components, *queueapi.Runtime) {
	t.Helper()
	svcRuntimeConfig := runtimeTestConfig{
		Handlers:     map[string]queueapi.HandlerFunc{kind: func(context.Context, queueapi.Job) error { return nil }},
		Workers:      1,
		PollInterval: 5 * time.Millisecond,
		IdleInterval: 5 * time.Millisecond,
		Logger:       logger,
	}
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: queue, Schedules: sched}, pocketjobs.WithMaxAttempts(1))
	if err != nil {
		t.Fatalf("NewService(%s): %v", kind, err)
	}
	rt, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	return svc, rt
}

// runUntil runs svc's runtime until cond holds (or the deadline), then drains it.
func runUntil(t *testing.T, rt *queueapi.Runtime, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	waitFor(t, 3*time.Second, cond)
	// Several more poll cycles: a foreign claim, if any, would land here.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not drain after cancel")
	}
}

func mustGet(t *testing.T, queue queueapi.QueueRepository, id string) queueapi.Job {
	t.Helper()
	j, err := queue.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return j
}

func TestRuntime_ClaimsOnlyRegisteredKinds(t *testing.T) {
	ctx := context.Background()
	queue := memory.NewQueue()
	capture := &captureHandler{}
	svcA, rtA := kindService(t, queue, nil, "a", slog.New(capture))
	_, rtB := kindService(t, queue, nil, "b", nil)

	for _, kind := range []string{"a", "b"} {
		if _, err := svcA.Queue.EnqueueJob(ctx, queueapi.Enqueue{ID: "job-" + kind, Kind: kind}); err != nil {
			t.Fatalf("queueapi.Enqueue %s: %v", kind, err)
		}
	}

	// Only runtime A runs: job-a completes, job-b is untouched.
	runUntil(t, rtA, func() bool { return mustGet(t, queue, "job-a").Status() == string(queueapi.StatusCompleted) })
	if !capture.saw("jobs runtime: claiming kinds") {
		t.Errorf("runtime did not log its claimed kinds; messages=%v", capture.messages())
	}
	b := mustGet(t, queue, "job-b")
	if b.Status() != string(queueapi.StatusPending) || b.RetryCount() != 0 || b.WorkerName != "" {
		t.Fatalf("job-b after runtime A alone: status=%q retries=%d worker=%q, want pending/0/\"\" (must wait for its own runtime)", b.Status(), b.RetryCount(), b.WorkerName)
	}

	// queueapi.Runtime B then owns it.
	runUntil(t, rtB, func() bool { return mustGet(t, queue, "job-b").Status() == string(queueapi.StatusCompleted) })
}

func TestScheduler_FiresOnlyRegisteredKinds(t *testing.T) {
	ctx := context.Background()
	queue := memory.NewQueue()
	sched := memory.NewSchedules()
	_, rtA := kindService(t, queue, sched, "a", nil)
	_, rtB := kindService(t, queue, sched, "b", nil)

	// Seed both schedules already due (straight into the repository — the
	// pocketjobs.Components would compute NextRunAt = now + Every).
	past := time.Now().UTC().Add(-time.Minute)
	sa, err := sched.Ensure(ctx, schedule.Ensure{Name: "sa", Kind: "a", Spec: schedule.Spec{Every: time.Hour}}, past)
	if err != nil {
		t.Fatalf("Ensure sa: %v", err)
	}
	sb, err := sched.Ensure(ctx, schedule.Ensure{Name: "sb", Kind: "b", Spec: schedule.Spec{Every: time.Hour}}, past)
	if err != nil {
		t.Fatalf("Ensure sb: %v", err)
	}

	fired := func(id string) bool {
		s, err := sched.Get(ctx, id)
		return err == nil && s.LastJobID != ""
	}

	// Only runtime A runs: sa fires (and its job completes), sb is untouched.
	runUntil(t, rtA, func() bool { return fired(sa.ID) })
	gotB, _ := sched.Get(ctx, sb.ID)
	if !gotB.NextRunAt.Equal(sb.NextRunAt) || gotB.LastJobID != "" {
		t.Fatalf("sb after runtime A alone: next=%v last=%q, want next unchanged %v and no job (must wait for its own runtime)", gotB.NextRunAt, gotB.LastJobID, sb.NextRunAt)
	}
	page, err := queue.List(ctx, queueapi.ListFilter{Kind: "b"}, list.Request{Limit: 10})
	if err != nil {
		t.Fatalf("List b: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("runtime A enqueued %d jobs of kind b, want 0", len(page.Items))
	}
	gotA, _ := sched.Get(ctx, sa.ID)
	if a := mustGet(t, queue, gotA.LastJobID); a.Status() != string(queueapi.StatusCompleted) {
		t.Fatalf("sa's fired job status = %q, want completed", a.Status())
	}

	// queueapi.Runtime B then fires sb.
	runUntil(t, rtB, func() bool { return fired(sb.ID) })
}

func TestFencedRuntime_ClaimsOnlyRegisteredKinds(t *testing.T) {
	ctx := context.Background()
	fq := memory.NewFencedQueue()
	svc, err := pocketjobs.New(pocketjobs.Repositories{FencedQueue: fq})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	fencedRuntime := func(kind string) *queueapi.FencedRuntime {
		rt, err := queueapi.NewFencedRuntime(svc.Queue, map[string]queueapi.FencedHandlerFunc{kind: func(context.Context, queueapi.FencedClaim) error { return nil }}, queueapi.WithFencedRuntimePolicy(queueapi.FencedRuntimePolicy{Workers: 1, PollInterval: 5 * time.Millisecond, IdleInterval: 5 * time.Millisecond, MaxAttempts: 1}))
		if err != nil {
			t.Fatalf("NewFencedRuntime(%s): %v", kind, err)
		}
		return rt
	}
	runFencedUntil := func(rt *queueapi.FencedRuntime, cond func() bool) {
		t.Helper()
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); _ = rt.Run(runCtx) }()
		waitFor(t, 3*time.Second, cond)
		time.Sleep(50 * time.Millisecond)
		cancel()
		<-done
	}
	completed := func(key string) bool {
		s, err := svc.Queue.LatestStatusByKey(ctx, key)
		return err == nil && s == "completed"
	}

	for _, kind := range []string{"a", "b"} {
		if _, err := fq.EnqueueOnce(ctx, queueapi.Enqueue{ID: "job-" + kind, Kind: kind, LogicalKey: "key-" + kind, Payload: json.RawMessage(`"x"`)}); err != nil {
			t.Fatalf("EnqueueOnce %s: %v", kind, err)
		}
	}

	runFencedUntil(fencedRuntime("a"), func() bool { return completed("key-a") })
	b, err := fq.Get(ctx, "job-b")
	if err != nil {
		t.Fatalf("Get job-b: %v", err)
	}
	if b.Status() != string(queueapi.StatusPending) || b.Retries != 0 || b.LeaseID != "" {
		t.Fatalf("job-b after fenced runtime A alone: status=%q retries=%d lease=%q, want pending/0/\"\"", b.Status(), b.Retries, b.LeaseID)
	}

	runFencedUntil(fencedRuntime("b"), func() bool { return completed("key-b") })
}
