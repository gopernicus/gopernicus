package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/jobs/domain/job"
	"github.com/gopernicus/gopernicus/pockets/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/pockets/jobs/memstore"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// The #37 regressions: N runtimes sharing ONE queue (and one schedules table)
// with disjoint handler registries. Before the fix, whichever runtime polled
// first claimed the other's job, failed it "no handler registered", and — at
// MaxAttempts 1, as configured here — dead-lettered it outright. Now the job of
// the other kind stays pending, uncharged, until its own runtime runs.

// kindService builds a Service over shared stores handling exactly one kind.
func kindService(t *testing.T, queue job.QueueRepository, sched schedule.Repository, kind string, logger *slog.Logger) *Service {
	t.Helper()
	svc, err := NewService(Repositories{Queue: queue, Schedules: sched}, Config{
		Handlers:     map[string]HandlerFunc{kind: func(context.Context, job.Job) error { return nil }},
		Workers:      1,
		MaxAttempts:  1,
		PollInterval: 5 * time.Millisecond,
		IdleInterval: 5 * time.Millisecond,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("NewService(%s): %v", kind, err)
	}
	return svc
}

// runUntil runs svc's runtime until cond holds (or the deadline), then drains it.
func runUntil(t *testing.T, svc *Service, cond func() bool) {
	t.Helper()
	rt, err := NewRuntime(svc)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
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

func mustGet(t *testing.T, queue job.QueueRepository, id string) job.Job {
	t.Helper()
	j, err := queue.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return j
}

func TestRuntime_ClaimsOnlyRegisteredKinds(t *testing.T) {
	ctx := context.Background()
	queue := memstore.NewQueue()
	capture := &captureHandler{}
	svcA := kindService(t, queue, nil, "a", slog.New(capture))
	svcB := kindService(t, queue, nil, "b", nil)

	for _, kind := range []string{"a", "b"} {
		if _, err := svcA.EnqueueJob(ctx, job.Enqueue{ID: "job-" + kind, Kind: kind}); err != nil {
			t.Fatalf("Enqueue %s: %v", kind, err)
		}
	}

	// Only runtime A runs: job-a completes, job-b is untouched.
	runUntil(t, svcA, func() bool { return mustGet(t, queue, "job-a").Status() == string(job.StatusCompleted) })
	if !capture.saw("jobs runtime: claiming kinds") {
		t.Errorf("runtime did not log its claimed kinds; messages=%v", capture.messages())
	}
	b := mustGet(t, queue, "job-b")
	if b.Status() != string(job.StatusPending) || b.RetryCount() != 0 || b.WorkerName != "" {
		t.Fatalf("job-b after runtime A alone: status=%q retries=%d worker=%q, want pending/0/\"\" (must wait for its own runtime)", b.Status(), b.RetryCount(), b.WorkerName)
	}

	// Runtime B then owns it.
	runUntil(t, svcB, func() bool { return mustGet(t, queue, "job-b").Status() == string(job.StatusCompleted) })
}

func TestScheduler_FiresOnlyRegisteredKinds(t *testing.T) {
	ctx := context.Background()
	queue := memstore.NewQueue()
	sched := memstore.NewSchedules()
	svcA := kindService(t, queue, sched, "a", nil)
	svcB := kindService(t, queue, sched, "b", nil)

	// Seed both schedules already due (straight into the repository — the
	// Service would compute NextRunAt = now + Every).
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
	runUntil(t, svcA, func() bool { return fired(sa.ID) })
	gotB, _ := sched.Get(ctx, sb.ID)
	if !gotB.NextRunAt.Equal(sb.NextRunAt) || gotB.LastJobID != "" {
		t.Fatalf("sb after runtime A alone: next=%v last=%q, want next unchanged %v and no job (must wait for its own runtime)", gotB.NextRunAt, gotB.LastJobID, sb.NextRunAt)
	}
	page, err := queue.List(ctx, job.ListFilter{Kind: "b"}, crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List b: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("runtime A enqueued %d jobs of kind b, want 0", len(page.Items))
	}
	gotA, _ := sched.Get(ctx, sa.ID)
	if a := mustGet(t, queue, gotA.LastJobID); a.Status() != string(job.StatusCompleted) {
		t.Fatalf("sa's fired job status = %q, want completed", a.Status())
	}

	// Runtime B then fires sb.
	runUntil(t, svcB, func() bool { return fired(sb.ID) })
}

func TestFencedRuntime_ClaimsOnlyRegisteredKinds(t *testing.T) {
	ctx := context.Background()
	fq := memstore.NewFencedQueue()
	svc, err := NewService(Repositories{FencedQueue: fq}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	fencedRuntime := func(kind string) *FencedRuntime {
		rt, err := NewFencedRuntime(svc, FencedRuntimeConfig{
			Workers:      1,
			MaxAttempts:  1,
			PollInterval: 5 * time.Millisecond,
			IdleInterval: 5 * time.Millisecond,
			Handlers:     map[string]FencedHandlerFunc{kind: func(context.Context, FencedClaim) error { return nil }},
		})
		if err != nil {
			t.Fatalf("NewFencedRuntime(%s): %v", kind, err)
		}
		return rt
	}
	runFencedUntil := func(rt *FencedRuntime, cond func() bool) {
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
		s, err := svc.LatestStatusByKey(ctx, key)
		return err == nil && s == "completed"
	}

	for _, kind := range []string{"a", "b"} {
		if _, err := fq.EnqueueOnce(ctx, job.Enqueue{ID: "job-" + kind, Kind: kind, LogicalKey: "key-" + kind, Payload: json.RawMessage(`"x"`)}); err != nil {
			t.Fatalf("EnqueueOnce %s: %v", kind, err)
		}
	}

	runFencedUntil(fencedRuntime("a"), func() bool { return completed("key-a") })
	b, err := fq.Get(ctx, "job-b")
	if err != nil {
		t.Fatalf("Get job-b: %v", err)
	}
	if b.Status() != string(job.StatusPending) || b.Retries != 0 || b.LeaseID != "" {
		t.Fatalf("job-b after fenced runtime A alone: status=%q retries=%d lease=%q, want pending/0/\"\"", b.Status(), b.Retries, b.LeaseID)
	}

	runFencedUntil(fencedRuntime("b"), func() bool { return completed("key-b") })
}

func TestHandlerKinds_SortedAndNilForEmpty(t *testing.T) {
	got := handlerKinds(map[string]HandlerFunc{"zeta": nil, "alpha": nil, "mid": nil})
	if want := []string{"alpha", "mid", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("handlerKinds = %v, want %v", got, want)
	}
	if got := handlerKinds(map[string]HandlerFunc{}); got != nil {
		t.Fatalf("handlerKinds(empty) = %v, want nil", got)
	}
}

func TestRegister_LogsKinds(t *testing.T) {
	capture := &captureHandler{}
	svc := kindService(t, memstore.NewQueue(), nil, "a", nil)
	if err := svc.Register(pocket.Mount{Router: &recordingRouter{}, Logger: slog.New(capture)}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !capture.saw("registered jobs pocket") {
		t.Fatalf("Register did not log; messages=%v", capture.messages())
	}
}
