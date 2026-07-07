package workers

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/tracing"
)

// --- fakes ---

type fakeJob struct {
	id     string
	status string
	retry  int
}

func (j fakeJob) ID() string      { return j.id }
func (j fakeJob) Status() string  { return j.status }
func (j fakeJob) RetryCount() int { return j.retry }

var _ JobStore[fakeJob] = (*memStore)(nil)

// memStore is an in-test durable queue. Claim pops the front of a pending
// slice; Fail owns retry policy (requeue below maxAttempts, dead-letter at it).
type memStore struct {
	mu sync.Mutex

	pending   []fakeJob
	retries   map[string]int
	claims    map[string]int
	completed map[string]bool
	dead      map[string]bool

	failMaxSeen []int
}

func newMemStore(jobs ...fakeJob) *memStore {
	return &memStore{
		pending:   append([]fakeJob(nil), jobs...),
		retries:   map[string]int{},
		claims:    map[string]int{},
		completed: map[string]bool{},
		dead:      map[string]bool{},
	}
}

func (s *memStore) Claim(ctx context.Context, workerID string, now time.Time) (fakeJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return fakeJob{}, ErrNoWork
	}
	job := s.pending[0]
	s.pending = s.pending[1:]
	s.claims[job.id]++
	return job, nil
}

func (s *memStore) Complete(ctx context.Context, jobID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed[jobID] = true
	return nil
}

func (s *memStore) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failMaxSeen = append(s.failMaxSeen, maxAttempts)
	s.retries[jobID]++
	if s.retries[jobID] >= maxAttempts {
		s.dead[jobID] = true
		return nil
	}
	s.pending = append(s.pending, fakeJob{id: jobID, status: "pending", retry: s.retries[jobID]})
	return nil
}

// snapshot helpers guard the maps behind the lock.
func (s *memStore) claimCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims[id]
}
func (s *memStore) isDead(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dead[id]
}
func (s *memStore) isCompleted(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completed[id]
}

// runnerPool wires a runner into a fast pool and returns both plus a Run
// channel; caller cancels ctx to stop.
func runnerPool(t *testing.T, r *Runner[fakeJob], workerCount int) (*Pool, context.CancelFunc, <-chan error) {
	t.Helper()
	pool := testPool(r.WorkFunc(), WithWorkerCount(workerCount), WithPollInterval(5*time.Millisecond), WithIdleInterval(20*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	return pool, cancel, runPool(pool, ctx)
}

// --- runner tests ---

func TestRunner_BasicLifecycleCompletes(t *testing.T) {
	store := newMemStore(fakeJob{id: "j1", status: "pending"})
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		job.status = "done"
		return job, nil
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(1))

	pool, cancel, done := runnerPool(t, runner, 1)
	_ = pool

	waitFor(t, time.Second, func() bool { return store.isCompleted("j1") })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if !store.isCompleted("j1") {
		t.Error("expected j1 to be completed")
	}
}

func TestRunner_ProcessFailureCallsFail(t *testing.T) {
	store := newMemStore(fakeJob{id: "jf", status: "pending"})
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		return job, errors.New("process error")
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(1))

	_, cancel, done := runnerPool(t, runner, 1)

	waitFor(t, time.Second, func() bool { return store.isDead("jf") })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if !store.isDead("jf") {
		t.Error("expected jf to be dead-lettered after failing at maxAttempts=1")
	}
}

func TestRunner_RetryThenFailAtMaxAttempts(t *testing.T) {
	store := newMemStore(fakeJob{id: "jr", status: "pending"})

	var attempts atomic.Int64
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		attempts.Add(1)
		return job, errors.New("always fails")
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(3))

	_, cancel, done := runnerPool(t, runner, 1)

	waitFor(t, 2*time.Second, func() bool { return store.isDead("jr") })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if got := store.claimCount("jr"); got != 3 {
		t.Errorf("expected jr claimed 3 times before dead-letter, got %d", got)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 process attempts, got %d", got)
	}
	if !store.isDead("jr") {
		t.Error("expected jr dead-lettered at maxAttempts")
	}
	store.mu.Lock()
	seen := append([]int(nil), store.failMaxSeen...)
	store.mu.Unlock()
	for _, m := range seen {
		if m != 3 {
			t.Errorf("expected Fail called with maxAttempts=3, got %d", m)
		}
	}
}

func TestRunner_RetryThenSucceed(t *testing.T) {
	store := newMemStore(fakeJob{id: "js", status: "pending"})

	var attempts atomic.Int64
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		if attempts.Add(1) < 3 {
			return job, errors.New("transient")
		}
		return job, nil
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(5))

	_, cancel, done := runnerPool(t, runner, 1)

	waitFor(t, 2*time.Second, func() bool { return store.isCompleted("js") })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 attempts (2 fail, 1 success), got %d", got)
	}
	if !store.isCompleted("js") {
		t.Error("expected js completed on the third attempt")
	}
}

func TestRunner_PanicContainment(t *testing.T) {
	store := newMemStore(fakeJob{id: "jp", status: "pending"})
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		panic("process panic")
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(1))

	_, cancel, done := runnerPool(t, runner, 1)

	waitFor(t, time.Second, func() bool { return store.isDead("jp") })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if !store.isDead("jp") {
		t.Error("expected a panicking job to be failed, not lost")
	}
}

func TestRunner_HookOrdering(t *testing.T) {
	store := newMemStore(fakeJob{id: "jh", status: "pending"})

	var mu sync.Mutex
	var order []string
	record := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		record("process")
		return job, nil
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(1))
	runner.AddPreProcessHooks(
		func(ctx context.Context, job fakeJob) error { record("pre1"); return nil },
		func(ctx context.Context, job fakeJob) error { record("pre2"); return nil },
	)
	runner.AddPostProcessHooks(
		func(ctx context.Context, job fakeJob, err error) error { record("post1"); return nil },
		func(ctx context.Context, job fakeJob, err error) error { record("post2"); return nil },
	)

	_, cancel, done := runnerPool(t, runner, 1)

	waitFor(t, time.Second, func() bool { return store.isCompleted("jh") })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	mu.Lock()
	defer mu.Unlock()
	want := []string{"pre1", "pre2", "process", "post1", "post2"}
	if len(order) < len(want) {
		t.Fatalf("expected hook order %v, got %v", want, order)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("hook order[%d] = %q, want %q (full: %v)", i, order[i], w, order)
		}
	}
}

func TestRunner_PostHookReceivesProcessError(t *testing.T) {
	store := newMemStore(fakeJob{id: "je", status: "pending"})
	procErr := errors.New("boom")
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, procErr }

	var mu sync.Mutex
	var captured error
	var called atomic.Bool

	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(1))
	runner.AddPostProcessHooks(func(ctx context.Context, job fakeJob, err error) error {
		mu.Lock()
		captured = err
		mu.Unlock()
		called.Store(true)
		return nil
	})

	_, cancel, done := runnerPool(t, runner, 1)
	waitFor(t, time.Second, func() bool { return called.Load() })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	mu.Lock()
	got := captured
	mu.Unlock()
	if !errors.Is(got, procErr) {
		t.Errorf("expected post hook to receive the process error, got %v", got)
	}
}

func TestRunner_EmptyClaimPropagatesNoWork(t *testing.T) {
	store := newMemStore() // empty
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, nil }
	runner := NewRunner[fakeJob](store, process, silentLogger())

	err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1"))
	if !errors.Is(err, ErrNoWork) {
		t.Errorf("expected ErrNoWork from an empty claim, got %v", err)
	}
}

func TestRunner_CustomClockReachesStore(t *testing.T) {
	store := newMemStore(fakeJob{id: "jc", status: "pending"})

	fixed := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var seen time.Time
	var got atomic.Bool

	clockStore := &clockSpyStore{inner: store, onClaim: func(now time.Time) {
		if got.CompareAndSwap(false, true) {
			mu.Lock()
			seen = now
			mu.Unlock()
		}
	}}

	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, nil }
	runner := NewRunner[fakeJob](clockStore, process, silentLogger(), WithClock(func() time.Time { return fixed }))

	_, cancel, done := runnerPool(t, runner, 1)
	waitFor(t, time.Second, func() bool { return got.Load() })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	mu.Lock()
	defer mu.Unlock()
	if !seen.Equal(fixed) {
		t.Errorf("expected store to see fixed clock %v, got %v", fixed, seen)
	}
}

func TestRunner_WorkerIDReachesStore(t *testing.T) {
	store := newMemStore()
	var mu sync.Mutex
	var seen string
	var got atomic.Bool

	spy := &clockSpyStore{inner: store, onClaimWorker: func(id string) {
		if got.CompareAndSwap(false, true) {
			mu.Lock()
			seen = id
			mu.Unlock()
		}
	}}

	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, nil }
	runner := NewRunner[fakeJob](spy, process, silentLogger())

	_, cancel, done := runnerPool(t, runner, 1)
	waitFor(t, time.Second, func() bool { return got.Load() })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	mu.Lock()
	defer mu.Unlock()
	if seen != "test-worker-1" {
		t.Errorf("expected workerID 'test-worker-1' at the store, got %q", seen)
	}
}

func TestRunner_ConcurrentWorkersClaimDistinctJobs(t *testing.T) {
	const n = 8
	jobs := make([]fakeJob, 0, n)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := "job-" + string(rune('a'+i))
		jobs = append(jobs, fakeJob{id: id, status: "pending"})
		ids = append(ids, id)
	}
	store := newMemStore(jobs...)

	var inProcess atomic.Int64
	var peak atomic.Int64
	release := make(chan struct{})

	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		cur := inProcess.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		<-release // hold all workers in process simultaneously
		inProcess.Add(-1)
		return job, nil
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(1))

	pool := testPool(runner.WorkFunc(), WithWorkerCount(n), WithPollInterval(2*time.Millisecond), WithIdleInterval(5*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	// Wait until all n workers are simultaneously inside process (each holding a
	// distinct claimed job), then release them.
	waitFor(t, 2*time.Second, func() bool { return inProcess.Load() == n })
	close(release)

	waitFor(t, 2*time.Second, func() bool {
		for _, id := range ids {
			if !store.isCompleted(id) {
				return false
			}
		}
		return true
	})
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if got := peak.Load(); got != n {
		t.Errorf("expected %d workers simultaneously holding distinct jobs, peak was %d", n, got)
	}
	for _, id := range ids {
		if c := store.claimCount(id); c != 1 {
			t.Errorf("job %s claimed %d times, want exactly 1 (distinct claims)", id, c)
		}
		if !store.isCompleted(id) {
			t.Errorf("job %s not completed", id)
		}
	}

	// Sanity: exactly n distinct jobs were claimed.
	store.mu.Lock()
	claimedIDs := make([]string, 0, len(store.claims))
	for id := range store.claims {
		claimedIDs = append(claimedIDs, id)
	}
	store.mu.Unlock()
	sort.Strings(claimedIDs)
	if len(claimedIDs) != n {
		t.Errorf("expected %d distinct claimed jobs, got %d (%v)", n, len(claimedIDs), claimedIDs)
	}
}

func TestRunner_DefaultMaxAttempts(t *testing.T) {
	store := newMemStore(fakeJob{id: "jd", status: "pending"})
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, errors.New("fail") }
	runner := NewRunner[fakeJob](store, process, silentLogger()) // no WithMaxAttempts

	_, cancel, done := runnerPool(t, runner, 1)
	waitFor(t, 2*time.Second, func() bool { return store.isDead("jd") })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	if got := store.claimCount("jd"); got != 3 {
		t.Errorf("expected default maxAttempts=3 (3 claims) before dead-letter, got %d", got)
	}
}

// clockSpyStore forwards to an inner memStore while observing Claim's args.
type clockSpyStore struct {
	inner         *memStore
	onClaim       func(now time.Time)
	onClaimWorker func(id string)
}

func (s *clockSpyStore) Claim(ctx context.Context, workerID string, now time.Time) (fakeJob, error) {
	if s.onClaim != nil {
		s.onClaim(now)
	}
	if s.onClaimWorker != nil {
		s.onClaimWorker(workerID)
	}
	return s.inner.Claim(ctx, workerID, now)
}
func (s *clockSpyStore) Complete(ctx context.Context, jobID string, now time.Time) error {
	return s.inner.Complete(ctx, jobID, now)
}
func (s *clockSpyStore) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	return s.inner.Fail(ctx, jobID, now, reason, maxAttempts)
}

func waitFor(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// --- tracer fake ---

// fakeTracer records span names, per-span attributes, recorded errors, and the
// finish count so tests can assert the lifecycle spans without a real backend.
type fakeTracer struct {
	mu       sync.Mutex
	started  []string
	finished int
	errs     []error
	attrs    map[string][]tracing.Attribute
}

func (t *fakeTracer) StartSpan(ctx context.Context, name string) (context.Context, tracing.SpanFinisher) {
	t.mu.Lock()
	t.started = append(t.started, name)
	t.mu.Unlock()
	return ctx, &fakeSpan{tracer: t, name: name}
}

type fakeSpan struct {
	tracer *fakeTracer
	name   string
}

func (s *fakeSpan) SetAttributes(attrs ...tracing.Attribute) {
	s.tracer.mu.Lock()
	defer s.tracer.mu.Unlock()
	if s.tracer.attrs == nil {
		s.tracer.attrs = map[string][]tracing.Attribute{}
	}
	s.tracer.attrs[s.name] = append(s.tracer.attrs[s.name], attrs...)
}
func (s *fakeSpan) RecordError(err error) {
	s.tracer.mu.Lock()
	defer s.tracer.mu.Unlock()
	s.tracer.errs = append(s.tracer.errs, err)
}
func (s *fakeSpan) Finish() {
	s.tracer.mu.Lock()
	defer s.tracer.mu.Unlock()
	s.tracer.finished++
}

// --- WithTracer tests ---

func TestRunner_WithTracer_TracesSuccessLifecycle(t *testing.T) {
	store := newMemStore(fakeJob{id: "jt", status: "pending"})
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { job.status = "done"; return job, nil }
	tr := &fakeTracer{}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(1), WithTracer(tr))

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}
	if !store.isCompleted("jt") {
		t.Fatal("expected jt completed")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, name := range []string{"job.claim", "job.process", "job.complete"} {
		if !slices.Contains(tr.started, name) {
			t.Errorf("expected a %s span, started=%v", name, tr.started)
		}
	}
	if slices.Contains(tr.started, "job.fail") {
		t.Errorf("did not expect a job.fail span on success, started=%v", tr.started)
	}
	if tr.finished != len(tr.started) {
		t.Errorf("expected every span finished: started=%d finished=%d", len(tr.started), tr.finished)
	}
	if len(tr.errs) != 0 {
		t.Errorf("expected no recorded span errors on success, got %v", tr.errs)
	}
}

func TestRunner_WithTracer_RecordsProcessErrorAndTracesFail(t *testing.T) {
	store := newMemStore(fakeJob{id: "jtf", status: "pending"})
	boom := errors.New("boom")
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, boom }
	tr := &fakeTracer{}
	runner := NewRunner[fakeJob](store, process, silentLogger(), WithMaxAttempts(1), WithTracer(tr))

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}
	if !store.isDead("jtf") {
		t.Fatal("expected jtf dead-lettered")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, name := range []string{"job.claim", "job.process", "job.fail"} {
		if !slices.Contains(tr.started, name) {
			t.Errorf("expected a %s span, started=%v", name, tr.started)
		}
	}
	if tr.finished != len(tr.started) {
		t.Errorf("expected every span finished: started=%d finished=%d", len(tr.started), tr.finished)
	}
	recorded := false
	for _, e := range tr.errs {
		if errors.Is(e, boom) {
			recorded = true
		}
	}
	if !recorded {
		t.Errorf("expected the process error recorded on the job.process span, errs=%v", tr.errs)
	}
}

// --- WithRetryWithinClaim tests ---

func TestRunner_WithRetryWithinClaim_PanicsOnNonPositiveMaxElapsed(t *testing.T) {
	for _, bad := range []time.Duration{0, -time.Second} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("expected a panic wiring maxElapsed=%v", bad)
				}
			}()
			store := newMemStore()
			process := func(ctx context.Context, job fakeJob) (fakeJob, error) { return job, nil }
			NewRunner[fakeJob](store, process, silentLogger(), WithRetryWithinClaim(3, time.Millisecond, bad))
		}()
	}
}

func TestRunner_WithRetryWithinClaim_RetryThenSucceed(t *testing.T) {
	store := newMemStore(fakeJob{id: "jrs", status: "pending"})
	var attempts atomic.Int64
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		if attempts.Add(1) < 3 {
			return job, errors.New("transient")
		}
		job.status = "done"
		return job, nil
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(),
		WithMaxAttempts(1),
		WithRetryWithinClaim(5, 2*time.Millisecond, time.Second),
	)

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 within-claim attempts (2 fail, 1 success), got %d", got)
	}
	if !store.isCompleted("jrs") {
		t.Error("expected jrs completed within the single claim")
	}
	store.mu.Lock()
	fails := len(store.failMaxSeen)
	store.mu.Unlock()
	if fails != 0 {
		t.Errorf("expected Fail never called on eventual success, got %d calls", fails)
	}
	if c := store.claimCount("jrs"); c != 1 {
		t.Errorf("expected exactly one claim (retry stays inside it), got %d", c)
	}
}

func TestRunner_WithRetryWithinClaim_ExhaustionCallsFailOnce(t *testing.T) {
	store := newMemStore(fakeJob{id: "jex", status: "pending"})
	var attempts atomic.Int64
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		attempts.Add(1)
		return job, errors.New("always fails")
	}
	runner := NewRunner[fakeJob](store, process, silentLogger(),
		WithMaxAttempts(1),
		WithRetryWithinClaim(3, 2*time.Millisecond, time.Second),
	)

	if err := runner.WorkFunc()(WithWorkerID(context.Background(), "w1")); err != nil {
		t.Fatalf("work returned error: %v", err)
	}

	if got := attempts.Load(); got != 3 {
		t.Errorf("expected exactly 3 within-claim attempts, got %d", got)
	}
	store.mu.Lock()
	seen := append([]int(nil), store.failMaxSeen...)
	store.mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("expected Fail called exactly once on exhaustion, got %d calls", len(seen))
	}
	if seen[0] != 1 {
		t.Errorf("expected Fail called with the unchanged durable maxAttempts=1, got %d", seen[0])
	}
	if c := store.claimCount("jex"); c != 1 {
		t.Errorf("expected a single claim across the within-claim retries, got %d", c)
	}
	if !store.isDead("jex") {
		t.Error("expected jex dead-lettered by the single Fail at maxAttempts=1")
	}
}

func TestRunner_WithRetryWithinClaim_CtxCancelMidBackoff(t *testing.T) {
	store := newMemStore(fakeJob{id: "jcc", status: "pending"})
	var attempts atomic.Int64
	called := make(chan struct{}, 1)
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		attempts.Add(1)
		select {
		case called <- struct{}{}:
		default:
		}
		return job, errors.New("fails")
	}
	// Long backoff (60ms) so cancellation lands during it; big maxElapsed so the
	// cap is not the limiter — ctx cancellation is what stops the loop.
	runner := NewRunner[fakeJob](store, process, silentLogger(),
		WithMaxAttempts(1),
		WithRetryWithinClaim(10, 60*time.Millisecond, 5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.WorkFunc()(WithWorkerID(ctx, "w1")) }()

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("first process attempt never ran")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("work returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("work did not return promptly after ctx cancel mid-backoff")
	}

	// The next backoff was 60ms; wait well past it and assert no further attempt.
	time.Sleep(150 * time.Millisecond)
	if got := attempts.Load(); got != 1 {
		t.Errorf("expected exactly one attempt (cancel stops the backoff), got %d", got)
	}
}

// leaseStore models one job behind a claim lease: a claim is exclusive until the
// job is completed/failed OR its lease expires (claimed_at + lease < now), at
// which point a later Claim reclaims it — exactly the memstore reclaim rule. It
// records peak concurrent live claims so a lease overrun (a second claim while
// the first holder is still retrying) is observable as concurrency > 1.
type leaseStore struct {
	mu sync.Mutex

	id    string
	lease time.Duration

	available bool
	dead      bool
	live      bool
	claimedAt time.Time

	claims         int
	concurrent     int
	peakConcurrent int
	fails          int
}

var _ JobStore[fakeJob] = (*leaseStore)(nil)

func (s *leaseStore) Claim(ctx context.Context, workerID string, now time.Time) (fakeJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return fakeJob{}, ErrNoWork
	}
	// Reclaim a stale live claim whose lease has expired. concurrent is NOT
	// decremented here: the previous holder is still running, so a reclaim now
	// is the double execution the maxElapsed guard is meant to prevent.
	if s.live && now.Sub(s.claimedAt) > s.lease {
		s.live = false
		s.available = true
	}
	if !s.available {
		return fakeJob{}, ErrNoWork
	}
	s.available = false
	s.live = true
	s.claimedAt = now
	s.claims++
	s.concurrent++
	if s.concurrent > s.peakConcurrent {
		s.peakConcurrent = s.concurrent
	}
	return fakeJob{id: s.id, status: "pending"}, nil
}

func (s *leaseStore) Complete(ctx context.Context, jobID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.concurrent > 0 {
		s.concurrent--
	}
	s.live = false
	s.dead = true
	return nil
}

func (s *leaseStore) Fail(ctx context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails++
	if s.concurrent > 0 {
		s.concurrent--
	}
	s.live = false
	if maxAttempts <= 1 {
		s.dead = true
	} else {
		s.available = true
	}
	return nil
}

func (s *leaseStore) snapshot() (claims, peak, fails int, dead bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims, s.peakConcurrent, s.fails, s.dead
}

func (s *leaseStore) isDeadNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dead
}

// TestRunner_WithRetryWithinClaim_NoLeaseOverrun is the lease-overrun regression:
// with maxElapsed sitting well below the store's lease, an always-failing job's
// within-claim retry window resolves (single Fail, dead-letter) before the lease
// can expire, so a second polling worker never re-claims the job mid-retry.
func TestRunner_WithRetryWithinClaim_NoLeaseOverrun(t *testing.T) {
	store := &leaseStore{id: "j-lease", lease: 300 * time.Millisecond, available: true}
	process := func(ctx context.Context, job fakeJob) (fakeJob, error) {
		return job, errors.New("keep failing")
	}
	// Compliant: maxElapsed (50ms) is far below the 300ms lease. Uncapped, the
	// 30ms initial delay doubling across 10 attempts would back off for hundreds
	// of ms and overrun the lease.
	runner := NewRunner[fakeJob](store, process, silentLogger(),
		WithMaxAttempts(1),
		WithRetryWithinClaim(10, 30*time.Millisecond, 50*time.Millisecond),
	)

	// Two workers: the second polls while the first holds the claim, so any lease
	// reclaim would immediately double-claim.
	pool := testPool(runner.WorkFunc(),
		WithWorkerCount(2),
		WithPollInterval(3*time.Millisecond),
		WithIdleInterval(5*time.Millisecond),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := runPool(pool, ctx)

	waitFor(t, 2*time.Second, func() bool { return store.isDeadNow() })
	cancel()
	assertReturns(t, done, 2*time.Second, "pool did not return")

	claims, peak, fails, dead := store.snapshot()
	if claims != 1 {
		t.Errorf("expected the job claimed exactly once (no lease-overrun re-claim), got %d", claims)
	}
	if peak != 1 {
		t.Errorf("expected peak concurrent claims 1 (no double execution), got %d", peak)
	}
	if fails != 1 {
		t.Errorf("expected exactly one Fail, got %d", fails)
	}
	if !dead {
		t.Error("expected the job dead-lettered by its single Fail")
	}
}
