package queue_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
)

func TestRuntimeHonorsPersistedRetryLimit(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		configured, persisted int
		attempts              int
		permanent             bool
	}{
		{"lower per-job ceiling", 5, 1, 1, false},
		{"higher per-job ceiling", 1, 5, 5, false},
		{"direct insert uses default", 2, 0, 2, false},
		{"permanent overrides ceiling", 5, 10, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			queue := memory.NewQueue()
			calls := 0
			svcRuntimeConfig := runtimeTestConfig{Workers: 1, PollInterval: time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Handlers: map[string]queueapi.HandlerFunc{"kind": func(context.Context, queueapi.Job) error {
				calls++
				if calls == tc.attempts {
					cancel()
				}
				if tc.permanent {
					return queueapi.Permanent("permanent")
				}
				return errors.New("temporary")
			}}}
			svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: queue}, pocketjobs.WithMaxAttempts(tc.configured))
			if err != nil {
				t.Fatal(err)
			}
			in := queueapi.Enqueue{Kind: "kind", MaxAttempts: tc.persisted}
			var inserted queueapi.Job
			if tc.persisted == 0 {
				inserted, err = queue.Enqueue(ctx, in)
			} else {
				inserted, err = svc.Queue.EnqueueJob(ctx, in)
			}
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := runtimeFromTestConfig(svc.Queue, svc.Schedules, svcRuntimeConfig)
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.Run(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := queue.Get(context.Background(), inserted.JobID)
			if err != nil {
				t.Fatal(err)
			}
			if calls != tc.attempts || got.Retries != tc.attempts || got.JobStatus != queueapi.StatusDeadLetter {
				t.Fatalf("calls=%d, job=%+v; want dead letter after %d failures", calls, got, tc.attempts)
			}
		})
	}
}
