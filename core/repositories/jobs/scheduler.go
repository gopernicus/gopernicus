// This file is created once by gopernicus and will NOT be overwritten.
// It adapts this project's job queue to the framework's scheduler engine.

package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/core/jobs/scheduler"

	"github.com/gopernicus/gopernicus/core/repositories/jobs/jobqueue"
)

// scheduledJobMaxRetries is how many processing attempts a scheduler-fired
// job gets before dead-lettering, matching the worker runner's default.
const scheduledJobMaxRetries = 3

// EnqueueScheduled inserts one scheduler firing into the job queue,
// mapping the queue's already-exists error to scheduler.ErrJobExists so
// idempotent refires stay silent. It satisfies scheduler.EnqueueFunc:
//
//	sched := scheduler.New(repos.JobSchedule, repos.EnqueueScheduled, log)
func (r *Repositories) EnqueueScheduled(ctx context.Context, job scheduler.Job) error {
	_, err := r.JobQueue.Create(ctx, jobqueue.CreateJobQueue{
		JobID:         job.JobID,
		EventType:     job.EventType,
		CorrelationID: job.JobID,
		Payload:       job.Payload,
		OccurredAt:    job.OccurredAt,
		Status:        "PENDING",
		MaxRetries:    scheduledJobMaxRetries,
	})
	if errors.Is(err, jobqueue.ErrJobQueueAlreadyExists) {
		return fmt.Errorf("%w: %s", scheduler.ErrJobExists, job.JobID)
	}
	return err
}
