package workers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// fencedFakeStore is an in-test lease-fenced queue: it enforces exactly the
// FencedStore fence — Complete/Fail succeed only for the job's current lease, a
// reclaimed lease is fenced with sdk.ErrConflict, and the last holder's repeat is
// idempotent. It records which lease completed/failed each job so a test can prove
// a stale worker never marks a reclaimed job done.
type fencedFakeStore struct {
	mu   sync.Mutex
	jobs map[string]*fencedFakeJob
}

type fencedFakeJob struct {
	id                string
	status            string
	leaseID           string
	leasedUntil       time.Time
	scheduledFor      time.Time
	claims            int
	completedLease    string
	failedLease       string
	rescheduledLease  string
	rescheduledReason string
	availableAt       time.Time
}

var _ FencedStore[fakeJob] = (*fencedFakeStore)(nil)

func newFencedFakeStore() *fencedFakeStore {
	return &fencedFakeStore{jobs: map[string]*fencedFakeJob{}}
}

func (s *fencedFakeStore) enqueue(id string, scheduledFor time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = &fencedFakeJob{id: id, status: "pending", scheduledFor: scheduledFor}
}

func (s *fencedFakeStore) Claim(_ context.Context, now time.Time, leaseID string, leaseFor time.Duration) (fakeJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		due := (j.status == "pending" && !j.scheduledFor.After(now)) ||
			(j.status == "running" && !j.leasedUntil.After(now))
		if !due {
			continue
		}
		j.status = "running"
		j.leaseID = leaseID
		j.leasedUntil = now.Add(leaseFor)
		j.claims++
		return fakeJob{id: j.id, status: "running", retry: j.claims}, nil
	}
	return fakeJob{}, ErrNoWork
}

func (s *fencedFakeStore) Complete(_ context.Context, id, leaseID string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if j.status == "completed" {
		if j.completedLease == leaseID {
			return nil // idempotent from the last holder
		}
		return sdk.ErrConflict
	}
	if j.status != "running" || j.leaseID != leaseID {
		return sdk.ErrConflict
	}
	j.status = "completed"
	j.completedLease = leaseID
	return nil
}

func (s *fencedFakeStore) Fail(_ context.Context, id, leaseID, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if j.status == "dead_letter" {
		if j.failedLease == leaseID {
			return nil // idempotent from the last holder
		}
		return sdk.ErrConflict
	}
	if j.status != "running" || j.leaseID != leaseID {
		return sdk.ErrConflict
	}
	j.status = "dead_letter"
	j.failedLease = leaseID
	return nil
}

func (s *fencedFakeStore) Reschedule(_ context.Context, id, leaseID string, availableAt time.Time, reason string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if j.status != "running" || j.leaseID != leaseID {
		return sdk.ErrConflict
	}
	j.status = "pending"
	j.leaseID = ""
	j.leasedUntil = time.Time{}
	j.scheduledFor = availableAt
	j.availableAt = availableAt
	j.rescheduledLease = leaseID
	j.rescheduledReason = reason
	return nil
}

// reclaim simulates a second worker taking the job under a fresh lease after the
// prior lease lapsed — the exact race the fence guards.
func (s *fencedFakeStore) reclaim(id, leaseID string, now time.Time, leaseFor time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	j.status = "running"
	j.leaseID = leaseID
	j.leasedUntil = now.Add(leaseFor)
	j.claims++
}

func (s *fencedFakeStore) snapshot(id string) fencedFakeJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.jobs[id]
}

// TestFencedRunner_CurrentOwnerCompletes: the happy path — the runner mints a
// lease, processes, and completes the job under that lease.
func TestFencedRunner_CurrentOwnerCompletes(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("j1", time.Time{})
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, nil }
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }))

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	got := store.snapshot("j1")
	if got.status != "completed" || got.completedLease != "L1" {
		t.Errorf("job status=%q completedLease=%q, want completed/L1", got.status, got.completedLease)
	}
}

// TestFencedRunner_StaleCompletionNotReportedAsSuccess: a reclaim lands mid-process
// (another worker takes lease L2). The runner's Complete under the stale lease L1
// is fenced with sdk.ErrConflict, so the job is NOT marked completed by the stale
// worker and the current L2 execution is intact and can still complete.
func TestFencedRunner_StaleCompletionNotReportedAsSuccess(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("j1", time.Time{})

	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		// A second worker reclaims the job while this one is "processing".
		store.reclaim("j1", "L2", time.Now(), time.Minute)
		return job, nil
	}
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }))

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	got := store.snapshot("j1")
	if got.completedLease == "L1" {
		t.Error("stale worker (L1) marked the reclaimed job completed — the runner reported a fenced completion as success")
	}
	if got.status != "running" || got.leaseID != "L2" {
		t.Errorf("current execution clobbered: status=%q leaseID=%q, want running/L2", got.status, got.leaseID)
	}

	// The current owner can still complete cleanly.
	if err := store.Complete(context.Background(), "j1", "L2", time.Now()); err != nil {
		t.Errorf("current owner Complete: %v", err)
	}
	if got := store.snapshot("j1"); got.completedLease != "L2" {
		t.Errorf("completedLease=%q, want L2", got.completedLease)
	}
}

// TestFencedRunner_ShutdownLeavesReclaimable: on ctx cancellation the runner does
// not Complete or Fail the in-flight job, so it stays reclaimable; a later worker
// reclaims it and the old worker cannot then clobber it.
func TestFencedRunner_ShutdownLeavesReclaimable(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("j1", time.Time{})

	ctx, cancel := context.WithCancel(context.Background())
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		cancel() // shutdown lands during processing
		return job, ctx.Err()
	}
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }),
		WithLeaseDuration(50*time.Millisecond))

	if err := runner.WorkFunc()(WithWorkerID(ctx, "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	got := store.snapshot("j1")
	if got.status == "completed" || got.status == "dead_letter" {
		t.Errorf("shutdown terminated the job (status=%q); it must stay reclaimable", got.status)
	}

	// Lease lapses; a fresh worker reclaims it, then the old L1 worker is fenced.
	next, err := store.Claim(context.Background(), time.Now().Add(time.Second), "L2", time.Minute)
	if err != nil {
		t.Fatalf("reclaim after shutdown: %v", err)
	}
	if next.ID() != "j1" {
		t.Fatalf("reclaim = %q, want j1", next.ID())
	}
	if err := store.Complete(context.Background(), "j1", "L1", time.Now()); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("old worker Complete after reclaim: err=%v, want sdk.ErrConflict", err)
	}
	if err := store.Complete(context.Background(), "j1", "L2", time.Now()); err != nil {
		t.Errorf("current owner Complete: %v", err)
	}
}

// TestFencedRunner_EmptyClaimPropagatesNoWork: an empty fenced claim rides ErrNoWork
// through so the pool backs off.
func TestFencedRunner_EmptyClaimPropagatesNoWork(t *testing.T) {
	store := newFencedFakeStore()
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, nil }
	runner := NewFencedRunner[fakeJob](store, process, silentLogger())

	err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1"))
	if !errors.Is(err, ErrNoWork) {
		t.Errorf("expected ErrNoWork from an empty fenced claim, got %v", err)
	}
}

// TestFencedRunner_ProcessFailureFailsUnderLease: a process error takes the terminal
// Fail path under the current lease when NO retry policy is configured — the AV3D-1.1
// default, unchanged by AV3D-1.4.
func TestFencedRunner_ProcessFailureFailsUnderLease(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("jf", time.Time{})
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, errors.New("boom") }
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }))

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	got := store.snapshot("jf")
	if got.status != "dead_letter" || got.failedLease != "L1" {
		t.Errorf("job status=%q failedLease=%q, want dead_letter/L1", got.status, got.failedLease)
	}
}

// TestFencedRunner_RetryReschedulesInsteadOfDeadLetter: with a retry policy that
// elects to retry, a process failure Reschedules the job to a durable future
// retry-at (clock()+delay) and clears the lease instead of dead-lettering it — no
// busy-loop, no in-runner sleep.
func TestFencedRunner_RetryReschedulesInsteadOfDeadLetter(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("jr", time.Time{})

	fixed := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	deadLettered := false
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, errors.New("transient") }
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }),
		WithFencedClock(func() time.Time { return fixed }),
		WithFencedRetry(func(attempt int) (time.Duration, bool) { return time.Minute, attempt < 3 }))
	runner.SetDeadLetterHook(func(ctx context.Context, job fakeJob) error { deadLettered = true; return nil })

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	got := store.snapshot("jr")
	if got.status != "pending" {
		t.Errorf("job status=%q, want pending (rescheduled, not dead-lettered)", got.status)
	}
	if got.leaseID != "" {
		t.Errorf("rescheduled job leaseID=%q, want cleared", got.leaseID)
	}
	if want := fixed.Add(time.Minute); !got.availableAt.Equal(want) {
		t.Errorf("retry-at=%v, want clock()+delay %v", got.availableAt, want)
	}
	if got.rescheduledLease != "L1" || got.rescheduledReason != "transient" {
		t.Errorf("reschedule fence=%q reason=%q, want L1/transient", got.rescheduledLease, got.rescheduledReason)
	}
	if deadLettered {
		t.Error("dead-letter hook fired on a retry-at reschedule; it must fire only on a permanent dead-letter")
	}
}

// TestFencedRunner_RetryDeciderDeadLettersPermanentImmediately: with an error-aware
// decider, a process error the consumer classifies as PERMANENT dead-letters on the
// first attempt (no retry), while a transient error reschedules — the decider sees the
// error, not just the attempt count (AV3D-3.4).
func TestFencedRunner_RetryDeciderDeadLettersPermanentImmediately(t *testing.T) {
	permanent := errors.New("permanent")

	// Permanent error → dead-letter at attempt 1.
	store := newFencedFakeStore()
	store.enqueue("perm", time.Time{})
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, permanent }
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }),
		WithFencedRetryDecider(func(err error, attempt int) (time.Duration, bool) {
			if errors.Is(err, permanent) {
				return 0, false
			}
			return time.Minute, attempt < 5
		}))
	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}
	if got := store.snapshot("perm"); got.status != "dead_letter" {
		t.Errorf("permanent error status=%q, want dead_letter on the first attempt", got.status)
	}

	// Transient error → rescheduled, not dead-lettered.
	store2 := newFencedFakeStore()
	store2.enqueue("trans", time.Time{})
	fixed := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	process2 := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, errors.New("transient") }
	runner2 := NewFencedRunner[fakeJob](store2, process2, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }),
		WithFencedClock(func() time.Time { return fixed }),
		WithFencedRetryDecider(func(err error, attempt int) (time.Duration, bool) {
			if errors.Is(err, permanent) {
				return 0, false
			}
			return time.Minute, attempt < 5
		}))
	if err := runner2.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}
	if got := store2.snapshot("trans"); got.status != "pending" {
		t.Errorf("transient error status=%q, want pending (rescheduled)", got.status)
	}
}

// TestFencedRunner_RetryExhaustionDeadLetters: once the retry policy stops electing
// to retry, the process failure takes the permanent Fail path and the dead-letter
// hook fires after the transition is recorded.
func TestFencedRunner_RetryExhaustionDeadLetters(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("je", time.Time{})

	var hookJob fakeJob
	hookRan := false
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, errors.New("permanent") }
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }),
		WithFencedRetry(func(attempt int) (time.Duration, bool) { return time.Minute, false }))
	runner.SetDeadLetterHook(func(ctx context.Context, job fakeJob) error {
		// The transition must already be recorded when the hook runs.
		if got := store.snapshot(job.ID()); got.status != "dead_letter" {
			t.Errorf("hook ran before the dead-letter transition was recorded: status=%q", got.status)
		}
		hookJob = job
		hookRan = true
		return nil
	})

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	got := store.snapshot("je")
	if got.status != "dead_letter" || got.failedLease != "L1" {
		t.Errorf("job status=%q failedLease=%q, want dead_letter/L1", got.status, got.failedLease)
	}
	if !hookRan || hookJob.ID() != "je" {
		t.Errorf("dead-letter hook ran=%v job=%q, want true/je", hookRan, hookJob.ID())
	}
}

// TestFencedRunner_DeadLetterHookNotFiredOnFencedFail: when the Fail is fenced by a
// reclaimed lease (this worker did not record the transition) the dead-letter hook
// does NOT fire, and a hook error never resurrects the job.
func TestFencedRunner_DeadLetterHookNotFiredOnFencedFail(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("jd", time.Time{})

	hookRan := false
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		// A second worker reclaims the job before this one records its failure.
		store.reclaim("jd", "L2", time.Now(), time.Minute)
		return job, errors.New("boom")
	}
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }))
	runner.SetDeadLetterHook(func(ctx context.Context, job fakeJob) error { hookRan = true; return errors.New("hook boom") })

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	if hookRan {
		t.Error("dead-letter hook fired on a fenced Fail; it must fire only after this worker records the transition")
	}
	got := store.snapshot("jd")
	if got.status != "running" || got.leaseID != "L2" {
		t.Errorf("current execution clobbered by the stale worker: status=%q leaseID=%q, want running/L2", got.status, got.leaseID)
	}
}

// TestFencedRunner_DeadLetterHookErrorSwallowed: a failing dead-letter hook is logged
// but never resurrects the job — the transition stays terminal.
func TestFencedRunner_DeadLetterHookErrorSwallowed(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("jh", time.Time{})

	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, errors.New("boom") }
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }))
	runner.SetDeadLetterHook(func(ctx context.Context, job fakeJob) error { return errors.New("hook boom") })

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	got := store.snapshot("jh")
	if got.status != "dead_letter" {
		t.Errorf("job status=%q after a failing hook, want dead_letter (the hook must not resurrect it)", got.status)
	}
}

// TestFencedRunner_ProcessTimeoutBoundsAttempt: a blocking ProcessFunc is cancelled
// by WithFencedProcessTimeout well inside the claim lease, so the attempt cannot run
// until the lease lapses and a second worker reclaims the job.
func TestFencedRunner_ProcessTimeoutBoundsAttempt(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("jt", time.Time{})

	const timeout = 50 * time.Millisecond
	const lease = 2 * time.Second
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		<-ctx.Done() // a stuck provider call; the process timeout must cancel it
		return job, ctx.Err()
	}
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }),
		WithLeaseDuration(lease),
		WithFencedProcessTimeout(timeout))

	start := time.Now()
	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed >= lease {
		t.Errorf("blocked process ran %v, want bounded well inside the %v lease by the %v timeout", elapsed, lease, timeout)
	}
}

// TestFencedRunner_ProcessTimeoutCancelledPromptly: the process-timeout wait is
// context-cancellable — pool shutdown cancels the parent and a blocked attempt
// returns promptly, long before the timeout, leaving the job reclaimable.
func TestFencedRunner_ProcessTimeoutCancelledPromptly(t *testing.T) {
	store := newFencedFakeStore()
	store.enqueue("jc", time.Time{})

	ctx, cancel := context.WithCancel(context.Background())
	process := func(pctx context.Context, job fakeJob) (fakeJob, error) {
		cancel() // shutdown lands during the timeout wait
		<-pctx.Done()
		return job, pctx.Err()
	}
	runner := NewFencedRunner[fakeJob](store, process, silentLogger(),
		WithLeaseTokenFunc(func() string { return "L1" }),
		WithFencedProcessTimeout(5*time.Second))

	start := time.Now()
	if err := runner.WorkFunc()(WithWorkerID(ctx, "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Errorf("cancelled process-timeout wait returned in %v, want promptly (well below the 5s timeout)", elapsed)
	}

	// On cancellation the job is left reclaimable — not completed or dead-lettered.
	got := store.snapshot("jc")
	if got.status == "completed" || got.status == "dead_letter" {
		t.Errorf("shutdown terminated the job (status=%q); it must stay reclaimable", got.status)
	}
}
