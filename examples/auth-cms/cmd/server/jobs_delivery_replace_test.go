package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/jobs"
	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
)

// This file proves the AV3D-3.3 duplicate / resend / stale-worker properties end to
// end on the host's real jobs-mode composition (auth.Service -> authjobs.Dispatcher
// -> generic jobs fenced queue -> jobs.FencedRuntime -> auth delivery processor),
// against the inspectable in-memory fenced queue AV3D-3.2 introduced (live pgx/turso
// are AV3D-3.5; env DSNs unset). It reuses that harness — inspectingQueue,
// bootDelivery, admitVerifiedForgotPassword — and extends the queue with the
// checkpoint gates the adversarial-replacement stages need:
//
//   - a receipt key maps to a PII-free generic LOGICAL key, never an execution id,
//     and a status lookup resolves via latest-by-key;
//   - a duplicate submit while active coalesces onto ONE execution end to end;
//   - Replace mints a fresh generation and status selects the latest; and
//   - ADVERSARIAL replacement while the old work is pending, initializing,
//     checkpointed, or sending never lets the stale handler checkpoint or record a
//     success/failure after supersession, while the fresh generation proceeds and
//     status reflects it.
//
// The unavoidable already-in-flight provider race is proven honestly in the
// checkpointed and sending stages: the stale handler cannot RETRACT a send it has
// already begun, so the old proof is delivered at-least-once, but it cannot record
// its outcome (Complete/Fail are lease-fenced -> sdk.ErrConflict), the freshly issued
// challenge REPLACES the old proof (IssueChallenge is a challenge-store Replace), and
// the latest generation is what status reports.

// gatingSender is a mailer that pauses the FIRST provider send mid-call so a test can
// supersede the claim while the send is in flight (the "sending" adversarial stage),
// then records every message. Only the first send is gated; every later send (the
// fresh generation's) passes straight through.
type gatingSender struct {
	mu    sync.Mutex
	msgs  []email.Message
	gated bool

	// enter is signaled with the first message as its send begins; release lets that
	// first send complete. Both are set before the runtime starts.
	enter   chan email.Message
	release chan struct{}
}

func (s *gatingSender) Send(_ context.Context, m email.Message) error {
	s.mu.Lock()
	first := !s.gated && s.enter != nil
	if first {
		s.gated = true
	}
	s.mu.Unlock()
	if first {
		s.enter <- m
		<-s.release
	}
	s.mu.Lock()
	s.msgs = append(s.msgs, m)
	s.mu.Unlock()
	return nil
}

func (s *gatingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

func (s *gatingSender) all() []email.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]email.Message(nil), s.msgs...)
}

// widenLease bumps the claim lease well past a test's orchestration window so a
// handler paused at an adversarial gate does not lapse its lease and get reclaimed as
// a duplicate before the test triggers the replacement.
func widenLease(c *jobs.FencedRuntimeConfig) { c.LeaseFor = 5 * time.Second }

// replaceGeneration drives the exact composition Replace path a resend takes —
// authjobs.Dispatcher.Replace -> jobs.Service.Replace -> fenced-queue supersession —
// admitting payload as a fresh generation under logicalKey and returning the new
// execution id. payload is the prior generation's opaque admission bytes, so the new
// generation is a faithful re-admission of the same enumeration-safe start.
func replaceGeneration(t *testing.T, b booted, logicalKey string, payload []byte) string {
	t.Helper()
	dispatcher := authjobs.NewDispatcher(b.jobs)
	// purpose is dropped by the adapter (it rides inside the sealed envelope); the real
	// value is passed for honesty.
	newID, err := dispatcher.Replace(context.Background(), auth.DeliveryJobKind, "password_reset", logicalKey, payload)
	if err != nil {
		t.Fatalf("dispatcher.Replace: %v", err)
	}
	if newID == "" {
		t.Fatal("Replace returned an empty execution id")
	}
	return newID
}

func jobStatus(t *testing.T, store *inspectingQueue, id string) job.Status {
	t.Helper()
	j, err := store.FencedQueue.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return j.JobStatus
}

func assertJobStatus(t *testing.T, store *inspectingQueue, id string, want job.Status) {
	t.Helper()
	if got := jobStatus(t, store, id); got != want {
		t.Fatalf("job %s status = %q, want %q", id, got, want)
	}
}

// assertLatestSucceeded proves a status lookup by the receipt key resolves to the
// latest generation in a terminal, non-failed (delivered) state. "succeeded" and the
// pending/failed flags are the frozen auth status vocabulary (delivery.StatusSucceeded).
func assertLatestSucceeded(t *testing.T, svc *auth.Service, receiptKey string) {
	t.Helper()
	st, err := svc.DeliveryStatus(context.Background(), receiptKey)
	if err != nil {
		t.Fatalf("DeliveryStatus(receipt key): %v", err)
	}
	if st.State != "succeeded" {
		t.Fatalf("latest status = %q, want succeeded", st.State)
	}
	if st.Pending || st.Failed {
		t.Fatalf("latest status flags pending=%v failed=%v, want a terminal success", st.Pending, st.Failed)
	}
}

// adversarialFixture is the shared "state before the replacement" every adversarial
// stage builds on: a verified account with one opaque forgot-password generation
// admitted (pending, no runtime running). It returns the surviving store and repos,
// the pending generation's execution id, that generation's opaque admission bytes
// (the fresh generation's payload), and its PII-free logical/receipt key.
func adversarialFixture(t *testing.T, addr string) (store *inspectingQueue, authRepos auth.Repositories, gen1ID string, gen1Payload []byte, logicalKey string) {
	t.Helper()
	store = newInspectingQueue()
	authRepos = authmem.New().Repositories()
	_, gen1ID = admitVerifiedForgotPassword(t, store, authRepos, addr)
	gen1, err := store.FencedQueue.Get(context.Background(), gen1ID)
	if err != nil {
		t.Fatalf("Get admitted generation: %v", err)
	}
	if gen1.JobStatus != job.StatusPending {
		t.Fatalf("admitted generation status = %q, want pending", gen1.JobStatus)
	}
	payload := make([]byte, len(gen1.Payload))
	copy(payload, gen1.Payload)
	return store, authRepos, gen1ID, payload, gen1.LogicalKey
}

// TestReceiptKeyMapsToPIIFreeLogicalKeyNotExecutionID proves the auth receipt key is a
// generic LOGICAL key (PII-free, never an execution id) and that status resolves via
// latest-by-key: the receipt key resolves, the execution id does not.
func TestReceiptKeyMapsToPIIFreeLogicalKeyNotExecutionID(t *testing.T) {
	stableDeliveryEnv(t)

	const addr = "receipt-key@example.com"
	store, authRepos, gen1ID, _, receiptKey := adversarialFixture(t, addr)
	ctx := context.Background()

	if receiptKey == "" {
		t.Fatal("logical/receipt key is empty")
	}
	if receiptKey == gen1ID {
		t.Fatal("receipt key must be the PII-free logical key, never the execution id")
	}
	// PII-free: neither the raw identifier nor its normalized/local fragments appear in
	// the key. The key is a keyed digest suffixed with the purpose.
	for _, canary := range []string{addr, strings.ToLower(addr), "receipt-key", strings.SplitN(addr, "@", 2)[0]} {
		if strings.Contains(receiptKey, canary) {
			t.Fatalf("plaintext identifier fragment %q leaked into the receipt/logical key %q", canary, receiptKey)
		}
	}

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap)

	// The execution id is not a status key: a lookup by it does not resolve.
	if _, err := b.svc.DeliveryStatus(ctx, gen1ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("status by execution id err = %v, want ErrNotFound (status keys on the logical key, not the execution id)", err)
	}

	// The receipt key resolves — pending before delivery, succeeded after — proving
	// resolution is latest-by-key.
	st, err := b.svc.DeliveryStatus(ctx, receiptKey)
	if err != nil {
		t.Fatalf("DeliveryStatus(receipt key) before delivery: %v", err)
	}
	if !st.Pending {
		t.Fatalf("pre-delivery status = %q (pending=%v), want pending", st.State, st.Pending)
	}
	cancel, done := runRuntime(b.runtime)
	waitMsgCount(t, cap, 1)
	stopRuntime(t, cancel, done)
	assertLatestSucceeded(t, b.svc, receiptKey)
}

// TestSubmitOnceCoalescesOntoOneActiveExecution proves a duplicate start (same flow,
// same identifier) while active coalesces onto ONE execution end to end: two
// forgot-password submits leave exactly one non-terminal generation and deliver
// exactly one message.
func TestSubmitOnceCoalescesOntoOneActiveExecution(t *testing.T) {
	stableDeliveryEnv(t)

	store := newInspectingQueue()
	authRepos := authmem.New().Repositories()
	ctx := context.Background()
	const addr = "coalesce@example.com"

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap)

	if _, err := b.svc.RegisterUser(ctx, addr, "correct-horse-battery-staple", "Coalesce User"); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	// Registration verification renders synchronously at admission, so the code is
	// readable from the store before the runtime runs — no delivery needed to verify.
	code, ok := renderedSecretFor(store, b.enc, addr)
	if !ok {
		t.Fatal("no rendered verification payload for the registered address")
	}
	if err := b.svc.Verify(ctx, addr, code); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Two duplicate forgot-password starts for the same identifier while active.
	if err := b.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword #1: %v", err)
	}
	if err := b.svc.ForgotPassword(ctx, addr); err != nil {
		t.Fatalf("ForgotPassword #2: %v", err)
	}

	fid, ok := opaqueEnqueueID(store, b.enc, addr)
	if !ok {
		t.Fatal("no opaque forgot-password admission found")
	}
	key := jobKey(t, store, fid)

	if ids := activeJobIDsByKey(t, store, key); len(ids) != 1 {
		t.Fatalf("active executions for a duplicate start = %v, want exactly one", ids)
	}

	// Running the runtime delivers exactly two messages — the verification and the ONE
	// coalesced reset; a duplicate submit that made a second job would deliver a third.
	cancel, done := runRuntime(b.runtime)
	waitMsgCount(t, cap, 2)
	time.Sleep(200 * time.Millisecond) // catch an erroneous duplicate from the second submit
	stopRuntime(t, cancel, done)
	if got := cap.count(); got != 2 {
		t.Fatalf("delivered %d messages (verification + resets), want exactly 2 — a duplicate submit made a second job", got)
	}
	assertJobStatus(t, store, fid, job.StatusCompleted)
	assertLatestSucceeded(t, b.svc, key)
}

// TestReplaceCreatesFreshGenerationStatusSelectsLatest proves a resend supersedes the
// prior active generation and mints a fresh one, and that status selects the latest:
// the old generation ends superseded, the new one delivers and completes, and the
// receipt key reports the latest (succeeded), not the superseded generation (canceled).
func TestReplaceCreatesFreshGenerationStatusSelectsLatest(t *testing.T) {
	stableDeliveryEnv(t)

	const addr = "replace-latest@example.com"
	store, authRepos, gen1ID, gen1Payload, key := adversarialFixture(t, addr)

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap)

	gen2ID := replaceGeneration(t, b, key, gen1Payload)
	if gen2ID == gen1ID {
		t.Fatal("Replace must mint a fresh execution id, not reuse the superseded one")
	}
	assertJobStatus(t, store, gen1ID, job.StatusSuperseded)

	cancel, done := runRuntime(b.runtime)
	waitMsgCount(t, cap, 1)
	time.Sleep(200 * time.Millisecond) // a superseded generation must never deliver
	stopRuntime(t, cancel, done)

	if got := cap.count(); got != 1 {
		t.Fatalf("delivered %d messages, want exactly one (only the fresh generation)", got)
	}
	assertJobStatus(t, store, gen1ID, job.StatusSuperseded)
	assertJobStatus(t, store, gen2ID, job.StatusCompleted)
	assertLatestSucceeded(t, b.svc, key)
}

// TestAdversarialReplaceWhilePending supersedes the old work while it is still PENDING
// (pre-claim): the stale generation is terminal at once and never claimed, and only
// the fresh generation is delivered.
func TestAdversarialReplaceWhilePending(t *testing.T) {
	stableDeliveryEnv(t)

	const addr = "adv-pending@example.com"
	store, authRepos, gen1ID, gen1Payload, key := adversarialFixture(t, addr)

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap)

	// Replace before the runtime ever claims the pending generation.
	gen2ID := replaceGeneration(t, b, key, gen1Payload)
	assertJobStatus(t, store, gen1ID, job.StatusSuperseded)

	cancel, done := runRuntime(b.runtime)
	waitMsgCount(t, cap, 1)
	time.Sleep(200 * time.Millisecond)
	stopRuntime(t, cancel, done)

	if got := cap.count(); got != 1 {
		t.Fatalf("delivered %d messages, want exactly one (the superseded pending generation must not deliver)", got)
	}
	assertJobStatus(t, store, gen1ID, job.StatusSuperseded)
	assertJobStatus(t, store, gen2ID, job.StatusCompleted)
	assertLatestSucceeded(t, b.svc, key)
}

// TestAdversarialReplaceWhileInitializing supersedes the old work while it is
// INITIALIZING (claimed, resolved/rendered, but paused entering its checkpoint). The
// stale handler's checkpoint is fenced with sdk.ErrConflict, so it never sends; the
// fresh generation is delivered and status reflects it.
func TestAdversarialReplaceWhileInitializing(t *testing.T) {
	stableDeliveryEnv(t)

	const addr = "adv-initializing@example.com"
	store, authRepos, gen1ID, gen1Payload, key := adversarialFixture(t, addr)

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap, widenLease)

	reached := make(chan struct{}, 1)
	release := make(chan struct{})
	doneErr := make(chan error, 1)
	store.setGates(
		func(id string) {
			if id != gen1ID {
				return
			}
			select {
			case reached <- struct{}{}:
			default:
			}
			<-release
		},
		func(id string, err error) {
			if id != gen1ID {
				return
			}
			select {
			case doneErr <- err:
			default:
			}
		},
	)

	cancel, done := runRuntime(b.runtime)
	<-reached // the stale handler has resolved/rendered and is entering its checkpoint
	gen2ID := replaceGeneration(t, b, key, gen1Payload)
	close(release) // let the stale checkpoint run — it must now be fenced

	if err := <-doneErr; !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale checkpoint after supersession err = %v, want sdk.ErrConflict", err)
	}

	waitMsgCount(t, cap, 1)
	time.Sleep(200 * time.Millisecond)
	stopRuntime(t, cancel, done)

	if got := cap.count(); got != 1 {
		t.Fatalf("delivered %d messages, want exactly one (the fenced stale handler must not send)", got)
	}
	assertJobStatus(t, store, gen1ID, job.StatusSuperseded)
	assertJobStatus(t, store, gen2ID, job.StatusCompleted)
	assertLatestSucceeded(t, b.svc, key)
}

// TestAdversarialReplaceWhileCheckpointed supersedes the old work after it has
// CHECKPOINTED (rendered payload durable) but before its send. The already-in-flight
// provider race is honest here: the stale handler still delivers the old proof (it
// cannot re-check supersession between a committed checkpoint and its send), but it
// cannot record success — Complete is lease-fenced -> conflict — so the generation
// stays superseded, the freshly issued challenge replaces the old proof, and status
// reflects the latest generation.
func TestAdversarialReplaceWhileCheckpointed(t *testing.T) {
	stableDeliveryEnv(t)

	const addr = "adv-checkpointed@example.com"
	store, authRepos, gen1ID, gen1Payload, key := adversarialFixture(t, addr)

	cap := &captureSender{}
	b := bootDelivery(t, authRepos, store, cap, widenLease)

	reached := make(chan struct{}, 1)
	release := make(chan struct{})
	store.setGates(nil, func(id string, err error) {
		if id != gen1ID || err != nil {
			return
		}
		select {
		case reached <- struct{}{}:
			<-release
		default:
		}
	})

	cancel, done := runRuntime(b.runtime)
	<-reached // the stale handler has committed its checkpoint and is about to send
	gen2ID := replaceGeneration(t, b, key, gen1Payload)
	close(release) // let the stale send proceed (the in-flight race), then be fenced at Complete

	// Both the stale in-flight proof and the fresh generation's proof are delivered.
	waitMsgCount(t, cap, 2)
	time.Sleep(200 * time.Millisecond)
	stopRuntime(t, cancel, done)

	if got := cap.count(); got != 2 {
		t.Fatalf("delivered %d messages, want exactly two (the in-flight stale proof + the fresh proof)", got)
	}
	msgs := cap.all()
	if sameRenderedMessage(msgs[0], msgs[1]) {
		t.Fatal("the fresh generation must carry a different secret — the reissued challenge supersedes the old proof")
	}
	// The stale handler could NOT record success after supersession.
	assertJobStatus(t, store, gen1ID, job.StatusSuperseded)
	assertJobStatus(t, store, gen2ID, job.StatusCompleted)
	assertLatestSucceeded(t, b.svc, key)
}

// TestAdversarialReplaceWhileSending supersedes the old work MID provider call. The
// stale handler cannot retract the in-flight send (the honest race), so the old proof
// is delivered, but it cannot record success — Complete is fenced -> conflict — so the
// generation stays superseded while the fresh generation delivers and status reflects it.
func TestAdversarialReplaceWhileSending(t *testing.T) {
	stableDeliveryEnv(t)

	const addr = "adv-sending@example.com"
	store, authRepos, gen1ID, gen1Payload, key := adversarialFixture(t, addr)

	sender := &gatingSender{enter: make(chan email.Message, 1), release: make(chan struct{})}
	b := bootDelivery(t, authRepos, store, sender, widenLease)

	cancel, done := runRuntime(b.runtime)
	<-sender.enter // the stale handler's provider call is in flight
	gen2ID := replaceGeneration(t, b, key, gen1Payload)
	close(sender.release) // let the in-flight send complete; it will then be fenced at Complete

	waitCount(t, sender.count, 2, "delivered messages")
	time.Sleep(200 * time.Millisecond)
	stopRuntime(t, cancel, done)

	if got := sender.count(); got != 2 {
		t.Fatalf("delivered %d messages, want exactly two (the in-flight stale proof + the fresh proof)", got)
	}
	msgs := sender.all()
	if sameRenderedMessage(msgs[0], msgs[1]) {
		t.Fatal("the fresh generation must carry a different secret — the reissued challenge supersedes the old proof")
	}
	assertJobStatus(t, store, gen1ID, job.StatusSuperseded)
	assertJobStatus(t, store, gen2ID, job.StatusCompleted)
	assertLatestSucceeded(t, b.svc, key)
}

// jobKey returns the logical key of the execution id.
func jobKey(t *testing.T, store *inspectingQueue, id string) string {
	t.Helper()
	j, err := store.FencedQueue.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return j.LogicalKey
}

// activeJobIDsByKey returns the ids of every non-terminal execution holding key,
// proving how many active generations a key has (exactly one after submit-once).
func activeJobIDsByKey(t *testing.T, store *inspectingQueue, key string) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	for _, id := range store.knownIDs() {
		j, err := store.FencedQueue.Get(ctx, id)
		if err != nil {
			continue
		}
		if j.LogicalKey == key && !j.Terminal() {
			out = append(out, j.JobID)
		}
	}
	return out
}

// waitCount blocks until count() reaches n or a deadline, for a sender that is not the
// captureSender waitMsgCount targets.
func waitCount(t *testing.T, count func() int, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if count() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected at least %d %s within 5s, got %d", n, what, count())
}
