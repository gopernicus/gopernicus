package delivery

import (
	"context"
	"log/slog"

	cmd "github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/command"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// Delivery lifecycle event vocabulary. One type per bounded transition, namespaced
// under the feature so a subscriber filters by exact topic (e.g. subscribe only to
// authentication.delivery.dead_lettered). The transition token is the bounded
// cmd.Transition string, so the type set is fixed and secret-free.
const (
	// eventTypePrefix namespaces every delivery lifecycle event type.
	eventTypePrefix = "authentication.delivery."
	// aggregateTypeDelivery labels the aggregate every per-execution event carries; its
	// aggregate ID is the opaque execution ID (never a recipient or the logical key).
	aggregateTypeDelivery = "authentication.delivery"
)

// DeliveryLifecycle is the generic event an EventObserver emits for one delivery
// transition. It embeds BaseEvent for the events port and adds only bounded,
// secret-free fields:
//
//   - ID is the stable de-duplication key, derived from ExecutionID and Transition
//     (never random), so a subscriber that sees the same transition twice — the bus
//     may redeliver, and an at-least-once delivery may re-emit — de-dupes on it.
//   - ExecutionID is the opaque unit-of-work ID (never a recipient or logical key).
//   - Kind, Purpose, and Transition are the bounded enums.
//   - Attempt is the delivery attempt count; Count is the purged-batch size.
//
// No destination, secret, resolution input, or raw logical key is ever carried, so
// the event is safe on any bus, log, or dashboard.
type DeliveryLifecycle struct {
	sdkevents.BaseEvent
	ID          string `json:"id"`
	ExecutionID string `json:"execution_id,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
	Transition  string `json:"transition"`
	Attempt     int    `json:"attempt,omitempty"`
	Count       int    `json:"count,omitempty"`
}

// EventObserver is the optional host adapter that maps a cmd.LifecycleEvent onto
// a generic DeliveryLifecycle event and emits it on the Mount.Events rail
// (sdkevents.Emitter). It is purely observational: emission is best-effort and an
// emit error is logged, never returned, so the events rail can never lose, retry,
// duplicate, or fail accepted delivery work. It satisfies cmd.Observer.
type EventObserver struct {
	emitter sdkevents.Emitter
	logger  *slog.Logger
}

var _ cmd.Observer = (*EventObserver)(nil)

// NewEventObserver builds an EventObserver over the given emitter. A nil emitter
// defaults to sdkevents.Noop so Observe stays unconditional; a nil logger defaults to
// slog.Default. The returned *EventObserver is the cmd.Observer a transport
// receives.
func NewEventObserver(emitter sdkevents.Emitter, logger *slog.Logger) *EventObserver {
	if emitter == nil {
		emitter = sdkevents.Noop{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &EventObserver{emitter: emitter, logger: logger}
}

// Observe emits one DeliveryLifecycle event best-effort. An emit failure is logged
// (by stable transition, never a payload field) and swallowed: the transport has
// already recorded the delivery transition, so a dropped or failed event changes
// nothing. It always returns nil — command.SafeObserve additionally contains any
// panic — so no observation path can surface into delivery state.
func (o *EventObserver) Observe(ctx context.Context, ev cmd.LifecycleEvent) error {
	evt := newDeliveryLifecycle(ev)
	if err := o.emitter.Emit(ctx, evt); err != nil {
		o.logger.WarnContext(ctx, "delivery: lifecycle event emit failed",
			"transition", evt.Transition, "error", err)
	}
	return nil
}

// newDeliveryLifecycle builds the generic event from a lifecycle transition. The
// de-duplication ID is ExecutionID+":"+transition for a per-execution transition, so
// the same transition on the same execution always produces the same ID; a batch
// purge (no execution ID) uses the transition token alone. Per-execution events carry
// the opaque execution ID as the aggregate ID for SSE routing.
func newDeliveryLifecycle(ev cmd.LifecycleEvent) DeliveryLifecycle {
	transition := ev.Transition.String()
	id := transition
	if ev.ExecutionID != "" {
		id = ev.ExecutionID + ":" + transition
	}
	base := sdkevents.NewBaseEvent(eventTypePrefix + transition)
	if ev.ExecutionID != "" {
		base = base.WithAggregate(aggregateTypeDelivery, ev.ExecutionID)
	}
	return DeliveryLifecycle{
		BaseEvent:   base,
		ID:          id,
		ExecutionID: ev.ExecutionID,
		Kind:        ev.Kind,
		Purpose:     ev.Purpose,
		Transition:  transition,
		Attempt:     ev.Attempt,
		Count:       ev.Count,
	}
}
