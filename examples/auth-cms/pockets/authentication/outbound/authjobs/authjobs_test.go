package authjobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/jobs"
	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	jobsmem "github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// fakeEnqueuer records the kind/key each primitive is invoked with.
type fakeEnqueuer struct {
	onceKind, onceKey       string
	replaceKind, replaceKey string
	latestKey               string
}

func (f *fakeEnqueuer) EnqueueOnce(_ context.Context, kind, key string, _ []byte) (string, error) {
	f.onceKind, f.onceKey = kind, key
	return "exec-once", nil
}

func (f *fakeEnqueuer) Replace(_ context.Context, kind, key string, _ []byte) (string, error) {
	f.replaceKind, f.replaceKey = kind, key
	return "exec-replace", nil
}

func (f *fakeEnqueuer) LatestStatusByKey(_ context.Context, key string) (work.Status, error) {
	f.latestKey = key
	return work.StatusPending, nil
}

// TestDispatcherMapsToSingleKind proves the dispatcher submits every rail under the
// single auth.DeliveryJobKind (the per-command rail/purpose ride inside the sealed
// payload) and forwards the logical key through to the fenced primitives.
func TestDispatcherMapsToSingleKind(t *testing.T) {
	fake := &fakeEnqueuer{}
	d := NewDispatcher(fake)
	ctx := context.Background()

	if _, err := d.Submit(ctx, "email", "password_reset", "key-1", []byte("p")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if fake.onceKind != delivery.JobKind {
		t.Fatalf("Submit kind = %q, want %q (rail dropped, single job kind)", fake.onceKind, delivery.JobKind)
	}
	if fake.onceKey != "key-1" {
		t.Fatalf("Submit key = %q, want key-1", fake.onceKey)
	}

	if _, err := d.Replace(ctx, "phone", "login_code", "key-2", []byte("p")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if fake.replaceKind != delivery.JobKind {
		t.Fatalf("Replace kind = %q, want %q", fake.replaceKind, delivery.JobKind)
	}
	if fake.replaceKey != "key-2" {
		t.Fatalf("Replace key = %q, want key-2", fake.replaceKey)
	}

	if _, err := d.LatestStatus(ctx, "key-3"); err != nil {
		t.Fatalf("LatestStatus: %v", err)
	}
	if fake.latestKey != "key-3" {
		t.Fatalf("LatestStatus key = %q, want key-3", fake.latestKey)
	}
}

// TestRuntimeRejectsTimeoutExceedingLease proves the COMPOSED jobs-mode
// runtime construction fails closed on an invalid timeout/lease combination (AV3D-3.4/
// 3.5): a ProcessTimeout at or beyond the claim lease would let a stuck provider send
// outlive the lease so a second worker reclaims and double-processes the job. The host
// wiring (authjobs.Runtime → queue.NewFencedRuntime) surfaces this as
// jobs.ErrProcessTimeoutExceedsLease rather than silently accepting the inversion. A
// timeout safely inside the lease constructs.
func TestRuntimeRejectsTimeoutExceedingLease(t *testing.T) {
	svc, err := jobs.New(jobs.Repositories{FencedQueue: jobsmem.NewFencedQueue()})
	if err != nil {
		t.Fatalf("jobs.NewService: %v", err)
	}
	rt := delivery.JobRuntime{
		Kind:   delivery.JobKind,
		Handle: func(context.Context, delivery.Claim) error { return nil },
	}

	// ProcessTimeout == LeaseFor and > LeaseFor both fail closed.
	for _, tc := range []struct{ lease, timeout time.Duration }{
		{lease: time.Second, timeout: time.Second},
		{lease: time.Second, timeout: 2 * time.Second},
	} {
		policy := job.FencedRuntimePolicy{LeaseFor: tc.lease, ProcessTimeout: tc.timeout}
		if _, err := NewRuntime(svc.Queue, rt, job.WithFencedRuntimePolicy(policy)); !errors.Is(err, job.ErrProcessTimeoutExceedsLease) {
			t.Fatalf("lease=%s timeout=%s: err = %v, want ErrProcessTimeoutExceedsLease", tc.lease, tc.timeout, err)
		}
	}

	// A timeout safely inside the lease constructs.
	policy := job.FencedRuntimePolicy{LeaseFor: 2 * time.Second, ProcessTimeout: time.Second}
	if _, err := NewRuntime(svc.Queue, rt, job.WithFencedRuntimePolicy(policy)); err != nil {
		t.Fatalf("timeout inside lease should construct: %v", err)
	}
}

// TestRuntimeBridgesClaim proves Runtime registers the auth
// handler under its kind and bridges a jobs FencedClaim (payload/attempt/checkpoint)
// to an auth DeliveryClaim, and wires the discard hook to the dead-letter path.
func TestRuntimeBridgesClaim(t *testing.T) {
	var gotPayload []byte
	var gotAttempt int
	var checkpointed []byte
	var discarded []byte

	rt := delivery.JobRuntime{
		Kind: delivery.JobKind,
		Handle: func(ctx context.Context, claim delivery.Claim) error {
			gotPayload = claim.Payload
			gotAttempt = claim.Attempt
			return claim.Checkpoint(ctx, []byte("cp"))
		},
		Discard: func(ctx context.Context, executionID string, payload []byte) error {
			discarded = payload
			return nil
		},
	}

	handler, ok := handlers(rt)[delivery.JobKind]
	if !ok {
		t.Fatalf("no handler registered under %q", delivery.JobKind)
	}

	ctx := context.Background()
	err := handler(ctx, job.FencedClaim{
		ExecutionID: "exec-1",
		LeaseID:     "lease-1",
		Payload:     json.RawMessage(`"sealed"`),
		Attempt:     2,
		Checkpoint: func(_ context.Context, payload json.RawMessage) error {
			checkpointed = []byte(payload)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if string(gotPayload) != `"sealed"` {
		t.Fatalf("bridged payload = %q, want \"sealed\"", string(gotPayload))
	}
	if gotAttempt != 2 {
		t.Fatalf("bridged attempt = %d, want 2", gotAttempt)
	}
	if string(checkpointed) != "cp" {
		t.Fatalf("checkpoint bridged = %q, want cp", string(checkpointed))
	}

	dl, ok := deadLetters(rt)[delivery.JobKind]
	if !ok {
		t.Fatalf("no dead-letter hook registered under %q", delivery.JobKind)
	}
	if err := dl(ctx, job.Job{JobID: "exec-1", Payload: json.RawMessage(`"dead"`)}); err != nil {
		t.Fatalf("dead-letter hook: %v", err)
	}
	if string(discarded) != `"dead"` {
		t.Fatalf("discard payload = %q, want \"dead\"", string(discarded))
	}
}
