package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	deliverycmd "github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/command"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/deliverychar"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// TestInProcessCharacterization runs the transport-neutral delivery characterization
// suite (deliverychar) against the BOUNDED in-process runtime (AV3D-4.5) — the third
// Harness over the same neutral cases the bespoke Worker (TestCharacterization) and the
// command Engine (TestProcessorCharacterization) pass. It drives the REAL
// delivery.InProcessQueue arbiter (submit-once, replace/generation fencing, latest-by-key
// status) and the REAL InProcessRuntime.process delivery loop (claim, checkpoint-before-send,
// bounded process-local retry, terminal dead-letter + discard) over the SAME
// command.Engine both other modes run, so the bounded mode is held to the identical
// OBSERVABLE guarantees before the bespoke queue is deleted (phase 5).
//
// The AV3D-4.3 log deferred this harness to 4.5 for two DURABLE-ONLY cases; the honest
// mapping is a per-case t.Skip with an in-test justification (silently dropping the suite
// is not honest). The bounded, EPHEMERAL, process-local runtime deliberately does NOT
// provide the durable/reschedule semantics of three cases, each skipped with the
// dedicated test that proves the property the bounded mode CAN honor:
//
//   - CrashAfterSendReplaysSameSecret: models a claim lease + cross-restart reclaim. The
//     ephemeral runtime has no lease and loses in-flight work on shutdown, so a
//     crash-after-send replay is out of scope. The identical-secret retry it CAN do is
//     proven by TestInProcessRetryReusesSameSecret.
//   - PurgeRespectsRetention: models an operator-driven terminal purge. The ephemeral
//     runtime reclaims terminal status automatically via bounded retention (max entries +
//     TTL), proven by TestInProcessQueueRetentionMaxEntries / TestInProcessQueueRetentionTTL.
//   - ProviderTimeoutIsBounded: models a timed-out send rescheduled to a durable PENDING
//     row. The runtime bounds a blocked send identically (the shared engine
//     ProviderTimeout, proven by TestInProcessProviderTimeoutBounded) but retries
//     in-worker to a terminal state rather than rescheduling, so "still pending after one
//     drain" is out of scope.
//
// Drain runs each admitted item through the real InProcessRuntime.process synchronously
// (rather than the concurrent Run pool, whose fixed size, shutdown drain, cancellation,
// and no-goroutine-per-request are proven by the AV3D-4.1 pool tests): the characterization
// asserts observable OUTCOMES, and running the SAME process loop is the faithful mapping.
// Backoff is forced to zero for the same reason — the suite asserts identical-secret retry
// and send counts, not backoff timing (proven by TestInProcessBackoffCancellationPrompt).
func TestInProcessCharacterization(t *testing.T) {
	deliverychar.Run(t, newInProcessHarness)
}

// inProcessHarness adapts the delivery.Service (over an InProcessQueue arbiter) plus the
// InProcessRuntime processing loop onto the neutral deliverychar.Harness.
type inProcessHarness struct {
	svc   *Service
	queue *InProcessQueue
	rt    *InProcessRuntime
}

// newInProcessHarness builds a fresh harness for one neutral Scenario, skipping the
// durable/reschedule-only cases honestly (see the suite doc above).
func newInProcessHarness(t *testing.T, s deliverychar.Scenario) deliverychar.Harness {
	t.Helper()
	if s.Provider == nil {
		t.Fatal("deliverychar.Scenario.Provider is required")
	}
	switch {
	case s.CrashCompletions > 0:
		t.Skip("in_process is EPHEMERAL: no claim lease, no cross-restart reclaim — a crash-after-send replay is out of scope; identical-secret retry is proven by TestInProcessRetryReusesSameSecret")
	case s.PurgeRetention > 0:
		t.Skip("in_process has no operator-driven purge: terminal status is reclaimed by automatic bounded retention — proven by TestInProcessQueueRetentionMaxEntries / TestInProcessQueueRetentionTTL")
	case s.ProviderTimeout > 0:
		t.Skip("in_process bounds a blocked send identically (engine ProviderTimeout, proven by TestInProcessProviderTimeoutBounded) but retries in-worker to terminal rather than rescheduling to a pending row — the reschedule semantic is out of scope")
	}

	enc := fakeEncrypter{}
	router := newRouter(t, &stubSender{}, map[string]notify.Notifier{identity.KindPhone: notify.Notifier(s.Provider)})

	queue := NewInProcessQueue(InProcessQueueConfig{Capacity: 64})
	svc, err := NewService(ServiceDeps{Dispatcher: queue, Encrypter: enc})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	procDeps := JobsProcessorDeps{
		Encrypter: enc,
		Router:    router,
		Config:    deliverycmd.Config{MaxAttempts: s.MaxAttempts},
	}
	if s.Initializer != nil {
		procDeps.Initializer = charInitializer{n: s.Initializer}
	}
	proc, err := NewJobsProcessor(procDeps)
	if err != nil {
		t.Fatalf("NewJobsProcessor: %v", err)
	}

	rt, err := NewInProcessRuntime(queue, proc, InProcessRuntimeConfig{
		Workers:     1,
		MaxAttempts: s.MaxAttempts,
		// Synchronous Drain: retries progress within one Drain. Backoff TIMING and
		// cancellation are proven by TestInProcessBackoffCancellationPrompt.
		Backoff: func(int) time.Duration { return 0 },
	})
	if err != nil {
		t.Fatalf("NewInProcessRuntime: %v", err)
	}
	return &inProcessHarness{svc: svc, queue: queue, rt: rt}
}

func (h *inProcessHarness) Submit(t *testing.T, sub deliverychar.Submission) string {
	t.Helper()
	rcpt, err := h.svc.Enqueue(context.Background(), command(sub))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return rcpt.Key
}

func (h *inProcessHarness) Replace(t *testing.T, sub deliverychar.Submission) string {
	t.Helper()
	rcpt, err := h.svc.Replace(context.Background(), command(sub))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	return rcpt.Key
}

// Drain runs every admitted item through the REAL InProcessRuntime.process loop until the
// bounded admission channel is empty. process claims (skipping a superseded generation),
// checkpoints before send, retries a transient failure inline (backoff forced to zero),
// caps attempts, and dead-letters + discards a terminal failure — the exact bounded-mode
// delivery policy, just driven synchronously instead of by the concurrent Run pool.
func (h *inProcessHarness) Drain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for {
		select {
		case item := <-h.queue.items:
			h.rt.process(ctx, item)
		default:
			return
		}
	}
}

// Advance is a no-op: the synchronous Drain forces zero backoff, so no manual clock
// drives progress (retry timing is proven by a dedicated runtime test).
func (h *inProcessHarness) Advance(time.Duration) {}

// Purge is a no-op returning zero: the bounded mode has no operator purge (the purge case
// is skipped in newInProcessHarness), so this is never exercised by a running case.
func (h *inProcessHarness) Purge(t *testing.T) int { return 0 }

func (h *inProcessHarness) Status(t *testing.T, key string) (deliverychar.Observation, bool) {
	t.Helper()
	st, err := h.svc.Status(context.Background(), key)
	if errors.Is(err, sdk.ErrNotFound) {
		return deliverychar.Observation{}, false
	}
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return deliverychar.Observation{
		State:   st.State,
		Attempt: st.Attempt,
		Pending: st.Pending,
		Failed:  st.Failed,
	}, true
}

// command maps a neutral deliverychar.Submission onto the producer Command shape the
// delivery.Service seals and submits. An opaque submission carries only the resolution
// input (the processor resolves + renders off the request path); a rendered submission
// carries its payload directly.
func command(sub deliverychar.Submission) Command {
	sub = sub.Normalized()
	env := Envelope{}
	if sub.Opaque {
		env.ResolutionInput = sub.ResolutionInput
	} else {
		env.Destination = sub.Rendered.Destination
		env.Body = sub.Rendered.Body
		env.Secret = sub.Rendered.Secret
	}
	return Command{Kind: sub.Kind, Purpose: sub.Purpose, IdempotencyKey: sub.Key, Envelope: env}
}

// charInitializer adapts the neutral deliverychar.Initializer onto the bespoke
// delivery.Initializer the JobsProcessor drives: Initialize stands for the
// off-request-path resolve + issue + render, returning a fully rendered Envelope;
// Discard stands for the best-effort challenge void.
type charInitializer struct{ n *deliverychar.Initializer }

func (c charInitializer) Initialize(_ context.Context, _, _ string, _ Envelope) (Envelope, bool, error) {
	r, deliver, err := c.n.Resolve()
	if err != nil || !deliver {
		return Envelope{}, deliver, err
	}
	return Envelope{Destination: r.Destination, Body: r.Body, Secret: r.Secret}, true, nil
}

func (c charInitializer) Discard(_ context.Context, _, _ string, _ Envelope) error {
	c.n.Discarded()
	return nil
}
