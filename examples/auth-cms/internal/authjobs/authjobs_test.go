package authjobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/jobs"
	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	jobsmem "github.com/gopernicus/gopernicus/features/jobs/memstore"
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
	if fake.onceKind != auth.DeliveryJobKind {
		t.Fatalf("Submit kind = %q, want %q (rail dropped, single job kind)", fake.onceKind, auth.DeliveryJobKind)
	}
	if fake.onceKey != "key-1" {
		t.Fatalf("Submit key = %q, want key-1", fake.onceKey)
	}

	if _, err := d.Replace(ctx, "phone", "login_code", "key-2", []byte("p")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if fake.replaceKind != auth.DeliveryJobKind {
		t.Fatalf("Replace kind = %q, want %q", fake.replaceKind, auth.DeliveryJobKind)
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

// TestFencedRuntimeConfigRejectsTimeoutExceedingLease proves the COMPOSED jobs-mode
// runtime construction fails closed on an invalid timeout/lease combination (AV3D-3.4/
// 3.5): a ProcessTimeout at or beyond the claim lease would let a stuck provider send
// outlive the lease so a second worker reclaims and double-processes the job. The host
// wiring (authjobs.FencedRuntimeConfig → jobs.NewFencedRuntime) surfaces this as
// jobs.ErrProcessTimeoutExceedsLease rather than silently accepting the inversion. A
// timeout safely inside the lease constructs.
func TestFencedRuntimeConfigRejectsTimeoutExceedingLease(t *testing.T) {
	svc, err := jobs.NewService(jobs.Repositories{FencedQueue: jobsmem.NewFencedQueue()}, jobs.Config{})
	if err != nil {
		t.Fatalf("jobs.NewService: %v", err)
	}
	rt := auth.DeliveryJobRuntime{
		Kind:   auth.DeliveryJobKind,
		Handle: func(context.Context, auth.DeliveryClaim) error { return nil },
	}

	// ProcessTimeout == LeaseFor and > LeaseFor both fail closed.
	for _, tc := range []struct{ lease, timeout time.Duration }{
		{lease: time.Second, timeout: time.Second},
		{lease: time.Second, timeout: 2 * time.Second},
	} {
		cfg := FencedRuntimeConfig(rt, func(c *jobs.FencedRuntimeConfig) {
			c.LeaseFor = tc.lease
			c.ProcessTimeout = tc.timeout
		})
		if _, err := jobs.NewFencedRuntime(svc, cfg); !errors.Is(err, jobs.ErrProcessTimeoutExceedsLease) {
			t.Fatalf("lease=%s timeout=%s: err = %v, want ErrProcessTimeoutExceedsLease", tc.lease, tc.timeout, err)
		}
	}

	// A timeout safely inside the lease constructs.
	ok := FencedRuntimeConfig(rt, func(c *jobs.FencedRuntimeConfig) {
		c.LeaseFor = 2 * time.Second
		c.ProcessTimeout = time.Second
	})
	if _, err := jobs.NewFencedRuntime(svc, ok); err != nil {
		t.Fatalf("timeout inside lease should construct: %v", err)
	}
}

// TestFencedRuntimeConfigBridgesClaim proves FencedRuntimeConfig registers the auth
// handler under its kind and bridges a jobs FencedClaim (payload/attempt/checkpoint)
// to an auth DeliveryClaim, and wires the discard hook to the dead-letter path.
func TestFencedRuntimeConfigBridgesClaim(t *testing.T) {
	var gotPayload []byte
	var gotAttempt int
	var checkpointed []byte
	var discarded []byte

	rt := auth.DeliveryJobRuntime{
		Kind: auth.DeliveryJobKind,
		Handle: func(ctx context.Context, claim auth.DeliveryClaim) error {
			gotPayload = claim.Payload
			gotAttempt = claim.Attempt
			return claim.Checkpoint(ctx, []byte("cp"))
		},
		Discard: func(ctx context.Context, executionID string, payload []byte) error {
			discarded = payload
			return nil
		},
	}

	cfg := FencedRuntimeConfig(rt)
	handler, ok := cfg.Handlers[auth.DeliveryJobKind]
	if !ok {
		t.Fatalf("no handler registered under %q", auth.DeliveryJobKind)
	}

	ctx := context.Background()
	err := handler(ctx, jobs.FencedClaim{
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

	dl, ok := cfg.DeadLetters[auth.DeliveryJobKind]
	if !ok {
		t.Fatalf("no dead-letter hook registered under %q", auth.DeliveryJobKind)
	}
	if err := dl(ctx, job.Job{JobID: "exec-1", Payload: json.RawMessage(`"dead"`)}); err != nil {
		t.Fatalf("dead-letter hook: %v", err)
	}
	if string(discarded) != `"dead"` {
		t.Fatalf("discard payload = %q, want \"dead\"", string(discarded))
	}
}
