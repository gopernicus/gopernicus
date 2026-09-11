package pgx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// A previously fast clock leaves a timestamp ahead of the current clock.
// New admissions must still be the latest generation, independently of IDs.
func TestFencedGenerationOrderAfterClockRegression(t *testing.T) {
	for _, operation := range []string{"replace", "enqueue after terminal"} {
		t.Run(operation, func(t *testing.T) {
			q, previous := futureGenerationStore(t)
			ctx := context.Background()
			previousID := "z-seed"
			scheduled := time.Now().UTC().Truncate(time.Microsecond)
			for _, id := range []string{"y-next", "x-next", "w-next"} {
				lease := "lease-" + previousID
				claimed, err := q.Claim(ctx, time.Now().UTC(), lease, time.Minute, []string{"email"})
				if err != nil || claimed.ID() != previousID {
					t.Fatalf("generation with future CreatedAt was not immediately claimable: %+v, %v", claimed, err)
				}
				in := job.Enqueue{ID: id, Kind: "email", LogicalKey: "key", ScheduledFor: scheduled}
				var got job.Job
				if operation == "replace" {
					got, err = q.Replace(ctx, in)
				} else {
					if err := q.Cancel(ctx, previousID, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					got, err = q.EnqueueOnce(ctx, in)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !got.CreatedAt.After(previous) {
					t.Fatalf("generation %q created at %v, must follow %v", got.ID(), got.CreatedAt, previous)
				}
				if !got.UpdatedAt.Equal(got.CreatedAt) || !got.ScheduledFor.Equal(scheduled) {
					t.Fatalf("generation timestamps changed scheduling or disagree: %+v", got)
				}
				latest, err := q.GetLatestByKey(ctx, "key")
				if err != nil || latest.ID() != id || latest.JobStatus != job.StatusPending {
					t.Fatalf("latest = %+v, %v; want pending %q", latest, err, id)
				}
				stored, err := q.Get(ctx, id)
				if err != nil || !stored.CreatedAt.Equal(got.CreatedAt) {
					t.Fatalf("stored timestamp = %v, %v; returned %v", stored.CreatedAt, err, got.CreatedAt)
				}
				if err := q.Complete(ctx, previousID, lease, time.Now().UTC()); !errors.Is(err, sdk.ErrConflict) {
					t.Fatalf("prior generation lease survived replacement/cancellation: %v", err)
				}
				previous, previousID = got.CreatedAt, id
			}
			claimed, err := q.Claim(ctx, time.Now().UTC(), "final-lease", time.Minute, []string{"email"})
			if err != nil || claimed.ID() != previousID {
				t.Fatalf("final replacement was not immediately claimable: %+v, %v", claimed, err)
			}
			// A future timestamp under another key must not adjust ordinary
			// wall-clock creation times for independent or unkeyed admissions.
			for _, key := range []string{"other", ""} {
				got, err := q.EnqueueOnce(ctx, job.Enqueue{Kind: "email", LogicalKey: key, ScheduledFor: scheduled})
				if err != nil {
					t.Fatal(err)
				}
				if !got.CreatedAt.Before(previous) {
					t.Fatalf("independent key %q inherited another key's future timestamp", key)
				}
			}
		})
	}
}

func TestFencedConcurrentGenerationOrderAfterClockRegression(t *testing.T) {
	q, previous := futureGenerationStore(t)
	ctx := context.Background()
	const count = 8
	type result struct {
		j   job.Job
		err error
	}
	results := make(chan result, count)
	for i := range count {
		go func() {
			j, err := q.Replace(ctx, job.Enqueue{ID: fmt.Sprintf("replacement-%02d", i), Kind: "email", LogicalKey: "key"})
			results <- result{j, err}
		}()
	}
	var generations []job.Job
	for range count {
		r := <-results
		if r.err != nil {
			t.Errorf("Replace: %v", r.err)
			continue
		}
		generations = append(generations, r.j)
	}
	if t.Failed() {
		return
	}
	sort.Slice(generations, func(i, j int) bool { return generations[i].CreatedAt.Before(generations[j].CreatedAt) })
	for _, j := range generations {
		if !j.CreatedAt.After(previous) {
			t.Fatalf("concurrent generation %q at %v did not follow %v", j.ID(), j.CreatedAt, previous)
		}
		previous = j.CreatedAt
	}
	latest, err := q.GetLatestByKey(ctx, "key")
	if err != nil || latest.ID() != generations[count-1].ID() || latest.JobStatus != job.StatusPending {
		t.Fatalf("latest = %+v, %v; want final pending generation %q", latest, err, generations[count-1].ID())
	}
}

// Updating only this owned fixture simulates a prior process with a faster
// wall clock, without a public clock hook or a timing-sensitive test.
func futureGenerationStore(t *testing.T) (*FencedQueue, time.Time) {
	t.Helper()
	db, opts := openAndMigrate(t, requireDSN(t))
	q := NewFencedQueueStore(db, opts...)
	ctx := context.Background()
	if _, err := q.EnqueueOnce(ctx, job.Enqueue{ID: "z-seed", Kind: "email", LogicalKey: "key"}); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if _, err := db.Exec(ctx, "UPDATE "+q.table("fenced_job_queue")+" SET created_at = @future WHERE job_id = @id", pgx.NamedArgs{"future": future, "id": "z-seed"}); err != nil {
		t.Fatal(err)
	}
	return q, future
}
