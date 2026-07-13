package authsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
)

// memDeliveryRepo is a concurrent-safe in-test deliveryjob.Repository. It enforces
// the durable-outbox invariants the real stores prove (idempotent enqueue by key,
// oldest-due single-claimant lease, lease-checked completion, latest-by-key status
// read) so the harness drives the real delivery.Service/Worker seal→claim→deliver
// path synchronously.
type memDeliveryRepo struct {
	mu   sync.Mutex
	seq  int
	jobs map[string]deliveryjob.Job
}

func newMemDeliveryRepo() *memDeliveryRepo { return &memDeliveryRepo{jobs: map[string]deliveryjob.Job{}} }

func (r *memDeliveryRepo) insertLocked(job deliveryjob.Job) deliveryjob.Job {
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

func (r *memDeliveryRepo) Enqueue(_ context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.jobs {
		if !ex.Terminal() && ex.IdempotencyKey == job.IdempotencyKey {
			return ex, nil
		}
	}
	return r.insertLocked(job), nil
}

func (r *memDeliveryRepo) Replace(_ context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
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

func (r *memDeliveryRepo) Claim(_ context.Context, now time.Time, leaseID string, leaseFor time.Duration) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now = now.UTC()
	var due deliveryjob.Job
	found := false
	for _, ex := range r.jobs {
		if !ex.Due(now) {
			continue
		}
		if !found || ex.CreatedAt.Before(due.CreatedAt) {
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

func (r *memDeliveryRepo) complete(id, leaseID, state, lastErr string, now time.Time) error {
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
	job.TerminalAt = now.UTC()
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now.UTC()
	r.jobs[id] = job
	return nil
}

func (r *memDeliveryRepo) Succeed(_ context.Context, id, leaseID string, now time.Time) error {
	return r.complete(id, leaseID, deliveryjob.StateSucceeded, "", now)
}

func (r *memDeliveryRepo) Fail(_ context.Context, id, leaseID, lastErr string, now time.Time) error {
	return r.complete(id, leaseID, deliveryjob.StateFailed, lastErr, now)
}

func (r *memDeliveryRepo) Retry(_ context.Context, id, leaseID string, availableAt time.Time, lastErr string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if job.Terminal() || job.LeaseID != leaseID {
		return sdk.ErrConflict
	}
	job.AvailableAt = availableAt.UTC()
	job.LastError = lastErr
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now.UTC()
	r.jobs[id] = job
	return nil
}

func (r *memDeliveryRepo) Cancel(_ context.Context, id string, now time.Time) error {
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
	job.TerminalAt = now.UTC()
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now.UTC()
	r.jobs[id] = job
	return nil
}

func (r *memDeliveryRepo) PurgeTerminal(_ context.Context, before time.Time, limit int) (int, error) {
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

func (r *memDeliveryRepo) GetLatestByIdempotencyKey(_ context.Context, key string) (deliveryjob.Job, error) {
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

// drainingQueue wraps the real delivery.Service and drains the real Worker
// synchronously after each enqueue, so a test that reads the recording mailer right
// after a service call sees the worker-delivered message. The async, non-draining
// behavior (bounded start latency under a blocking provider) is exercised directly in
// TestForgotPasswordBoundedTiming with the plain service and a manually run worker.
type drainingQueue struct {
	svc    *delivery.Service
	worker *delivery.Worker
	t      *testing.T
}

func (d *drainingQueue) Enqueue(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	r, err := d.svc.Enqueue(ctx, cmd)
	if err != nil {
		return r, err
	}
	d.drain(ctx)
	return r, nil
}

func (d *drainingQueue) Replace(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	r, err := d.svc.Replace(ctx, cmd)
	if err != nil {
		return r, err
	}
	d.drain(ctx)
	return r, nil
}

func (d *drainingQueue) Status(ctx context.Context, key string) (delivery.Status, error) {
	return d.svc.Status(ctx, key)
}

func (d *drainingQueue) drain(ctx context.Context) {
	for i := 0; i < 100; i++ {
		worked, err := d.worker.RunOnce(ctx)
		if err != nil {
			d.t.Fatalf("delivery worker RunOnce: %v", err)
		}
		if !worked {
			return
		}
	}
	d.t.Fatalf("delivery worker did not drain (possible loop)")
}

// wireSyncDelivery builds the real outbox seam (a router over mailer, the
// delivery.Service queue, and a Worker whose Initializer is svc) and injects it into
// svc white-box, wrapped in a draining queue that makes the send path synchronous so
// a test can assert on the delivered mail right after a service call. It returns the
// in-mem job repo for status assertions. Notifiers routes non-email kinds.
func wireSyncDelivery(t *testing.T, svc *Service, mailer email.Sender, notifiers map[string]notify.Notifier) *memDeliveryRepo {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	router, err := delivery.NewRouter(delivery.Deps{Mailer: mailer, MailFrom: "noreply@example.com", Notifiers: notifiers, Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	repo := newMemDeliveryRepo()
	enc := fakeEncrypter{}
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Repo: repo, Encrypter: enc})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	worker, err := delivery.NewWorker(delivery.WorkerDeps{
		Repo:        repo,
		Encrypter:   enc,
		Router:      router,
		Initializer: svc,
		Logger:      quiet,
	})
	if err != nil {
		t.Fatalf("delivery.NewWorker: %v", err)
	}
	svc.deliver = router
	svc.queue = &drainingQueue{svc: dsvc, worker: worker, t: t}
	return repo
}

// wireDelivery injects the synchronous outbox into the main harness service.
func (h *harness) wireDelivery(t *testing.T) {
	t.Helper()
	h.deliveryRepo = wireSyncDelivery(t, h.svc, h.mailer, nil)
}

// blockingSender is an email.Sender that blocks in Send until released — the
// "blocking fake provider" the enumeration-timing test runs the worker against to
// prove the unauthenticated start never waits on the provider.
type blockingSender struct{ release chan struct{} }

func (b *blockingSender) Send(ctx context.Context, _ email.Message) error {
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestForgotPasswordEnqueuesOpaqueSamePath proves the enumeration contract (design
// §4.1): a known-verified and an unknown address traverse the SAME request path —
// each enqueues exactly one OPAQUE job (only the normalized identifier, no rendered
// body, no account resolution) and neither is claimed on the request path. The
// worker resolves off the request path, so known and unknown are indistinguishable.
func TestForgotPasswordEnqueuesOpaqueSamePath(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")

	// Swap the synchronous draining queue for the plain async service so
	// ForgotPassword only enqueues (the worker never runs on the request path).
	enc := fakeEncrypter{}
	repo := newMemDeliveryRepo()
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Repo: repo, Encrypter: enc})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	h.svc.queue = dsvc

	for _, addr := range []string{"known@example.com", "ghost@example.com"} {
		if err := h.svc.ForgotPassword(context.Background(), addr); err != nil {
			t.Fatalf("ForgotPassword(%q): %v", addr, err)
		}
	}

	repo.mu.Lock()
	jobs := make([]deliveryjob.Job, 0, len(repo.jobs))
	for _, j := range repo.jobs {
		jobs = append(jobs, j)
	}
	repo.mu.Unlock()
	if len(jobs) != 2 {
		t.Fatalf("enqueued %d jobs, want 2 (one per address, same path)", len(jobs))
	}
	for _, j := range jobs {
		if j.AttemptCount != 0 {
			t.Errorf("job claimed on the request path (attempt=%d); resolution must be off-path", j.AttemptCount)
		}
		env, err := delivery.Open(enc, j.Payload)
		if err != nil {
			t.Fatalf("open payload: %v", err)
		}
		if env.Body != "" || env.HTML != "" {
			t.Errorf("request-path job is not opaque: body=%q html=%q", env.Body, env.HTML)
		}
		if env.ResolutionInput == "" {
			t.Errorf("opaque job missing resolution input")
		}
	}
}

// TestForgotPasswordDoesNotBlockOnProvider proves a blocking provider does not delay
// the unauthenticated start (phase acceptance): with the worker running against a
// provider that blocks indefinitely, ForgotPassword returns promptly for both a
// known and an unknown address, and their handler timing is comparable.
func TestForgotPasswordDoesNotBlockOnProvider(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")

	enc := fakeEncrypter{}
	repo := newMemDeliveryRepo()
	blocking := &blockingSender{release: make(chan struct{})}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	router, err := delivery.NewRouter(delivery.Deps{Mailer: blocking, MailFrom: "noreply@example.com", Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Repo: repo, Encrypter: enc})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	worker, err := delivery.NewWorker(delivery.WorkerDeps{Repo: repo, Encrypter: enc, Router: router, Initializer: h.svc, Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewWorker: %v", err)
	}
	h.svc.queue = dsvc

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = worker.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		close(blocking.release)
		<-done
	})

	timeStart := func(addr string) time.Duration {
		start := time.Now()
		if err := h.svc.ForgotPassword(context.Background(), addr); err != nil {
			t.Fatalf("ForgotPassword(%q): %v", addr, err)
		}
		return time.Since(start)
	}
	known := timeStart("known@example.com")
	unknown := timeStart("ghost@example.com")

	const bound = 200 * time.Millisecond
	if known > bound || unknown > bound {
		t.Fatalf("start blocked on the provider: known=%v unknown=%v (bound %v)", known, unknown, bound)
	}
}

// TestDeliveryStatusReflectsState proves the session-gated status projection reads
// the durable outbox through the new read-only port: after a delivered job the
// receipt key reports succeeded, and an unknown receipt is sdk.ErrNotFound.
func TestDeliveryStatusReflectsState(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()

	if _, err := h.svc.queue.Enqueue(ctx, delivery.Command{
		Kind:           "email",
		Purpose:        delivery.PurposeRegistrationVerification,
		IdempotencyKey: "status-key",
		Envelope:       delivery.Envelope{Destination: "s@example.com", Body: "hi", HTML: "<p>hi</p>"},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	st, err := h.svc.DeliveryStatus(ctx, "status-key")
	if err != nil {
		t.Fatalf("DeliveryStatus: %v", err)
	}
	if st.State != deliveryjob.StateSucceeded {
		t.Errorf("status state = %q, want succeeded (drained)", st.State)
	}
	if _, err := h.svc.DeliveryStatus(ctx, "no-such-receipt"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("unknown receipt err = %v, want ErrNotFound", err)
	}
}
