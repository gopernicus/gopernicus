package delivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// ---------------------------------------------------------------------------
// AV3D-4.1 — bounded in-process delivery runtime: fixed pool + bounded admission
//
// These prove the pool is FIXED (concurrency never exceeds the configured worker
// count), admission is BOUNDED (a full queue rejects with a typed capacity error
// within the admission deadline; a shutting-down runtime rejects with a typed closed
// error), shutdown DRAINS within a bound (a provider ignoring cancellation cannot
// stall it), in-flight provider calls OBSERVE cancellation, and the pool starts NO
// goroutine until Run — construction alone processes nothing. All run under -race.
// ---------------------------------------------------------------------------

// countingProcessor records how many items it handled and its peak concurrency. Its
// Handle blocks until released or ctx is canceled, so a test can pin several workers
// in flight simultaneously and measure the peak.
type countingProcessor struct {
	active   atomic.Int32
	maxSeen  atomic.Int32
	handled  atomic.Int32
	release  chan struct{}
	entered  chan struct{} // signalled once per Handle entry (buffered by the test)
	observed atomic.Bool   // set true if a Handle saw ctx cancellation
}

func newCountingProcessor(bufferedEntries int) *countingProcessor {
	return &countingProcessor{
		release: make(chan struct{}),
		entered: make(chan struct{}, bufferedEntries),
	}
}

func (p *countingProcessor) Handle(ctx context.Context, _ string, _ []byte, _ int, checkpoint func(ctx context.Context, sealed []byte) error) error {
	n := p.active.Add(1)
	defer p.active.Add(-1)
	for {
		m := p.maxSeen.Load()
		if n <= m || p.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	p.handled.Add(1)
	// Exercise the (no-op) checkpoint the runtime passes so the seam is on the tested path.
	_ = checkpoint(ctx, nil)
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		p.observed.Store(true)
		return ctx.Err()
	}
}

func (p *countingProcessor) Discard(context.Context, string, []byte) error { return nil }

// TestInProcessRuntimeFixedPoolSize proves concurrency never exceeds the configured
// worker count even when far more items are admitted than there are workers.
func TestInProcessRuntimeFixedPoolSize(t *testing.T) {
	t.Parallel()
	const workers = 3
	const submitted = 12

	q := NewInProcessQueue(InProcessQueueConfig{Capacity: submitted})
	proc := newCountingProcessor(submitted)
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: workers, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	// Distinct logical keys: AV3D-4.2 coalesces duplicate submits under one key, so a
	// fixed-pool proof must admit distinct keys to get `submitted` independent items.
	for i := 0; i < submitted; i++ {
		if _, err := q.Submit(context.Background(), "authentication.delivery", "verification", fmt.Sprintf("k%d", i), []byte("p")); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	// Wait until exactly `workers` items are in flight and blocked.
	waitFor(t, time.Second, func() bool { return proc.active.Load() == workers })
	// Hold them there briefly so any (bug) over-scheduling would show.
	time.Sleep(30 * time.Millisecond)
	if peak := proc.maxSeen.Load(); peak > workers {
		t.Fatalf("peak concurrency %d exceeded fixed pool size %d", peak, workers)
	}
	if peak := proc.maxSeen.Load(); peak != workers {
		t.Fatalf("peak concurrency %d, want exactly %d (pool underutilized)", peak, workers)
	}

	close(proc.release) // let the pinned workers finish and drain the rest
	waitFor(t, 2*time.Second, func() bool { return proc.handled.Load() == submitted })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if peak := proc.maxSeen.Load(); peak != workers {
		t.Fatalf("final peak concurrency %d, want %d", peak, workers)
	}
}

// TestInProcessQueueAdmissionCapacity proves a full bounded queue rejects with a typed
// capacity error within the admission deadline (never an unbounded block, never a
// silent drop) — proven WITHOUT running the pool so capacity is deterministic.
func TestInProcessQueueAdmissionCapacity(t *testing.T) {
	t.Parallel()
	const deadline = 60 * time.Millisecond
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 1, AdmissionDeadline: deadline})

	// Distinct keys: AV3D-4.2 coalesces a duplicate under one key (it admits no second
	// item), so the capacity proof uses two keys that each genuinely try to admit.
	if _, err := q.Submit(context.Background(), "k", "p", "lk-a", []byte("a")); err != nil {
		t.Fatalf("first submit (fills the single slot): %v", err)
	}

	start := time.Now()
	_, err := q.Submit(context.Background(), "k", "p", "lk-b", []byte("b"))
	elapsed := time.Since(start)

	if !errors.Is(err, ErrDeliveryCapacity) {
		t.Fatalf("err = %v, want ErrDeliveryCapacity", err)
	}
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("ErrDeliveryCapacity must wrap the backpressure kind (sdk.ErrUnavailable); err = %v", err)
	}
	if errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("ErrDeliveryCapacity must NOT wrap sdk.ErrConflict (backpressure is retriable unchanged, not state contention); err = %v", err)
	}
	if elapsed < deadline {
		t.Fatalf("admission returned after %v, before the %v deadline — it did not wait the bounded window", elapsed, deadline)
	}
	if elapsed > deadline+500*time.Millisecond {
		t.Fatalf("admission took %v, far beyond the %v deadline — it blocked longer than bounded", elapsed, deadline)
	}
}

// TestInProcessRuntimeClosedRejectsAdmission proves that once the runtime is shut down,
// admission fails closed with the typed closed error.
func TestInProcessRuntimeClosedRejectsAdmission(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	proc := newCountingProcessor(4)
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 1, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	// Ensure the pool is running, then shut it down.
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	_, err = q.Submit(context.Background(), "k", "p", "lk", []byte("x"))
	if !errors.Is(err, ErrDeliveryClosed) {
		t.Fatalf("post-shutdown submit err = %v, want ErrDeliveryClosed", err)
	}
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("ErrDeliveryClosed must wrap the backpressure kind (sdk.ErrUnavailable); err = %v", err)
	}
	if errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("ErrDeliveryClosed must NOT wrap sdk.ErrConflict (shutdown is retriable unchanged, not state contention); err = %v", err)
	}
}

// stuckProcessor blocks in Handle ignoring context cancellation until explicitly
// released — it models a provider that does not honor cancellation, proving the
// shutdown drain is BOUNDED regardless.
type stuckProcessor struct {
	release chan struct{}
	entered chan struct{}
}

func (p *stuckProcessor) Handle(_ context.Context, _ string, _ []byte, _ int, _ func(ctx context.Context, sealed []byte) error) error {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release // deliberately ignores ctx: only an explicit release ends it
	return nil
}

func (p *stuckProcessor) Discard(context.Context, string, []byte) error { return nil }

// TestInProcessRuntimeShutdownDrainBounded proves Run returns within the shutdown
// deadline even when an in-flight provider ignores cancellation.
func TestInProcessRuntimeShutdownDrainBounded(t *testing.T) {
	t.Parallel()
	const shutdown = 80 * time.Millisecond
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 2})
	proc := &stuckProcessor{release: make(chan struct{}), entered: make(chan struct{}, 1)}
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 1, ShutdownDeadline: shutdown})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), "k", "p", "lk", []byte("x")); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	// Wait until the stuck worker is in flight, then request shutdown.
	select {
	case <-proc.entered:
	case <-time.After(time.Second):
		t.Fatal("worker never entered Handle")
	}
	start := time.Now()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(shutdown + 500*time.Millisecond):
		close(proc.release) // avoid leaking the stuck goroutine
		t.Fatalf("Run did not return within the bounded shutdown window (%v)", shutdown)
	}
	elapsed := time.Since(start)
	close(proc.release) // release the stuck worker now that Run returned
	if elapsed < shutdown {
		t.Fatalf("Run returned after %v, before the %v shutdown deadline — the stuck worker was not given the bounded window", elapsed, shutdown)
	}
}

// TestInProcessRuntimeCancellationPropagates proves an in-flight provider call observes
// context cancellation on shutdown (the worker context is derived from Run's ctx and
// propagated into Handle).
func TestInProcessRuntimeCancellationPropagates(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 2})
	proc := newCountingProcessor(2)
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 1, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), "k", "p", "lk", []byte("x")); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	select {
	case <-proc.entered:
	case <-time.After(time.Second):
		t.Fatal("worker never entered Handle")
	}
	// The processor is blocked selecting on ctx.Done and release; cancellation must
	// unblock it via ctx.
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !proc.observed.Load() {
		t.Fatal("in-flight Handle did not observe context cancellation on shutdown")
	}
}

// TestInProcessRuntimeStartsNothingUntilRun proves construction and admission start no
// goroutine that processes work: only Run drains the queue. This is the delivery-layer
// proof behind the standing invariant "Register starts no goroutines" — NewService
// builds this runtime but never runs it.
func TestInProcessRuntimeStartsNothingUntilRun(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	proc := newCountingProcessor(4)
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 2, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	// Distinct keys so AV3D-4.2 coalescing does not collapse the three items into one.
	for i := 0; i < 3; i++ {
		if _, err := q.Submit(context.Background(), "k", "p", fmt.Sprintf("lk%d", i), []byte("x")); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}
	// Without Run, nothing must be processed.
	time.Sleep(50 * time.Millisecond)
	if got := proc.handled.Load(); got != 0 {
		t.Fatalf("processor handled %d items before Run — the pool started without a host lifecycle", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	close(proc.release) // let items complete immediately
	waitFor(t, 2*time.Second, func() bool { return proc.handled.Load() == 3 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestInProcessRuntimeConstructionNegatives proves nil collaborators fail construction.
func TestInProcessRuntimeConstructionNegatives(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{})
	if _, err := NewInProcessRuntime(nil, newCountingProcessor(1), InProcessRuntimeConfig{}); !errors.Is(err, ErrInProcessQueueRequired) {
		t.Fatalf("nil queue err = %v, want ErrInProcessQueueRequired", err)
	}
	if _, err := NewInProcessRuntime(q, nil, InProcessRuntimeConfig{}); !errors.Is(err, ErrInProcessProcessorRequired) {
		t.Fatalf("nil processor err = %v, want ErrInProcessProcessorRequired", err)
	}
}

// TestInProcessRuntimeSingleRun proves Run is single-owner: a concurrent second Run
// returns ErrInProcessAlreadyRunning.
func TestInProcessRuntimeSingleRun(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 1})
	proc := newCountingProcessor(1)
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 1, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	// Wait until the first Run has claimed ownership.
	waitFor(t, time.Second, func() bool { return rt.running.Load() })
	if err := rt.Run(context.Background()); !errors.Is(err, ErrInProcessAlreadyRunning) {
		t.Fatalf("second Run err = %v, want ErrInProcessAlreadyRunning", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestInProcessQueueLatestStatusUnknown proves an unknown / never-admitted key is an
// honest unknown (sdk.ErrNotFound) — never a fabricated pending/succeeded/failed.
func TestInProcessQueueLatestStatusUnknown(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{})
	if _, err := q.LatestStatus(context.Background(), "never-admitted"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("LatestStatus err = %v, want sdk.ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// AV3D-4.2 — process-local idempotency, replacement, and status retention
//
// These prove the arbiter under one lock: a duplicate Submit COALESCES onto the active
// generation (no second item), Replace SUPERSEDES prior generations (a queued old
// generation never starts, an in-flight old generation's checkpoint/completion is
// fenced), latest-by-key status is deterministic, status retention is bounded by a
// maximum entry count and a TTL (the map never grows with process lifetime), and
// concurrent Submit/Replace on one key yields exactly one winner. All run under -race.
// ---------------------------------------------------------------------------

// successProcessor exercises the fenced checkpoint then completes, recording every
// completed execution ID by logical key so a test can assert which generation won.
type successProcessor struct {
	mu        sync.Mutex
	completed map[string][]string // key inferred by the test via the returned execID set
	byExecID  map[string]bool
}

func newSuccessProcessor() *successProcessor {
	return &successProcessor{completed: map[string][]string{}, byExecID: map[string]bool{}}
}

func (p *successProcessor) Handle(ctx context.Context, execID string, _ []byte, _ int, checkpoint func(ctx context.Context, sealed []byte) error) error {
	if err := checkpoint(ctx, nil); err != nil {
		return err // fenced: superseded before checkpoint
	}
	p.mu.Lock()
	p.byExecID[execID] = true
	p.mu.Unlock()
	return nil
}

func (p *successProcessor) Discard(context.Context, string, []byte) error { return nil }

func (p *successProcessor) didComplete(execID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byExecID[execID]
}

func (p *successProcessor) completedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byExecID)
}

// TestInProcessQueueSubmitOnceCoalesces proves a duplicate Submit under an active
// logical key returns the SAME execution ID and admits NO second item.
func TestInProcessQueueSubmitOnceCoalesces(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 8})

	e1, err := q.Submit(context.Background(), "k", "p", "same", []byte("a"))
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	e2, err := q.Submit(context.Background(), "k", "p", "same", []byte("b"))
	if err != nil {
		t.Fatalf("duplicate Submit: %v", err)
	}
	if e1 != e2 {
		t.Fatalf("duplicate Submit returned a different execution ID (%q vs %q) — it did not coalesce", e1, e2)
	}
	if n := len(q.items); n != 1 {
		t.Fatalf("queue holds %d items after a coalesced duplicate, want exactly 1", n)
	}
	if state, err := q.LatestStatus(context.Background(), "same"); err != nil || state != string(work.StatusPending) {
		t.Fatalf("LatestStatus = (%q, %v), want (%q, nil)", state, err, string(work.StatusPending))
	}
}

// TestInProcessQueueReplaceSupersedesQueued proves Replace mints a fresh generation and
// the superseded, still-queued generation NEVER starts.
func TestInProcessQueueReplaceSupersedesQueued(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 8})

	e1, err := q.Submit(context.Background(), "k", "p", "key", []byte("v1"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	e2, err := q.Replace(context.Background(), "k", "p", "key", []byte("v2"))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if e1 == e2 {
		t.Fatal("Replace returned the same execution ID as the superseded generation")
	}

	proc := newSuccessProcessor()
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 1, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	waitFor(t, 2*time.Second, func() bool {
		state, _ := q.LatestStatus(context.Background(), "key")
		return state == string(work.StatusCompleted)
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if proc.didComplete(e1) {
		t.Fatal("the superseded queued generation started and completed — Replace did not prevent it")
	}
	if !proc.didComplete(e2) {
		t.Fatal("the replacement generation never completed")
	}
	if state, _ := q.LatestStatus(context.Background(), "key"); state != string(work.StatusCompleted) {
		t.Fatalf("latest status = %q, want %q (the replacement outcome)", state, string(work.StatusCompleted))
	}
}

// fenceProbeProcessor pins its FIRST invocation (the superseded-to-be generation) so a
// test can Replace it mid-flight, then records the error its post-release checkpoint
// returns. Every later invocation checkpoints and completes normally.
type fenceProbeProcessor struct {
	first    atomic.Bool
	entered  chan string
	release  chan struct{}
	cpErr    chan error
	mu       sync.Mutex
	byExecID map[string]bool
}

func newFenceProbeProcessor() *fenceProbeProcessor {
	return &fenceProbeProcessor{
		entered:  make(chan string, 1),
		release:  make(chan struct{}),
		cpErr:    make(chan error, 1),
		byExecID: map[string]bool{},
	}
}

func (p *fenceProbeProcessor) Handle(ctx context.Context, execID string, _ []byte, _ int, checkpoint func(ctx context.Context, sealed []byte) error) error {
	if p.first.CompareAndSwap(false, true) {
		p.entered <- execID
		<-p.release
		err := checkpoint(ctx, nil) // fenced: this generation was superseded meanwhile
		p.cpErr <- err
		return err
	}
	if err := checkpoint(ctx, nil); err != nil {
		return err
	}
	p.mu.Lock()
	p.byExecID[execID] = true
	p.mu.Unlock()
	return nil
}

func (p *fenceProbeProcessor) Discard(context.Context, string, []byte) error { return nil }

func (p *fenceProbeProcessor) didComplete(execID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byExecID[execID]
}

// TestInProcessQueueReplaceFencesInFlight proves a superseded IN-FLIGHT generation's
// checkpoint is fenced (ErrDeliverySuperseded) and it records no transition, while the
// replacement generation completes and owns the latest-by-key status.
func TestInProcessQueueReplaceFencesInFlight(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 8})
	proc := newFenceProbeProcessor()
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 2, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	e1, err := q.Submit(context.Background(), "k", "p", "key", []byte("v1"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	var pinned string
	select {
	case pinned = <-proc.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first generation never entered Handle")
	}
	if pinned != e1 {
		t.Fatalf("pinned generation = %q, want the submitted %q", pinned, e1)
	}

	// Supersede the in-flight generation while it is pinned in Handle.
	e2, err := q.Replace(context.Background(), "k", "p", "key", []byte("v2"))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}

	// The replacement completes on the second worker.
	waitFor(t, 2*time.Second, func() bool { return proc.didComplete(e2) })

	// Release the pinned superseded generation; its checkpoint must be fenced.
	close(proc.release)
	select {
	case cpErr := <-proc.cpErr:
		if !errors.Is(cpErr, ErrDeliverySuperseded) {
			t.Fatalf("superseded checkpoint err = %v, want ErrDeliverySuperseded", cpErr)
		}
		if !errors.Is(cpErr, sdk.ErrConflict) {
			t.Fatalf("ErrDeliverySuperseded must wrap sdk.ErrConflict; err = %v", cpErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the pinned generation never reported its checkpoint result")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if proc.didComplete(e1) {
		t.Fatal("the superseded in-flight generation recorded a completion")
	}
	if state, _ := q.LatestStatus(context.Background(), "key"); state != string(work.StatusCompleted) {
		t.Fatalf("latest status = %q, want %q (the replacement outcome, not the fenced stale one)", state, string(work.StatusCompleted))
	}
}

// TestInProcessQueueLatestByKeyDeterministic proves each independent key resolves to its
// own terminal completed status.
func TestInProcessQueueLatestByKeyDeterministic(t *testing.T) {
	t.Parallel()
	const keys = 16
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: keys})
	proc := newSuccessProcessor()
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 4, ShutdownDeadline: time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	keyNames := make([]string, keys)
	for i := range keyNames {
		keyNames[i] = fmt.Sprintf("key-%d", i)
		if _, err := q.Submit(context.Background(), "k", "p", keyNames[i], []byte("x")); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	waitFor(t, 3*time.Second, func() bool { return proc.completedCount() == keys })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, k := range keyNames {
		if state, err := q.LatestStatus(context.Background(), k); err != nil || state != string(work.StatusCompleted) {
			t.Fatalf("LatestStatus(%q) = (%q, %v), want (%q, nil)", k, state, err, string(work.StatusCompleted))
		}
	}
}

// TestInProcessQueueRetentionMaxEntries proves the latest-by-key status map stays at or
// below the configured maximum entry count across many more keys than the cap — retention
// never grows with process lifetime. It drives the arbiter directly (drain + settle) so
// the bound is proven without timing on the pool.
func TestInProcessQueueRetentionMaxEntries(t *testing.T) {
	t.Parallel()
	const maxEntries = 8
	const keys = 200
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 32, StatusMaxEntries: maxEntries})

	for i := 0; i < keys; i++ {
		key := fmt.Sprintf("key-%d", i)
		execID, err := q.Submit(context.Background(), "k", "p", key, []byte("x"))
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		item := <-q.items // drain the admitted item so the channel never saturates
		if item.executionID != execID {
			t.Fatalf("drained item %q != submitted execution %q", item.executionID, execID)
		}
		q.settle(item, nil) // terminalize so the entry becomes eviction-eligible
		if got := q.entryCount(); got > maxEntries {
			t.Fatalf("after %d keys the retention map holds %d entries, exceeding the max of %d", i+1, got, maxEntries)
		}
	}
	if got := q.entryCount(); got != maxEntries {
		t.Fatalf("final retention map holds %d entries, want exactly the max of %d", got, maxEntries)
	}
}

// TestInProcessQueueRetentionTTL proves a terminal status past its TTL reads as unknown
// (sdk.ErrNotFound) and is evicted, so retention does not persist for the process lifetime.
func TestInProcessQueueRetentionTTL(t *testing.T) {
	t.Parallel()
	clk := newClock()
	const ttl = time.Minute
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 8, StatusTTL: ttl, Now: clk.now})

	execID, err := q.Submit(context.Background(), "k", "p", "key", []byte("x"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	item := <-q.items
	if item.executionID != execID {
		t.Fatalf("drained %q != %q", item.executionID, execID)
	}
	q.settle(item, nil)

	if state, err := q.LatestStatus(context.Background(), "key"); err != nil || state != string(work.StatusCompleted) {
		t.Fatalf("pre-TTL LatestStatus = (%q, %v), want (%q, nil)", state, err, string(work.StatusCompleted))
	}

	clk.advance(ttl + time.Second)

	if _, err := q.LatestStatus(context.Background(), "key"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("post-TTL LatestStatus err = %v, want sdk.ErrNotFound", err)
	}
	if got := q.entryCount(); got != 0 {
		t.Fatalf("post-TTL retention map holds %d entries, want 0 (the expired entry was evicted)", got)
	}
}

// TestInProcessQueueConcurrentSubmitReplaceOneWinner proves that many concurrent Submit
// and Replace operations on ONE logical key settle to exactly one retained generation
// with a deterministic terminal status — never two simultaneously-current generations,
// never a lost or duplicated record. Race detection covers the shared arbiter state.
func TestInProcessQueueConcurrentSubmitReplaceOneWinner(t *testing.T) {
	t.Parallel()
	const goroutines = 32
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4 * goroutines})
	proc := newSuccessProcessor()
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{Workers: 4, ShutdownDeadline: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		replace := i%2 == 0
		go func(replace bool) {
			defer wg.Done()
			<-start
			if replace {
				_, _ = q.Replace(context.Background(), "k", "p", "one-key", []byte("r"))
			} else {
				_, _ = q.Submit(context.Background(), "k", "p", "one-key", []byte("s"))
			}
		}(replace)
	}
	close(start)
	wg.Wait()

	// After all admissions settle there must be exactly one retained generation for the
	// key, and it must reach a deterministic terminal completed status.
	waitFor(t, 3*time.Second, func() bool {
		if len(q.items) != 0 {
			return false
		}
		state, err := q.LatestStatus(context.Background(), "one-key")
		return err == nil && state == string(work.StatusCompleted)
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := q.entryCount(); got != 1 {
		t.Fatalf("retention map holds %d entries for one key, want exactly 1 (one winner)", got)
	}
	if state, err := q.LatestStatus(context.Background(), "one-key"); err != nil || state != string(work.StatusCompleted) {
		t.Fatalf("final LatestStatus = (%q, %v), want (%q, nil)", state, err, string(work.StatusCompleted))
	}
	if proc.completedCount() < 1 {
		t.Fatal("no generation completed for the contended key")
	}
}

// waitFor polls cond until it is true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %v", timeout)
	}
}

// compile assertion: the fakes satisfy the runtime's processor seam.
var (
	_ InProcessProcessor = (*countingProcessor)(nil)
	_ InProcessProcessor = (*stuckProcessor)(nil)
	_ InProcessProcessor = (*successProcessor)(nil)
	_ InProcessProcessor = (*fenceProbeProcessor)(nil)
)
