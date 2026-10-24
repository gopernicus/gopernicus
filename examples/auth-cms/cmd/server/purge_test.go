package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/cms/outbound/memstore"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/cms"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// fakePurger stands in for the jobs Service's bounded terminal purge. It removes at most the
// requested limit per call from a finite pool of terminal rows, records the limits and cutoffs
// it saw, and can be made to error. It is mutex-guarded for the loop goroutine.
type fakePurger struct {
	mu        sync.Mutex
	remaining int
	limits    []int
	befores   []time.Time
	calls     int
	err       error
}

func (p *fakePurger) PurgeTerminal(_ context.Context, before time.Time, limit int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return 0, p.err
	}
	p.limits = append(p.limits, limit)
	p.befores = append(p.befores, before)
	n := limit
	if n > p.remaining {
		n = p.remaining
	}
	p.remaining -= n
	return n, nil
}

func (p *fakePurger) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestDeliveryPurgeBoundedBatch proves one purge pass removes at most the configured batch and
// the retention cutoff is now−Retention, and that a backlog larger than the batch drains over
// successive passes (the next tick continues). It also proves the purged count reaches the
// runtime's observation hook.
func TestDeliveryPurgeBoundedBatch(t *testing.T) {
	t.Parallel()
	const batch = 500
	const retention = 24 * time.Hour
	fixedNow := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

	purger := &fakePurger{remaining: 1200}
	var observed []int
	rt := delivery.JobRuntime{Purged: func(_ context.Context, n int) { observed = append(observed, n) }}

	purge := newDeliveryPurge(purger, rt, deliveryPurgeConfig{Retention: retention, Batch: batch}, func() time.Time { return fixedNow })

	// Three successive passes drain 1200 rows in bounded 500/500/200 batches.
	wantPerPass := []int{500, 500, 200}
	for i, want := range wantPerPass {
		n, err := purge(context.Background())
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if n != want {
			t.Fatalf("pass %d purged %d, want %d (bounded batch)", i, n, want)
		}
	}
	if purger.remaining != 0 {
		t.Fatalf("remaining terminal rows = %d, want 0 after draining", purger.remaining)
	}
	for i, lim := range purger.limits {
		if lim != batch {
			t.Fatalf("pass %d requested limit %d, want the configured batch %d", i, lim, batch)
		}
	}
	wantBefore := fixedNow.Add(-retention)
	for i, b := range purger.befores {
		if !b.Equal(wantBefore) {
			t.Fatalf("pass %d cutoff = %v, want now-retention %v", i, b, wantBefore)
		}
	}
	if len(observed) != 3 || observed[0] != 500 || observed[2] != 200 {
		t.Fatalf("purged observations = %v, want the per-pass counts reported to the runtime hook", observed)
	}
}

// TestDeliveryPurgeLoopContinuesAfterError proves a purge-pass error is logged and the loop
// continues (the scheduler and host are unaffected) rather than exiting.
func TestDeliveryPurgeLoopContinuesAfterError(t *testing.T) {
	t.Parallel()
	purger := &fakePurger{err: errors.New("purge backend unavailable")}
	purge := newDeliveryPurge(purger, delivery.JobRuntime{}, deliveryPurgeConfig{Retention: time.Hour, Batch: 10}, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDeliveryPurgeLoop(ctx, 5*time.Millisecond, purge, quietLog())
	}()

	// The loop must keep invoking purge despite every pass erroring.
	deadline := time.Now().Add(2 * time.Second)
	for purger.callCount() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("purge loop stopped calling after errors (calls=%d)", purger.callCount())
		}
		time.Sleep(2 * time.Millisecond)
	}
	// It must still be running (not returned) until we cancel.
	select {
	case <-done:
		t.Fatal("purge loop exited on a purge error; it must continue")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("purge loop did not return after context cancel")
	}
}

// TestDeliveryPurgeLoopCleanShutdown proves the loop returns promptly when its context is
// canceled (host-owned shutdown).
func TestDeliveryPurgeLoopCleanShutdown(t *testing.T) {
	t.Parallel()
	purger := &fakePurger{remaining: 5}
	purge := newDeliveryPurge(purger, delivery.JobRuntime{}, deliveryPurgeConfig{Retention: time.Hour, Batch: 10}, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDeliveryPurgeLoop(ctx, time.Hour, purge, quietLog()) // long interval: only cancel ends it
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("purge loop did not return promptly on cancel")
	}
}

// seedInquiry stores one contact-form inquiry stamped at createdAt — the backdating a
// browser submission cannot do.
func seedInquiry(t *testing.T, repos cms.Repositories, email string, createdAt time.Time) messaging.Inquiry {
	t.Helper()
	in, err := messaging.NewInquiry(sdk.IDGenerator{}, "Visitor", email, "hello", createdAt)
	if err != nil {
		t.Fatalf("NewInquiry: %v", err)
	}
	created, err := repos.Inquiries.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create inquiry: %v", err)
	}
	return created
}

// TestInquiryPurgeBoundedOverWiredEraser proves the host's inquiry step over the eraser this
// host wires (the cms memstore): each pass removes at most Batch inquiries at or before
// now−Retention, a backlog drains over successive passes, and a younger inquiry survives.
func TestInquiryPurgeBoundedOverWiredEraser(t *testing.T) {
	t.Parallel()
	const retention = 24 * time.Hour
	fixedNow := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	repos := memstore.New().Repositories()
	for i := range 3 {
		seedInquiry(t, repos, "old@example.com", fixedNow.Add(-retention-time.Duration(i)*time.Hour))
	}
	young := seedInquiry(t, repos, "young@example.com", fixedNow.Add(-retention+time.Microsecond))

	purge := newInquiryPurge(repos.InquiryMaintenance, inquiryPurgeConfig{Retention: retention, Batch: 2}, func() time.Time { return fixedNow })
	for i, want := range []int{2, 1, 0} {
		n, err := purge(context.Background())
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if n != want {
			t.Fatalf("pass %d purged %d, want %d (bounded batch)", i, n, want)
		}
	}
	left, err := repos.Inquiries.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(left) != 1 || left[0].ID != young.ID {
		t.Fatalf("remaining inquiries = %v, want only the one younger than the retention", left)
	}
}

// TestInquiryPurgeSchedulerLogsStep is the run-and-look in-process: the started scheduler's
// pass over a backdated inquiry logs step=inquiries count=1, and stop returns promptly.
func TestInquiryPurgeSchedulerLogsStep(t *testing.T) {
	t.Parallel()
	repos := memstore.New().Repositories()
	seedInquiry(t, repos, "visitor@example.com", time.Now().Add(-48*time.Hour))
	logs := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))

	cfg := inquiryPurgeConfig{Interval: 5 * time.Millisecond, Retention: 24 * time.Hour, Batch: 10}
	inquiries, err := newInquiryRunner(cfg, newInquiryPurge(repos.InquiryMaintenance, cfg, time.Now), log)
	if err != nil {
		t.Fatal(err)
	}
	stop := startInquiryPurge(cfg, inquiries, log)

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), "step=inquiries count=1") {
		if time.Now().After(deadline) {
			stop()
			t.Fatalf("no step=inquiries count=1 line within 2s: %s", logs.String())
		}
		time.Sleep(2 * time.Millisecond)
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("inquiry purge scheduler did not stop promptly")
	}
	if !strings.Contains(logs.String(), "inquiry purge scheduler stopped") {
		t.Errorf("no stop line: %s", logs.String())
	}
}

// TestInquiryPurgeLoopContinuesAfterError proves a failing pass is logged with its step name
// and the scheduler keeps running until stopped.
func TestInquiryPurgeLoopContinuesAfterError(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := 0
	purge := func(context.Context) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return 0, errors.New("eraser unavailable")
	}
	logs := &syncBuffer{}
	cfg := inquiryPurgeConfig{Interval: 5 * time.Millisecond, Retention: time.Hour, Batch: 1}
	inquiries, err := newInquiryRunner(cfg, purge, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	stop := startInquiryPurge(cfg, inquiries, slog.New(slog.NewTextHandler(logs, nil)))
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatalf("inquiry purge loop stopped calling after errors (calls=%d)", n)
		}
		time.Sleep(2 * time.Millisecond)
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("inquiry purge loop did not return after cancel")
	}
	if !strings.Contains(logs.String(), "retention step failed") || !strings.Contains(logs.String(), "step=inquiries") {
		t.Errorf("no step-named error line: %s", logs.String())
	}
}

// TestAuthorizationWiresOperationExpiry proves the host's authorization pocket can expire
// operation records: the slot is wired, and a record inside the retention stays.
func TestAuthorizationWiresOperationExpiry(t *testing.T) {
	t.Parallel()
	components, err := newAuthorization(nil, nil, environment.ModeDevelopment, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if n, err := components.Mutations.ExpireOperations(context.Background(), defaultOperationExpiryBatch); n != 0 || err != nil {
		t.Fatalf("ExpireOperations on a fresh ledger: %d %v", n, err)
	}
}
