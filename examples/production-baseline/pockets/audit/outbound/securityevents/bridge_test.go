package securityevents

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/sdk"
)

// memoryRecorder keeps every Input it is handed.
type memoryRecorder struct {
	mu   sync.Mutex
	rows []securityevent.Input
}

// blockingRecorder signals entered and holds each write until release closes.
type blockingRecorder struct {
	memoryRecorder
	entered chan struct{}
	release chan struct{}
}

func (r *memoryRecorder) Record(_ context.Context, in securityevent.Input) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, in)
}

func (r *memoryRecorder) recorded() []securityevent.Input {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]securityevent.Input(nil), r.rows...)
}

func (r *blockingRecorder) Record(ctx context.Context, in securityevent.Input) {
	r.entered <- struct{}{}
	<-r.release
	r.memoryRecorder.Record(ctx, in)
}

func denial(resourceID string) decisions.DecisionEvent {
	return decisions.DecisionEvent{
		Operation:    "Check",
		Principal:    sdk.Principal{Type: "user", ID: "u1"},
		Permission:   "manage",
		ResourceType: "project",
		ResourceID:   resourceID,
		Outcome:      decisions.OutcomeDenied,
	}
}

func TestDenialRecordsAuthorizationDenied(t *testing.T) {
	rec := &memoryRecorder{}
	New(rec)(context.Background(), denial("p1"))

	want := []securityevent.Input{{
		UserID: "u1",
		Type:   decisions.EventAuthorizationDenied,
		Status: securityevent.StatusFailure,
		Details: map[string]any{
			"operation":     "Check",
			"permission":    "manage",
			"resource_type": "project",
			"resource_id":   "p1",
		},
	}}
	if got := rec.recorded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
}

func TestRefusalRecordsRoleWriteRefused(t *testing.T) {
	rec := &memoryRecorder{}
	New(rec)(context.Background(), decisions.DecisionEvent{
		Operation:    "RoleWrite",
		Principal:    sdk.Principal{Type: "user", ID: "actor-1"},
		Subject:      sdk.Principal{Type: "user", ID: "subject-1"},
		Permission:   "owner",
		ResourceType: "project",
		ResourceID:   "p1",
		Outcome:      decisions.OutcomeRefused,
	})

	want := []securityevent.Input{{
		Actor:  securityevent.Principal{Type: "user", ID: "actor-1"},
		UserID: "subject-1",
		Type:   decisions.EventRoleWriteRefused,
		Status: securityevent.StatusBlocked,
		Details: map[string]any{
			"role":       "owner",
			"scope_type": "project",
			"scope_id":   "p1",
		},
	}}
	if got := rec.recorded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
}

func TestOtherOutcomesRecordNothing(t *testing.T) {
	rec := &memoryRecorder{}
	observe := New(rec)
	for _, outcome := range []decisions.DecisionOutcome{decisions.OutcomeAllowed, decisions.OutcomeComplete, decisions.OutcomeError} {
		ev := denial("p1")
		ev.Outcome = outcome
		observe(context.Background(), ev)
	}
	if got := rec.recorded(); len(got) != 0 {
		t.Fatalf("rows = %#v, want none", got)
	}
}

// TestFullInFlightDropsAndCounts holds the one in-flight slot, proves the next
// event is dropped and counted, and that the drop left no dedupe mark behind.
func TestFullInFlightDropsAndCounts(t *testing.T) {
	ctx := context.Background()
	rec := &blockingRecorder{entered: make(chan struct{}), release: make(chan struct{})}
	bridge := NewBridge(rec, WithMaxInFlight(1))
	observe := bridge.Observer()

	done := make(chan struct{})
	go func() {
		observe(ctx, denial("p1"))
		close(done)
	}()
	<-rec.entered

	observe(ctx, denial("p2"))
	if got := bridge.Dropped(); got != 1 {
		t.Fatalf("Dropped = %d, want 1", got)
	}
	close(rec.release)
	<-done

	go func() { <-rec.entered }()
	observe(ctx, denial("p2"))
	if got := len(rec.recorded()); got != 2 {
		t.Fatalf("rows = %d, want 2 (the held write and the retried drop)", got)
	}
	if got := bridge.Dropped(); got != 1 {
		t.Fatalf("Dropped = %d, want still 1", got)
	}
}

func TestDedupeWindowCollapsesIdenticalDenials(t *testing.T) {
	ctx := context.Background()
	rec := &memoryRecorder{}
	bridge := NewBridge(rec)
	observe := bridge.Observer()

	for range 30 {
		observe(ctx, denial("p1"))
	}
	if got := len(rec.recorded()); got != 1 {
		t.Fatalf("rows after thirty identical denials = %d, want 1", got)
	}

	observe(ctx, denial("p2"))
	rows := rec.recorded()
	if len(rows) != 2 || rows[1].Details["resource_id"] != "p2" {
		t.Fatalf("rows = %#v, want a second row for p2", rows)
	}
	if got := bridge.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}
