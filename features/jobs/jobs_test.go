package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// memQueue is a minimal in-memory QueueRepository: enqueue with idempotency and
// a claim that transitions one pending job to running.
type memQueue struct {
	mu   sync.Mutex
	jobs map[string]*job.Job
	seq  int
}

func newMemQueue() *memQueue { return &memQueue{jobs: map[string]*job.Job{}} }

func (q *memQueue) Enqueue(ctx context.Context, in job.Enqueue) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	id := in.ID
	if id == "" {
		q.seq++
		id = "job-" + time.Now().Format("150405.000000000")
	}
	if _, ok := q.jobs[id]; ok {
		return job.Job{}, sdk.ErrAlreadyExists
	}
	j := job.Job{JobID: id, Kind: in.Kind, Payload: in.Payload, JobStatus: job.StatusPending, MaxAttempts: in.MaxAttempts, ScheduledFor: in.ScheduledFor}
	q.jobs[id] = &j
	return j, nil
}
func (q *memQueue) Claim(ctx context.Context, workerID string, now time.Time) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.JobStatus == job.StatusPending {
			j.JobStatus = job.StatusRunning
			return *j, nil
		}
	}
	return job.Job{}, workers.ErrNoWork
}
func (q *memQueue) Complete(ctx context.Context, jobID string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.jobs[jobID]; ok {
		j.JobStatus = job.StatusCompleted
	}
	return nil
}
func (q *memQueue) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.jobs[jobID]; ok {
		j.JobStatus = job.StatusFailed
	}
	return nil
}
func (q *memQueue) Get(ctx context.Context, id string) (job.Job, error) {
	return job.Job{}, sdk.ErrNotFound
}
func (q *memQueue) List(ctx context.Context, _ job.ListFilter, _ crud.ListRequest) (crud.Page[job.Job], error) {
	return crud.Page[job.Job]{}, nil
}

// noopSchedules satisfies schedule.Repository; only Ensure is exercised.
type noopSchedules struct {
	ensured []schedule.Ensure
}

func (s *noopSchedules) Ensure(ctx context.Context, in schedule.Ensure, next time.Time) (schedule.Schedule, error) {
	s.ensured = append(s.ensured, in)
	return schedule.Schedule{ID: "s", Name: in.Name, NextRunAt: next}, nil
}
func (s *noopSchedules) ListDue(ctx context.Context, now time.Time, limit int) ([]schedule.Schedule, error) {
	return nil, nil
}
func (s *noopSchedules) ClaimDue(ctx context.Context, id string, prev, next, now time.Time) (bool, error) {
	return false, nil
}
func (s *noopSchedules) SetLastJob(ctx context.Context, id, jobID string, now time.Time) error {
	return nil
}
func (s *noopSchedules) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	return schedule.Schedule{}, sdk.ErrNotFound
}
func (s *noopSchedules) List(ctx context.Context, _ crud.ListRequest) (crud.Page[schedule.Schedule], error) {
	return crud.Page[schedule.Schedule]{}, nil
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

func demoHandlers() map[string]HandlerFunc {
	return map[string]HandlerFunc{"demo": func(context.Context, job.Job) error { return nil }}
}

// TestWakeWiring_SharedByConstruction proves Service.Enqueue and the Runtime's
// pool signal on the SAME channel (§3.4).
func TestWakeWiring_SharedByConstruction(t *testing.T) {
	svc, err := NewService(Repositories{Queue: newMemQueue()}, Config{Handlers: demoHandlers()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rt, err := NewRuntime(svc)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if svc.wakeChan() != rt.wakeChan() {
		t.Fatal("Service and Runtime must share one wake channel by construction")
	}
}

// TestEnqueue_WakesPoolPromptly is the behavioral proof: with poll/idle set far
// longer than the deadline, only the enqueue→wake signal can make the handler
// run in time.
func TestEnqueue_WakesPoolPromptly(t *testing.T) {
	handled := make(chan string, 1)
	cfg := Config{
		Handlers: map[string]HandlerFunc{"demo": func(ctx context.Context, j job.Job) error {
			handled <- j.ID()
			return nil
		}},
		Workers:      1,
		PollInterval: 30 * time.Second, // poll would never fire within the deadline
		IdleInterval: 30 * time.Second,
	}
	svc, err := NewService(Repositories{Queue: newMemQueue()}, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rt, err := NewRuntime(svc)
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

	if _, err := svc.Enqueue(context.Background(), "demo", nil); err != nil {
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

// recordingRouter records Handle calls so a test can prove Register mounts none.
type recordingRouter struct{ calls int }

func (r *recordingRouter) Handle(method, path string, h http.HandlerFunc, mw ...web.Middleware) {
	r.calls++
}

// TestNewService_Validation covers the construction-time rejections the host
// gets at build (the seam Register no longer rebuilds).
func TestNewService_Validation(t *testing.T) {
	t.Run("nil queue", func(t *testing.T) {
		_, err := NewService(Repositories{}, Config{Handlers: demoHandlers()})
		if !errors.Is(err, ErrQueueRequired) {
			t.Fatalf("err = %v, want ErrQueueRequired", err)
		}
	})

	t.Run("nil handler value", func(t *testing.T) {
		_, err := NewService(Repositories{Queue: newMemQueue()}, Config{Handlers: map[string]HandlerFunc{"demo": nil}})
		if !errors.Is(err, ErrInvalidHandler) {
			t.Fatalf("err = %v, want ErrInvalidHandler", err)
		}
	})
}

func TestRegister_ValidationAndNoRoutes(t *testing.T) {
	router := &recordingRouter{}
	mount := feature.Mount{Router: router, Logger: discardLogger()}

	t.Run("empty handlers", func(t *testing.T) {
		svc, err := NewService(Repositories{Queue: newMemQueue()}, Config{})
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		if err := svc.Register(mount); !errors.Is(err, ErrHandlersRequired) {
			t.Fatalf("err = %v, want ErrHandlersRequired", err)
		}
	})

	t.Run("happy path mounts no routes", func(t *testing.T) {
		svc, err := NewService(Repositories{Queue: newMemQueue()}, Config{Handlers: demoHandlers()})
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		if err := svc.Register(mount); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if router.calls != 0 {
			t.Fatalf("Register registered %d routes, want 0", router.calls)
		}
	})
}

func TestNewRuntime_RequiresHandlers(t *testing.T) {
	svc, err := NewService(Repositories{Queue: newMemQueue()}, Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := NewRuntime(svc); !errors.Is(err, ErrHandlersRequired) {
		t.Fatalf("err = %v, want ErrHandlersRequired", err)
	}
}

func TestEnsureSchedule_QueueOnlyHost(t *testing.T) {
	svc, err := NewService(Repositories{Queue: newMemQueue()}, Config{Handlers: demoHandlers()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_, err = svc.EnsureSchedule(context.Background(), schedule.Ensure{Name: "x", Kind: "demo", Spec: schedule.Spec{Every: time.Minute}})
	if !errors.Is(err, ErrSchedulesNotConfigured) {
		t.Fatalf("err = %v, want ErrSchedulesNotConfigured", err)
	}
}

func TestEnsureSchedule_CronNilLoud_AtSurface(t *testing.T) {
	sched := &noopSchedules{}
	svc, err := NewService(Repositories{Queue: newMemQueue(), Schedules: sched}, Config{Handlers: demoHandlers()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_, err = svc.EnsureSchedule(context.Background(), schedule.Ensure{Name: "c", Kind: "demo", Spec: schedule.Spec{Cron: "* * * * *"}})
	if !errors.Is(err, ErrCronRequired) {
		t.Fatalf("err = %v, want ErrCronRequired", err)
	}
}

func TestEnsureSchedule_EveryPath(t *testing.T) {
	sched := &noopSchedules{}
	svc, err := NewService(Repositories{Queue: newMemQueue(), Schedules: sched}, Config{Handlers: demoHandlers()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.EnsureSchedule(context.Background(), schedule.Ensure{Name: "iv", Kind: "demo", Spec: schedule.Spec{Every: 15 * time.Second}}); err != nil {
		t.Fatalf("Every path (no parser) must succeed: %v", err)
	}
	if len(sched.ensured) != 1 {
		t.Fatalf("ensured %d schedules, want 1", len(sched.ensured))
	}
}

// runOneJob starts the runtime for cfg, enqueues one demo job, waits for it to
// run, then drains — enough for the pools to emit their operational log lines.
func runOneJob(t *testing.T, cfg Config) {
	t.Helper()
	handled := make(chan struct{}, 1)
	cfg.Handlers = map[string]HandlerFunc{"demo": func(context.Context, job.Job) error {
		select {
		case handled <- struct{}{}:
		default:
		}
		return nil
	}}

	svc, err := NewService(Repositories{Queue: newMemQueue()}, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rt, err := NewRuntime(svc)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- rt.Run(ctx) }()

	if _, err := svc.Enqueue(context.Background(), "demo", nil); err != nil {
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
// distinguishable handler-backed Config.Logger receives the runtime pools'
// operational lines, and a nil Config.Logger still falls back to slog.Default().
func TestConfigLogger_RuntimePoolsLogThroughIt(t *testing.T) {
	const poolLine = "processing job" // runner logs this before invoking a handler

	t.Run("wired logger receives pool lines", func(t *testing.T) {
		capture := &captureHandler{}
		runOneJob(t, Config{
			Handlers:     demoHandlers(),
			Logger:       slog.New(capture),
			Workers:      1,
			PollInterval: 30 * time.Second,
			IdleInterval: 30 * time.Second,
		})
		if !capture.saw(poolLine) {
			t.Fatalf("wired Config.Logger saw no %q line; messages=%v", poolLine, capture.messages())
		}
	})

	t.Run("nil logger falls back to slog.Default", func(t *testing.T) {
		capture := &captureHandler{}
		prev := slog.Default()
		slog.SetDefault(slog.New(capture))
		defer slog.SetDefault(prev)

		runOneJob(t, Config{
			Handlers:     demoHandlers(),
			Workers:      1,
			PollInterval: 30 * time.Second,
			IdleInterval: 30 * time.Second,
		})
		if !capture.saw(poolLine) {
			t.Fatalf("nil Config.Logger did not fall back to slog.Default; messages=%v", capture.messages())
		}
	})
}

// TestSeamAssertions is a runtime witness that the compile-time seams in
// logic/job hold: job.Job is a workers.Job and job.QueueRepository is a
// workers.JobStore[job.Job].
func TestSeamAssertions(t *testing.T) {
	var j workers.Job = job.Job{JobID: "x", JobStatus: job.StatusPending, Retries: 2}
	if j.ID() != "x" || j.Status() != string(job.StatusPending) || j.RetryCount() != 2 {
		t.Fatalf("workers.Job view = (%q,%q,%d)", j.ID(), j.Status(), j.RetryCount())
	}
	var _ workers.JobStore[job.Job] = newMemQueue()
}
