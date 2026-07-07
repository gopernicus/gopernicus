// Package memstore is the jobs feature's in-core reference store: stdlib-only,
// mutex-backed implementations of both feature ports (job.QueueRepository and
// schedule.Repository) that back the storetest conformance suite and the proof
// host. It is a PUBLIC package inside the feature core (ratified R3) — the named
// exception to the test-scoped-default rule, because a lease-respecting
// concurrent queue is too substantial to duplicate example-locally.
//
// It is honest: it enforces exactly what the port doc comments promise —
// enqueue ID-uniqueness (errs.ErrAlreadyExists), claim ordering (priority DESC,
// then created_at), scheduled_for gating, lease reclaim of stale running jobs,
// the workers.ErrNoWork empty-queue signal, retry-then-dead-letter transitions,
// and the schedule value-CAS — so a naive implementation's drift is caught here
// against the reference before any dialect store runs the same suite.
package memstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/logic/job"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/workers"
)

// DefaultLease is the stale-claim recovery window applied when no WithLease
// option is passed: a running job whose claimed_at is older than this is
// reclaimable by a later Claim (design §6.3, default 15m).
const DefaultLease = 15 * time.Minute

// orderField is the keyset order column List paginates by; it must match the
// cursor's order field so a stale cursor from a different sort is ignored.
const orderField = "created_at"

// Compile-time seam: the Queue fills the exact job.QueueRepository port.
var _ job.QueueRepository = (*Queue)(nil)

// Option configures a Queue.
type Option func(*Queue)

// WithLease sets the stale-claim recovery window. A running job whose claimed_at
// is older than d becomes claimable again (design §6.3). Non-positive values are
// ignored and the default is kept.
func WithLease(d time.Duration) Option {
	return func(q *Queue) {
		if d > 0 {
			q.lease = d
		}
	}
}

// Queue is the in-memory job.QueueRepository. It serializes every operation on a
// single mutex, so concurrent Claim callers never receive the same job by
// construction (the honesty note in storetest: the concurrency assertions are
// only load-bearing against real dialects).
type Queue struct {
	mu    sync.Mutex
	jobs  map[string]job.Job
	lease time.Duration
}

// NewQueue builds an empty in-memory queue store with the default lease unless
// WithLease overrides it.
func NewQueue(opts ...Option) *Queue {
	q := &Queue{
		jobs:  map[string]job.Job{},
		lease: DefaultLease,
	}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

// Enqueue inserts one job. A caller-supplied ID that already exists yields
// errs.ErrAlreadyExists (the idempotency key); an empty ID is generated. The job
// is stored pending with the input's scheduled_for, priority, and max_attempts
// verbatim — the store invents no defaults (that is the service's job).
func (q *Queue) Enqueue(_ context.Context, in job.Enqueue) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	id := in.ID
	if id == "" {
		id = newID("job")
	}
	if _, ok := q.jobs[id]; ok {
		return job.Job{}, errs.ErrAlreadyExists
	}

	now := time.Now().UTC()
	j := job.Job{
		JobID:        id,
		Kind:         in.Kind,
		Payload:      in.Payload,
		JobStatus:    job.StatusPending,
		Priority:     in.Priority,
		MaxAttempts:  in.MaxAttempts,
		ScheduledFor: in.ScheduledFor,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	q.jobs[id] = j
	return j, nil
}

// Claim atomically transitions exactly one due job to running for workerID and
// returns it, or returns workers.ErrNoWork when none is due. "Due" is a pending
// job with scheduled_for <= now, OR a running job whose lease has expired
// (claimed_at < now - lease). Selection order is priority DESC, then created_at
// ascending, with the job id as a final tie-break for determinism.
func (q *Queue) Claim(_ context.Context, workerID string, now time.Time) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	staleBefore := now.Add(-q.lease)
	var best job.Job
	found := false
	for _, j := range q.jobs {
		if !q.due(j, now, staleBefore) {
			continue
		}
		if !found || claimBefore(j, best) {
			best = j
			found = true
		}
	}
	if !found {
		return job.Job{}, workers.ErrNoWork
	}

	claimed := now
	best.JobStatus = job.StatusRunning
	best.WorkerName = workerID
	best.ClaimedAt = &claimed
	best.UpdatedAt = now
	q.jobs[best.JobID] = best
	return best, nil
}

// due reports whether j can be claimed at now: a pending job past its
// scheduled_for, or a running job whose lease expired.
func (q *Queue) due(j job.Job, now, staleBefore time.Time) bool {
	switch j.JobStatus {
	case job.StatusPending:
		return !j.ScheduledFor.After(now)
	case job.StatusRunning:
		return j.ClaimedAt != nil && j.ClaimedAt.Before(staleBefore)
	default:
		return false
	}
}

// claimBefore reports whether a should be claimed ahead of b: higher priority
// first, then older created_at, then lower id.
func claimBefore(a, b job.Job) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.JobID < b.JobID
}

// Complete marks the job done. A missing id yields errs.ErrNotFound.
func (q *Queue) Complete(_ context.Context, jobID string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[jobID]
	if !ok {
		return errs.ErrNotFound
	}
	completed := now
	j.JobStatus = job.StatusCompleted
	j.CompletedAt = &completed
	j.UpdatedAt = now
	q.jobs[jobID] = j
	return nil
}

// Fail increments retry_count and either reschedules the job to pending (below
// maxAttempts) or dead-letters it once the attempts are exhausted. A rescheduled
// job clears its claim so it is immediately claimable again; reason is recorded
// as the failure cause. A missing id yields errs.ErrNotFound.
func (q *Queue) Fail(_ context.Context, jobID string, now time.Time, reason string, maxAttempts int) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[jobID]
	if !ok {
		return errs.ErrNotFound
	}
	j.Retries++
	j.FailureReason = reason
	j.UpdatedAt = now
	if j.Retries >= maxAttempts {
		j.JobStatus = job.StatusDeadLetter
	} else {
		j.JobStatus = job.StatusPending
		j.WorkerName = ""
		j.ClaimedAt = nil
	}
	q.jobs[jobID] = j
	return nil
}

// Get returns the job with the given id, or errs.ErrNotFound.
func (q *Queue) Get(_ context.Context, id string) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[id]
	if !ok {
		return job.Job{}, errs.ErrNotFound
	}
	return j, nil
}

// List returns a cursor-paginated page of jobs matching the filter, ordered by
// (created_at, id) descending.
func (q *Queue) List(_ context.Context, f job.ListFilter, req crud.ListRequest) (crud.Page[job.Job], error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var all []job.Job
	for _, j := range q.jobs {
		if f.Kind != "" && j.Kind != f.Kind {
			continue
		}
		if f.Status != "" && j.JobStatus != f.Status {
			continue
		}
		all = append(all, j)
	}
	return page(all, req, func(j job.Job) (time.Time, string) { return j.CreatedAt, j.JobID })
}

// page sorts items by (created_at, id) DESC, applies the keyset cursor, and
// trims via the shared codec — the keyset shape a dialect store implements in
// SQL, hand-rolled here so the reference paginates identically.
func page[T any](items []T, req crud.ListRequest, key func(T) (time.Time, string)) (crud.Page[T], error) {
	sort.Slice(items, func(i, j int) bool {
		ti, ii := key(items[i])
		tj, ij := key(items[j])
		if ti.Equal(tj) {
			return ii > ij
		}
		return ti.After(tj)
	})

	cur, err := crud.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return crud.Page[T]{}, err
	}
	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		var after []T
		for _, it := range items {
			t, id := key(it)
			if t.Before(cv) || (t.Equal(cv) && id < cur.PK) {
				after = append(after, it)
			}
		}
		items = after
	}

	limit := req.NormalizedLimit()
	if len(items) > limit+1 {
		items = items[:limit+1]
	}
	return crud.TrimPage(items, limit, func(it T) (string, error) {
		t, id := key(it)
		return crud.EncodeCursor(orderField, t, id)
	})
}

// newID returns a random, collision-free identifier with the given prefix.
func newID(prefix string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
