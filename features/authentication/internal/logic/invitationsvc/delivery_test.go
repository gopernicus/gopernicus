package invitationsvc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
)

// fakeEncrypter is a reversible, non-secret test encrypter so the sealed outbox
// payload round-trips for the worker.
type fakeEncrypter struct{}

func (fakeEncrypter) Encrypt(plaintext string) (string, error)  { return "enc:" + plaintext, nil }
func (fakeEncrypter) Decrypt(ciphertext string) (string, error) { return strings.TrimPrefix(ciphertext, "enc:"), nil }

// memDeliveryRepo is a concurrent-safe in-test deliveryjob.Repository enforcing the
// durable-outbox invariants the real stores prove, so newSvc drives the real
// delivery.Service/Worker seal→claim→deliver path synchronously.
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

// drainingQueue wraps the real delivery.Service and drains the real Worker after each
// enqueue so the invitation/member-added mail lands on the recording transports
// synchronously (invitationsvc only enqueues pre-rendered jobs, so the worker needs
// no Initializer).
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

// wireSyncDelivery injects the real synchronous outbox seam into svc white-box so the
// invitation/member-added send sites enqueue and the worker delivers to the wired
// transports within the call.
func wireSyncDelivery(t *testing.T, svc *Service, mailer email.Sender, notifiers map[string]notify.Notifier) {
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
	worker, err := delivery.NewWorker(delivery.WorkerDeps{Repo: repo, Encrypter: enc, Router: router, Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewWorker: %v", err)
	}
	svc.deliverer = router
	svc.queue = &drainingQueue{svc: dsvc, worker: worker, t: t}
}
