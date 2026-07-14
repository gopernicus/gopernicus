package delivery

import (
	"context"
	"testing"
	"time"

	cmd "github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/command"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/deliverychar"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// TestProcessorCharacterization runs the transport-neutral delivery characterization
// suite (deliverychar) against the AV3D-2.2 command Engine, driven by a minimal test
// executor that simulates claim/checkpoint/retry over an in-memory job store — NOT a
// real queue (phases 3/4 supply the durable and bounded runtimes). It is the second
// Harness over the same neutral cases the bespoke Worker passes (TestCharacterization),
// proving the processor path preserves identical-secret retry, no-send-before-checkpoint,
// enumeration safety, and known/unknown parity.
func TestProcessorCharacterization(t *testing.T) {
	deliverychar.Run(t, newProcessorHarness)
}

// --- processor executor store ------------------------------------------------

// Local job states for the minimal executor. They are distinct string values so the
// secret-free Observation can carry them verbatim.
const (
	pjPending   = "pending"
	pjSucceeded = "succeeded"
	pjFailed    = "failed"
	pjCanceled  = "canceled"
)

// pjob is one unit of executor-owned work. Payload is the sealed command Envelope the
// processor opens; the store checkpoints it in place (lease-fenced) when the processor
// renders an opaque start, so a retry reuses the identical rendered bytes.
type pjob struct {
	id          string
	key         string
	payload     []byte
	state       string
	attempt     int
	availableAt time.Time
	leaseID     string
	leasedUntil time.Time
	terminalAt  time.Time
	createdAt   time.Time
	seq         int
}

func (j pjob) terminal() bool { return j.state != pjPending }

func (j pjob) due(now time.Time) bool {
	if j.state != pjPending || j.availableAt.After(now) {
		return false
	}
	return j.leasedUntil.IsZero() || !j.leasedUntil.After(now)
}

// pstore is the minimal, mutex-guarded executor store. It enforces the same durable
// invariants the processor relies on: idempotent enqueue by logical key, atomic
// supersession, oldest-due single-claimant lease, lease-fenced checkpoint/completion,
// expired-lease reclaim, and bounded terminal purge — none of which is a real queue.
type pstore struct {
	seq  int
	jobs map[string]pjob
}

func newPStore() *pstore { return &pstore{jobs: map[string]pjob{}} }

func (s *pstore) insert(payload []byte, key string, now time.Time) pjob {
	s.seq++
	j := pjob{
		id:          key + "#" + itoa(s.seq),
		key:         key,
		payload:     payload,
		state:       pjPending,
		availableAt: now,
		createdAt:   now,
		seq:         s.seq,
	}
	s.jobs[j.id] = j
	return j
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// enqueue admits payload under key idempotently. inserted reports whether a NEW job
// was admitted (true) or an existing active job made it a no-op (false), so the
// transport only observes an Accepted transition for genuinely new work.
func (s *pstore) enqueue(payload []byte, key string, now time.Time) (job pjob, inserted bool) {
	for _, ex := range s.jobs {
		if !ex.terminal() && ex.key == key {
			return ex, false
		}
	}
	return s.insert(payload, key, now), true
}

// replace supersedes every active job under key and admits a fresh one. It returns
// the new job plus the execution IDs it superseded, so the transport observes one
// Superseded transition per canceled generation.
func (s *pstore) replace(payload []byte, key string, now time.Time) (job pjob, superseded []string) {
	for id, ex := range s.jobs {
		if !ex.terminal() && ex.key == key {
			ex.state = pjCanceled
			ex.terminalAt = now
			ex.leaseID = ""
			ex.leasedUntil = time.Time{}
			s.jobs[id] = ex
			superseded = append(superseded, id)
		}
	}
	return s.insert(payload, key, now), superseded
}

func (s *pstore) claim(now time.Time, leaseID string, leaseFor time.Duration) (pjob, bool) {
	var due pjob
	found := false
	for _, ex := range s.jobs {
		if !ex.due(now) {
			continue
		}
		if !found || pjOlder(ex, due) {
			due = ex
			found = true
		}
	}
	if !found {
		return pjob{}, false
	}
	due.attempt++
	due.leaseID = leaseID
	due.leasedUntil = now.Add(leaseFor)
	s.jobs[due.id] = due
	return due, true
}

func pjOlder(a, b pjob) bool {
	if !a.availableAt.Equal(b.availableAt) {
		return a.availableAt.Before(b.availableAt)
	}
	if !a.createdAt.Equal(b.createdAt) {
		return a.createdAt.Before(b.createdAt)
	}
	return a.seq < b.seq
}

// checkpoint persists the rendered payload for the current claim. A stale or
// superseded claim (a mismatched or cleared lease, or a terminal job) is a conflict:
// the processor treats it as a signal to abort the send.
func (s *pstore) checkpoint(id, leaseID string, payload []byte) bool {
	j, ok := s.jobs[id]
	if !ok || j.terminal() || j.leaseID != leaseID {
		return false
	}
	j.payload = payload
	s.jobs[id] = j
	return true
}

func (s *pstore) complete(id, leaseID, state string, now time.Time) bool {
	j, ok := s.jobs[id]
	if !ok || j.terminal() || j.leaseID != leaseID {
		return false
	}
	j.state = state
	j.terminalAt = now
	j.leaseID = ""
	j.leasedUntil = time.Time{}
	s.jobs[id] = j
	return true
}

func (s *pstore) retry(id, leaseID string, availableAt, now time.Time) bool {
	j, ok := s.jobs[id]
	if !ok || j.terminal() || j.leaseID != leaseID {
		return false
	}
	j.availableAt = availableAt
	j.leaseID = ""
	j.leasedUntil = time.Time{}
	s.jobs[id] = j
	return true
}

func (s *pstore) purge(before time.Time, limit int) int {
	n := 0
	for id, ex := range s.jobs {
		if limit > 0 && n >= limit {
			break
		}
		if ex.terminal() && !ex.terminalAt.After(before) {
			delete(s.jobs, id)
			n++
		}
	}
	return n
}

func (s *pstore) latest(key string) (pjob, bool) {
	var latest pjob
	found := false
	for _, ex := range s.jobs {
		if ex.key != key {
			continue
		}
		if !found || ex.createdAt.After(latest.createdAt) ||
			(ex.createdAt.Equal(latest.createdAt) && ex.seq > latest.seq) {
			latest = ex
			found = true
		}
	}
	return latest, found
}

// --- Harness -----------------------------------------------------------------

// processorHarness adapts the command Engine onto the neutral deliverychar.Harness.
// Submit/Replace seal a command Envelope into the store; Drain claims each due job,
// runs one Process, and applies the returned Result (recording a dead-letter BEFORE
// the best-effort Discard, per the load-bearing order); Advance drives the shared
// manual clock; Purge runs the terminal sweep; Status maps the latest job's lifecycle
// onto the secret-free Observation. crashBudget models a crash after the provider
// accepts but before completion is recorded.
type processorHarness struct {
	clk         *fakeClock
	enc         cryptids.Encrypter
	store       *pstore
	proc        *cmd.Engine
	obs         cmd.Observer
	leaseFor    time.Duration
	retention   time.Duration
	crashBudget int
}

func newProcessorHarness(t *testing.T, s deliverychar.Scenario) deliverychar.Harness {
	return newProcessorHarnessWith(t, s, nil)
}

// newProcessorHarnessWith builds the executor with an optional lifecycle observer
// wired at every transition. The observer is passed through command.SafeObserve, so a
// nil, erroring, or panicking observer must yield IDENTICAL delivery outcomes — the
// equivalence the observer-parity tests assert by running the full characterization
// suite under each observer variant.
func newProcessorHarnessWith(t *testing.T, s deliverychar.Scenario, obs cmd.Observer) deliverychar.Harness {
	t.Helper()
	if s.Provider == nil {
		t.Fatal("deliverychar.Scenario.Provider is required")
	}
	clk := newClock()
	router := newRouter(t, &stubSender{}, map[string]notify.Notifier{identity.KindPhone: notify.Notifier(s.Provider)})

	deps := cmd.ProcessorDeps{
		Encrypter: fakeEncrypter{},
		Deliverer: routerDeliverer{router: router},
		Now:       clk.now,
		Config: cmd.Config{
			MaxAttempts:     s.MaxAttempts,
			ProviderTimeout: s.ProviderTimeout,
			Backoff:         s.Backoff,
		},
	}
	if s.Initializer != nil {
		deps.Initializer = charInit{n: s.Initializer}
	}
	proc, err := cmd.NewProcessor(deps)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}

	leaseFor := s.LeaseFor
	if leaseFor <= 0 {
		leaseFor = 30 * time.Second
	}
	retention := s.PurgeRetention
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	return &processorHarness{
		clk:         clk,
		enc:         fakeEncrypter{},
		store:       newPStore(),
		proc:        proc,
		obs:         obs,
		leaseFor:    leaseFor,
		retention:   retention,
		crashBudget: s.CrashCompletions,
	}
}

// observe forwards one secret-free transition through command.SafeObserve. It is the
// single call site the harness uses so a nil/erroring/panicking observer is contained
// exactly as a real transport would contain it.
func (h *processorHarness) observe(ctx context.Context, execID string, tr cmd.Transition, attempt, count int) {
	cmd.SafeObserve(ctx, h.obs, cmd.LifecycleEvent{
		ExecutionID: execID,
		Transition:  tr,
		Attempt:     attempt,
		Count:       count,
	})
}

// envelope maps a neutral Submission onto a validated command Envelope: opaque work
// carries only the resolution input, pre-rendered work carries its rendered content.
func (h *processorHarness) envelope(t *testing.T, sub deliverychar.Submission) cmd.Envelope {
	t.Helper()
	sub = sub.Normalized()
	if sub.Opaque {
		env, err := cmd.NewOpaque(sub.Kind, sub.Purpose, sub.Key, sub.ResolutionInput)
		if err != nil {
			t.Fatalf("NewOpaque: %v", err)
		}
		return env
	}
	env, err := cmd.NewRendered(sub.Kind, sub.Purpose, sub.Key, sub.Rendered.Destination, "", sub.Rendered.Body, "", sub.Rendered.Secret)
	if err != nil {
		t.Fatalf("NewRendered: %v", err)
	}
	return env
}

func (h *processorHarness) seal(t *testing.T, env cmd.Envelope) []byte {
	t.Helper()
	b, err := cmd.Seal(h.enc, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return b
}

func (h *processorHarness) Submit(t *testing.T, sub deliverychar.Submission) string {
	t.Helper()
	env := h.envelope(t, sub)
	job, inserted := h.store.enqueue(h.seal(t, env), env.Key, h.clk.now())
	if inserted {
		h.observe(context.Background(), job.id, cmd.TransitionAccepted, 0, 0)
	}
	return env.Key
}

func (h *processorHarness) Replace(t *testing.T, sub deliverychar.Submission) string {
	t.Helper()
	env := h.envelope(t, sub)
	job, superseded := h.store.replace(h.seal(t, env), env.Key, h.clk.now())
	for _, id := range superseded {
		h.observe(context.Background(), id, cmd.TransitionSuperseded, 0, 0)
	}
	h.observe(context.Background(), job.id, cmd.TransitionAccepted, 0, 0)
	return env.Key
}

// Drain claims and processes every due job until none is immediately runnable. It
// simulates the transport: it applies the processor's Result, and for a crash-modeled
// completion it leaves the claim leased (reclaimable) instead of recording success.
func (h *processorHarness) Drain(t *testing.T) {
	t.Helper()
	const leaseID = "proc-lease"
	ctx := context.Background()
	for {
		job, ok := h.store.claim(h.clk.now(), leaseID, h.leaseFor)
		if !ok {
			return
		}
		claim := cmd.Claim{
			Payload:    job.payload,
			Attempt:    job.attempt,
			Checkpoint: checkpointer{store: h.store, id: job.id, leaseID: leaseID, h: h, attempt: job.attempt},
		}
		res := h.proc.Process(ctx, claim)
		switch res.Outcome {
		case cmd.OutcomeCompleted, cmd.OutcomeSkipped:
			if res.Outcome == cmd.OutcomeCompleted && h.crashBudget > 0 {
				// Crash after the provider accepted but before completion is recorded: leave
				// the claim leased so it is reclaimed and the identical secret is replayed. No
				// terminal transition is observed — completion was never recorded.
				h.crashBudget--
				continue
			}
			h.store.complete(job.id, leaseID, pjSucceeded, h.clk.now())
			if res.Outcome == cmd.OutcomeSkipped {
				h.observe(ctx, job.id, cmd.TransitionSkipped, job.attempt, 0)
			} else {
				h.observe(ctx, job.id, cmd.TransitionDelivered, job.attempt, 0)
			}
		case cmd.OutcomeRetry:
			h.store.retry(job.id, leaseID, res.RetryAt, h.clk.now())
			h.observe(ctx, job.id, cmd.TransitionRetried, job.attempt, 0)
		case cmd.OutcomePermanent:
			// Record the dead-letter FIRST, then observe it and trigger the best-effort
			// discard — the load-bearing order: discard (and observe) only after a recorded
			// terminal transition.
			h.store.complete(job.id, leaseID, pjFailed, h.clk.now())
			h.observe(ctx, job.id, cmd.TransitionDeadLettered, job.attempt, 0)
			_ = h.proc.Discard(ctx, claim)
		}
	}
}

func (h *processorHarness) Advance(d time.Duration) { h.clk.advance(d) }

func (h *processorHarness) Purge(t *testing.T) int {
	t.Helper()
	n := h.store.purge(h.clk.now().Add(-h.retention), 500)
	if n > 0 {
		h.observe(context.Background(), "", cmd.TransitionPurged, 0, n)
	}
	return n
}

func (h *processorHarness) Status(t *testing.T, key string) (deliverychar.Observation, bool) {
	t.Helper()
	job, ok := h.store.latest(key)
	if !ok {
		return deliverychar.Observation{}, false
	}
	return deliverychar.Observation{
		State:   job.state,
		Attempt: job.attempt,
		Pending: job.state == pjPending,
		Failed:  job.state == pjFailed,
	}, true
}

// --- collaborator adapters ---------------------------------------------------

// checkpointer is the claim-scoped, lease-fenced payload checkpoint the processor
// uses to persist a freshly rendered envelope before any send. A successful
// checkpoint is exactly the Initialized transition (an opaque start was resolved and
// its rendered payload persisted), so the transport observes it here.
type checkpointer struct {
	store   *pstore
	id      string
	leaseID string
	h       *processorHarness
	attempt int
}

func (c checkpointer) Checkpoint(ctx context.Context, sealed []byte) error {
	if !c.store.checkpoint(c.id, c.leaseID, sealed) {
		return errBoom
	}
	c.h.observe(ctx, c.id, cmd.TransitionInitialized, c.attempt, 0)
	return nil
}

// routerDeliverer bridges the command Deliverer contract onto the delivery Router:
// it routes a rendered command Envelope by Kind and performs one send. The processor
// owns the provider deadline (it passes a bounded ctx), so this adapter just delivers.
type routerDeliverer struct{ router *Router }

func (d routerDeliverer) Deliver(ctx context.Context, env cmd.Envelope) error {
	return d.router.Deliver(ctx, env.Kind, Envelope{
		Destination: env.Destination,
		Subject:     env.Subject,
		Body:        env.Body,
		HTML:        env.HTML,
		Secret:      env.Secret,
	})
}

// charInit adapts the neutral deliverychar.Initializer onto command Initializer:
// Initialize stands for the off-request-path resolve + issue + render, returning a
// fully rendered command; Discard stands for the best-effort challenge void.
type charInit struct{ n *deliverychar.Initializer }

func (c charInit) Initialize(_ context.Context, opaque cmd.Envelope) (cmd.Envelope, bool, error) {
	r, deliver, err := c.n.Resolve()
	if err != nil {
		return cmd.Envelope{}, false, err
	}
	if !deliver {
		return cmd.Envelope{}, false, nil
	}
	env, err := cmd.NewRendered(opaque.Kind, opaque.Purpose, opaque.Key, r.Destination, "", r.Body, "", r.Secret)
	if err != nil {
		return cmd.Envelope{}, false, err
	}
	return env, true, nil
}

func (c charInit) Discard(_ context.Context, _ cmd.Envelope) error {
	c.n.Discarded()
	return nil
}

// --- lifecycle observer parity + positive observation ------------------------
//
// AV3D-2.5: prove no observer, an erroring observer, or a panicking observer can
// lose, retry, duplicate, or fail accepted delivery work. The characterization suite
// IS the outcome spec, so passing it identically under each observer variant is the
// equivalence proof; the positive tests prove the observer is actually exercised (a
// harness that never observed would pass parity vacuously).

// erroringObserver fails on every transition.
type erroringObserver struct{}

func (erroringObserver) Observe(context.Context, cmd.LifecycleEvent) error { return errBoom }

// panickingObserver panics on every transition; command.SafeObserve must contain it.
type panickingObserver struct{}

func (panickingObserver) Observe(context.Context, cmd.LifecycleEvent) error {
	panic("observer boom")
}

// captureObserver records every transition it is handed for positive assertions.
type captureObserver struct{ seen []cmd.LifecycleEvent }

func (o *captureObserver) Observe(_ context.Context, ev cmd.LifecycleEvent) error {
	o.seen = append(o.seen, ev)
	return nil
}

func (o *captureObserver) transitions() []cmd.Transition {
	out := make([]cmd.Transition, len(o.seen))
	for i, e := range o.seen {
		out[i] = e.Transition
	}
	return out
}

func sameTransitions(a, b []cmd.Transition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestProcessorCharacterizationErroringObserver runs the full transport-neutral suite
// with an observer that errors on EVERY transition. It passes identically to the
// nil-observer TestProcessorCharacterization, proving an erroring observer changes no
// delivery outcome.
func TestProcessorCharacterizationErroringObserver(t *testing.T) {
	deliverychar.Run(t, func(t *testing.T, s deliverychar.Scenario) deliverychar.Harness {
		return newProcessorHarnessWith(t, s, erroringObserver{})
	})
}

// TestProcessorCharacterizationPanickingObserver runs the full suite with an observer
// that PANICS on every transition. command.SafeObserve contains the panic and the
// suite passes identically, proving a panicking observer cannot fail delivery.
func TestProcessorCharacterizationPanickingObserver(t *testing.T) {
	deliverychar.Run(t, func(t *testing.T, s deliverychar.Scenario) deliverychar.Harness {
		return newProcessorHarnessWith(t, s, panickingObserver{})
	})
}

// TestProcessorCharacterizationHealthyObserver runs the full suite with the real
// EventObserver over a recording emitter; outcomes are identical to the nil run, so a
// healthy, emitting observer is equally side-effect-free on delivery state.
func TestProcessorCharacterizationHealthyObserver(t *testing.T) {
	deliverychar.Run(t, func(t *testing.T, s deliverychar.Scenario) deliverychar.Harness {
		return newProcessorHarnessWith(t, s, NewEventObserver(&recordEmitter{}, nil))
	})
}

// TestProcessorObserverReceivesRenderedTransitions proves a rendered submission drives
// exactly Accepted then Delivered, and that no secret reaches the observer.
func TestProcessorObserverReceivesRenderedTransitions(t *testing.T) {
	obs := &captureObserver{}
	h := newProcessorHarnessWith(t, deliverychar.Scenario{Provider: deliverychar.NewProvider(0, false)}, obs)
	h.Submit(t, deliverychar.RenderedSubmission("obs-rendered", "s3cr3t"))
	h.Drain(t)

	if got, want := obs.transitions(), []cmd.Transition{cmd.TransitionAccepted, cmd.TransitionDelivered}; !sameTransitions(got, want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	for _, e := range obs.seen {
		if e.ExecutionID == "" {
			t.Errorf("transition %s carried no execution ID", e.Transition)
		}
	}
}

// TestProcessorObserverReceivesOpaqueTransitions proves an opaque start drives
// Accepted, Initialized (checkpoint), then Delivered.
func TestProcessorObserverReceivesOpaqueTransitions(t *testing.T) {
	obs := &captureObserver{}
	h := newProcessorHarnessWith(t, deliverychar.Scenario{
		Provider:    deliverychar.NewProvider(0, false),
		Initializer: deliverychar.NewInitializer("otp", true),
	}, obs)
	h.Submit(t, deliverychar.OpaqueSubmission("obs-opaque"))
	h.Drain(t)

	if got, want := obs.transitions(), []cmd.Transition{cmd.TransitionAccepted, cmd.TransitionInitialized, cmd.TransitionDelivered}; !sameTransitions(got, want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
}

// TestProcessorObserverReceivesDeadLettered proves a permanently-failing send drives
// Accepted then DeadLettered, and that the terminal failure is still recorded (the
// observer runs AFTER the dead-letter is recorded, never in its place).
func TestProcessorObserverReceivesDeadLettered(t *testing.T) {
	obs := &captureObserver{}
	h := newProcessorHarnessWith(t, deliverychar.Scenario{
		Provider:    deliverychar.NewProvider(100, false),
		MaxAttempts: 1,
	}, obs)
	h.Submit(t, deliverychar.RenderedSubmission("obs-dead", "s"))
	h.Drain(t)

	if got, want := obs.transitions(), []cmd.Transition{cmd.TransitionAccepted, cmd.TransitionDeadLettered}; !sameTransitions(got, want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	st, ok := h.Status(t, "obs-dead")
	if !ok || !st.Failed {
		t.Fatalf("status after dead-letter = %+v ok=%v, want Failed", st, ok)
	}
}
