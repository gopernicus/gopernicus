package memory

import (
	"context"
	"testing"
	"time"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
)

func TestFencedQueueReplaceAfterClockMovesBackward(t *testing.T) {
	ctx := context.Background()
	q := NewFencedQueue()
	future := time.Now().UTC().Add(time.Hour)
	previous := job.Job{
		JobID: "z-old", LogicalKey: "same", JobStatus: job.StatusPending,
		CreatedAt: future, UpdatedAt: future,
	}
	other := job.Job{
		JobID: "other", LogicalKey: "different", JobStatus: job.StatusPending,
		CreatedAt: future.Add(time.Hour), UpdatedAt: future.Add(time.Hour),
	}
	q.jobs[previous.JobID] = previous
	q.jobs[other.JobID] = other

	for _, id := range []string{"b-new", "a-new"} {
		replacement, err := q.Replace(ctx, job.Enqueue{ID: id, Kind: "email", LogicalKey: "same"})
		if err != nil {
			t.Fatalf("Replace: %v", err)
		}
		if replacement.JobStatus != job.StatusPending || !replacement.CreatedAt.After(previous.CreatedAt) {
			t.Fatalf("replacement = %+v, want pending with CreatedAt after %v", replacement, previous.CreatedAt)
		}
		if !replacement.CreatedAt.Before(other.CreatedAt) {
			t.Fatal("unrelated key affected the replacement timestamp")
		}
		latest, err := q.GetLatestByKey(ctx, "same")
		if err != nil || latest.JobID != replacement.JobID || latest.JobStatus != job.StatusPending {
			t.Fatalf("GetLatestByKey = %+v, %v; want pending %q", latest, err, replacement.JobID)
		}
		old, err := q.Get(ctx, previous.JobID)
		if err != nil || old.JobStatus != job.StatusSuperseded || old.TerminalAt == nil {
			t.Fatalf("previous generation = %+v, %v; want superseded", old, err)
		}
		if !old.CreatedAt.Equal(previous.CreatedAt) {
			t.Error("replacement changed the prior creation timestamp")
		}
		previous = replacement
	}

	unchanged, err := q.Get(ctx, other.JobID)
	if err != nil || !unchanged.CreatedAt.Equal(other.CreatedAt) || !unchanged.UpdatedAt.Equal(other.UpdatedAt) || unchanged.JobStatus != other.JobStatus {
		t.Fatalf("unrelated job changed: %+v, %v", unchanged, err)
	}
}

func TestFencedQueueEnqueueTimestampByLogicalKey(t *testing.T) {
	for _, key := range []string{"same", ""} {
		t.Run("key="+key, func(t *testing.T) {
			ctx := context.Background()
			q := NewFencedQueue()
			future := time.Now().UTC().Add(time.Hour)
			q.jobs["z-old"] = job.Job{
				JobID: "z-old", LogicalKey: key, JobStatus: job.StatusCanceled,
				CreatedAt: future, UpdatedAt: future,
			}
			admitted, err := q.EnqueueOnce(ctx, job.Enqueue{ID: "a-new", Kind: "email", LogicalKey: key})
			if err != nil {
				t.Fatalf("EnqueueOnce: %v", err)
			}
			if key == "" {
				if !admitted.CreatedAt.Before(future) {
					t.Fatal("empty keys unexpectedly share creation ordering")
				}
			} else if !admitted.CreatedAt.After(future) {
				t.Fatal("new keyed execution did not follow the terminal generation")
			}
		})
	}
}
