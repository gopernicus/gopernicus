package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	cmd "github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/command"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// recordEmitter captures emitted events and can be told to fail, to prove the
// EventObserver is best-effort and secret-free.
type recordEmitter struct {
	seen []sdkevents.Event
	err  error
}

func (e *recordEmitter) Emit(_ context.Context, event sdkevents.Event, _ ...sdkevents.EmitOption) error {
	e.seen = append(e.seen, event)
	return e.err
}

func TestEventObserverMapsEveryTransition(t *testing.T) {
	transitions := []cmd.Transition{
		cmd.TransitionAccepted,
		cmd.TransitionInitialized,
		cmd.TransitionSkipped,
		cmd.TransitionDelivered,
		cmd.TransitionRetried,
		cmd.TransitionDeadLettered,
		cmd.TransitionSuperseded,
	}
	for _, tr := range transitions {
		em := &recordEmitter{}
		obs := NewEventObserver(em, nil)
		ev := cmd.LifecycleEvent{
			ExecutionID: "exec-42",
			Kind:        "email",
			Purpose:     "verify",
			Transition:  tr,
			Attempt:     3,
		}
		if err := obs.Observe(context.Background(), ev); err != nil {
			t.Fatalf("Observe(%s): %v", tr, err)
		}
		if len(em.seen) != 1 {
			t.Fatalf("%s: emitted %d events, want 1", tr, len(em.seen))
		}
		got, ok := em.seen[0].(DeliveryLifecycle)
		if !ok {
			t.Fatalf("%s: emitted %T, want DeliveryLifecycle", tr, em.seen[0])
		}
		if want := eventTypePrefix + tr.String(); got.Type() != want {
			t.Errorf("%s: Type() = %q, want %q", tr, got.Type(), want)
		}
		if want := "exec-42:" + tr.String(); got.ID != want {
			t.Errorf("%s: ID = %q, want %q", tr, got.ID, want)
		}
		if got.Transition != tr.String() {
			t.Errorf("%s: Transition = %q", tr, got.Transition)
		}
		if got.ExecutionID != "exec-42" || got.Kind != "email" || got.Purpose != "verify" || got.Attempt != 3 {
			t.Errorf("%s: bounded fields not carried: %+v", tr, got)
		}
		// Per-execution events carry the opaque execution ID as the aggregate ID for
		// SSE routing; the aggregate ID is never a recipient or the logical key.
		if got.AggregateID() == nil || *got.AggregateID() != "exec-42" {
			t.Errorf("%s: aggregate ID = %v, want exec-42", tr, got.AggregateID())
		}
	}
}

func TestEventObserverPurgeIsBatch(t *testing.T) {
	em := &recordEmitter{}
	obs := NewEventObserver(em, nil)
	if err := obs.Observe(context.Background(), cmd.LifecycleEvent{
		Transition: cmd.TransitionPurged,
		Count:      7,
	}); err != nil {
		t.Fatalf("Observe(purged): %v", err)
	}
	got := em.seen[0].(DeliveryLifecycle)
	// A batch purge has no execution ID: the dedup ID is the transition token alone and
	// no aggregate is attached.
	if got.ID != "purged" {
		t.Errorf("purge ID = %q, want %q", got.ID, "purged")
	}
	if got.Count != 7 {
		t.Errorf("purge Count = %d, want 7", got.Count)
	}
	if got.AggregateID() != nil {
		t.Errorf("purge carried an aggregate ID: %v", got.AggregateID())
	}
}

func TestEventObserverIDIsStableNotRandom(t *testing.T) {
	// The same transition on the same execution must always produce the same dedup ID,
	// so a subscriber can de-duplicate a redelivered event.
	em := &recordEmitter{}
	obs := NewEventObserver(em, nil)
	ev := cmd.LifecycleEvent{ExecutionID: "exec-9", Transition: cmd.TransitionDelivered}
	_ = obs.Observe(context.Background(), ev)
	_ = obs.Observe(context.Background(), ev)
	a := em.seen[0].(DeliveryLifecycle).ID
	b := em.seen[1].(DeliveryLifecycle).ID
	if a != b {
		t.Fatalf("dedup ID not stable: %q != %q", a, b)
	}
	if a != "exec-9:delivered" {
		t.Fatalf("dedup ID = %q, want exec-9:delivered", a)
	}
}

func TestEventObserverPayloadIsSecretFree(t *testing.T) {
	// The LifecycleEvent seam structurally cannot carry a destination, secret, or the
	// raw logical key; this pins that the encoded event exposes only bounded fields.
	em := &recordEmitter{}
	obs := NewEventObserver(em, nil)
	_ = obs.Observe(context.Background(), cmd.LifecycleEvent{
		ExecutionID: "exec-1",
		Kind:        "email",
		Purpose:     "verify",
		Transition:  cmd.TransitionDelivered,
		Attempt:     1,
	})
	raw, err := json.Marshal(em.seen[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	blob := string(raw)
	for _, canary := range []string{"destination", "secret", "resolution_input", "@", "body", "html"} {
		if strings.Contains(blob, canary) {
			t.Errorf("emitted payload leaked %q: %s", canary, blob)
		}
	}
}

func TestEventObserverSwallowsEmitError(t *testing.T) {
	// A failing emitter must not propagate: Observe logs and returns nil so the event
	// rail can never fail accepted delivery work.
	em := &recordEmitter{err: errors.New("bus down")}
	obs := NewEventObserver(em, nil)
	if err := obs.Observe(context.Background(), cmd.LifecycleEvent{Transition: cmd.TransitionDelivered}); err != nil {
		t.Fatalf("Observe returned %v, want nil (best-effort)", err)
	}
}

func TestEventObserverNilEmitterDefaultsNoop(t *testing.T) {
	// A nil emitter must default to Noop so Observe stays unconditional.
	obs := NewEventObserver(nil, nil)
	if err := obs.Observe(context.Background(), cmd.LifecycleEvent{Transition: cmd.TransitionAccepted}); err != nil {
		t.Fatalf("Observe with nil emitter: %v", err)
	}
}
