package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

func runQueueDeferral(t *testing.T, newRepo func(*testing.T) job.QueueRepository) {
	for _, failures := range []int{0, 2} {
		t.Run(fmt.Sprintf("Preserves%dFailures", failures), func(t *testing.T) {
			repo := newRepo(t)
			deferrer, ok := repo.(workers.JobDeferrer)
			if !ok {
				t.Skip("optional JobDeferrer is not implemented")
			}
			ctx := context.Background()
			mustEnqueue(t, repo, deferralInput())
			for i := range failures {
				now := suiteBase.Add(time.Duration(i) * time.Second)
				if _, err := repo.Claim(ctx, "failed-worker", now, nil); err != nil {
					t.Fatal(err)
				}
				if err := repo.Fail(ctx, "deferred", now, "processing failed", 5); err != nil {
					t.Fatal(err)
				}
			}
			now := suiteBase.Add(10 * time.Second)
			claimed, err := repo.Claim(ctx, "gated-worker", now, nil)
			if err != nil || claimed.Retries != failures {
				t.Fatalf("claim = %+v, %v; want %d prior failures", claimed, err, failures)
			}
			until := now.Add(time.Minute)
			if err := deferrer.Defer(ctx, claimed.ID(), until, "capacity unavailable", now); err != nil {
				t.Fatal(err)
			}
			want := deferredJob(claimed, until, "capacity unavailable", now)
			assertDeferralStored(t, repo.Get, want)
			if err := deferrer.Defer(ctx, claimed.ID(), until, "duplicate", now); !errors.Is(err, sdk.ErrConflict) {
				t.Fatalf("duplicate Defer = %v, want conflict", err)
			}
			assertDeferralStored(t, repo.Get, want)
			if _, err := repo.Claim(ctx, "early-worker", until.Add(-time.Second), nil); !errors.Is(err, workers.ErrNoWork) {
				t.Fatalf("early Claim = %v, want no work", err)
			}
			reclaimed, err := repo.Claim(ctx, "next-worker", until, nil)
			if err != nil || reclaimed.ID() != claimed.ID() || reclaimed.Retries != failures {
				t.Fatalf("due Claim = %+v, %v; want same job and %d failures", reclaimed, err, failures)
			}
		})
	}
	for _, state := range []string{"pending", "completed", "dead_letter", "invalid_future"} {
		t.Run("Rejects_"+state, func(t *testing.T) {
			repo := newRepo(t)
			deferrer, ok := repo.(workers.JobDeferrer)
			if !ok {
				t.Skip("optional JobDeferrer is not implemented")
			}
			ctx := context.Background()
			now := suiteBase
			until := now.Add(time.Minute)
			if err := deferrer.Defer(ctx, "missing", until, "gate", now); !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("unknown Defer = %v, want not found", err)
			}
			mustEnqueue(t, repo, deferralInput())
			if state != "pending" {
				if _, err := repo.Claim(ctx, "worker", now, nil); err != nil {
					t.Fatal(err)
				}
			}
			switch state {
			case "completed":
				if err := repo.Complete(ctx, "deferred", now); err != nil {
					t.Fatal(err)
				}
			case "dead_letter":
				if err := repo.Fail(ctx, "deferred", now, "failed", 1); err != nil {
					t.Fatal(err)
				}
			}
			before, err := repo.Get(ctx, "deferred")
			if err != nil {
				t.Fatal(err)
			}
			if state == "invalid_future" {
				for _, invalid := range []time.Time{{}, now.Add(-time.Second), now} {
					if err := deferrer.Defer(ctx, before.ID(), invalid, "gate", now); !errors.Is(err, sdk.ErrInvalidInput) {
						t.Fatalf("Defer(%v) = %v, want invalid input", invalid, err)
					}
					assertDeferralStored(t, repo.Get, before)
				}
				return
			}
			if err := deferrer.Defer(ctx, before.ID(), until, "gate", now); !errors.Is(err, sdk.ErrConflict) {
				t.Fatalf("Defer(%s) = %v, want conflict", state, err)
			}
			assertDeferralStored(t, repo.Get, before)
		})
	}
}

func runFencedDeferral(t *testing.T, newRepo func(*testing.T) job.FencedQueueRepository) {
	for _, failures := range []int{0, 1} {
		t.Run(fmt.Sprintf("RefundsClaimAfter%dFailures", failures), func(t *testing.T) {
			repo := requireFenced(t, newRepo)
			deferrer, ok := repo.(workers.FencedDeferrer)
			if !ok {
				t.Skip("optional FencedDeferrer is not implemented")
			}
			ctx := context.Background()
			mustEnqueueOnce(t, repo, deferralInput())
			for i := range failures {
				now := suiteBase.Add(time.Duration(i) * time.Second)
				if _, err := repo.Claim(ctx, now, "failed-lease", time.Minute, nil); err != nil {
					t.Fatal(err)
				}
				if err := repo.Reschedule(ctx, "deferred", "failed-lease", now.Add(time.Second), "processing failed", now); err != nil {
					t.Fatal(err)
				}
			}
			now := suiteBase.Add(10 * time.Second)
			claimed, err := repo.Claim(ctx, now, "gated-lease", time.Minute, nil)
			if err != nil || claimed.Retries != failures+1 {
				t.Fatalf("claim = %+v, %v; want %d attempts", claimed, err, failures+1)
			}
			if err := repo.Checkpoint(ctx, claimed.ID(), claimed.LeaseID, []byte{0, 255, '\n', 128}, now); err != nil {
				t.Fatal(err)
			}
			claimed, err = repo.Get(ctx, claimed.ID())
			if err != nil {
				t.Fatal(err)
			}
			until := now.Add(2 * time.Minute)
			if err := deferrer.Defer(ctx, claimed.ID(), claimed.LeaseID, until, "capacity unavailable", now); err != nil {
				t.Fatal(err)
			}
			want := deferredJob(claimed, until, "capacity unavailable", now)
			want.Retries = failures
			want.LeaseID = ""
			want.LeasedUntil = time.Time{}
			assertDeferralStored(t, repo.Get, want)
			if err := deferrer.Defer(ctx, claimed.ID(), claimed.LeaseID, until, "duplicate", now); !errors.Is(err, sdk.ErrConflict) {
				t.Fatalf("duplicate Defer = %v, want conflict", err)
			}
			assertDeferralStored(t, repo.Get, want)
			if _, err := repo.Claim(ctx, until.Add(-time.Second), "early-lease", time.Minute, nil); !errors.Is(err, workers.ErrNoWork) {
				t.Fatalf("early Claim = %v, want no work", err)
			}
			reclaimed, err := repo.Claim(ctx, until, "next-lease", time.Minute, nil)
			if err != nil || reclaimed.ID() != claimed.ID() || reclaimed.Retries != failures+1 {
				t.Fatalf("due Claim = %+v, %v; want same job and %d attempts", reclaimed, err, failures+1)
			}
		})
	}
	for _, state := range []string{"pending", "wrong_lease", "expired", "reclaimed", "completed", "dead_letter", "canceled", "superseded", "invalid_future"} {
		t.Run("Rejects_"+state, func(t *testing.T) {
			repo := requireFenced(t, newRepo)
			deferrer, ok := repo.(workers.FencedDeferrer)
			if !ok {
				t.Skip("optional FencedDeferrer is not implemented")
			}
			ctx := context.Background()
			now := suiteBase
			if err := deferrer.Defer(ctx, "missing", "lease", now.Add(time.Minute), "gate", now); !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("unknown Defer = %v, want not found", err)
			}
			mustEnqueueOnce(t, repo, deferralInput())
			if state != "pending" {
				if _, err := repo.Claim(ctx, now, "lease", time.Minute, nil); err != nil {
					t.Fatal(err)
				}
			}
			leaseID := "lease"
			var transitionErr error
			var replacement job.Job
			switch state {
			case "wrong_lease":
				leaseID = "other-lease"
			case "expired":
				now = now.Add(time.Minute)
			case "reclaimed":
				now = now.Add(time.Minute)
				_, transitionErr = repo.Claim(ctx, now, "new-lease", time.Minute, nil)
			case "completed":
				transitionErr = repo.Complete(ctx, "deferred", leaseID, now)
			case "dead_letter":
				transitionErr = repo.Fail(ctx, "deferred", leaseID, "failed", now)
			case "canceled":
				transitionErr = repo.Cancel(ctx, "deferred", now)
			case "superseded":
				input := deferralInput()
				input.ID = "replacement"
				replacement, transitionErr = repo.Replace(ctx, input)
			}
			if transitionErr != nil {
				t.Fatal(transitionErr)
			}
			before, err := repo.Get(ctx, "deferred")
			if err != nil {
				t.Fatal(err)
			}
			if state == "invalid_future" {
				for _, invalid := range []time.Time{{}, now.Add(-time.Second), now} {
					if err := deferrer.Defer(ctx, before.ID(), leaseID, invalid, "gate", now); !errors.Is(err, sdk.ErrInvalidInput) {
						t.Fatalf("Defer(%v) = %v, want invalid input", invalid, err)
					}
					assertDeferralStored(t, repo.Get, before)
				}
				return
			}
			if err := deferrer.Defer(ctx, before.ID(), leaseID, now.Add(time.Minute), "gate", now); !errors.Is(err, sdk.ErrConflict) {
				t.Fatalf("Defer(%s) = %v, want conflict", state, err)
			}
			assertDeferralStored(t, repo.Get, before)
			if replacement.ID() != "" {
				assertDeferralStored(t, repo.Get, replacement)
			}
		})
	}
	t.Run("ConcurrentRefund", func(t *testing.T) {
		repo := requireFenced(t, newRepo)
		deferrer, ok := repo.(workers.FencedDeferrer)
		if !ok {
			t.Skip("optional FencedDeferrer is not implemented")
		}
		ctx := context.Background()
		mustEnqueueOnce(t, repo, deferralInput())
		claimed, err := repo.Claim(ctx, suiteBase, "lease", time.Minute, nil)
		if err != nil {
			t.Fatal(err)
		}
		until := suiteBase.Add(2 * time.Minute)
		start := make(chan struct{})
		results := make(chan error, 2)
		for range 2 {
			go func() {
				<-start
				results <- deferrer.Defer(ctx, claimed.ID(), claimed.LeaseID, until, "gate", suiteBase)
			}()
		}
		close(start)
		succeeded, conflicted := 0, 0
		for range 2 {
			err := <-results
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, sdk.ErrConflict):
				conflicted++
			default:
				t.Errorf("concurrent Defer = %v", err)
			}
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("concurrent Defer: %d successes and %d conflicts, want one each", succeeded, conflicted)
		}
		want := deferredJob(claimed, until, "gate", suiteBase)
		want.Retries = 0
		want.LeaseID = ""
		want.LeasedUntil = time.Time{}
		assertDeferralStored(t, repo.Get, want)
	})
}

func deferralInput() job.Enqueue {
	return job.Enqueue{
		ID: "deferred", Kind: "delivery", TenantID: "tenant", LogicalKey: "delivery-key",
		Payload: []byte(`{ "delivery": "unchanged", "order": 1 }`), ScheduledFor: suiteBase, Priority: 3, MaxAttempts: 5,
	}
}

func deferredJob(before job.Job, until time.Time, reason string, now time.Time) job.Job {
	before.JobStatus = job.StatusPending
	before.ScheduledFor = until
	before.WorkerName = ""
	before.ClaimedAt = nil
	before.FailureReason = reason
	before.UpdatedAt = now
	return before
}

func assertDeferralStored(t *testing.T, get func(context.Context, string) (job.Job, error), want job.Job) {
	t.Helper()
	got, err := get(context.Background(), want.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored job = %+v; want %+v (including exact payload and metadata)", got, want)
	}
}
