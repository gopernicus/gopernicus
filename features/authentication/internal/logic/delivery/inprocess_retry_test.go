package delivery

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deliverycmd "github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/command"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// ---------------------------------------------------------------------------
// AV3D-4.3 — checkpoint, retry, timeout, and terminal cleanup
//
// These prove the bounded in-process runtime applies the SAME provider timeout, error
// classification, attempt cap, capped context-cancellable backoff, observer transitions,
// and terminal challenge discard as jobs mode — running the SAME transport-neutral
// processor (command.Engine) behind the fixed pool. A retry within the process reuses the
// byte-identical checkpointed secret; the attempt cap and permanent failures dead-letter
// then discard (only after the terminal is recorded); a superseded generation records no
// transition; and a shutdown interrupts a backoff wait promptly with no goroutine leak.
// There is NO claim lease across restart — nothing here implies durability or cross-
// instance single execution. All run under -race.
// ---------------------------------------------------------------------------

// sealOpaqueEnvelope seals an enumeration-safe opaque start (no rendered content) under
// key, the payload a forgot-password/passwordless start admits. The real processor opens
// it and resolves+renders it off the request path through the Initializer.
func sealOpaqueEnvelope(t *testing.T, key string) []byte {
	t.Helper()
	env, err := deliverycmd.NewOpaque(identity.KindPhone, "characterization", key, "+15550001111")
	if err != nil {
		t.Fatalf("NewOpaque: %v", err)
	}
	b, err := deliverycmd.Seal(fakeEncrypter{}, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return b
}

// sealRenderedEnvelope seals an already-rendered command carrying secret in its body.
func sealRenderedEnvelope(t *testing.T, key, secret string) []byte {
	t.Helper()
	env, err := deliverycmd.NewRendered(identity.KindPhone, "characterization", key, "+15550001111", "", "Sign in: "+secret, "", secret)
	if err != nil {
		t.Fatalf("NewRendered: %v", err)
	}
	b, err := deliverycmd.Seal(fakeEncrypter{}, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return b
}

// phoneRouter builds a Router over a stub email sender plus a single phone notifier,
// so the delivery processor renders + sends phone-kind commands.
func phoneRouter(t *testing.T, notifier notify.Notifier) *Router {
	t.Helper()
	return newRouter(t, &stubSender{}, map[string]notify.Notifier{identity.KindPhone: notifier})
}

// renderedPhoneEnvelope is a rendered phone Envelope whose Body carries secret verbatim,
// so a retry that resends the byte-identical body is observable.
func renderedPhoneEnvelope(secret string) Envelope {
	return Envelope{Destination: "+15550001111", Body: "Sign in: " + secret, Secret: secret}
}

// fakeInitializer is a delivery.Initializer that resolves opaque work to a fixed
// rendered Envelope, counting resolve and discard calls so a test proves opaque work is
// initialized exactly once (checkpoint reuse) and the challenge is discarded once on a
// terminal failure.
type fakeInitializer struct {
	rendered    Envelope
	deliver     bool
	mu          sync.Mutex
	initCount   int
	discardCall int
}

func (f *fakeInitializer) Initialize(_ context.Context, _, _ string, _ Envelope) (Envelope, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initCount++
	if !f.deliver {
		return Envelope{}, false, nil
	}
	return f.rendered, true, nil
}

func (f *fakeInitializer) Discard(_ context.Context, _, _ string, _ Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discardCall++
	return nil
}

func (f *fakeInitializer) inits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.initCount
}

func (f *fakeInitializer) discards() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discardCall
}

// recordingNotifier records every send's body and fails the first failFirst calls
// (returning a transient error) so a test drives a bounded retry to a byte-identical
// resend or a terminal dead-letter.
type recordingNotifier struct {
	kind      string
	failFirst int
	mu        sync.Mutex
	sends     []string
}

func (n *recordingNotifier) Kind() string { return n.kind }

func (n *recordingNotifier) Notify(_ context.Context, _ identity.Address, msg notify.Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sends = append(n.sends, msg.Body)
	if len(n.sends) <= n.failFirst {
		return errBoom
	}
	return nil
}

func (n *recordingNotifier) bodies() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.sends))
	copy(out, n.sends)
	return out
}

func (n *recordingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sends)
}

// newInProcProcessor builds the REAL jobs-mode processor (the command.Engine wrapper) the
// bounded pool runs — the same one jobs mode uses — over a phone-notifier Router, so the
// runtime's retry/timeout/discard/observer behavior is proven end to end, not against a
// hand-rolled classifier. A nil Initializer covers pre-rendered-only cases.
func newInProcProcessor(t *testing.T, notifier notify.Notifier, init Initializer, obs deliverycmd.Observer, cfg deliverycmd.Config) *JobsProcessor {
	t.Helper()
	p, err := NewJobsProcessor(JobsProcessorDeps{
		Encrypter:   fakeEncrypter{},
		Router:      phoneRouter(t, notifier),
		Initializer: init,
		Observer:    obs,
		Config:      cfg,
	})
	if err != nil {
		t.Fatalf("NewJobsProcessor: %v", err)
	}
	return p
}

// runRuntime launches rt.Run and returns a stop func that cancels it and waits for a
// clean drain, failing on a non-nil Run error.
func runRuntime(t *testing.T, rt *InProcessRuntime) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
}

// TestInProcessRetryReusesSameSecret proves a transient failure retries WITHIN the process
// reusing the byte-identical checkpointed secret rather than re-resolving/re-minting: the
// opaque start is initialized exactly once and the two provider sends carry the same body.
func TestInProcessRetryReusesSameSecret(t *testing.T) {
	t.Parallel()
	notifier := &recordingNotifier{kind: identity.KindPhone, failFirst: 1} // first send fails → retry
	init := &fakeInitializer{rendered: renderedPhoneEnvelope("REUSE-SECRET"), deliver: true}
	proc := newInProcProcessor(t, notifier, init, nil, deliverycmd.Config{})
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{
		Workers:          1,
		ShutdownDeadline: 2 * time.Second,
		Backoff:          func(int) time.Duration { return time.Millisecond },
	})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), JobKind, "characterization", "reuse", sealOpaqueEnvelope(t, "reuse")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	stop := runRuntime(t, rt)
	waitFor(t, 3*time.Second, func() bool {
		state, _ := q.LatestStatus(context.Background(), "reuse")
		return state == string(work.StatusCompleted)
	})
	stop()

	if init.inits() != 1 {
		t.Fatalf("opaque work initialized %d times, want 1 (checkpoint reused, not re-resolved)", init.inits())
	}
	bodies := notifier.bodies()
	if len(bodies) != 2 {
		t.Fatalf("provider sends = %d, want 2 (one failed + one retried)", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("retry sent a different payload: %q != %q (the same secret must be resent, never re-minted)", bodies[0], bodies[1])
	}
	if !strings.Contains(bodies[1], "REUSE-SECRET") {
		t.Fatalf("retry did not carry the checkpointed secret: %q", bodies[1])
	}
}

// terminalProbe is a fake processor that fails every Handle (transient by default, or
// permanent) and, on Discard, records the latest-by-key status it observes — so a test can
// assert the runtime DEAD-LETTERS (records the terminal) BEFORE it discards, discards
// exactly once, and caps attempts. It never checkpoints.
type terminalProbe struct {
	q         *InProcessQueue
	key       string
	permanent bool
	handles   atomic.Int32
	discards  atomic.Int32
	discardAt atomic.Value // string: status observed inside Discard
}

func (p *terminalProbe) Handle(_ context.Context, _ string, _ []byte, _ int, _ func(context.Context, []byte) error) error {
	p.handles.Add(1)
	if p.permanent {
		return &jobFailure{reason: "permanent-boom", permanent: true}
	}
	return errors.New("transient-boom")
}

func (p *terminalProbe) Discard(ctx context.Context, _ string, _ []byte) error {
	st, _ := p.q.LatestStatus(ctx, p.key)
	p.discardAt.Store(st)
	p.discards.Add(1)
	return nil
}

// TestInProcessAttemptCapDeadLettersThenDiscards proves a persistently transient failure
// exhausts the finite attempt cap, records a terminal dead-letter, and THEN discards the
// challenge exactly once — the discard observing the already-recorded dead_letter.
func TestInProcessAttemptCapDeadLettersThenDiscards(t *testing.T) {
	t.Parallel()
	const maxAttempts = 3
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	proc := &terminalProbe{q: q, key: "cap"}
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{
		Workers:          1,
		ShutdownDeadline: 2 * time.Second,
		MaxAttempts:      maxAttempts,
		Backoff:          func(int) time.Duration { return time.Millisecond },
	})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), JobKind, "characterization", "cap", []byte("x")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	stop := runRuntime(t, rt)
	waitFor(t, 3*time.Second, func() bool { return proc.discards.Load() == 1 })
	stop()

	if got := proc.handles.Load(); got != maxAttempts {
		t.Fatalf("handled %d attempts, want exactly the cap of %d", got, maxAttempts)
	}
	if got := proc.discards.Load(); got != 1 {
		t.Fatalf("discarded %d times, want exactly 1", got)
	}
	if st, _ := proc.discardAt.Load().(string); st != string(work.StatusDeadLetter) {
		t.Fatalf("discard observed status %q, want %q (discard must follow the recorded terminal)", st, string(work.StatusDeadLetter))
	}
	if state, _ := q.LatestStatus(context.Background(), "cap"); state != string(work.StatusDeadLetter) {
		t.Fatalf("final status = %q, want %q", state, string(work.StatusDeadLetter))
	}
}

// TestInProcessPermanentDeadLettersImmediately proves a permanent failure dead-letters on
// the FIRST attempt (no retry) and discards the challenge, without spending the budget.
func TestInProcessPermanentDeadLettersImmediately(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	proc := &terminalProbe{q: q, key: "perm", permanent: true}
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{
		Workers:          1,
		ShutdownDeadline: 2 * time.Second,
		MaxAttempts:      5,
		Backoff:          func(int) time.Duration { return time.Second }, // long: a retry would stall the test
	})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), JobKind, "characterization", "perm", []byte("x")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	stop := runRuntime(t, rt)
	waitFor(t, 3*time.Second, func() bool { return proc.discards.Load() == 1 })
	stop()

	if got := proc.handles.Load(); got != 1 {
		t.Fatalf("permanent failure handled %d times, want exactly 1 (immediate dead-letter, no retry)", got)
	}
	if st, _ := proc.discardAt.Load().(string); st != string(work.StatusDeadLetter) {
		t.Fatalf("discard observed status %q, want %q", st, string(work.StatusDeadLetter))
	}
}

// signalingTransientProcessor fails every Handle transiently and signals its first entry,
// so a test can cancel the runtime while a worker is parked in the backoff wait.
type signalingTransientProcessor struct {
	entered chan struct{}
	handles atomic.Int32
}

func (p *signalingTransientProcessor) Handle(_ context.Context, _ string, _ []byte, _ int, _ func(context.Context, []byte) error) error {
	p.handles.Add(1)
	select {
	case p.entered <- struct{}{}:
	default:
	}
	return errors.New("transient-boom")
}

func (p *signalingTransientProcessor) Discard(context.Context, string, []byte) error { return nil }

// TestInProcessBackoffCancellationPrompt proves a shutdown interrupts a backoff wait
// PROMPTLY (well within the long backoff) with no busy-loop and no per-retry goroutine:
// only the first attempt ran, and Run returns fast after cancellation.
func TestInProcessBackoffCancellationPrompt(t *testing.T) {
	t.Parallel()
	proc := &signalingTransientProcessor{entered: make(chan struct{}, 1)}
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{
		Workers:          1,
		ShutdownDeadline: 2 * time.Second,
		MaxAttempts:      5,
		Backoff:          func(int) time.Duration { return 10 * time.Second }, // long: cancellation must not wait it out
	})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), JobKind, "characterization", "cancel", []byte("x")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()

	select {
	case <-proc.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never entered Handle")
	}
	// The worker is now parked in the 10s backoff wait; cancellation must return promptly.
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return promptly after cancellation during backoff (backoff not context-cancellable)")
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("shutdown took %v — it waited out the 10s backoff instead of cancelling it", elapsed)
	}
	if got := proc.handles.Load(); got != 1 {
		t.Fatalf("handled %d attempts, want 1 (the backoff was cancelled before a second attempt)", got)
	}
}

// supersedeBackoffProbe fails the FIRST generation transiently (once) and completes any
// later (replacement) generation, tracking Handle calls per execution so a test can prove
// a generation superseded during its backoff never sends again nor records a transition.
type supersedeBackoffProbe struct {
	mu      sync.Mutex
	first   string
	entered chan string
	calls   map[string]int
}

func newSupersedeBackoffProbe() *supersedeBackoffProbe {
	return &supersedeBackoffProbe{entered: make(chan string, 1), calls: map[string]int{}}
}

func (p *supersedeBackoffProbe) Handle(_ context.Context, execID string, _ []byte, _ int, _ func(context.Context, []byte) error) error {
	p.mu.Lock()
	p.calls[execID]++
	if p.first == "" {
		p.first = execID
	}
	isFirst := execID == p.first
	firstCall := p.calls[execID] == 1
	p.mu.Unlock()
	if isFirst {
		if firstCall {
			p.entered <- execID
		}
		return errors.New("transient-boom") // the superseded generation must be called only once
	}
	return nil // the replacement completes
}

func (p *supersedeBackoffProbe) Discard(context.Context, string, []byte) error { return nil }

func (p *supersedeBackoffProbe) callsFor(execID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[execID]
}

// TestInProcessSupersededDuringBackoffCannotTransition proves a generation superseded by a
// Replace WHILE it is parked in its retry backoff never starts a fresh provider call and
// records no transition — the replacement owns the latest-by-key status.
func TestInProcessSupersededDuringBackoffCannotTransition(t *testing.T) {
	t.Parallel()
	const backoff = 200 * time.Millisecond
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 8})
	proc := newSupersedeBackoffProbe()
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{
		Workers:          2, // one for the superseded generation's backoff, one for the replacement
		ShutdownDeadline: 2 * time.Second,
		MaxAttempts:      5,
		Backoff:          func(int) time.Duration { return backoff },
	})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	e1, err := q.Submit(context.Background(), JobKind, "characterization", "sup", []byte("v1"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	stop := runRuntime(t, rt)

	var pinned string
	select {
	case pinned = <-proc.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first generation never entered Handle")
	}
	if pinned != e1 {
		t.Fatalf("pinned generation = %q, want the submitted %q", pinned, e1)
	}

	// The first generation is now parked in its backoff; supersede it.
	e2, err := q.Replace(context.Background(), JobKind, "characterization", "sup", []byte("v2"))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if e1 == e2 {
		t.Fatal("Replace returned the superseded generation's execution ID")
	}

	// The replacement completes and owns status.
	waitFor(t, 3*time.Second, func() bool {
		state, _ := q.LatestStatus(context.Background(), "sup")
		return state == string(work.StatusCompleted)
	})
	// Let the superseded generation's backoff elapse so its post-backoff fence check fires.
	time.Sleep(backoff + 200*time.Millisecond)
	stop()

	if got := proc.callsFor(e1); got != 1 {
		t.Fatalf("superseded generation handled %d times, want 1 (it must not resend after being superseded during backoff)", got)
	}
	if proc.callsFor(e2) < 1 {
		t.Fatal("the replacement generation never ran")
	}
	if state, _ := q.LatestStatus(context.Background(), "sup"); state != string(work.StatusCompleted) {
		t.Fatalf("latest status = %q, want %q (the replacement's outcome, not the fenced stale one)", state, string(work.StatusCompleted))
	}
}

// recordingCmdObserver records the transitions the real processor emits and can be primed
// to fail or panic, proving observation is best-effort (never changes an outcome).
type recordingCmdObserver struct {
	mu          sync.Mutex
	transitions []string
	err         error
	panics      bool
}

func (o *recordingCmdObserver) Observe(_ context.Context, ev deliverycmd.LifecycleEvent) error {
	o.mu.Lock()
	o.transitions = append(o.transitions, ev.Transition.String())
	o.mu.Unlock()
	if o.panics {
		panic("observer boom")
	}
	return o.err
}

func (o *recordingCmdObserver) has(transition string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, tr := range o.transitions {
		if tr == transition {
			return true
		}
	}
	return false
}

// TestInProcessObserverTransitions proves the bounded pool emits the SAME secret-free
// lifecycle transitions as jobs mode (retried during retries; dead_lettered AFTER the
// recorded terminal, from the discard hook), reusing the processor's observer wiring.
func TestInProcessObserverTransitions(t *testing.T) {
	t.Parallel()
	obs := &recordingCmdObserver{}
	notifier := &recordingNotifier{kind: identity.KindPhone, failFirst: 1_000_000} // never succeeds
	init := &fakeInitializer{rendered: renderedPhoneEnvelope("OBS-SECRET"), deliver: true}
	proc := newInProcProcessor(t, notifier, init, obs, deliverycmd.Config{MaxAttempts: 2})
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{
		Workers:          1,
		ShutdownDeadline: 2 * time.Second,
		MaxAttempts:      2,
		Backoff:          func(int) time.Duration { return time.Millisecond },
	})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), JobKind, "characterization", "obs", sealOpaqueEnvelope(t, "obs")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	stop := runRuntime(t, rt)
	waitFor(t, 3*time.Second, func() bool {
		state, _ := q.LatestStatus(context.Background(), "obs")
		return state == string(work.StatusDeadLetter)
	})
	stop()

	if !obs.has(deliverycmd.TransitionRetried.String()) {
		t.Fatalf("observer never saw a retried transition: %v", obs.transitions)
	}
	if !obs.has(deliverycmd.TransitionDeadLettered.String()) {
		t.Fatalf("observer never saw a dead_lettered transition: %v", obs.transitions)
	}
	if init.discards() != 1 {
		t.Fatalf("challenge discarded %d times, want 1", init.discards())
	}
}

// TestInProcessObserverFailureChangesNothing proves a nil, erroring, or panicking observer
// changes no delivery outcome — the message still delivers and status still reads succeeded.
func TestInProcessObserverFailureChangesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		obs  deliverycmd.Observer
	}{
		{"nil observer", nil},
		{"erroring observer", &recordingCmdObserver{err: errors.New("bus down")}},
		{"panicking observer", &recordingCmdObserver{panics: true}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			notifier := &recordingNotifier{kind: identity.KindPhone}
			init := &fakeInitializer{rendered: renderedPhoneEnvelope("OK-SECRET"), deliver: true}
			proc := newInProcProcessor(t, notifier, init, tc.obs, deliverycmd.Config{})
			q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
			rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{
				Workers:          1,
				ShutdownDeadline: 2 * time.Second,
				Backoff:          func(int) time.Duration { return time.Millisecond },
			})
			if err != nil {
				t.Fatalf("NewInProcessRuntime: %v", err)
			}
			if _, err := q.Submit(context.Background(), JobKind, "characterization", "ok", sealOpaqueEnvelope(t, "ok")); err != nil {
				t.Fatalf("Submit: %v", err)
			}
			stop := runRuntime(t, rt)
			waitFor(t, 3*time.Second, func() bool {
				state, _ := q.LatestStatus(context.Background(), "ok")
				return state == string(work.StatusCompleted)
			})
			stop()
			if notifier.count() != 1 {
				t.Fatalf("provider sends = %d, want exactly 1 (observation must not change the outcome)", notifier.count())
			}
		})
	}
}

// blockingCountingNotifier counts every send and blocks until the send's context is
// canceled — modeling a provider that hangs, so the runtime's per-send provider timeout
// (inherited from the shared command.Engine) is what bounds each call.
type blockingCountingNotifier struct {
	kind  string
	calls atomic.Int32
}

func (n *blockingCountingNotifier) Kind() string { return n.kind }

func (n *blockingCountingNotifier) Notify(ctx context.Context, _ identity.Address, _ notify.Message) error {
	n.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

// TestInProcessProviderTimeoutBounded proves each provider call is bounded by the shared
// engine's provider timeout: a hanging provider does not stall the worker — the send times
// out, the attempt is classified as a bounded retry, and the finite cap dead-letters
// promptly. If the timeout were not applied, the first send would hang forever and the
// work would never reach a terminal state.
func TestInProcessProviderTimeoutBounded(t *testing.T) {
	t.Parallel()
	const maxAttempts = 3
	notifier := &blockingCountingNotifier{kind: identity.KindPhone}
	proc := newInProcProcessor(t, notifier, nil, nil, deliverycmd.Config{
		ProviderTimeout: 20 * time.Millisecond,
		MaxAttempts:     maxAttempts,
	})
	q := NewInProcessQueue(InProcessQueueConfig{Capacity: 4})
	rt, err := NewInProcessRuntime(q, proc, InProcessRuntimeConfig{
		Workers:          1,
		ShutdownDeadline: 2 * time.Second,
		MaxAttempts:      maxAttempts,
		Backoff:          func(int) time.Duration { return time.Millisecond },
	})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}

	if _, err := q.Submit(context.Background(), JobKind, "characterization", "timeout", sealRenderedEnvelope(t, "timeout", "S")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	stop := runRuntime(t, rt)
	start := time.Now()
	waitFor(t, 3*time.Second, func() bool {
		state, _ := q.LatestStatus(context.Background(), "timeout")
		return state == string(work.StatusDeadLetter)
	})
	elapsed := time.Since(start)
	stop()

	if got := notifier.calls.Load(); got != maxAttempts {
		t.Fatalf("provider called %d times, want the cap of %d (each call bounded by the provider timeout)", got, maxAttempts)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("reaching a terminal state took %v — a provider send was not bounded by the timeout", elapsed)
	}
}

// compile assertion: the AV3D-4.3 fakes satisfy the runtime's processor seam and the
// notify seam.
var (
	_ InProcessProcessor   = (*terminalProbe)(nil)
	_ InProcessProcessor   = (*signalingTransientProcessor)(nil)
	_ InProcessProcessor   = (*supersedeBackoffProbe)(nil)
	_ notify.Notifier      = (*blockingCountingNotifier)(nil)
	_ deliverycmd.Observer = (*recordingCmdObserver)(nil)
)
