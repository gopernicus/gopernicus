package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// errBoom is a generic transport/crypto sentinel for the failure paths.
var errBoom = errors.New("boom")

// fakeClock is a mutex-guarded manual clock so lease expiry, retry backoff, and
// purge retention are deterministic under -race.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeEncrypter is a reversible, non-secret test encrypter. encErr/decErr force the
// encryption-failure paths.
type fakeEncrypter struct {
	encErr error
	decErr error
}

func (f fakeEncrypter) Encrypt(plaintext string) (string, error) {
	if f.encErr != nil {
		return "", f.encErr
	}
	return "enc:" + plaintext, nil
}

func (f fakeEncrypter) Decrypt(ciphertext string) (string, error) {
	if f.decErr != nil {
		return "", f.decErr
	}
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

// memRepo is a concurrent-safe in-test deliveryjob.Repository. It hand-enforces the
// same durable-outbox invariants storetest proves: idempotent enqueue by key,
// oldest-due single-claimant lease, lease-checked completion, expired-lease
// reclaim, and bounded terminal purge. Each promised atomic op runs under one lock.
type memRepo struct {
	mu   sync.Mutex
	seq  int
	jobs map[string]deliveryjob.Job
}

func newMemRepo() *memRepo { return &memRepo{jobs: map[string]deliveryjob.Job{}} }

func (r *memRepo) insertLocked(job deliveryjob.Job) deliveryjob.Job {
	if job.ID == "" {
		r.seq++
		job.ID = fmt.Sprintf("job-%d", r.seq)
	}
	job.State = deliveryjob.StatePending
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.TerminalAt = time.Time{}
	r.jobs[job.ID] = job
	return job
}

func (r *memRepo) Enqueue(_ context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.jobs {
		if !ex.Terminal() && ex.IdempotencyKey == job.IdempotencyKey {
			return ex, nil
		}
	}
	return r.insertLocked(job), nil
}

func (r *memRepo) Replace(_ context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := job.UpdatedAt
	for id, ex := range r.jobs {
		if !ex.Terminal() && ex.IdempotencyKey == job.IdempotencyKey {
			ex.State = deliveryjob.StateCanceled
			ex.TerminalAt = now
			ex.LeaseID = ""
			ex.LeasedUntil = time.Time{}
			ex.UpdatedAt = now
			r.jobs[id] = ex
		}
	}
	return r.insertLocked(job), nil
}

func (r *memRepo) Claim(_ context.Context, now time.Time, leaseID string, leaseFor time.Duration) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var due deliveryjob.Job
	found := false
	for _, ex := range r.jobs {
		if !ex.Due(now) {
			continue
		}
		if !found || older(ex, due) {
			due = ex
			found = true
		}
	}
	if !found {
		return deliveryjob.Job{}, sdk.ErrNotFound
	}
	due.AttemptCount++
	due.LeaseID = leaseID
	due.LeasedUntil = now.Add(leaseFor)
	due.UpdatedAt = now
	r.jobs[due.ID] = due
	return due, nil
}

func (r *memRepo) GetLatestByIdempotencyKey(_ context.Context, key string) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest deliveryjob.Job
	found := false
	for _, ex := range r.jobs {
		if ex.IdempotencyKey != key {
			continue
		}
		if !found || ex.CreatedAt.After(latest.CreatedAt) {
			latest = ex
			found = true
		}
	}
	if !found {
		return deliveryjob.Job{}, sdk.ErrNotFound
	}
	return latest, nil
}

func older(a, b deliveryjob.Job) bool {
	if !a.AvailableAt.Equal(b.AvailableAt) {
		return a.AvailableAt.Before(b.AvailableAt)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func (r *memRepo) Succeed(_ context.Context, id, leaseID string, now time.Time) error {
	return r.complete(id, leaseID, deliveryjob.StateSucceeded, "", now)
}

func (r *memRepo) Fail(_ context.Context, id, leaseID, lastErr string, now time.Time) error {
	return r.complete(id, leaseID, deliveryjob.StateFailed, lastErr, now)
}

func (r *memRepo) complete(id, leaseID, state, lastErr string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if job.State == state {
		return nil
	}
	if job.Terminal() || job.LeaseID != leaseID {
		return sdk.ErrConflict
	}
	job.State = state
	job.LastError = lastErr
	job.TerminalAt = now
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now
	r.jobs[id] = job
	return nil
}

func (r *memRepo) Retry(_ context.Context, id, leaseID string, availableAt time.Time, lastErr string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if job.Terminal() || job.LeaseID != leaseID {
		return sdk.ErrConflict
	}
	job.AvailableAt = availableAt
	job.LastError = lastErr
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now
	r.jobs[id] = job
	return nil
}

func (r *memRepo) Cancel(_ context.Context, id string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if job.State == deliveryjob.StateCanceled {
		return nil
	}
	if job.Terminal() {
		return sdk.ErrConflict
	}
	job.State = deliveryjob.StateCanceled
	job.TerminalAt = now
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now
	r.jobs[id] = job
	return nil
}

func (r *memRepo) PurgeTerminal(_ context.Context, before time.Time, limit int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for id, ex := range r.jobs {
		if limit > 0 && n >= limit {
			break
		}
		if ex.Terminal() && !ex.TerminalAt.After(before) {
			delete(r.jobs, id)
			n++
		}
	}
	return n, nil
}

// get returns a job snapshot by ID for assertions.
func (r *memRepo) get(id string) (deliveryjob.Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	return j, ok
}

// findByKeyState returns the first job with the given key and state.
func (r *memRepo) findByKeyState(key, state string) (deliveryjob.Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.jobs {
		if j.IdempotencyKey == key && j.State == state {
			return j, true
		}
	}
	return deliveryjob.Job{}, false
}

// countState counts jobs in a given state.
func (r *memRepo) countState(state string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, j := range r.jobs {
		if j.State == state {
			n++
		}
	}
	return n
}

// flakyRepo wraps memRepo to fail Succeed a bounded number of times, modeling a
// worker that crashes after the provider accepts a message but before it records
// success.
type flakyRepo struct {
	*memRepo
	mu       sync.Mutex
	failSucc int
}

func (r *flakyRepo) Succeed(ctx context.Context, id, leaseID string, now time.Time) error {
	r.mu.Lock()
	if r.failSucc > 0 {
		r.failSucc--
		r.mu.Unlock()
		return errBoom
	}
	r.mu.Unlock()
	return r.memRepo.Succeed(ctx, id, leaseID, now)
}

// recordingNotifier records every Notify call (message + destination) and can be
// primed to fail the first N sends or to block until the context is canceled.
type recordingNotifier struct {
	mu        sync.Mutex
	kind      string
	sends     []notify.Message
	tos       []identity.Address
	failFirst int
	block     bool
}

func (n *recordingNotifier) Kind() string { return n.kind }

func (n *recordingNotifier) Notify(ctx context.Context, to identity.Address, msg notify.Message) error {
	if n.block {
		<-ctx.Done()
		return ctx.Err()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sends = append(n.sends, msg)
	n.tos = append(n.tos, to)
	if len(n.sends) <= n.failFirst {
		return errBoom
	}
	return nil
}

func (n *recordingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sends)
}

func (n *recordingNotifier) bodies() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.sends))
	for i, m := range n.sends {
		out[i] = m.Body
	}
	return out
}

// fakeInitializer renders an opaque job into a fixed envelope and records its calls.
type fakeInitializer struct {
	mu           sync.Mutex
	rendered     Envelope
	deliver      bool
	err          error
	initCalls    int
	discardCalls int
}

func (f *fakeInitializer) Initialize(_ context.Context, _ deliveryjob.Job, _ Envelope) (Envelope, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initCalls++
	if f.err != nil {
		return Envelope{}, false, f.err
	}
	return f.rendered, f.deliver, nil
}

func (f *fakeInitializer) Discard(_ context.Context, _ deliveryjob.Job, _ Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discardCalls++
	return nil
}

func (f *fakeInitializer) inits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.initCalls
}

func (f *fakeInitializer) discards() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discardCalls
}

// phoneRouter builds a Router whose only non-email transport is the given phone
// notifier — the simplest capture surface for worker delivery tests.
func phoneRouter(t *testing.T, phone notify.Notifier) *Router {
	t.Helper()
	return newRouter(t, &stubSender{}, map[string]notify.Notifier{identity.KindPhone: phone})
}

// renderedPhoneEnvelope is a ready-to-send SMS payload (Body set → not opaque).
func renderedPhoneEnvelope(secret string) Envelope {
	return Envelope{Destination: "+15550001111", Body: "Sign in: " + secret, Secret: secret}
}

// newWorker builds a Worker over the given repo/router with a manual clock and the
// supplied options applied to WorkerDeps.
func newWorker(t *testing.T, repo deliveryjob.Repository, router *Router, clk *fakeClock, mutate func(*WorkerDeps)) *Worker {
	t.Helper()
	d := WorkerDeps{
		Repo:      repo,
		Encrypter: fakeEncrypter{},
		Router:    router,
		Now:       clk.now,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if mutate != nil {
		mutate(&d)
	}
	w, err := NewWorker(d)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	return w
}

// --- Worker construction -----------------------------------------------------

func TestNewWorkerRequiresCollaborators(t *testing.T) {
	router := phoneRouter(t, &recordingNotifier{kind: identity.KindPhone})
	if _, err := NewWorker(WorkerDeps{Encrypter: fakeEncrypter{}, Router: router}); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("missing repo err=%v, want ErrRepositoryRequired", err)
	}
	if _, err := NewWorker(WorkerDeps{Repo: newMemRepo(), Router: router}); !errors.Is(err, ErrEncrypterRequired) {
		t.Fatalf("missing encrypter err=%v, want ErrEncrypterRequired", err)
	}
	if _, err := NewWorker(WorkerDeps{Repo: newMemRepo(), Encrypter: fakeEncrypter{}}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing router err=%v, want sdk.ErrInvalidInput", err)
	}
}

// --- Opaque initialize → deliver ---------------------------------------------

// An opaque start job is resolved+rendered once (Replace persists the rendered
// payload) and then delivered on the next claim, carrying the initializer's secret.
func TestWorkerInitializesOpaqueThenDelivers(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone}
	init := &fakeInitializer{rendered: renderedPhoneEnvelope("TOK9"), deliver: true}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) { d.Initializer = init })

	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	rcpt, err := svc.Enqueue(context.Background(), Command{
		Kind:           identity.KindPhone,
		Purpose:        PurposeMagicLink,
		IdempotencyKey: "key-1",
		Envelope:       Envelope{ResolutionInput: "+15550001111"},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// First claim initializes and persists; nothing is sent yet.
	if worked, err := w.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("RunOnce#1 worked=%v err=%v", worked, err)
	}
	if init.inits() != 1 {
		t.Fatalf("Initialize called %d times, want 1", init.inits())
	}
	if phone.count() != 0 {
		t.Fatalf("a message was sent during initialization: %d", phone.count())
	}
	if opaque, ok := repo.get(rcpt.JobID); !ok || opaque.State != deliveryjob.StateCanceled {
		t.Fatalf("opaque job not superseded: %+v ok=%v", opaque, ok)
	}

	// Second claim delivers the persisted rendered job.
	if worked, err := w.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("RunOnce#2 worked=%v err=%v", worked, err)
	}
	if init.inits() != 1 {
		t.Fatalf("Initialize re-run on the rendered job: %d", init.inits())
	}
	if phone.count() != 1 || !strings.Contains(phone.bodies()[0], "TOK9") {
		t.Fatalf("rendered secret not delivered: %v", phone.bodies())
	}
	if got, ok := repo.findByKeyState("key-1", deliveryjob.StateSucceeded); !ok {
		t.Fatalf("rendered job not succeeded: %+v", got)
	}
}

// Initialization that finds nothing to deliver (unknown identifier) terminates the
// job successfully with no send — known and unknown identifiers are indistinguishable.
func TestWorkerSkipsWhenNothingToDeliver(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone}
	init := &fakeInitializer{deliver: false}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) { d.Initializer = init })

	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	if _, err := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: Envelope{ResolutionInput: "+1"}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if phone.count() != 0 {
		t.Fatalf("unknown identifier produced a send: %d", phone.count())
	}
	if repo.countState(deliveryjob.StateSucceeded) != 1 {
		t.Fatalf("skip did not terminate the job successfully")
	}
}

// An opaque job with no initializer wired fails loudly rather than sending an
// unrendered message.
func TestWorkerOpaqueWithoutInitializerFails(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	w := newWorker(t, repo, phoneRouter(t, &recordingNotifier{kind: identity.KindPhone}), clk, nil)
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	if _, err := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: Envelope{ResolutionInput: "+1"}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if repo.countState(deliveryjob.StateFailed) != 1 {
		t.Fatalf("opaque job without initializer was not failed")
	}
}

// --- Delivery, retry, terminal failure ---------------------------------------

// A pre-rendered job delivers on the first claim and succeeds.
func TestWorkerDeliversRenderedJob(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, nil)

	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	rcpt, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: renderedPhoneEnvelope("S1")})

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if phone.count() != 1 {
		t.Fatalf("send count = %d, want 1", phone.count())
	}
	if j, _ := repo.get(rcpt.JobID); j.State != deliveryjob.StateSucceeded {
		t.Fatalf("job state = %q, want succeeded", j.State)
	}
}

// A transient transport failure reschedules with backoff; a later claim (after the
// backoff window) delivers the identical secret and succeeds.
func TestWorkerRetryThenSucceed(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone, failFirst: 1}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) {
		d.Config.Backoff = func(int) time.Duration { return time.Minute }
	})
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	rcpt, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: renderedPhoneEnvelope("S2")})

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce#1: %v", err)
	}
	if j, _ := repo.get(rcpt.JobID); j.State != deliveryjob.StatePending || j.LeaseID != "" {
		t.Fatalf("after failure job = %+v, want pending+unleased (rescheduled)", j)
	}
	// Not yet due: within the backoff window nothing is claimed.
	if worked, _ := w.RunOnce(context.Background()); worked {
		t.Fatal("job was claimed before its backoff elapsed")
	}
	clk.advance(2 * time.Minute)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce#2: %v", err)
	}
	if phone.count() != 2 {
		t.Fatalf("send attempts = %d, want 2", phone.count())
	}
	for _, b := range phone.bodies() {
		if !strings.Contains(b, "S2") {
			t.Fatalf("a retry changed the secret: %v", phone.bodies())
		}
	}
	if j, _ := repo.get(rcpt.JobID); j.State != deliveryjob.StateSucceeded {
		t.Fatalf("job state = %q, want succeeded", j.State)
	}
}

// The retry budget is finite: at the attempt cap the job fails terminally and its
// challenge is canceled through the initializer.
func TestWorkerTerminalFailureCancelsChallenge(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone, failFirst: 100}
	init := &fakeInitializer{}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) {
		d.Initializer = init
		d.Config.MaxAttempts = 2
		d.Config.Backoff = func(int) time.Duration { return time.Second }
	})
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	rcpt, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: renderedPhoneEnvelope("S3")})

	for i := 0; i < 5; i++ {
		if _, err := w.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		clk.advance(time.Minute)
	}
	j, _ := repo.get(rcpt.JobID)
	if j.State != deliveryjob.StateFailed {
		t.Fatalf("job state = %q, want failed", j.State)
	}
	if strings.Contains(j.LastError, "S3") || strings.Contains(j.LastError, "+15550001111") {
		t.Fatalf("last_error leaked a secret/destination: %q", j.LastError)
	}
	if init.discards() != 1 {
		t.Fatalf("challenge Discard called %d times, want 1", init.discards())
	}
}

// --- Crash-after-send replay + lease recovery --------------------------------

// A worker that crashes after the provider accepts a message but before it records
// success leaves the job reclaimable; the replay resends the IDENTICAL persisted
// secret (at-least-once), and the job eventually succeeds.
func TestWorkerCrashAfterSendReplaysSameSecret(t *testing.T) {
	repo := &flakyRepo{memRepo: newMemRepo(), failSucc: 1}
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) { d.Config.LeaseFor = time.Minute })
	svc, _ := NewService(ServiceDeps{Repo: repo.memRepo, Encrypter: fakeEncrypter{}, Now: clk.now})
	rcpt, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: renderedPhoneEnvelope("SAME")})

	// First attempt: the provider accepts (send #1) but marking succeeded "crashes".
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce#1: %v", err)
	}
	if phone.count() != 1 {
		t.Fatalf("send count = %d, want 1", phone.count())
	}
	if j, _ := repo.get(rcpt.JobID); j.State != deliveryjob.StatePending {
		t.Fatalf("job after crash = %q, want still pending", j.State)
	}

	// The lease expires; a later claim reclaims the same job and resends.
	clk.advance(2 * time.Minute)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce#2: %v", err)
	}
	if phone.count() != 2 {
		t.Fatalf("send count = %d, want 2 (replay)", phone.count())
	}
	bodies := phone.bodies()
	if bodies[0] != bodies[1] {
		t.Fatalf("replay changed the message: %q vs %q", bodies[0], bodies[1])
	}
	if j, _ := repo.get(rcpt.JobID); j.State != deliveryjob.StateSucceeded {
		t.Fatalf("job state = %q, want succeeded", j.State)
	}
}

// --- Replacement -------------------------------------------------------------

// A user-requested resend (Replace) cancels the prior pending job so only the
// replacement delivers.
func TestWorkerReplaceSupersedesPriorJob(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, nil)
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})

	first, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: renderedPhoneEnvelope("OLD")})
	if _, err := svc.Replace(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: renderedPhoneEnvelope("NEW")}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if j, _ := repo.get(first.JobID); j.State != deliveryjob.StateCanceled {
		t.Fatalf("prior job = %q, want canceled", j.State)
	}

	// Drain: only the replacement delivers.
	for {
		worked, err := w.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !worked {
			break
		}
	}
	if phone.count() != 1 || !strings.Contains(phone.bodies()[0], "NEW") {
		t.Fatalf("expected only the replacement delivered, got %v", phone.bodies())
	}
}

// --- Encryption failure ------------------------------------------------------

// A payload the worker cannot open is a permanent error: the job fails terminally
// with a secret-free reason.
func TestWorkerUndecryptablePayloadFailsTerminally(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) { d.Encrypter = fakeEncrypter{decErr: errBoom} })
	// Enqueue directly with a good encrypter so the row exists; the worker's
	// encrypter cannot open it.
	now := clk.now()
	if _, err := repo.Enqueue(context.Background(), deliveryjob.Job{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Payload: []byte("enc:{}"), AvailableAt: now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if phone.count() != 0 {
		t.Fatalf("an undecryptable job was delivered")
	}
	if repo.countState(deliveryjob.StateFailed) != 1 {
		t.Fatalf("undecryptable job was not failed terminally")
	}
}

// --- Provider timeout + cancellation -----------------------------------------

// A notifier that blocks is bounded by the worker's provider timeout: the send
// returns promptly and the job is rescheduled (not left hanging).
func TestWorkerProviderTimeoutBounds(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone, block: true}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) {
		d.Config.ProviderTimeout = 20 * time.Millisecond
		d.Config.MaxAttempts = 10
		d.Config.Backoff = func(int) time.Duration { return time.Minute }
	})
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	rcpt, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: renderedPhoneEnvelope("S")})

	done := make(chan struct{})
	go func() {
		_, _ = w.RunOnce(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnce did not return within the provider timeout window")
	}
	if j, _ := repo.get(rcpt.JobID); j.State != deliveryjob.StatePending {
		t.Fatalf("timed-out job = %q, want rescheduled pending", j.State)
	}
}

// --- Graceful shutdown / no goroutine leak -----------------------------------

// Run returns promptly when its context is canceled, even while a notifier is
// blocked in a send that honors cancellation; the in-flight job is left leased for
// reclaim rather than failed.
func TestWorkerGracefulShutdownNoLeak(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone, block: true}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) {
		d.Config.ProviderTimeout = time.Hour // only a parent-cancel can unblock the send
		d.Config.PollInterval = time.Millisecond
	})
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})
	rcpt, _ := svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "k", Envelope: renderedPhoneEnvelope("S")})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Let the worker claim and enter the blocked send, then shut down.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation (goroutine leak)")
	}
	if j, _ := repo.get(rcpt.JobID); j.State != deliveryjob.StatePending {
		t.Fatalf("job after shutdown = %q, want still pending (left for reclaim)", j.State)
	}
}

// --- Contention --------------------------------------------------------------

// Under concurrent workers each job is claimed by exactly one worker at a time and
// delivered; no job is double-succeeded and the send count equals the job count.
func TestWorkerContentionSingleClaimant(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone}
	router := phoneRouter(t, phone)
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})

	const jobs = 50
	for i := 0; i < jobs; i++ {
		if _, err := svc.Enqueue(context.Background(), Command{
			Kind:           identity.KindPhone,
			Purpose:        PurposeMagicLink,
			IdempotencyKey: fmt.Sprintf("k-%d", i),
			Envelope:       renderedPhoneEnvelope(fmt.Sprintf("S-%d", i)),
		}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := newWorker(t, repo, router, clk, func(d *WorkerDeps) { d.Config.LeaseFor = time.Hour })
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				worked, err := w.RunOnce(context.Background())
				if err != nil {
					t.Errorf("RunOnce: %v", err)
					return
				}
				if !worked {
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := repo.countState(deliveryjob.StateSucceeded); got != jobs {
		t.Fatalf("succeeded jobs = %d, want %d", got, jobs)
	}
	if got := phone.count(); got != jobs {
		t.Fatalf("send count = %d, want %d (each job delivered once, no crashes)", got, jobs)
	}
}

// --- Purge -------------------------------------------------------------------

// Purge removes terminal rows past the retention window and leaves recent and
// non-terminal rows in place.
func TestWorkerPurgeRespectRetention(t *testing.T) {
	repo := newMemRepo()
	clk := newClock()
	phone := &recordingNotifier{kind: identity.KindPhone}
	w := newWorker(t, repo, phoneRouter(t, phone), clk, func(d *WorkerDeps) { d.Config.PurgeRetention = time.Hour })
	svc, _ := NewService(ServiceDeps{Repo: repo, Encrypter: fakeEncrypter{}, Now: clk.now})

	// One delivered (terminal) job.
	svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "old", Envelope: renderedPhoneEnvelope("S")})
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// A still-pending job that must survive purge.
	svc.Enqueue(context.Background(), Command{Kind: identity.KindPhone, Purpose: PurposeMagicLink, IdempotencyKey: "live", Envelope: renderedPhoneEnvelope("S")})

	// Not yet past retention.
	if n, err := w.Purge(context.Background()); err != nil || n != 0 {
		t.Fatalf("early Purge n=%d err=%v, want 0", n, err)
	}
	clk.advance(2 * time.Hour)
	if n, err := w.Purge(context.Background()); err != nil || n != 1 {
		t.Fatalf("Purge n=%d err=%v, want 1", n, err)
	}
	if _, ok := repo.findByKeyState("live", deliveryjob.StatePending); !ok {
		t.Fatalf("purge removed a non-terminal job")
	}
}
