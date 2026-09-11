package queue_test

import (
	"context"
	"errors"
	"testing"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestFencedAdmissionRejectsEmptyKindBeforeMutation(t *testing.T) {
	ctx := context.Background()
	queue := memory.NewFencedQueue()
	svc, err := pocketjobs.New(pocketjobs.Repositories{FencedQueue: queue})
	if err != nil {
		t.Fatal(err)
	}
	id, err := svc.Queue.EnqueueOnce(ctx, "kind", "key", []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		call func() (string, error)
	}{
		{"EnqueueOnce", func() (string, error) { return svc.Queue.EnqueueOnce(ctx, "", "new", nil) }},
		{"Replace", func() (string, error) { return svc.Queue.Replace(ctx, "", "key", nil) }},
		{"EnqueueOnceIn", func() (string, error) { return svc.Queue.EnqueueOnceIn(ctx, queueapi.EnqueueOnceInput{}) }},
		{"ReplaceIn", func() (string, error) { return svc.Queue.ReplaceIn(ctx, queueapi.ReplaceInput{LogicalKey: "key"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.call(); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("empty kind = %v, want invalid input", err)
			}
			latest, err := queue.GetLatestByKey(ctx, "key")
			if err != nil || latest.JobID != id || latest.JobStatus != queueapi.StatusPending || string(latest.Payload) != "original" {
				t.Fatalf("invalid admission changed existing work: %+v, %v", latest, err)
			}
		})
	}
	if _, err := svc.Queue.EnqueueOnceIn(ctx, queueapi.EnqueueOnceInput{Kind: "kind"}); err != nil {
		t.Fatalf("optional key must remain supported: %v", err)
	}
}
