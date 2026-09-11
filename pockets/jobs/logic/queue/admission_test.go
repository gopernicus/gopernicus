package queue_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestOrdinaryAdmissionJSON(t *testing.T) {
	ctx := context.Background()
	queue := memory.NewQueue()
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: queue})
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []json.RawMessage{[]byte(`{"incomplete":`), []byte(" "), {0xff, 0x00}} {
		if _, err := svc.Queue.Enqueue(ctx, "kind", payload); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid JSON %q = %v, want invalid input", payload, err)
		}
	}
	page, err := queue.List(ctx, job.ListFilter{}, list.Request{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("invalid JSON inserted work: %+v, %v", page, err)
	}
	for _, tc := range []struct {
		name    string
		payload json.RawMessage
		want    string
	}{
		{"nil", nil, `{}`},
		{"empty", json.RawMessage{}, `{}`},
		{"preserves valid bytes", json.RawMessage(" { \"b\": 2, \"a\": 1 } \n"), " { \"b\": 2, \"a\": 1 } \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inserted, err := svc.Queue.EnqueueJob(ctx, job.Enqueue{Kind: "kind", Payload: tc.payload})
			if err != nil {
				t.Fatal(err)
			}
			got, err := queue.Get(ctx, inserted.JobID)
			if err != nil || string(got.Payload) != tc.want {
				t.Fatalf("stored payload = %q, %v; want %q", got.Payload, err, tc.want)
			}
		})
	}
}
