package jobqueue

import "github.com/gopernicus/gopernicus/sdk/workers"

// compile-time check: JobQueue satisfies workers.Job.
var _ workers.Job = JobQueue{}

// GetID implements workers.Job.
func (j JobQueue) GetID() string { return j.JobID }

// GetStatus implements workers.Job.
func (j JobQueue) GetStatus() string { return j.Status }

// GetRetryCount implements workers.Job.
func (j JobQueue) GetRetryCount() int { return j.RetryCount }
