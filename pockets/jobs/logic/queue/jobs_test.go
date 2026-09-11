package queue_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// memQueue is a minimal in-memory QueueRepository: enqueue with idempotency and
// a claim that transitions one pending job to running.
type memQueue struct {
	mu   sync.Mutex
	jobs map[string]*queueapi.Job
	seq  int
}

func newMemQueue() *memQueue { return &memQueue{jobs: map[string]*queueapi.Job{}} }

func (q *memQueue) Enqueue(ctx context.Context, in queueapi.Enqueue) (queueapi.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	id := in.ID
	if id == "" {
		q.seq++
		id = "job-" + time.Now().Format("150405.000000000")
	}
	if _, ok := q.jobs[id]; ok {
		return queueapi.Job{}, sdk.ErrAlreadyExists
	}
	j := queueapi.Job{JobID: id, Kind: in.Kind, Payload: in.Payload, JobStatus: queueapi.StatusPending, MaxAttempts: in.MaxAttempts, ScheduledFor: in.ScheduledFor}
	q.jobs[id] = &j
	return j, nil
}
func (q *memQueue) Claim(ctx context.Context, workerID string, now time.Time, _ []string) (queueapi.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.JobStatus == queueapi.StatusPending {
			j.JobStatus = queueapi.StatusRunning
			return *j, nil
		}
	}
	return queueapi.Job{}, workers.ErrNoWork
}
func (q *memQueue) Complete(ctx context.Context, jobID string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.jobs[jobID]; ok {
		j.JobStatus = queueapi.StatusCompleted
	}
	return nil
}
func (q *memQueue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.jobs[jobID]; ok {
		j.JobStatus = queueapi.StatusFailed
	}
	return nil
}
func (q *memQueue) Get(ctx context.Context, id string) (queueapi.Job, error) {
	return queueapi.Job{}, sdk.ErrNotFound
}
func (q *memQueue) List(ctx context.Context, _ queueapi.ListFilter, _ list.Request) (list.Page[queueapi.Job], error) {
	return list.Page[queueapi.Job]{}, nil
}

// noopSchedules satisfies schedule.Repository; only Ensure is exercised.
type noopSchedules struct {
	ensured []schedule.Ensure
}

func (s *noopSchedules) Ensure(ctx context.Context, in schedule.Ensure, next time.Time) (schedule.Schedule, error) {
	s.ensured = append(s.ensured, in)
	return schedule.Schedule{ID: "s", Name: in.Name, NextRunAt: next}, nil
}
func (s *noopSchedules) ListDue(ctx context.Context, now time.Time, limit int, _ []string) ([]schedule.Schedule, error) {
	return nil, nil
}
func (s *noopSchedules) ClaimDue(ctx context.Context, _ schedule.Schedule, next, now time.Time) (bool, error) {
	return false, nil
}
func (s *noopSchedules) AckOccurrence(ctx context.Context, jobID string, now time.Time) error {
	return nil
}
func (s *noopSchedules) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	return schedule.Schedule{}, sdk.ErrNotFound
}
func (s *noopSchedules) List(ctx context.Context, _ list.Request) (list.Page[schedule.Schedule], error) {
	return list.Page[schedule.Schedule]{}, nil
}
func (s *noopSchedules) SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	return nil
}
func (s *noopSchedules) Delete(ctx context.Context, id string) error { return nil }

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// captureHandler is a distinguishable slog.Handler that records the message of
// every record it handles, so a test can prove which logger the runtime pools
// wrote through.
type captureHandler struct {
	mu   sync.Mutex
	msgs []string
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, r.Message)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// saw reports whether any recorded message equals msg.
func (h *captureHandler) saw(msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.msgs {
		if m == msg {
			return true
		}
	}
	return false
}

func (h *captureHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.msgs...)
}

func demoHandlers() map[string]queueapi.HandlerFunc {
	return map[string]queueapi.HandlerFunc{"demo": func(context.Context, queueapi.Job) error { return nil }}
}

// TestEnqueue_WakesPoolPromptly is the behavioral proof: with poll/idle set far
// longer than the deadline, only the enqueue→wake signal can make the handler
// run in time.
func TestEnqueue_WakesPoolPromptly(t *testing.T) {
	handled := make(chan string, 1)
	cfg := runtimeTestConfig{
		Handlers: map[string]queueapi.HandlerFunc{"demo": func(ctx context.Context, j queueapi.Job) error {
			handled <- j.ID()
			return nil
		}},
		Workers:      1,
		PollInterval: 30 * time.Second, // poll would never fire within the deadline
		IdleInterval: 30 * time.Second,
	}
	svcRuntimeConfig := cfg
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: newMemQueue()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rt, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- rt.Run(ctx) }()

	// Let the pool run its initial tick against the empty queue and settle into
	// the long idle interval, so pickup below is attributable to the wake.
	time.Sleep(150 * time.Millisecond)

	if _, err := svc.Queue.Enqueue(context.Background(), "demo", nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	select {
	case <-handled:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not run promptly after enqueue — wake wiring is broken")
	}

	cancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not drain after cancel")
	}
}

// TestNewService_Validation covers the construction-time rejections the host
// gets at build (the seam Register no longer rebuilds).
func TestNewService_Validation(t *testing.T) {
	t.Run("nil queue", func(t *testing.T) {
		_, err := pocketjobs.New(pocketjobs.Repositories{})
		if !errors.Is(err, queueapi.ErrQueueRequired) {
			t.Fatalf("err = %v, want queueapi.ErrQueueRequired", err)
		}
	})

	t.Run("nil handler value", func(t *testing.T) {
		svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: newMemQueue()})
		if err != nil {
			t.Fatal(err)
		}
		_, err = queueapi.NewRuntime(svc.Queue, map[string]queueapi.HandlerFunc{"demo": nil})
		if !errors.Is(err, queueapi.ErrInvalidHandler) {
			t.Fatalf("err = %v, want queueapi.ErrInvalidHandler", err)
		}
	})
}

func TestNewRuntime_RequiresHandlers(t *testing.T) {
	svcRuntimeConfig := runtimeTestConfig{}
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: newMemQueue()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig); !errors.Is(err, queueapi.ErrHandlersRequired) {
		t.Fatalf("err = %v, want queueapi.ErrHandlersRequired", err)
	}
}

func TestEnsureSchedule_QueueOnlyHost(t *testing.T) {
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: newMemQueue()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc.Schedules != nil {
		t.Fatal("queue-only assembly unexpectedly built scheduling")
	}
}

func TestEnsureSchedule_CronNilLoud_AtSurface(t *testing.T) {
	sched := &noopSchedules{}
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: newMemQueue(), Schedules: sched})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_, err = svc.Schedules.EnsureSchedule(context.Background(), schedule.Ensure{Name: "c", Kind: "demo", Spec: schedule.Spec{Cron: "* * * * *"}})
	if !errors.Is(err, schedule.ErrCronRequired) {
		t.Fatalf("err = %v, want schedule.ErrCronRequired", err)
	}
}

func TestEnsureSchedule_EveryPath(t *testing.T) {
	sched := &noopSchedules{}
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: newMemQueue(), Schedules: sched})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.Schedules.EnsureSchedule(context.Background(), schedule.Ensure{Name: "iv", Kind: "demo", Spec: schedule.Spec{Every: 15 * time.Second}}); err != nil {
		t.Fatalf("Every path (no parser) must succeed: %v", err)
	}
	if len(sched.ensured) != 1 {
		t.Fatalf("ensured %d schedules, want 1", len(sched.ensured))
	}
}

// runOneJob starts the runtime for cfg, enqueues one demo job, waits for it to
// run, then drains — enough for the pools to emit their operational log lines.
func runOneJob(t *testing.T, cfg runtimeTestConfig) {
	t.Helper()
	handled := make(chan struct{}, 1)
	cfg.Handlers = map[string]queueapi.HandlerFunc{"demo": func(context.Context, queueapi.Job) error {
		select {
		case handled <- struct{}{}:
		default:
		}
		return nil
	}}

	svcRuntimeConfig := cfg
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: newMemQueue()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rt, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- rt.Run(ctx) }()

	if _, err := svc.Queue.Enqueue(context.Background(), "demo", nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-handled:
	case <-time.After(3 * time.Second):
		t.Fatal("demo job did not run")
	}

	cancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not drain after cancel")
	}
}

// TestConfigLogger_RuntimePoolsLogThroughIt proves the task-5 knob: a
// distinguishable handler-backed runtimeTestConfig.Logger receives the runtime pools'
// operational lines, and a nil runtimeTestConfig.Logger still falls back to slog.Default().
func TestConfigLogger_RuntimePoolsLogThroughIt(t *testing.T) {
	const poolLine = "processing job" // runner logs this before invoking a handler

	t.Run("wired logger receives pool lines", func(t *testing.T) {
		capture := &captureHandler{}
		runOneJob(t, runtimeTestConfig{
			Handlers:     demoHandlers(),
			Logger:       slog.New(capture),
			Workers:      1,
			PollInterval: 30 * time.Second,
			IdleInterval: 30 * time.Second,
		})
		if !capture.saw(poolLine) {
			t.Fatalf("wired runtimeTestConfig.Logger saw no %q line; messages=%v", poolLine, capture.messages())
		}
	})

	t.Run("nil logger falls back to slog.Default", func(t *testing.T) {
		capture := &captureHandler{}
		prev := slog.Default()
		slog.SetDefault(slog.New(capture))
		defer slog.SetDefault(prev)

		runOneJob(t, runtimeTestConfig{
			Handlers:     demoHandlers(),
			Workers:      1,
			PollInterval: 30 * time.Second,
			IdleInterval: 30 * time.Second,
		})
		if !capture.saw(poolLine) {
			t.Fatalf("nil runtimeTestConfig.Logger did not fall back to slog.Default; messages=%v", capture.messages())
		}
	})
}

// TestSeamAssertions is a runtime witness that the compile-time seam in
// logic/queue holds: queueapi.Job is a workers.Job. (queueapi.QueueRepository is no longer
// a workers.JobStore — its Claim carries the kinds filter; the runtime's
// kind-scoped adapter is what the kernel drives.)
func TestSeamAssertions(t *testing.T) {
	var j workers.FencedJob = queueapi.Job{JobID: "x", JobStatus: queueapi.StatusPending, Retries: 2}
	if j.ID() != "x" || j.RetryCount() != 2 {
		t.Fatalf("workers.FencedJob view = (%q,%d)", j.ID(), j.RetryCount())
	}
}

func (s *noopSchedules) ListPending(context.Context, int, []string) ([]schedule.Occurrence, error) {
	return nil, nil
}

func (s *noopSchedules) RecordAttempt(context.Context, string) (bool, error) { return true, nil }
