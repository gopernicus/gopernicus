package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// RunQueueOwnership checks detached results and terminal-state preservation.
func RunQueueOwnership(t *testing.T, newRepo func(*testing.T) job.QueueRepository) {
	t.Helper()
	t.Run("DetachedValues", func(t *testing.T) {
		ctx := context.Background()
		repo := newRepo(t)
		payload := []byte(`{"n":1}`)
		inserted, err := repo.Enqueue(ctx, job.Enqueue{Kind: "owned", Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		payload[5] = '2'
		inserted.Payload[5] = '3'
		assertPayload(t, repo, inserted.JobID, `{"n":1}`)
		got, err := repo.Get(ctx, inserted.JobID)
		if err != nil {
			t.Fatal(err)
		}
		got.Payload[5] = '4'
		page, err := repo.List(ctx, job.ListFilter{}, list.Request{})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("List = %+v, %v", page, err)
		}
		page.Items[0].Payload[5] = '5'
		assertPayload(t, repo, inserted.JobID, `{"n":1}`)
		now := time.Now().UTC()
		claimed, err := repo.Claim(ctx, "worker", now, nil)
		if err != nil {
			t.Fatal(err)
		}
		claimed.Payload[5] = '6'
		claimedAt := *claimed.ClaimedAt
		*claimed.ClaimedAt = now.Add(-time.Hour)
		got, err = repo.Get(ctx, inserted.JobID)
		if err != nil || got.ClaimedAt == nil || !got.ClaimedAt.Equal(claimedAt) {
			t.Fatalf("returned claim changed stored ClaimedAt: %+v, %v", got, err)
		}
		assertPayload(t, repo, inserted.JobID, `{"n":1}`)
		if err := repo.Complete(ctx, inserted.JobID, now); err != nil {
			t.Fatal(err)
		}
		completed, err := repo.Get(ctx, inserted.JobID)
		if err != nil || completed.CompletedAt == nil {
			t.Fatalf("completed = %+v, %v", completed, err)
		}
		original := *completed.CompletedAt
		*completed.CompletedAt = now.Add(time.Hour)
		got, err = repo.Get(ctx, inserted.JobID)
		if err != nil || !got.CompletedAt.Equal(original) {
			t.Fatalf("returned CompletedAt changed stored value: %+v, %v", got, err)
		}
	})
	t.Run("TerminalTransitions", func(t *testing.T) {
		ctx := context.Background()
		repo := newRepo(t)
		now := time.Now().UTC()
		completed, err := repo.Enqueue(ctx, job.Enqueue{Kind: "done"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Claim(ctx, "worker", now.Add(time.Second), nil); err != nil {
			t.Fatal(err)
		}
		if err := repo.Complete(ctx, completed.JobID, now.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		before, _ := repo.Get(ctx, completed.JobID)
		if err := repo.Fail(ctx, completed.JobID, now.Add(3*time.Second), "late", 5); !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("Fail completed = %v, want conflict", err)
		}
		if err := repo.Complete(ctx, completed.JobID, now.Add(4*time.Second)); err != nil {
			t.Fatalf("repeat Complete = %v", err)
		}
		after, _ := repo.Get(ctx, completed.JobID)
		if after.JobStatus != job.StatusCompleted || after.Retries != before.Retries || !after.CompletedAt.Equal(*before.CompletedAt) || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("terminal completion changed: before=%+v after=%+v", before, after)
		}
		dead, err := repo.Enqueue(ctx, job.Enqueue{Kind: "dead"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Claim(ctx, "worker", now.Add(5*time.Second), nil); err != nil {
			t.Fatal(err)
		}
		if err := repo.Fail(ctx, dead.JobID, now.Add(6*time.Second), "original", 1); err != nil {
			t.Fatal(err)
		}
		before, _ = repo.Get(ctx, dead.JobID)
		if err := repo.Complete(ctx, dead.JobID, now.Add(7*time.Second)); !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("Complete dead letter = %v, want conflict", err)
		}
		if err := repo.Fail(ctx, dead.JobID, now.Add(8*time.Second), "late", 5); err != nil {
			t.Fatalf("repeat Fail = %v", err)
		}
		after, _ = repo.Get(ctx, dead.JobID)
		if after.JobStatus != job.StatusDeadLetter || after.Retries != before.Retries || after.FailureReason != "original" || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("terminal failure changed: before=%+v after=%+v", before, after)
		}
	})
}

// RunFencedOwnership checks payload/metadata ownership and failed replacement.
func RunFencedOwnership(t *testing.T, newRepo func(*testing.T) job.FencedQueueRepository) {
	t.Helper()
	t.Run("DetachedValues", func(t *testing.T) {
		ctx := context.Background()
		repo := requireFenced(t, newRepo)
		payload := []byte("original")
		inserted, err := repo.EnqueueOnce(ctx, job.Enqueue{Kind: "owned", LogicalKey: "key", Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		payload[0] = 'A'
		inserted.Payload[0] = 'B'
		again, err := repo.EnqueueOnce(ctx, job.Enqueue{Kind: "owned", LogicalKey: "key"})
		if err != nil {
			t.Fatal(err)
		}
		again.Payload[0] = 'C'
		latest, err := repo.GetLatestByKey(ctx, "key")
		if err != nil {
			t.Fatal(err)
		}
		latest.Payload[0] = 'D'
		assertPayload(t, repo, inserted.JobID, "original")
		now := time.Now().UTC()
		claimed, err := repo.Claim(ctx, now, "lease", time.Hour, nil)
		if err != nil {
			t.Fatal(err)
		}
		claimed.Payload[0] = 'E'
		*claimed.ClaimedAt = now.Add(-time.Hour)
		assertPayload(t, repo, inserted.JobID, "original")
		checkpoint := []byte("rendered")
		if err := repo.Checkpoint(ctx, inserted.JobID, "lease", checkpoint, now); err != nil {
			t.Fatal(err)
		}
		checkpoint[0] = 'F'
		assertPayload(t, repo, inserted.JobID, "rendered")
		got, err := repo.Get(ctx, inserted.JobID)
		if err != nil || got.ClaimedAt == nil || got.ClaimedAt.Before(now.Add(-time.Second)) {
			t.Fatalf("returned claim changed stored ClaimedAt: %+v, %v", got, err)
		}
		got.Payload[0] = 'G'
		if err := repo.Complete(ctx, inserted.JobID, "lease", now); err != nil {
			t.Fatal(err)
		}
		completed, err := repo.Get(ctx, inserted.JobID)
		if err != nil || completed.CompletedAt == nil || completed.TerminalAt == nil {
			t.Fatalf("completed = %+v, %v", completed, err)
		}
		completedAt, terminalAt := *completed.CompletedAt, *completed.TerminalAt
		*completed.CompletedAt = now.Add(time.Hour)
		*completed.TerminalAt = now.Add(time.Hour)
		got, err = repo.Get(ctx, inserted.JobID)
		if err != nil || !got.CompletedAt.Equal(completedAt) || !got.TerminalAt.Equal(terminalAt) {
			t.Fatalf("returned terminal metadata changed storage: %+v, %v", got, err)
		}
		assertPayload(t, repo, inserted.JobID, "rendered")
		payload = []byte("replacement")
		replacement, err := repo.Replace(ctx, job.Enqueue{Kind: "owned", LogicalKey: "key", Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		payload[0] = 'H'
		replacement.Payload[0] = 'I'
		assertPayload(t, repo, replacement.JobID, "replacement")
	})
	t.Run("FailedReplacePreservesActive", func(t *testing.T) {
		ctx := context.Background()
		repo := requireFenced(t, newRepo)
		first, err := repo.EnqueueOnce(ctx, job.Enqueue{ID: "original", Kind: "owned", LogicalKey: "key", Payload: []byte("original")})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, err := repo.Claim(ctx, now, "lease", time.Hour, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Replace(ctx, job.Enqueue{ID: first.JobID, Kind: "owned", LogicalKey: "key"}); !errors.Is(err, sdk.ErrAlreadyExists) {
			t.Fatalf("duplicate Replace = %v", err)
		}
		got, err := repo.GetLatestByKey(ctx, "key")
		if err != nil || got.JobID != first.JobID || got.JobStatus != job.StatusRunning || got.LeaseID != "lease" {
			t.Fatalf("failed Replace changed active generation: %+v, %v", got, err)
		}
		if err := repo.Complete(ctx, first.JobID, "lease", now); err != nil {
			t.Fatalf("failed Replace invalidated original lease: %v", err)
		}
	})
}

func assertPayload(t *testing.T, repo interface {
	Get(context.Context, string) (job.Job, error)
}, id, want string) {
	t.Helper()
	got, err := repo.Get(context.Background(), id)
	if err != nil || string(got.Payload) != want {
		t.Fatalf("Get %s payload = %q, %v; want %q", id, got.Payload, err, want)
	}
}
