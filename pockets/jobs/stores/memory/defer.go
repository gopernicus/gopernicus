package memory

import (
	"context"
	"time"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

var (
	_ workers.JobDeferrer    = (*Queue)(nil)
	_ workers.FencedDeferrer = (*FencedQueue)(nil)
)

// Defer releases a running job until availableAt without consuming a retry.
func (q *Queue) Defer(ctx context.Context, id string, availableAt time.Time, reason string, now time.Time) error {
	if !availableAt.After(now) {
		return sdk.ErrInvalidInput
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if j.JobStatus != job.StatusRunning {
		return sdk.ErrConflict
	}
	j.JobStatus = job.StatusPending
	j.ScheduledFor = availableAt
	j.WorkerName = ""
	j.ClaimedAt = nil
	j.FailureReason = reason
	j.UpdatedAt = now
	q.jobs[id] = j
	return nil
}

// Defer releases a live claim until availableAt and refunds only that claim's
// attempt. The ownership check makes a duplicate or stale refund impossible.
func (q *FencedQueue) Defer(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error {
	if !availableAt.After(now) {
		return sdk.ErrInvalidInput
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if !heldBy(j, leaseID, now) || j.Retries < 1 {
		return sdk.ErrConflict
	}
	j.JobStatus = job.StatusPending
	j.ScheduledFor = availableAt
	j.Retries--
	j.LeaseID = ""
	j.LeasedUntil = time.Time{}
	j.WorkerName = ""
	j.ClaimedAt = nil
	j.FailureReason = reason
	j.UpdatedAt = now
	q.jobs[id] = j
	return nil
}
