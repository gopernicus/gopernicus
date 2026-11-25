package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/events/logic/outbox"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

const (
	// lockEntries and lockBatch size the two-poller case: several batches, so
	// the pollers contend for the lock more than once.
	lockEntries = 40
	lockBatch   = 7
)

// RunLock exercises the outbox.Lock contract. newPair returns two holders of
// ONE outbox's lock, as two pollers on two instances would hold them (an
// in-process lock may return the same value twice). newRepo returns a clean,
// isolated repository for the two-poller case, as for Run.
func RunLock(t *testing.T, newPair func(t *testing.T) (a, b outbox.Lock), newRepo func(t *testing.T) outbox.EntryRepository) {
	t.Helper()

	t.Run("Exclusive", func(t *testing.T) { testLockExclusive(t, newPair) })
	t.Run("ReleasedOnReturn", func(t *testing.T) { testLockReleasedOnReturn(t, newPair) })
	t.Run("ReleasedOnError", func(t *testing.T) { testLockReleasedOnError(t, newPair) })
	t.Run("ReleasedOnPanic", func(t *testing.T) { testLockReleasedOnPanic(t, newPair) })
	t.Run("CancelledContext", func(t *testing.T) { testLockCancelledContext(t, newPair) })
	t.Run("TwoPollersDeliverOnceInOrder", func(t *testing.T) { testLockTwoPollers(t, newPair, newRepo) })
}

// testLockExclusive proves that while A runs, B neither takes the lock nor
// runs its fn.
func testLockExclusive(t *testing.T, newPair func(t *testing.T) (a, b outbox.Lock)) {
	a, b := newPair(t)
	ctx := context.Background()
	ran, err := a.TryRun(ctx, func(ctx context.Context) error {
		bRan, bErr := b.TryRun(ctx, func(context.Context) error {
			t.Error("B ran while A held the lock")
			return nil
		})
		if bRan || bErr != nil {
			t.Errorf("B.TryRun while held = (%v, %v), want (false, nil)", bRan, bErr)
		}
		return nil
	})
	if !ran || err != nil {
		t.Fatalf("A.TryRun = (%v, %v), want (true, nil)", ran, err)
	}
}

// testLockReleasedOnReturn proves the lock is free once fn returns.
func testLockReleasedOnReturn(t *testing.T, newPair func(t *testing.T) (a, b outbox.Lock)) {
	a, b := newPair(t)
	mustRun(t, a, nil)
	mustRun(t, b, nil)
	mustRun(t, a, nil)
}

// testLockReleasedOnError proves fn's error passes through with ran true and
// the lock is free afterwards.
func testLockReleasedOnError(t *testing.T, newPair func(t *testing.T) (a, b outbox.Lock)) {
	a, b := newPair(t)
	fnErr := errors.New("batch failed")
	mustRun(t, a, fnErr)
	mustRun(t, b, nil)
}

// testLockReleasedOnPanic proves a panic in fn re-raises from TryRun after the
// lock is released.
func testLockReleasedOnPanic(t *testing.T, newPair func(t *testing.T) (a, b outbox.Lock)) {
	a, b := newPair(t)
	func() {
		defer func() {
			if r := recover(); r != "batch panicked" {
				t.Fatalf("recovered %v, want the fn's panic", r)
			}
		}()
		_, _ = a.TryRun(context.Background(), func(context.Context) error { panic("batch panicked") })
	}()
	mustRun(t, b, nil)
}

// testLockCancelledContext proves a cancelled context neither runs fn nor
// leaves the lock held.
func testLockCancelledContext(t *testing.T, newPair func(t *testing.T) (a, b outbox.Lock)) {
	a, b := newPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran, err := a.TryRun(ctx, func(context.Context) error {
		t.Error("fn ran on a cancelled context")
		return nil
	})
	if ran || !errors.Is(err, context.Canceled) {
		t.Fatalf("TryRun(cancelled) = (%v, %v), want (false, context.Canceled)", ran, err)
	}
	mustRun(t, b, nil)
	mustRun(t, a, nil)
}

// testLockTwoPollers drives two pollers concurrently over one outbox, each
// with its own holder of the lock, and proves every entry is delivered exactly
// once and in append order.
func testLockTwoPollers(t *testing.T, newPair func(t *testing.T) (a, b outbox.Lock), newRepo func(t *testing.T) outbox.EntryRepository) {
	a, b := newPair(t)
	repo := newRepo(t)
	want := make([]string, lockEntries)
	recs := make([]sdkevents.Record, lockEntries)
	for i := range want {
		want[i] = fmt.Sprintf("lock-%03d", i) // one Append: the EventID tie-break orders them
		recs[i] = rec(want[i])
	}
	mustAppend(t, repo, recs...)

	var mu sync.Mutex
	var delivered []string
	deliver := func(ctx context.Context, e sdkevents.Event) error {
		ided, ok := e.(interface{ EventID() string })
		if !ok {
			return errors.New("delivered event has no EventID")
		}
		mu.Lock()
		delivered = append(delivered, ided.EventID())
		mu.Unlock()
		return nil
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	for _, lock := range []outbox.Lock{a, b} {
		poller, err := outbox.NewPoller(repo, deliver, outbox.WithBatchSize(lockBatch), outbox.WithLock(lock))
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			// Poll until the outbox is empty. ErrNoWork means either empty or
			// held elsewhere, so check the outbox before stopping.
			for {
				err := poller.Poll(ctx)
				if err != nil && !errors.Is(err, workers.ErrNoWork) {
					t.Errorf("Poll: %v", err)
					return
				}
				if err == nil {
					continue
				}
				pending, err := repo.ListUnpublished(ctx, 1)
				if err != nil {
					t.Errorf("ListUnpublished: %v", err)
					return
				}
				if len(pending) == 0 {
					return
				}
			}
		})
	}
	wg.Wait()

	if !slices.Equal(delivered, want) {
		t.Fatalf("delivered %d entries %v, want each of %d once in append order", len(delivered), delivered, lockEntries)
	}
}

// mustRun runs fn (returning fnErr) under l and requires that it ran and that
// fnErr came back unchanged.
func mustRun(t *testing.T, l outbox.Lock, fnErr error) {
	t.Helper()
	called := false
	ran, err := l.TryRun(context.Background(), func(context.Context) error {
		called = true
		return fnErr
	})
	if !ran || !called || !errors.Is(err, fnErr) || (fnErr == nil && err != nil) {
		t.Fatalf("TryRun = (%v, %v) called=%v, want (true, %v) with fn run", ran, err, called, fnErr)
	}
}
