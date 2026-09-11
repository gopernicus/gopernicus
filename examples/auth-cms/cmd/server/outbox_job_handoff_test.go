package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/outboxmem"
	outbox2 "github.com/gopernicus/gopernicus/pockets/events/logic/outbox"
	"github.com/gopernicus/gopernicus/pockets/jobs"
	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	jobsmem "github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

type failingOutboxMark struct {
	*outboxmem.Store
	err error
}

func (r *failingOutboxMark) MarkPublished(ctx context.Context, id string) error {
	if r.err != nil {
		return r.err
	}
	return r.Store.MarkPublished(ctx, id)
}

// Exercise the real Poller -> Memory.Dispatch -> queue.Service composition, using
// memory repositories as stand-ins for the host's durable stores. The stable
// outbox ID must survive failed marking so repeated handoffs enqueue just one job.
func TestOutboxDispatchToJobsPreservesIdempotentHandoff(t *testing.T) {
	ctx := context.Background()
	outbox := &failingOutboxMark{Store: outboxmem.New(), err: errors.New("outbox mark unavailable")}
	queue := jobsmem.NewQueue()
	svc, err := jobs.New(jobs.Repositories{Queue: queue})
	if err != nil {
		t.Fatal(err)
	}
	bus := sdkevents.NewMemory()
	defer bus.Close(ctx)
	rec, err := sdkevents.NewRecord(sdkevents.NewBaseEvent("document.changed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Append(ctx, rec); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	_, err = bus.Subscribe(rec.Type, func(ctx context.Context, evt sdkevents.Event) error {
		attempts++
		raw, err := sdkevents.EncodeEvent(evt)
		if err != nil {
			return err
		}
		_, err = svc.Queue.EnqueueJob(ctx, job.Enqueue{ID: evt.(sdkevents.Identified).EventID(), Kind: "document-index", Payload: json.RawMessage(raw)})
		if errors.Is(err, sdk.ErrAlreadyExists) {
			return nil
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	poller, err := outbox2.NewPoller(outbox, bus.Dispatch)
	if err != nil {
		t.Fatal(err)
	}
	if err := poller.Poll(ctx); !errors.Is(err, outbox.err) {
		t.Fatalf("failed mark: %v", err)
	}
	stored, err := queue.Get(ctx, rec.EventID)
	if err != nil || stored.Kind != "document-index" {
		t.Fatalf("handoff not completed before mark: job=%+v err=%v", stored, err)
	}
	outbox.err = nil
	if err := poller.Poll(ctx); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("handoff attempts=%d, want 2", attempts)
	}
	if err := poller.Poll(ctx); !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("completed outbox: %v", err)
	}
}
