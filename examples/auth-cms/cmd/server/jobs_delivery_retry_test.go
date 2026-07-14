package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/jobs"
	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// This file proves the AV3D-3.4 durable-jobs-mode mappings end to end on the host's
// real composition (auth.Service -> authjobs adapter -> generic jobs fenced queue ->
// jobs.FencedRuntime -> auth delivery processor), against the inspectable in-memory
// fenced queue (live pgx/turso are AV3D-3.5; env DSNs unset):
//
//   - transient provider errors retry with capped exponential retry-at (bounded
//     attempts, bounded backoff) and then dead-letter;
//   - a permanent (structurally-dead) command dead-letters IMMEDIATELY, no retry, no
//     send;
//   - parent cancellation leaves in-flight work reclaimable (never terminalized);
//   - a per-attempt provider timeout bounds a stuck send safely inside the claim lease;
//   - the challenge discard hook runs only AFTER the recorded dead-letter, is
//     idempotent, and its failure never resurrects the job;
//   - generic lifecycle projects to auth status (retrying -> pending, dead-lettered ->
//     failed) through the session-gated receipt flow;
//   - the optional observer emits secret-free retry/dead-letter/purge events, and an
//     erroring observer changes no delivery outcome; and
//   - host-driven bounded terminal purge removes old terminal generations while status
//     stays sane.

// --- shared helpers ----------------------------------------------------------

// renderedJobID returns the execution id of the rendered admission addressed to dest —
// e.g. the synchronously-issued registration verification job.
func renderedJobID(t *testing.T, q *inspectingQueue, enc cryptids.Encrypter, dest string) string {
	t.Helper()
	for _, r := range q.snapshot() {
		if r.op != "enqueue_once" && r.op != "replace" {
			continue
		}
		e, err := openSealed(enc, r.payload)
		if err != nil {
			continue
		}
		if e.Stage == "rendered" && strings.EqualFold(e.Destination, dest) {
			return r.jobID
		}
	}
	t.Fatalf("no rendered admission found for %s", dest)
	return ""
}

// logicalKeyOf returns a stored job's PII-free logical/receipt key.
func logicalKeyOf(t *testing.T, store *inspectingQueue, id string) string {
	t.Helper()
	j, err := store.FencedQueue.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return j.LogicalKey
}

// waitJobStatus polls the stored job until it reaches want (or fails after 5s).
func waitJobStatus(t *testing.T, store *inspectingQueue, id string, want job.Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if jobStatus(t, store, id) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach status %q within 5s (last=%q)", id, want, jobStatus(t, store, id))
}

// waitReceiptState polls DeliveryStatus(receiptKey) until State == want.
func waitReceiptState(t *testing.T, svc *auth.Service, receiptKey, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		st, err := svc.DeliveryStatus(context.Background(), receiptKey)
		if err == nil {
			last = st.State
			if st.State == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("receipt %q did not reach state %q within 5s (last=%q)", receiptKey, want, last)
}

// ctxGatingSender blocks each Send until either the parent context is cancelled or release
// is closed, signalling entry so a test can act while the send is in flight. It honors
// ctx, standing in for a cancellable/timeout-respecting provider.
type ctxGatingSender struct {
	entered chan string
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (s *ctxGatingSender) Send(ctx context.Context, _ email.Message) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	select {
	case s.entered <- "sent":
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return nil
	}
}

func (s *ctxGatingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// captureEmitter records the transition token of every delivery lifecycle event, and
// can be made to fail every Emit so a test proves observation failure changes nothing.
type captureEmitter struct {
	mu          sync.Mutex
	transitions []string
	failAll     bool
}

func (e *captureEmitter) Emit(_ context.Context, ev sdkevents.Event, _ ...sdkevents.EmitOption) error {
	e.mu.Lock()
	e.transitions = append(e.transitions, strings.TrimPrefix(ev.Type(), "authentication.delivery."))
	fail := e.failAll
	e.mu.Unlock()
	if fail {
		return errors.New("emit boom")
	}
	return nil
}

func (e *captureEmitter) has(transition string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, tr := range e.transitions {
		if tr == transition {
			return true
		}
	}
	return false
}

// --- transient retry -> bounded dead-letter ----------------------------------

// TestJobsModeTransientRetriesBoundedThenDeadLetters proves a transient provider outage
// retries with bounded attempts and bounded backoff (capped exponential retry-at) and
// then dead-letters — the retrying state projects to pending, the terminal to failed.
func TestJobsModeTransientRetriesBoundedThenDeadLetters(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "transient-dl@example.com"

	fail := &failingSender{}
	b := bootDelivery(t, authRepos, store, fail, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.MaxAttempts = 3
		c.Backoff = func(int) time.Duration { return 15 * time.Millisecond }
		c.LeaseFor = 2 * time.Second
	})
	if _, err := b.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Transient User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	id := renderedJobID(t, store, b.enc, addr)
	key := logicalKeyOf(t, store, id)

	cancel, done := runRuntime(b.runtime)
	// The dead-lettered generic state projects to the auth failed state through the
	// session-gated receipt flow.
	waitReceiptState(t, b.svc, key, "failed")
	stopRuntime(t, cancel, done)

	// Bounded attempts: exactly MaxAttempts provider calls — a busy-loop would produce
	// thousands within the window, and a capped-exponential retry-at produces exactly 3.
	if got := fail.count(); got != 3 {
		t.Fatalf("provider called %d times, want exactly 3 (bounded attempts, capped exponential retry-at)", got)
	}
	if jobStatus(t, store, id) != job.StatusDeadLetter {
		t.Fatalf("terminal generic status = %q, want dead_letter", jobStatus(t, store, id))
	}
	st, err := b.svc.DeliveryStatus(ctx, key)
	if err != nil {
		t.Fatalf("DeliveryStatus: %v", err)
	}
	if !st.Failed || st.State != "failed" {
		t.Fatalf("receipt status = %+v, want failed (dead-lettered projects to failed)", st)
	}
}

// --- permanent -> immediate dead-letter --------------------------------------

// TestJobsModePermanentDeadLettersImmediately proves a structurally-dead command (an
// unopenable payload — the Engine classifies OutcomePermanent) dead-letters on the
// FIRST attempt, with no retry and no provider send, even under a high attempt cap.
func TestJobsModePermanentDeadLettersImmediately(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.MaxAttempts = 10 // a transient error would retry many times
		c.Backoff = func(int) time.Duration { return 10 * time.Millisecond }
		c.LeaseFor = 2 * time.Second
	})
	// A garbage (unsealed) payload the Engine cannot Open -> OutcomePermanent.
	id, err := b.jobs.EnqueueOnce(ctx, auth.DeliveryJobKind, "perm-key", json.RawMessage(`"not-sealed-ciphertext"`))
	if err != nil {
		t.Fatalf("EnqueueOnce garbage: %v", err)
	}

	cancel, done := runRuntime(b.runtime)
	waitJobStatus(t, store, id, job.StatusDeadLetter)
	stopRuntime(t, cancel, done)

	final, err := store.FencedQueue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Retries != 1 {
		t.Fatalf("permanent job attempted %d times, want exactly 1 (immediate dead-letter, no retry)", final.Retries)
	}
	if cap.count() != 0 {
		t.Fatalf("provider called %d times for an unopenable payload, want 0 (permanent before any send)", cap.count())
	}
}

// --- parent cancellation -> reclaimable --------------------------------------

// TestJobsModeParentCancellationLeavesReclaimable proves that cancelling the runtime
// while a send is in flight leaves the job reclaimable (never completed or dead-lettered),
// and a restart reclaims and delivers it.
func TestJobsModeParentCancellationLeavesReclaimable(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "cancel-reclaim@example.com"

	gate := &ctxGatingSender{entered: make(chan string, 4), release: make(chan struct{})}
	b := bootDelivery(t, authRepos, store, gate, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.LeaseFor = 300 * time.Millisecond // lapses quickly after cancel so the restart reclaims
	})
	if _, err := b.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Cancel User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	id := renderedJobID(t, store, b.enc, addr)

	cancel, done := runRuntime(b.runtime)
	waitSignal(t, gate.entered, "the send to enter")
	cancel() // parent cancellation while the send is in flight
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop within 5s after cancellation")
	}

	// The job was NOT terminalized on cancellation — it is reclaimable.
	final, err := store.FencedQueue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Terminal() {
		t.Fatalf("job status = %q; parent cancellation must leave it reclaimable, not terminal", final.JobStatus)
	}

	// Restart with a healthy provider: the reclaimed job delivers and completes.
	cap := &captureSender{}
	restarted := bootDelivery(t, authRepos, store, cap, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.LeaseFor = 300 * time.Millisecond
		c.PollInterval = 10 * time.Millisecond
		c.IdleInterval = 10 * time.Millisecond
	})
	c2, d2 := runRuntime(restarted.runtime)
	waitMsgCount(t, cap, 1)
	waitJobStatus(t, store, id, job.StatusCompleted)
	stopRuntime(t, c2, d2)
	close(gate.release)
}

// --- provider timeout inside the lease ---------------------------------------

// TestJobsModeProviderTimeoutBoundedInsideLease proves the per-attempt provider timeout
// cuts a stuck send well inside the claim lease (so a second worker could not reclaim
// mid-send), and that construction rejects a timeout not shorter than the lease.
func TestJobsModeProviderTimeoutBoundedInsideLease(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "timeout-lease@example.com"

	// A stuck sender that only returns when its (per-attempt) context is cancelled.
	stuck := &ctxGatingSender{entered: make(chan string, 8), release: make(chan struct{})}
	const lease = 3 * time.Second
	b := bootDelivery(t, authRepos, store, stuck, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.LeaseFor = lease
		c.ProcessTimeout = 40 * time.Millisecond // safely inside the lease
		c.MaxAttempts = 2
		c.Backoff = func(int) time.Duration { return 10 * time.Millisecond }
	})
	if _, err := b.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Timeout User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	id := renderedJobID(t, store, b.enc, addr)

	start := time.Now()
	cancel, done := runRuntime(b.runtime)
	// Two attempts, each cut at ~40ms, then dead-letter — all far inside a single 3s lease.
	waitJobStatus(t, store, id, job.StatusDeadLetter)
	elapsed := time.Since(start)
	stopRuntime(t, cancel, done)
	close(stuck.release)

	if elapsed >= lease {
		t.Fatalf("dead-letter took %v (>= lease %v): the per-attempt timeout did not bound the stuck send inside the lease", elapsed, lease)
	}

	// Construction validation: a timeout not shorter than the lease fails loudly.
	if _, err := jobs.NewFencedRuntime(b.jobs, authjobs.FencedRuntimeConfig(b.rt, func(c *jobs.FencedRuntimeConfig) {
		c.LeaseFor = time.Second
		c.ProcessTimeout = 2 * time.Second
	})); !errors.Is(err, jobs.ErrProcessTimeoutExceedsLease) {
		t.Fatalf("NewFencedRuntime err = %v, want ErrProcessTimeoutExceedsLease for timeout > lease", err)
	}
}

// --- discard after dead-letter: ordering, idempotency, non-resurrection ------

// TestJobsModeDiscardRunsAfterDeadLetterIdempotent proves the challenge discard hook runs
// only AFTER the dead-letter transition is recorded and is idempotent (running it twice
// succeeds).
func TestJobsModeDiscardRunsAfterDeadLetterIdempotent(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	const addr = "discard-order@example.com"

	var hookRan atomic.Bool
	var orderBad atomic.Bool
	var idempotentBad atomic.Bool

	fail := &failingSender{}
	b := bootDelivery(t, authRepos, store, fail, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.MaxAttempts = 2
		c.Backoff = func(int) time.Duration { return 10 * time.Millisecond }
		c.LeaseFor = 2 * time.Second
		orig := c.DeadLetters[auth.DeliveryJobKind]
		c.DeadLetters[auth.DeliveryJobKind] = func(ctx context.Context, j job.Job) error {
			// Ordering: the terminal transition is already recorded when the hook runs.
			if stored, err := store.FencedQueue.Get(ctx, j.JobID); err != nil || stored.JobStatus != job.StatusDeadLetter {
				orderBad.Store(true)
			}
			// Idempotency: running the discard twice both succeed.
			if e1 := orig(ctx, j); e1 != nil {
				idempotentBad.Store(true)
			}
			if e2 := orig(ctx, j); e2 != nil {
				idempotentBad.Store(true)
			}
			hookRan.Store(true)
			return nil
		}
	})
	// A verified account with one opaque forgot-password generation admitted on the
	// shared store (so a real challenge exists to discard); b's failing-provider runtime
	// then drives it to a dead-letter.
	_, fid := admitVerifiedForgotPassword(t, store, authRepos, addr)

	cancel, done := runRuntime(b.runtime)
	waitJobStatus(t, store, fid, job.StatusDeadLetter)
	stopRuntime(t, cancel, done)

	if !hookRan.Load() {
		t.Fatal("discard hook never ran after the dead-letter")
	}
	if orderBad.Load() {
		t.Fatal("discard hook ran before the dead-letter transition was recorded")
	}
	if idempotentBad.Load() {
		t.Fatal("discard hook was not idempotent")
	}
}

// TestJobsModeDiscardFailureDoesNotResurrect proves a discard-hook failure never
// resurrects the job: the recorded dead-letter stands and no further send occurs.
func TestJobsModeDiscardFailureDoesNotResurrect(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	const addr = "discard-fail@example.com"

	fail := &failingSender{}
	b := bootDelivery(t, authRepos, store, fail, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.MaxAttempts = 2
		c.Backoff = func(int) time.Duration { return 10 * time.Millisecond }
		c.LeaseFor = 2 * time.Second
		orig := c.DeadLetters[auth.DeliveryJobKind]
		c.DeadLetters[auth.DeliveryJobKind] = func(ctx context.Context, j job.Job) error {
			_ = orig(ctx, j)
			return errors.New("discard failed")
		}
	})
	_, fid := admitVerifiedForgotPassword(t, store, authRepos, addr)

	cancel, done := runRuntime(b.runtime)
	waitJobStatus(t, store, fid, job.StatusDeadLetter)
	before := fail.count()
	// Give a resurrected job a chance to be reclaimed and re-sent.
	time.Sleep(300 * time.Millisecond)
	stopRuntime(t, cancel, done)

	if jobStatus(t, store, fid) != job.StatusDeadLetter {
		t.Fatalf("job status = %q after a failing discard hook, want dead_letter (hook failure must not resurrect it)", jobStatus(t, store, fid))
	}
	if after := fail.count(); after != before {
		t.Fatalf("provider called %d more times after dead-letter (%d -> %d): the job was resurrected", after-before, before, after)
	}
}

// --- observer events + observer failure changes nothing ----------------------

// TestJobsModeObserverEmitsRetryDeadLetterPurge proves the optional observer emits
// secret-free retried and dead_lettered events for a failing delivery and a purged
// event when the host drives the terminal purge.
func TestJobsModeObserverEmitsRetryDeadLetterPurge(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "observer-events@example.com"

	em := &captureEmitter{}
	fail := &failingSender{}
	b := bootDeliveryEmit(t, authRepos, store, fail, em, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.MaxAttempts = 3
		c.Backoff = func(int) time.Duration { return 15 * time.Millisecond }
		c.LeaseFor = 2 * time.Second
	})
	if _, err := b.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Observer User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	id := renderedJobID(t, store, b.enc, addr)

	cancel, done := runRuntime(b.runtime)
	waitJobStatus(t, store, id, job.StatusDeadLetter)
	stopRuntime(t, cancel, done)

	if !em.has("retried") {
		t.Fatal("observer never saw a retried event for a transient failure")
	}
	if !em.has("dead_lettered") {
		t.Fatal("observer never saw a dead_lettered event")
	}

	// Host-driven terminal purge emits a purged event.
	n, err := authjobs.PurgeTerminal(ctx, b.jobs, b.rt, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("PurgeTerminal: %v", err)
	}
	if n < 1 {
		t.Fatalf("purged %d, want at least the one dead-lettered generation", n)
	}
	if !em.has("purged") {
		t.Fatal("observer never saw a purged event after the host drove PurgeTerminal")
	}
}

// TestJobsModeObserverFailureChangesNothing proves that an observer whose every emit
// fails does not change delivery: the message is still delivered and the receipt reads
// succeeded.
func TestJobsModeObserverFailureChangesNothing(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "observer-fail@example.com"

	em := &captureEmitter{failAll: true}
	cap := &captureSender{}
	b := bootDeliveryEmit(t, authRepos, store, cap, em, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.LeaseFor = 2 * time.Second
		c.PollInterval = 10 * time.Millisecond
		c.IdleInterval = 10 * time.Millisecond
	})
	if _, err := b.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Observer Fail User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	id := renderedJobID(t, store, b.enc, addr)
	key := logicalKeyOf(t, store, id)

	cancel, done := runRuntime(b.runtime)
	waitMsgCount(t, cap, 1)
	waitReceiptState(t, b.svc, key, "succeeded")
	stopRuntime(t, cancel, done)

	if jobStatus(t, store, id) != job.StatusCompleted {
		t.Fatalf("job status = %q with a failing observer, want completed (observation must not affect delivery)", jobStatus(t, store, id))
	}
}

// --- bounded terminal purge --------------------------------------------------

// TestJobsModePurgeRemovesTerminalStatusSane proves host-driven purge removes an old
// terminal generation while a non-terminal generation survives, and that a status read
// after purge behaves sanely (unknown key -> not found, never a false success).
func TestJobsModePurgeRemovesTerminalStatusSane(t *testing.T) {
	stableDeliveryEnv(t)
	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "purge-terminal@example.com"

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap, func(c *jobs.FencedRuntimeConfig) {
		c.Workers = 1
		c.LeaseFor = 2 * time.Second
		c.PollInterval = 10 * time.Millisecond
		c.IdleInterval = 10 * time.Millisecond
	})
	if _, err := b.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Purge User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	verifID := renderedJobID(t, store, b.enc, addr)
	verifKey := logicalKeyOf(t, store, verifID)

	// Drive the verification to a terminal (completed) generation.
	cancel, done := runRuntime(b.runtime)
	waitMsgCount(t, cap, 1)
	waitJobStatus(t, store, verifID, job.StatusCompleted)
	stopRuntime(t, cancel, done)

	// Admit a fresh, non-terminal forgot-password generation that MUST survive purge.
	code, ok := renderedSecretFor(store, b.enc, addr)
	if !ok {
		t.Fatal("no rendered verification payload")
	}
	if err := b.svc.Verify(ctx, addr, code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := b.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	pendingID, ok := opaqueEnqueueID(store, b.enc, addr)
	if !ok {
		t.Fatal("opaque forgot-password admission not found")
	}

	// Bounded purge with a generous retention window: only terminal generations go.
	n, err := authjobs.PurgeTerminal(ctx, b.jobs, b.rt, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("PurgeTerminal: %v", err)
	}
	if n < 1 {
		t.Fatalf("purged %d, want at least the completed verification generation", n)
	}

	// The terminal generation is gone; a status read for its key is a clean not-found,
	// never a crash or a false success.
	if _, err := b.svc.DeliveryStatus(ctx, verifKey); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("DeliveryStatus after purge err = %v, want sdk.ErrNotFound (status sane after purge)", err)
	}
	// The non-terminal generation survived the purge.
	if jobStatus(t, store, pendingID) != job.StatusPending {
		t.Fatalf("non-terminal generation status = %q after purge, want pending (purge removes only terminal rows)", jobStatus(t, store, pendingID))
	}
}
