package job_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// TestStatusAliasesWorkVocabulary proves job.Status and its seven constants are
// aliases of the canonical sdk/capabilities/work vocabulary — one source of truth,
// not a duplicate literal set — and that the persisted strings stay byte-identical.
func TestStatusAliasesWorkVocabulary(t *testing.T) {
	cases := []struct {
		got  job.Status
		want work.Status
		str  string
	}{
		{job.StatusPending, work.StatusPending, "pending"},
		{job.StatusRunning, work.StatusRunning, "running"},
		{job.StatusCompleted, work.StatusCompleted, "completed"},
		{job.StatusFailed, work.StatusFailed, "failed"},
		{job.StatusDeadLetter, work.StatusDeadLetter, "dead_letter"},
		{job.StatusCanceled, work.StatusCanceled, "canceled"},
		{job.StatusSuperseded, work.StatusSuperseded, "superseded"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("job constant %q != work constant %q", c.got, c.want)
		}
		if string(c.got) != c.str {
			t.Errorf("job constant %q != persisted string %q", c.got, c.str)
		}
	}
}

// TestJobTerminalDelegates proves Job.Terminal delegates to the aliased
// work.Status.Terminal predicate: the four end states are terminal, and
// pending/running/failed (retryable) are not.
func TestJobTerminalDelegates(t *testing.T) {
	cases := []struct {
		status   job.Status
		terminal bool
	}{
		{job.StatusPending, false},
		{job.StatusRunning, false},
		{job.StatusFailed, false},
		{job.StatusCompleted, true},
		{job.StatusDeadLetter, true},
		{job.StatusCanceled, true},
		{job.StatusSuperseded, true},
	}
	for _, c := range cases {
		if got := (job.Job{JobStatus: c.status}).Terminal(); got != c.terminal {
			t.Errorf("Job{%q}.Terminal() = %v, want %v", c.status, got, c.terminal)
		}
	}
}
