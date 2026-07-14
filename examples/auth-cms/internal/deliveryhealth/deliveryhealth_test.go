package deliveryhealth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// failingEmitter always errors, standing in for a broken bus so the observer-failure path
// is exercised.
type failingEmitter struct{}

func (failingEmitter) Emit(context.Context, sdkevents.Event, ...sdkevents.EmitOption) error {
	return errors.New("bus down")
}

// deliveryEvent builds a delivery lifecycle event carrying the given transition suffix.
func deliveryEvent(transition string) sdkevents.Event {
	return sdkevents.NewBaseEvent(transitionPrefix + transition)
}

func TestClassifyCountsBoundedTransitions(t *testing.T) {
	h := New("jobs")
	em := h.Emitter(sdkevents.Noop{})
	for _, tr := range []string{"delivered", "delivered", "skipped", "retried", "retried", "retried", "dead_lettered", "superseded", "purged"} {
		if err := em.Emit(context.Background(), deliveryEvent(tr)); err != nil {
			t.Fatalf("emit %s: %v", tr, err)
		}
	}
	// A non-delivery topic must be ignored (never counted, never a leak).
	if err := em.Emit(context.Background(), sdkevents.NewBaseEvent("content.updated")); err != nil {
		t.Fatalf("emit content.updated: %v", err)
	}

	s := h.Snapshot()
	if s.Delivered != 2 || s.Skipped != 1 || s.Retried != 3 || s.DeadLettered != 1 || s.Superseded != 1 || s.Purged != 1 {
		t.Fatalf("unexpected counters: %+v", s)
	}
	if s.ObserverFailures != 0 {
		t.Fatalf("observer_failures = %d, want 0 (forward succeeded)", s.ObserverFailures)
	}
}

func TestObserverFailureCountedOnEmitError(t *testing.T) {
	h := New("jobs")
	em := h.Emitter(failingEmitter{})
	// The wrapper must forward the error verbatim (best-effort observer semantics preserved)
	// AND count it.
	if err := em.Emit(context.Background(), deliveryEvent("dead_lettered")); err == nil {
		t.Fatal("expected the wrapped emit error to be returned verbatim")
	}
	s := h.Snapshot()
	if s.ObserverFailures != 1 {
		t.Fatalf("observer_failures = %d, want 1", s.ObserverFailures)
	}
	// The transition is still classified even though the forward failed.
	if s.DeadLettered != 1 {
		t.Fatalf("dead_lettered = %d, want 1", s.DeadLettered)
	}
}

func TestRuntimeLifecycleEnum(t *testing.T) {
	h := New("in_process")
	if got := h.Snapshot().Runtime; got != runtimeNotStarted {
		t.Fatalf("runtime = %q, want %q", got, runtimeNotStarted)
	}
	h.MarkStarted()
	if got := h.Snapshot().Runtime; got != runtimeRunning {
		t.Fatalf("runtime = %q, want %q", got, runtimeRunning)
	}
	h.MarkStopped()
	if got := h.Snapshot().Runtime; got != runtimeNotStarted {
		t.Fatalf("runtime after stop = %q, want %q", got, runtimeNotStarted)
	}
}

func TestDepthSourceReportsSaturation(t *testing.T) {
	h := New("in_process")
	// Not wired: no queued/capacity, not saturated.
	if s := h.Snapshot(); s.Queued != 0 || s.Capacity != 0 || s.Saturated {
		t.Fatalf("unwired depth: %+v", s)
	}
	h.SetDepthSource(func() (int, int, bool) { return 1, 1, true })
	if s := h.Snapshot(); s.Queued != 1 || s.Capacity != 1 || !s.Saturated {
		t.Fatalf("saturated depth: %+v", s)
	}
	h.SetDepthSource(func() (int, int, bool) { return 0, 4, true })
	if s := h.Snapshot(); s.Saturated {
		t.Fatalf("empty queue should not be saturated: %+v", s)
	}
}

func TestOutstandingGaugeClampsAtZero(t *testing.T) {
	h := New("jobs")
	// Terminal events with no admissions must never produce a negative outstanding.
	em := h.Emitter(sdkevents.Noop{})
	_ = em.Emit(context.Background(), deliveryEvent("delivered"))
	if s := h.Snapshot(); s.Outstanding != 0 {
		t.Fatalf("outstanding = %d, want 0 (clamped)", s.Outstanding)
	}
}

// erroringDispatcher stands in for a delivery dispatcher whose admission fails (queue full /
// closed), so the health accounting can prove a REJECTED admission is not counted (IX-12).
type erroringDispatcher struct {
	fail bool
}

func (d *erroringDispatcher) Submit(context.Context, string, string, string, []byte) (string, error) {
	if d.fail {
		return "", errors.New("queue at capacity")
	}
	return "exec-1", nil
}

func (d *erroringDispatcher) Replace(context.Context, string, string, string, []byte) (string, error) {
	if d.fail {
		return "", errors.New("queue closed")
	}
	return "exec-2", nil
}

func (d *erroringDispatcher) LatestStatus(context.Context, string) (string, error) {
	return "", nil
}

// TestAdmittedCountsOnlyAcceptedRequests proves the IX-12 accounting fix: a REJECTED admission
// (Submit/Replace returning an error) is not counted, while each accepted admission request
// counts exactly once. It removes the pre-increment that overstated accepted work by counting
// failed admissions.
func TestAdmittedCountsOnlyAcceptedRequests(t *testing.T) {
	h := New("jobs")

	// Rejected admissions: admitted must stay 0.
	failing := h.Dispatcher(&erroringDispatcher{fail: true})
	if _, err := failing.Submit(context.Background(), "k", "p", "lk", nil); err == nil {
		t.Fatal("expected the failing Submit to error")
	}
	if _, err := failing.Replace(context.Background(), "k", "p", "lk", nil); err == nil {
		t.Fatal("expected the failing Replace to error")
	}
	if s := h.Snapshot(); s.Admitted != 0 {
		t.Fatalf("admitted = %d after two REJECTED admissions, want 0", s.Admitted)
	}

	// Accepted admissions: each successful request counts exactly once.
	ok := h.Dispatcher(&erroringDispatcher{fail: false})
	if _, err := ok.Submit(context.Background(), "k", "p", "lk", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := ok.Replace(context.Background(), "k", "p", "lk", nil); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if s := h.Snapshot(); s.Admitted != 2 {
		t.Fatalf("admitted = %d after two ACCEPTED requests, want 2 (each accepted request counted once)", s.Admitted)
	}
}

// TestOutstandingSubtractsSupersededTerminal proves a superseded (replaced) generation is
// treated as terminal in the outstanding request-accounting figure, so a replace does not
// inflate outstanding indefinitely.
func TestOutstandingSubtractsSupersededTerminal(t *testing.T) {
	h := New("jobs")
	ok := h.Dispatcher(&erroringDispatcher{fail: false})
	// Two accepted requests (a submit then a replace of the same key).
	_, _ = ok.Submit(context.Background(), "k", "p", "lk", nil)
	_, _ = ok.Replace(context.Background(), "k", "p", "lk", nil)
	// The replaced generation emits a superseded terminal event, and the replacement delivers.
	em := h.Emitter(sdkevents.Noop{})
	_ = em.Emit(context.Background(), deliveryEvent("superseded"))
	_ = em.Emit(context.Background(), deliveryEvent("delivered"))
	if s := h.Snapshot(); s.Outstanding != 0 {
		t.Fatalf("outstanding = %d, want 0 (admitted 2 − superseded 1 − delivered 1)", s.Outstanding)
	}
}

func TestHandlerServesBoundedJSON(t *testing.T) {
	h := New("jobs")
	h.MarkStarted()
	em := h.Emitter(sdkevents.Noop{})
	_ = em.Emit(context.Background(), deliveryEvent("dead_lettered"))

	rec := httptest.NewRecorder()
	h.Handler()(rec, httptest.NewRequest(http.MethodGet, "/healthz/delivery", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Mode != "jobs" || got.Runtime != runtimeRunning || got.DeadLettered != 1 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing Cache-Control: no-store")
	}
}
