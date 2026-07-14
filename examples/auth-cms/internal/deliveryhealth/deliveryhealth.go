// Package deliveryhealth is the host-owned, secret-free operational health surface for
// authentication's outbound delivery (authv3-delivery-refactor AV3D-5.3). It is HOST
// code: the recommended architecture is host-COMPOSED health over the two features'
// existing narrow seams, not a new feature route. It observes three points, all bounded
// and secret-free:
//
//   - Runtime lifecycle — the host owns the delivery goroutine, so MarkStarted/MarkStopped
//     record whether the selected runtime (the durable jobs FencedRuntime or the bounded
//     in_process pool) is running. This distinguishes "not started" from "running".
//   - Delivery lifecycle events — Emitter wraps Config.DeliveryEventsEmitter and classifies
//     each bounded, secret-free authentication.delivery.<transition> topic into a monotonic
//     counter (delivered/skipped/retried/dead_lettered/purged/superseded), then forwards to
//     the real bus. A forward failure increments observer_failures, so an erroring emitter
//     is visible. This gives provider retry + dead-letter activity and observer failure.
//   - Request accounting / backlog — jobs mode: Dispatcher wraps Config.DeliveryDispatcher to
//     count ACCEPTED admission requests (a rejected admission is not counted), and
//     outstanding = admitted − (delivered+skipped+dead_lettered+superseded) (clamped at zero).
//     outstanding is DERIVED request accounting, NOT an authoritative backlog (a coalesced
//     duplicate or a replace is one accepted request; lifecycle events are best-effort). The
//     jobs fenced queue exposes no cheap authoritative nonterminal count, so jobs mode does not
//     report an authoritative queue depth. in_process mode: SetDepthSource wires the auth
//     Service's InProcessQueueDepth read, so queued/capacity/saturated report the bounded
//     pool's AUTHORITATIVE live backlog.
//
// Nothing here carries a recipient, destination, payload, secret, or logical key: the
// output is counters, gauges, and enums only. The wrapped event carries only its bounded
// type string into the classifier — its ExecutionID/Kind/Purpose fields are never read or
// stored.
package deliveryhealth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// transitionPrefix namespaces every delivery lifecycle event type the observer emits
// (mirrors the feature's EventObserver). The classifier reads only the bounded suffix.
const transitionPrefix = "authentication.delivery."

// runtime-state enums for the health projection. Bounded, secret-free.
const (
	runtimeNotStarted = "not_started"
	runtimeRunning    = "running"
)

// depthSource is the bounded, secret-free in_process queue-depth read the host wires from
// the auth Service (InProcessQueueDepth). It returns (queued, capacity, ok).
type depthSource func() (queued, capacity int, ok bool)

// Health aggregates the host-composed delivery health counters. Every field is a bounded
// counter/gauge/enum; the type carries no recipient, payload, or logical key. All mutators
// are safe for concurrent use.
type Health struct {
	mode string

	started atomic.Bool

	admitted     atomic.Int64
	delivered    atomic.Int64
	skipped      atomic.Int64
	retried      atomic.Int64
	deadLettered atomic.Int64
	superseded   atomic.Int64
	purged       atomic.Int64
	observerFail atomic.Int64

	depth atomic.Pointer[depthSource]
}

// New builds a Health for the given delivery mode string (e.g. "jobs" or "in_process").
func New(mode string) *Health {
	return &Health{mode: mode}
}

// MarkStarted records that the host has started the delivery runtime.
func (h *Health) MarkStarted() { h.started.Store(true) }

// MarkStopped records that the delivery runtime has returned (shutdown).
func (h *Health) MarkStopped() { h.started.Store(false) }

// SetDepthSource wires the in_process queue-depth read (the auth Service's
// InProcessQueueDepth). Call it once, before serving. A nil source leaves backlog reported
// via the admission/terminal gauge only.
func (h *Health) SetDepthSource(fn depthSource) {
	if fn == nil {
		h.depth.Store(nil)
		return
	}
	h.depth.Store(&fn)
}

// Dispatcher wraps a delivery dispatcher (jobs mode) so every admitted command is counted.
// The returned value is a drop-in auth.DeliveryDispatcher; it forwards unchanged and only
// counts. A nil next returns nil (the caller's nil-dispatcher semantics are preserved).
func (h *Health) Dispatcher(next auth.DeliveryDispatcher) auth.DeliveryDispatcher {
	if next == nil {
		return nil
	}
	return countingDispatcher{next: next, h: h}
}

// Emitter wraps a delivery lifecycle emitter so each bounded transition is counted and a
// forward failure is recorded as observer_failures. It forwards to next unchanged and
// returns next's error verbatim, so the feature's best-effort observer semantics are
// preserved. A nil next defaults to a no-op emitter that only counts transitions.
func (h *Health) Emitter(next sdkevents.Emitter) sdkevents.Emitter {
	if next == nil {
		next = sdkevents.Noop{}
	}
	return countingEmitter{next: next, h: h}
}

// countingDispatcher counts admissions and forwards to the wrapped dispatcher.
type countingDispatcher struct {
	next auth.DeliveryDispatcher
	h    *Health
}

var _ auth.DeliveryDispatcher = countingDispatcher{}

func (d countingDispatcher) Submit(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (string, error) {
	id, err := d.next.Submit(ctx, kind, purpose, logicalKey, payload)
	if err == nil {
		// Count only an ACCEPTED admission request (IX-12): a rejected Submit (capacity/closed)
		// never entered the queue, so counting it would overstate accepted work.
		d.h.admitted.Add(1)
	}
	return id, err
}

func (d countingDispatcher) Replace(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (string, error) {
	id, err := d.next.Replace(ctx, kind, purpose, logicalKey, payload)
	if err == nil {
		d.h.admitted.Add(1)
	}
	return id, err
}

func (d countingDispatcher) LatestStatus(ctx context.Context, logicalKey string) (string, error) {
	return d.next.LatestStatus(ctx, logicalKey)
}

// countingEmitter classifies each delivery transition and forwards to the wrapped emitter.
type countingEmitter struct {
	next sdkevents.Emitter
	h    *Health
}

var _ sdkevents.Emitter = countingEmitter{}

// Emit classifies the event's bounded type suffix into a counter, then forwards to the
// wrapped emitter. A forward failure increments observer_failures and is returned verbatim
// so the feature's observer logs it exactly as before. Only ev.Type() is read; no other
// field of the event is inspected or stored.
func (e countingEmitter) Emit(ctx context.Context, ev sdkevents.Event, opts ...sdkevents.EmitOption) error {
	e.h.classify(ev.Type())
	if err := e.next.Emit(ctx, ev, opts...); err != nil {
		e.h.observerFail.Add(1)
		return err
	}
	return nil
}

// classify increments the counter for a bounded delivery transition topic. An unknown or
// non-delivery topic is ignored.
func (h *Health) classify(eventType string) {
	transition, ok := strings.CutPrefix(eventType, transitionPrefix)
	if !ok {
		return
	}
	switch transition {
	case "delivered":
		h.delivered.Add(1)
	case "skipped":
		h.skipped.Add(1)
	case "retried":
		h.retried.Add(1)
	case "dead_lettered":
		h.deadLettered.Add(1)
	case "superseded":
		h.superseded.Add(1)
	case "purged":
		h.purged.Add(1)
	}
}

// Snapshot is the bounded, secret-free health projection. Every field is a counter, gauge,
// or enum — no recipient, payload, or logical key ever appears.
type Snapshot struct {
	Mode    string `json:"mode"`
	Runtime string `json:"runtime"`
	// Admitted is the count of ACCEPTED admission requests (jobs mode) — successful
	// Submit/Replace calls only; a rejected admission is never counted. It is request
	// accounting, not a live queue depth.
	Admitted int64 `json:"admitted"`
	// Outstanding is accepted admission requests not yet observed terminal via a lifecycle
	// event (admitted − delivered − skipped − dead_lettered − superseded, clamped at zero).
	// It is a DERIVED request-accounting figure, NOT an authoritative backlog: a coalesced
	// duplicate or a replace is one accepted request, and lifecycle events are best-effort.
	// The authoritative in_process backlog is queued/capacity/saturated below.
	Outstanding int64 `json:"outstanding"`
	// Queued/Capacity/Saturated are the AUTHORITATIVE in_process backlog (a live read of the
	// bounded pool's queue depth). They are zero/false in jobs mode (no cheap authoritative
	// nonterminal count is exposed by the fenced queue).
	Queued           int   `json:"queued"`
	Capacity         int   `json:"capacity"`
	Saturated        bool  `json:"saturated"`
	Delivered        int64 `json:"delivered"`
	Skipped          int64 `json:"skipped"`
	Retried          int64 `json:"retried"`
	DeadLettered     int64 `json:"dead_lettered"`
	Superseded       int64 `json:"superseded"`
	Purged           int64 `json:"purged"`
	ObserverFailures int64 `json:"observer_failures"`
}

// Snapshot reads the current bounded counters into a Snapshot. It is safe for concurrent
// use.
func (h *Health) Snapshot() Snapshot {
	admitted := h.admitted.Load()
	delivered := h.delivered.Load()
	skipped := h.skipped.Load()
	deadLettered := h.deadLettered.Load()
	superseded := h.superseded.Load()

	// outstanding is accepted admission requests not yet observed terminal (IX-12): request
	// accounting, NOT an authoritative backlog. Subtract every terminal transition (delivered,
	// skipped, dead_lettered, superseded). Clamp at zero: the admission counter and the
	// best-effort terminal events race, and a terminal may be counted before its admission on
	// a fast path, which must never surface a negative.
	outstanding := admitted - (delivered + skipped + deadLettered + superseded)
	if outstanding < 0 {
		outstanding = 0
	}

	runtime := runtimeNotStarted
	if h.started.Load() {
		runtime = runtimeRunning
	}

	s := Snapshot{
		Mode:             h.mode,
		Runtime:          runtime,
		Admitted:         admitted,
		Outstanding:      outstanding,
		Delivered:        delivered,
		Skipped:          skipped,
		Retried:          h.retried.Load(),
		DeadLettered:     deadLettered,
		Superseded:       superseded,
		Purged:           h.purged.Load(),
		ObserverFailures: h.observerFail.Load(),
	}

	if p := h.depth.Load(); p != nil {
		if queued, capacity, ok := (*p)(); ok {
			s.Queued = queued
			s.Capacity = capacity
			s.Saturated = capacity > 0 && queued >= capacity
		}
	}
	return s
}

// Handler serves the current Snapshot as JSON. It is unauthenticated by design (an
// operator probe cannot log in) and exposes nothing sensitive — bounded counters only.
func (h *Health) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(h.Snapshot())
	}
}
