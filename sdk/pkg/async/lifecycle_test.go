package async

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestPool_CloseWakesBlockedSubmissions(t *testing.T) {
	for _, method := range []string{"Go", "GoContext"} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(2))
				releaseSlot := make(chan struct{})
				releaseAnchor := make(chan struct{})
				pool.Go(func() { <-releaseSlot })
				pool.Go(func() { <-releaseAnchor })
				admitted := make(chan bool, 1)
				var lateTask atomic.Bool
				go func() {
					fn := func() { lateTask.Store(true) }
					if method == "GoContext" {
						admitted <- pool.GoContext(context.Background(), fn)
					} else {
						admitted <- pool.Go(fn)
					}
				}()
				synctest.Wait()
				select {
				case result := <-admitted:
					t.Fatalf("submission did not wait for capacity: %v", result)
				default:
				}

				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := pool.Close(ctx); !errors.Is(err, context.Canceled) {
					t.Fatalf("Close = %v, want canceled while tasks remain", err)
				}
				synctest.Wait()
				select {
				case result := <-admitted:
					if result {
						t.Fatal("blocked submission was admitted during closing")
					}
				default:
					t.Fatal("closing did not wake the blocked submission")
				}
				if stats := pool.Stats(); stats.Active != 2 || stats.Total != 2 || stats.Dropped != 0 {
					t.Fatalf("rejected submission changed task counters: %+v", stats)
				}
				close(releaseSlot)
				close(releaseAnchor)
				if err := pool.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				if lateTask.Load() {
					t.Fatal("a blocked submission ran after closing")
				}
			})
		})
	}
}

func TestPool_CloseCallersHaveIndependentContexts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool := NewPool(WithLogger(quietLogger()))
		release := make(chan struct{})
		pool.Go(func() { <-release })
		first := make(chan error, 1)
		go func() { first <- pool.Close(context.Background()) }()
		synctest.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := pool.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("concurrent Close = %v, want caller deadline", err)
		}
		if pool.Stats().Active != 1 {
			t.Fatal("Close canceled or forgot the running task")
		}
		canceled, cancelAgain := context.WithCancel(context.Background())
		cancelAgain()
		if err := pool.Close(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("repeated Close = %v, want cancellation while still draining", err)
		}
		select {
		case err := <-first:
			t.Fatalf("another caller's timeout ended the shared drain: %v", err)
		default:
		}

		close(release)
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if err := pool.Close(canceled); err != nil {
			t.Fatalf("already drained Close = %v, want nil", err)
		}
	})
}

func TestPool_GoContextRejectsCanceledContextWithCapacity(t *testing.T) {
	for _, dropOnFull := range []bool{false, true} {
		pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(1), WithDropOnFull(dropOnFull))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if pool.GoContext(ctx, func() { t.Error("canceled submission ran") }) {
			t.Fatalf("dropOnFull=%v: admitted canceled context", dropOnFull)
		}
		if stats := pool.Stats(); stats.Total != 0 || stats.Dropped != 0 {
			t.Fatalf("canceled submission counted as started or dropped: %+v", stats)
		}
		if !pool.Go(func() {}) {
			t.Fatal("canceled submission consumed capacity")
		}
		if err := pool.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPool_GoContextRechecksCancellationAfterCapacity(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted := make(chan bool, 1)
	var executed atomic.Bool

	// Hold final admission while the submitter takes a capacity slot. Taking
	// and restoring that token provides a barrier without scheduling sleeps.
	pool.admissionMu.Lock()
	go func() { admitted <- pool.GoContext(ctx, func() { executed.Store(true) }) }()
	<-pool.semaphore
	pool.semaphore <- struct{}{}
	cancel()
	pool.admissionMu.Unlock()
	if <-admitted {
		t.Fatal("submission admitted after its context was canceled")
	}
	if !pool.Go(func() {}) {
		t.Fatal("rejected submission leaked its capacity slot")
	}
	if err := pool.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executed.Load() || pool.Stats().Total != 1 {
		t.Fatal("rejected work ran or was counted")
	}
}

func TestPool_CloseConcurrentWithAdmissionDrainsAcceptedTasks(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(2))
	start := make(chan struct{})
	var submitters sync.WaitGroup
	var accepted atomic.Int64
	var executed atomic.Int64
	for range 100 {
		submitters.Go(func() {
			<-start
			if pool.Go(func() { executed.Add(1) }) {
				accepted.Add(1)
			}
		})
	}
	closed := make(chan error, 1)
	completedAtClose := make(chan int64, 1)
	go func() {
		<-start
		err := pool.Close(context.Background())
		completedAtClose <- executed.Load()
		closed <- err
	}()
	close(start)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	submitters.Wait()
	if got := <-completedAtClose; got != accepted.Load() {
		t.Fatalf("Close returned with only %d of %d accepted tasks finished", got, accepted.Load())
	}
	if stats := pool.Stats(); stats.Active != 0 || stats.Total != accepted.Load() || executed.Load() != accepted.Load() {
		t.Fatalf("accepted=%d, executed=%d, final stats=%+v", accepted.Load(), executed.Load(), stats)
	}
}

func TestPool_WaitSupportsSuccessiveBatches(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()), WithMaxConcurrency(2))
	var completed atomic.Int64
	for batch := int64(1); batch <= 3; batch++ {
		for range 5 {
			if !pool.Go(func() { completed.Add(1) }) {
				t.Fatal("Wait closed admission")
			}
		}
		pool.Wait()
		if completed.Load() != batch*5 || pool.Stats().Active != 0 {
			t.Fatalf("batch %d did not finish", batch)
		}
	}
	if err := pool.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPool_CloseIncludesPanicAccounting(t *testing.T) {
	pool := NewPool(WithLogger(quietLogger()))
	pool.Go(func() { panic("task panic") })
	if err := pool.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stats := pool.Stats(); stats.Panics != 1 || stats.Total != 1 || stats.Active != 0 {
		t.Fatalf("drained snapshot = %+v", stats)
	}
}

func TestPool_NilLoggerUsesDefault(t *testing.T) {
	pool := NewPool(WithLogger(nil))
	if err := pool.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
