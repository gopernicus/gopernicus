package events_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

func TestMemory_Lifecycle_CloseWaitsForAdmittedWork(t *testing.T) {
	for _, method := range []string{"Emit", "Dispatch"} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bus := newMemory(events.WithWorkerCount(1))
				started := make(chan struct{})
				release := make(chan struct{})
				releaseWork := sync.OnceFunc(func() { close(release) })
				defer func() {
					releaseWork()
					if err := bus.Close(context.Background()); err != nil {
						t.Error(err)
					}
				}()
				if _, err := bus.Subscribe("lifecycle.blocked", func(context.Context, events.Event) error {
					close(started)
					<-release
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				publish := bus.Emit
				if method == "Dispatch" {
					publish = bus.Dispatch
				}
				published := make(chan error, 1)
				go func() { published <- publish(context.Background(), events.NewBaseEvent("lifecycle.blocked")) }()
				<-started

				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := bus.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Close = %v, want deadline while the handler remains blocked", err)
				}

				closed := make(chan error, 2)
				for range 2 {
					go func() { closed <- bus.Close(context.Background()) }()
				}
				synctest.Wait()
				select {
				case err := <-closed:
					t.Fatalf("repeated Close returned before the handler finished: %v", err)
				default:
				}
				canceled, cancelAgain := context.WithCancel(context.Background())
				cancelAgain()
				if err := bus.Close(canceled); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled Close = %v, want cancellation while still draining", err)
				}
				if err := publish(context.Background(), events.NewBaseEvent("lifecycle.blocked")); !errors.Is(err, events.ErrClosed) {
					t.Fatalf("%s during drain = %v, want ErrClosed", method, err)
				}

				releaseWork()
				if err := <-published; err != nil {
					t.Fatalf("admitted %s = %v", method, err)
				}
				for range 2 {
					if err := <-closed; err != nil {
						t.Fatalf("waiting Close = %v", err)
					}
				}
				if err := bus.Close(canceled); err != nil {
					t.Fatalf("already drained Close = %v, want nil", err)
				}
			})
		})
	}
}

func TestMemory_Lifecycle_CloseDrainsConcurrentAdmissions(t *testing.T) {
	const submitterCount = 64
	bus := newMemory(events.WithWorkerCount(2), events.WithQueueSize(submitterCount+1))
	defer bus.Close(context.Background())
	var delivered atomic.Int64
	if _, err := bus.Subscribe("lifecycle.concurrent", func(context.Context, events.Event) error {
		delivered.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	event := events.NewBaseEvent("lifecycle.concurrent")
	if err := bus.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	accepted.Store(1)
	start := make(chan struct{})
	var submitters sync.WaitGroup
	for range submitterCount {
		submitters.Go(func() {
			<-start
			err := bus.Emit(context.Background(), event)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, events.ErrClosed) {
				t.Errorf("Emit = %v, want admission or ErrClosed", err)
			}
		})
	}
	closed := make(chan error, 1)
	var completedAtClose int64
	go func() {
		<-start
		err := bus.Close(context.Background())
		completedAtClose = delivered.Load()
		closed <- err
	}()
	close(start)
	if err := <-closed; err != nil {
		t.Error(err)
	}
	submitters.Wait()
	if completedAtClose != accepted.Load() {
		t.Errorf("Close returned after %d deliveries, want all %d admitted events", completedAtClose, accepted.Load())
	}
	if delivered.Load() != accepted.Load() {
		t.Errorf("delivered = %d, admitted = %d", delivered.Load(), accepted.Load())
	}
}

func TestMemory_Lifecycle_PanicDoesNotSkipRemainingHandlers(t *testing.T) {
	for _, method := range []string{"Emit", "Dispatch"} {
		t.Run(method, func(t *testing.T) {
			bus := newMemory(events.WithWorkerCount(1))
			defer bus.Close(context.Background())
			if _, err := bus.Subscribe("lifecycle.panic", func(context.Context, events.Event) error {
				panic("expected handler panic")
			}); err != nil {
				t.Fatal(err)
			}
			var delivered atomic.Int64
			if _, err := bus.Subscribe("lifecycle.panic", func(context.Context, events.Event) error {
				delivered.Add(1)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			event := events.NewBaseEvent("lifecycle.panic")
			if method == "Dispatch" {
				if err := bus.Dispatch(context.Background(), event); !errors.Is(err, events.ErrHandlerPanic) {
					t.Fatalf("Dispatch = %v, want ErrHandlerPanic", err)
				}
			} else if err := bus.Emit(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			if err := bus.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if delivered.Load() != 1 {
				t.Fatalf("healthy handler received the same event %d times, want 1", delivered.Load())
			}
		})
	}
}

func TestMemory_Lifecycle_DispatchPreservesFailureOnCancellation(t *testing.T) {
	for _, boundary := range []string{"between_callbacks", "after_last_callback"} {
		t.Run(boundary, func(t *testing.T) {
			bus := newMemory()
			defer bus.Close(context.Background())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("first handler failed")
			if _, err := bus.Subscribe("lifecycle.cancel", func(context.Context, events.Event) error {
				return failure
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := bus.Subscribe("lifecycle.cancel", func(context.Context, events.Event) error {
				cancel()
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var lateCalls int
			if boundary == "between_callbacks" {
				if _, err := bus.Subscribe("lifecycle.cancel", func(context.Context, events.Event) error {
					lateCalls++
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			err := bus.Dispatch(ctx, events.NewBaseEvent("lifecycle.cancel"))
			if !errors.Is(err, failure) || !errors.Is(err, context.Canceled) {
				t.Errorf("Dispatch = %v, want both the handler failure and cancellation", err)
			}
			if lateCalls != 0 {
				t.Errorf("called %d later handlers after cancellation", lateCalls)
			}
		})
	}
}

func TestMemory_Lifecycle_RejectsCanceledAdmission(t *testing.T) {
	for _, method := range []string{"Emit", "Dispatch"} {
		t.Run(method, func(t *testing.T) {
			bus := newMemory(events.WithWorkerCount(1))
			defer bus.Close(context.Background())
			var delivered atomic.Int64
			if _, err := bus.Subscribe("lifecycle.canceled", func(context.Context, events.Event) error {
				delivered.Add(1)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			publish := bus.Emit
			if method == "Dispatch" {
				publish = bus.Dispatch
			}
			if err := publish(ctx, events.NewBaseEvent("lifecycle.canceled")); !errors.Is(err, context.Canceled) {
				t.Errorf("%s = %v, want cancellation", method, err)
			}
			if err := bus.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if delivered.Load() != 0 {
				t.Errorf("delivered %d events after rejected admission", delivered.Load())
			}
		})
	}
}

func TestMemory_Lifecycle_NilLoggerUsesDefault(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	defer slog.SetDefault(previous)
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	bus := events.NewMemory(events.WithLogger(nil), events.WithWorkerCount(1))
	slog.SetDefault(previous)
	defer bus.Close(context.Background())
	if _, err := bus.Subscribe("lifecycle.logger", func(context.Context, events.Event) error {
		return errors.New("expected handler failure")
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(context.Background(), events.NewBaseEvent("lifecycle.logger")); err != nil {
		t.Fatal(err)
	}
	if err := bus.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(output.String(), "expected handler failure"); count != 1 {
		t.Errorf("default logger recorded the handler failure %d times, want 1; output: %s", count, output.String())
	}
}
