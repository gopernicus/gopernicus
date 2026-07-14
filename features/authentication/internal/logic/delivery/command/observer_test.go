package command

import (
	"context"
	"errors"
	"testing"
)

func TestTransitionString(t *testing.T) {
	cases := map[Transition]string{
		TransitionAccepted:     "accepted",
		TransitionInitialized:  "initialized",
		TransitionSkipped:      "skipped",
		TransitionDelivered:    "delivered",
		TransitionRetried:      "retried",
		TransitionDeadLettered: "dead_lettered",
		TransitionSuperseded:   "superseded",
		TransitionPurged:       "purged",
		Transition(999):        "unknown",
	}
	for tr, want := range cases {
		if got := tr.String(); got != want {
			t.Errorf("Transition(%d).String() = %q, want %q", int(tr), got, want)
		}
	}
}

// recordObserver captures the events it is handed and can be told to error or panic,
// to prove SafeObserve contains both.
type recordObserver struct {
	seen  []LifecycleEvent
	err   error
	panic bool
}

func (o *recordObserver) Observe(_ context.Context, ev LifecycleEvent) error {
	o.seen = append(o.seen, ev)
	if o.panic {
		panic("observer boom")
	}
	return o.err
}

func TestSafeObserveNilIsNoop(t *testing.T) {
	// A nil observer must be a zero-cost no-op: the call simply returns.
	SafeObserve(context.Background(), nil, LifecycleEvent{Transition: TransitionDelivered})
}

func TestSafeObserveForwardsToObserver(t *testing.T) {
	obs := &recordObserver{}
	ev := LifecycleEvent{ExecutionID: "x1", Kind: "email", Purpose: "verify", Transition: TransitionDelivered, Attempt: 2}
	SafeObserve(context.Background(), obs, ev)
	if len(obs.seen) != 1 {
		t.Fatalf("observer saw %d events, want 1", len(obs.seen))
	}
	if obs.seen[0] != ev {
		t.Fatalf("observer saw %+v, want %+v", obs.seen[0], ev)
	}
}

func TestSafeObserveContainsError(t *testing.T) {
	// A returned error must be swallowed: SafeObserve returns nothing and does not
	// propagate the failure.
	obs := &recordObserver{err: errors.New("emit failed")}
	SafeObserve(context.Background(), obs, LifecycleEvent{Transition: TransitionRetried})
	if len(obs.seen) != 1 {
		t.Fatalf("observer was not invoked")
	}
}

func TestSafeObserveContainsPanic(t *testing.T) {
	// A panicking observer must not escape SafeObserve. If the recover were missing,
	// this test would panic and fail.
	obs := &recordObserver{panic: true}
	SafeObserve(context.Background(), obs, LifecycleEvent{Transition: TransitionDeadLettered})
	if len(obs.seen) != 1 {
		t.Fatalf("observer was not invoked before panicking")
	}
}
