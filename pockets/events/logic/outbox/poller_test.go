package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// =============================================================================
// Test doubles
// =============================================================================

// fakeRepo is a hermetic in-memory EntryRepository for the poller tests. It
// preserves the ListUnpublished ascending-CreatedAt contract and records
// MarkPublished calls so the tests can assert the emit discipline.
type fakeRepo struct {
	mu         sync.Mutex
	entries    []*Entry
	markCalls  map[string]int
	markErr    error
	listErr    error
	listCalled int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{markCalls: map[string]int{}}
}

// seed appends an entry with the given record and CreatedAt, unpublished.
func (r *fakeRepo) seed(rec sdkevents.Record, createdAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, &Entry{Record: rec, CreatedAt: createdAt})
}

func (r *fakeRepo) Append(ctx context.Context, recs ...sdkevents.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range recs {
		r.entries = append(r.entries, &Entry{Record: rec, CreatedAt: time.Now().UTC()})
	}
	return nil
}

func (r *fakeRepo) ListUnpublished(ctx context.Context, limit int) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listCalled++
	if r.listErr != nil {
		return nil, r.listErr
	}

	// Collect unpublished, then sort by CreatedAt ascending.
	var pending []*Entry
	for _, e := range r.entries {
		if e.PublishedAt == nil {
			pending = append(pending, e)
		}
	}
	for i := 1; i < len(pending); i++ {
		for j := i; j > 0 && pending[j].CreatedAt.Before(pending[j-1].CreatedAt); j-- {
			pending[j], pending[j-1] = pending[j-1], pending[j]
		}
	}

	out := make([]Entry, 0, len(pending))
	for _, e := range pending {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, *e)
	}
	return out, nil
}

func (r *fakeRepo) MarkPublished(ctx context.Context, eventID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markCalls[eventID]++
	if r.markErr != nil {
		return r.markErr
	}
	for _, e := range r.entries {
		if e.EventID == eventID && e.PublishedAt == nil {
			now := time.Now().UTC()
			e.PublishedAt = &now
		}
	}
	return nil
}

func (r *fakeRepo) PurgePublished(ctx context.Context, before time.Time) (int, error) {
	return 0, nil
}

func (r *fakeRepo) markCount(eventID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.markCalls[eventID]
}

var _ EntryRepository = (*fakeRepo)(nil)

// stubDelivery records emit calls and can be made to fail. It is not a real bus: it
// delivers to no subscribers, so it isolates the poller's emit/mark ordering.
type stubDelivery struct {
	mu       sync.Mutex
	emitErr  error
	emitted  []string // EventIDs seen (via the EventID() interface)
	emitCall int
}

func (b *stubDelivery) deliver(ctx context.Context, e sdkevents.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emitCall++
	if b.emitErr != nil {
		return b.emitErr
	}
	if ided, ok := e.(interface{ EventID() string }); ok {
		b.emitted = append(b.emitted, ided.EventID())
	}
	return nil
}

// sampleEvent is a typed event whose JSON payload the Unmarshaler slow path
// rehydrates.
type sampleEvent struct {
	sdkevents.BaseEvent
	Name string `json:"name"`
}

// recordFor builds a durable Record from a sampleEvent — the realistic payload
// the poller rehydrates.
func recordFor(t *testing.T, name string) sdkevents.Record {
	t.Helper()
	evt := sampleEvent{
		BaseEvent: sdkevents.NewBaseEvent("content.published").WithAggregate("content", name),
		Name:      name,
	}
	rec, err := sdkevents.NewRecord(evt)
	if err != nil {
		t.Fatalf("NewRecord: %v", err)
	}
	return rec
}

// =============================================================================
// Tests
// =============================================================================

func TestPoll_EmptyBatch_ReturnsErrNoWork(t *testing.T) {
	repo := newFakeRepo()
	bus := &stubDelivery{}
	p := newTestPoller(t, repo, bus.deliver)

	err := p.Poll(context.Background())
	if !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("Poll on empty outbox = %v, want workers.ErrNoWork", err)
	}
	if bus.emitCall != 0 {
		t.Fatalf("empty outbox emitted %d events, want 0", bus.emitCall)
	}
}

func TestPoll_DrainsInCreatedAtOrder_MarksEachOnce(t *testing.T) {
	repo := newFakeRepo()
	bus := sdkevents.NewMemory()
	t.Cleanup(func() { _ = bus.Close(context.Background()) })

	var mu sync.Mutex
	var got []string
	_, err := bus.Subscribe("*", func(_ context.Context, e sdkevents.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if ided, ok := e.(interface{ EventID() string }); ok {
			got = append(got, ided.EventID())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	base := time.Now().UTC()
	recC := recordFor(t, "c")
	recA := recordFor(t, "a")
	recB := recordFor(t, "b")
	// Seed out of CreatedAt order to prove ListUnpublished ascending ordering.
	repo.seed(recC, base.Add(2*time.Second))
	repo.seed(recA, base)
	repo.seed(recB, base.Add(1*time.Second))

	pl := newTestPoller(t, repo, bus.Dispatch)
	if err := pl.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	// Dispatch completes handlers before Poll returns.
	mu.Lock()
	order := append([]string(nil), got...)
	mu.Unlock()

	want := []string{recA.EventID, recB.EventID, recC.EventID}
	if len(order) != len(want) {
		t.Fatalf("delivered %d events, want %d (%v)", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("delivery order[%d] = %s, want %s (full %v)", i, order[i], want[i], order)
		}
	}
	for _, rec := range []sdkevents.Record{recA, recB, recC} {
		if n := repo.markCount(rec.EventID); n != 1 {
			t.Fatalf("MarkPublished(%s) called %d times, want 1", rec.EventID, n)
		}
	}

	// A second poll finds nothing left.
	if err := pl.Poll(context.Background()); !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("second Poll = %v, want ErrNoWork", err)
	}
}

func TestPoll_EmitError_DoesNotMark_RetriedNextPoll(t *testing.T) {
	repo := newFakeRepo()
	bus := &stubDelivery{emitErr: errors.New("bus down")}
	rec := recordFor(t, "x")
	repo.seed(rec, time.Now().UTC())

	p := newTestPoller(t, repo, bus.deliver)

	// First poll: emit fails, entry must NOT be marked published.
	if err := p.Poll(context.Background()); err == nil {
		t.Fatal("Poll with failing bus returned nil, want the emit error")
	}
	if n := repo.markCount(rec.EventID); n != 0 {
		t.Fatalf("MarkPublished called %d times after emit error, want 0 (P1)", n)
	}

	// Recover the bus: the still-unpublished entry is retried and marked.
	bus.mu.Lock()
	bus.emitErr = nil
	bus.mu.Unlock()

	if err := p.Poll(context.Background()); err != nil {
		t.Fatalf("retry Poll: %v", err)
	}
	if n := repo.markCount(rec.EventID); n != 1 {
		t.Fatalf("MarkPublished called %d times after retry, want 1", n)
	}
}

func TestPoll_MarkError_LeavesUnpublished_DuplicateEmitNextPoll(t *testing.T) {
	repo := newFakeRepo()
	repo.markErr = errors.New("mark failed")
	bus := &stubDelivery{}
	rec := recordFor(t, "x")
	repo.seed(rec, time.Now().UTC())

	p := newTestPoller(t, repo, bus.deliver)

	if err := p.Poll(context.Background()); err == nil {
		t.Fatal("Poll with failing MarkPublished returned nil, want the mark error")
	}
	// Emit happened; mark failed → entry stays unpublished.
	if bus.emitCall != 1 {
		t.Fatalf("emit count = %d after first poll, want 1", bus.emitCall)
	}

	// Next poll re-emits the same entry (documented duplicate; consumers de-dupe).
	if err := p.Poll(context.Background()); err == nil {
		t.Fatal("second Poll returned nil, want the mark error again")
	}
	if bus.emitCall != 2 {
		t.Fatalf("emit count = %d after second poll, want 2 (duplicate emit)", bus.emitCall)
	}
}

func TestPoll_EventIDSurfacesOnRehydratedEvent(t *testing.T) {
	repo := newFakeRepo()
	bus := sdkevents.NewMemory()
	t.Cleanup(func() { _ = bus.Close(context.Background()) })

	rec := recordFor(t, "x")
	var gotID string
	_, err := bus.Subscribe("*", func(_ context.Context, e sdkevents.Event) error {
		ided, ok := e.(interface{ EventID() string })
		if !ok {
			t.Error("emitted event does not expose EventID()")
			return nil
		}
		gotID = ided.EventID()
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	repo.seed(rec, time.Now().UTC())

	p := newTestPoller(t, repo, bus.Dispatch)
	if err := p.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if gotID != rec.EventID {
		t.Fatalf("EventID() = %q, want %q", gotID, rec.EventID)
	}
}

func TestPoll_TypedHandlerRehydratesViaUnmarshaler(t *testing.T) {
	repo := newFakeRepo()
	bus := sdkevents.NewMemory()
	t.Cleanup(func() { _ = bus.Close(context.Background()) })

	var got sampleEvent
	var seen bool
	_, err := bus.Subscribe("*", sdkevents.TypedHandler(func(_ context.Context, e sampleEvent) error {
		got = e
		seen = true
		return nil
	}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	rec := recordFor(t, "widget")
	repo.seed(rec, time.Now().UTC())

	p := newTestPoller(t, repo, bus.Dispatch)
	if err := p.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if !seen {
		t.Fatal("TypedHandler never fired; the Unmarshaler slow path did not rehydrate")
	}
	if got.Name != "widget" {
		t.Fatalf("rehydrated Name = %q, want %q", got.Name, "widget")
	}
	if got.Type() != "content.published" {
		t.Fatalf("rehydrated Type = %q, want content.published", got.Type())
	}
}

func TestPoll_ClosedDispatchDoesNotMark(t *testing.T) {
	repo := newFakeRepo()
	rec := recordFor(t, "closed")
	repo.seed(rec, time.Now())
	bus := sdkevents.NewMemory()
	if err := bus.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := newTestPoller(t, repo, bus.Dispatch).Poll(context.Background()); !errors.Is(err, sdkevents.ErrClosed) {
		t.Fatalf("Poll = %v", err)
	}
	if repo.markCount(rec.EventID) != 0 {
		t.Fatal("closed bus lost the outbox entry")
	}
}

func TestPoll_CanceledDeliveryDoesNotMarkOrStartNext(t *testing.T) {
	repo := newFakeRepo()
	first, second := recordFor(t, "first"), recordFor(t, "second")
	repo.seed(first, time.Now())
	repo.seed(second, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	p := newTestPoller(t, repo, func(context.Context, sdkevents.Event) error {
		calls++
		cancel()
		return nil // Cancellation must be checked even if delivery returns success.
	})
	if err := p.Poll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Poll = %v", err)
	}
	if calls != 1 || repo.markCount(first.EventID) != 0 || repo.markCount(second.EventID) != 0 {
		t.Fatal("canceled poll advanced the outbox")
	}
}

func TestPoll_RequiresDeliveryAndValidRecord(t *testing.T) {
	repo := newFakeRepo()
	repo.seed(sdkevents.Record{Type: "valid", Payload: []byte(`{}`)}, time.Now())
	p := newTestPoller(t, repo, func(context.Context, sdkevents.Event) error { t.Error("invalid record delivered"); return nil })
	if err := p.Poll(context.Background()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid record: %v", err)
	}
}
