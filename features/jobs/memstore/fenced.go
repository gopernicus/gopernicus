package memstore

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

// Compile-time seam: FencedQueue fills the frozen job.FencedQueueRepository port
// (which is a strict superset of the kernel's workers.FencedStore).
var (
	_ job.FencedQueueRepository    = (*FencedQueue)(nil)
	_ workers.FencedStore[job.Job] = (*FencedQueue)(nil)
)

// FencedQueue is the in-memory reference for the hardened, lease-fenced queue
// (job.FencedQueueRepository). It serializes every operation on a single mutex, so
// two concurrent claimers never receive the same job by construction, and it
// enforces the per-claim lease fence: Checkpoint/Complete/Fail/Reschedule succeed
// only for the job's current LeaseID, and a reclaimed or superseded holder is
// fenced out with sdk.ErrConflict.
//
// It is a SEPARATE type from Queue: a single store type cannot satisfy both the
// unfenced QueueRepository.Claim/Complete/Fail (which the cron/schedule runtime
// still drives) and the lease-fenced shapes here. The two coexist until the phase-5
// migration retires the bespoke path. This task (AV3D-1.1) implements and proves
// the lease-fenced transitions; the logical-key (AV3D-1.2), checkpoint (AV3D-1.3),
// and retry-at/purge (AV3D-1.4) methods are implemented so the port is satisfied,
// but their shared conformance cases stay skipped until those tasks activate them
// with pgx/turso parity.
type FencedQueue struct {
	mu   sync.Mutex
	jobs map[string]job.Job
}

// NewFencedQueue builds an empty in-memory fenced queue store. The claim lease is
// per-claim (the caller supplies leaseFor to Claim), so there is no store-level
// lease default to configure.
func NewFencedQueue() *FencedQueue {
	return &FencedQueue{jobs: map[string]job.Job{}}
}

// EnqueueOnce inserts in as a new pending execution unless a non-terminal job
// already holds in.LogicalKey, in which case that active job is returned unchanged
// (idempotent admission). A duplicate explicit in.ID yields sdk.ErrAlreadyExists;
// an empty in.ID is generated; an empty LogicalKey disables the once semantics.
func (q *FencedQueue) EnqueueOnce(_ context.Context, in job.Enqueue) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if in.LogicalKey != "" {
		if active, ok := q.activeByKey(in.LogicalKey); ok {
			return active, nil
		}
	}
	return q.insert(in)
}

// Replace atomically supersedes: it marks every non-terminal job holding
// in.LogicalKey StatusSuperseded (terminal, stamping TerminalAt and clearing any
// live lease so a running holder is fenced) and inserts in as one fresh pending
// execution, which it returns.
func (q *FencedQueue) Replace(_ context.Context, in job.Enqueue) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now().UTC()
	if in.LogicalKey != "" {
		for id, j := range q.jobs {
			if j.LogicalKey != in.LogicalKey || j.Terminal() {
				continue
			}
			j.JobStatus = job.StatusSuperseded
			terminal := now
			j.TerminalAt = &terminal
			j.LeaseID = ""
			j.LeasedUntil = time.Time{}
			j.UpdatedAt = now
			q.jobs[id] = j
		}
	}
	return q.insert(in)
}

// Claim atomically leases the oldest due job under the caller-supplied fresh
// leaseID for leaseFor, incrementing Retries and returning it; no due job yields
// workers.ErrNoWork. "Due" is a pending job with ScheduledFor at or before now, or
// a running job whose lease has passed.
func (q *FencedQueue) Claim(_ context.Context, now time.Time, leaseID string, leaseFor time.Duration) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var best job.Job
	found := false
	for _, j := range q.jobs {
		if !fencedDue(j, now) {
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
	best.LeaseID = leaseID
	best.LeasedUntil = now.Add(leaseFor)
	best.ClaimedAt = &claimed
	best.Retries++
	best.UpdatedAt = now
	q.jobs[best.JobID] = best
	return best, nil
}

// Checkpoint atomically replaces the payload of the running job id while the caller
// still holds the current lease, byte-for-byte, preserving identity and status. A
// reclaimed or superseded lease yields sdk.ErrConflict; a non-running or unknown
// job yields sdk.ErrConflict / sdk.ErrNotFound.
func (q *FencedQueue) Checkpoint(_ context.Context, id, leaseID string, payload json.RawMessage, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if !heldBy(j, leaseID, now) {
		return sdk.ErrConflict
	}
	j.Payload = payload
	j.UpdatedAt = now
	q.jobs[id] = j
	return nil
}

// Complete marks the leaseID-held job StatusCompleted (terminal). A reclaimed or
// superseded lease yields sdk.ErrConflict; an already-completed job from the same
// holder is idempotent nil; an unknown id yields sdk.ErrNotFound.
func (q *FencedQueue) Complete(_ context.Context, id, leaseID string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if j.JobStatus == job.StatusCompleted {
		if j.LeaseID == leaseID {
			return nil // idempotent from the last holder
		}
		return sdk.ErrConflict
	}
	if !heldBy(j, leaseID, now) {
		return sdk.ErrConflict
	}
	completed := now
	j.JobStatus = job.StatusCompleted
	j.CompletedAt = &completed
	j.TerminalAt = &completed
	j.UpdatedAt = now
	q.jobs[id] = j
	return nil
}

// Reschedule moves the leaseID-held job back to StatusPending at availableAt
// (retry-at), clearing the lease and recording reason. A reclaimed/superseded lease
// or an already-terminal job yields sdk.ErrConflict; an unknown id yields
// sdk.ErrNotFound.
func (q *FencedQueue) Reschedule(_ context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if !heldBy(j, leaseID, now) {
		return sdk.ErrConflict
	}
	j.JobStatus = job.StatusPending
	j.ScheduledFor = availableAt
	j.LeaseID = ""
	j.LeasedUntil = time.Time{}
	j.ClaimedAt = nil
	j.FailureReason = reason
	j.UpdatedAt = now
	q.jobs[id] = j
	return nil
}

// Fail permanently dead-letters the leaseID-held job (StatusDeadLetter, terminal)
// with reason. A reclaimed or superseded lease yields sdk.ErrConflict; an
// already-dead-lettered job from the same holder is idempotent nil; an unknown id
// yields sdk.ErrNotFound.
func (q *FencedQueue) Fail(_ context.Context, id, leaseID, reason string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if j.JobStatus == job.StatusDeadLetter {
		if j.LeaseID == leaseID {
			return nil // idempotent from the last holder
		}
		return sdk.ErrConflict
	}
	if !heldBy(j, leaseID, now) {
		return sdk.ErrConflict
	}
	terminal := now
	j.JobStatus = job.StatusDeadLetter
	j.FailureReason = reason
	j.TerminalAt = &terminal
	j.UpdatedAt = now
	q.jobs[id] = j
	return nil
}

// Cancel terminally cancels a non-terminal job by id (StatusCanceled), independent
// of any lease. An already-canceled job is idempotent nil; an
// already-completed/dead-lettered/superseded job yields sdk.ErrConflict; an unknown
// id yields sdk.ErrNotFound.
func (q *FencedQueue) Cancel(_ context.Context, id string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if j.JobStatus == job.StatusCanceled {
		return nil // idempotent
	}
	if j.Terminal() {
		return sdk.ErrConflict
	}
	terminal := now
	j.JobStatus = job.StatusCanceled
	j.TerminalAt = &terminal
	j.LeaseID = ""
	j.LeasedUntil = time.Time{}
	j.UpdatedAt = now
	q.jobs[id] = j
	return nil
}

// PurgeTerminal deletes up to limit terminal jobs whose TerminalAt is at or before
// before and returns the number removed, never touching a non-terminal job.
func (q *FencedQueue) PurgeTerminal(_ context.Context, before time.Time, limit int) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var candidates []job.Job
	for _, j := range q.jobs {
		if !j.Terminal() || j.TerminalAt == nil || j.TerminalAt.After(before) {
			continue
		}
		candidates = append(candidates, j)
	}
	sort.Slice(candidates, func(i, k int) bool {
		if !candidates[i].TerminalAt.Equal(*candidates[k].TerminalAt) {
			return candidates[i].TerminalAt.Before(*candidates[k].TerminalAt)
		}
		return candidates[i].JobID < candidates[k].JobID
	})
	if limit >= 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	for _, j := range candidates {
		delete(q.jobs, j.JobID)
	}
	return len(candidates), nil
}

// GetLatestByKey returns the most-recently-created execution holding logicalKey
// (greatest CreatedAt, JobID DESC tiebreak), or sdk.ErrNotFound. It never mutates.
func (q *FencedQueue) GetLatestByKey(_ context.Context, logicalKey string) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var latest job.Job
	found := false
	for _, j := range q.jobs {
		if j.LogicalKey != logicalKey {
			continue
		}
		if !found || newerByKey(j, latest) {
			latest = j
			found = true
		}
	}
	if !found {
		return job.Job{}, sdk.ErrNotFound
	}
	return latest, nil
}

// Get returns the job with the given unique execution id, or sdk.ErrNotFound.
func (q *FencedQueue) Get(_ context.Context, id string) (job.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.jobs[id]
	if !ok {
		return job.Job{}, sdk.ErrNotFound
	}
	return j, nil
}

// insert stores a fresh pending execution from in. The caller holds the mutex.
func (q *FencedQueue) insert(in job.Enqueue) (job.Job, error) {
	id := in.ID
	if id == "" {
		id = newID("job")
	}
	if _, ok := q.jobs[id]; ok {
		return job.Job{}, sdk.ErrAlreadyExists
	}

	now := time.Now().UTC()
	j := job.Job{
		JobID:        id,
		Kind:         in.Kind,
		Payload:      in.Payload,
		JobStatus:    job.StatusPending,
		Priority:     in.Priority,
		MaxAttempts:  in.MaxAttempts,
		LogicalKey:   in.LogicalKey,
		ScheduledFor: in.ScheduledFor,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	q.jobs[id] = j
	return j, nil
}

// activeByKey returns the single non-terminal job holding logicalKey. The caller
// holds the mutex.
func (q *FencedQueue) activeByKey(logicalKey string) (job.Job, bool) {
	for _, j := range q.jobs {
		if j.LogicalKey == logicalKey && !j.Terminal() {
			return j, true
		}
	}
	return job.Job{}, false
}

// heldBy reports whether j is the running job currently held by leaseID at now —
// the fence Checkpoint/Complete/Fail/Reschedule enforce. A reclaimed lease (a
// different LeaseID) or an expired lease (now at or after LeasedUntil) is not held.
func heldBy(j job.Job, leaseID string, now time.Time) bool {
	return j.JobStatus == job.StatusRunning && j.LeaseID == leaseID && now.Before(j.LeasedUntil)
}

// fencedDue reports whether j can be claimed at now: a pending job past its
// ScheduledFor, or a running job whose lease has passed (stale-claim recovery).
func fencedDue(j job.Job, now time.Time) bool {
	switch j.JobStatus {
	case job.StatusPending:
		return !j.ScheduledFor.After(now)
	case job.StatusRunning:
		return !j.LeasedUntil.After(now)
	default:
		return false
	}
}

// newerByKey reports whether a is a newer generation than b under the same logical
// key: greater CreatedAt, then greater JobID as the deterministic tiebreak.
func newerByKey(a, b job.Job) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.JobID > b.JobID
}
