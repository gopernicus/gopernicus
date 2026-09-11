package memory

import (
	"bytes"
	"time"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
)

// cloneJob detaches mutable values at the store boundary. A returned job must
// never provide a way to change persisted payloads or claim/terminal metadata.
func cloneJob(j job.Job) job.Job {
	j.Payload = bytes.Clone(j.Payload)
	j.ClaimedAt = cloneTime(j.ClaimedAt)
	j.CompletedAt = cloneTime(j.CompletedAt)
	j.TerminalAt = cloneTime(j.TerminalAt)
	return j
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}
