package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// markerSpy is a deliveryHealthMarker that records the started/stopped lifecycle so a test
// can assert the supervisor flips the health surface to not-running on any exit.
type markerSpy struct {
	started atomic.Int32
	stopped atomic.Int32
}

func (m *markerSpy) MarkStarted() { m.started.Add(1) }
func (m *markerSpy) MarkStopped() { m.stopped.Add(1) }

// TestSuperviseDeliveryUnexpectedErrorCancelsHost proves the IX-02 reaction: a delivery
// runtime that returns an error while its context is NOT canceled (the host is running)
// cancels the host context and records the cause, so web.Run drains and run returns nonzero.
func TestSuperviseDeliveryUnexpectedErrorCancelsHost(t *testing.T) {
	t.Parallel()
	injected := errors.New("provider engine crashed")

	deliveryCtx, cancelDelivery := context.WithCancel(context.Background())
	defer cancelDelivery()
	hostCtx, cancelHost := context.WithCancel(context.Background())
	defer cancelHost()

	marker := &markerSpy{}
	// The runtime exits promptly with the injected error while deliveryCtx is still live.
	sup := superviseDelivery(deliveryCtx, cancelHost, func(context.Context) error {
		return injected
	}, marker, quietLog())

	// The supervisor must cancel the host so web.Run would drain and main exit nonzero.
	select {
	case <-hostCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not cancel the host after an unexpected delivery error")
	}
	if !sup.wait(2 * time.Second) {
		t.Fatal("supervisor goroutine did not return")
	}
	if got := sup.exitErr(); !errors.Is(got, injected) {
		t.Fatalf("exitErr = %v, want it to wrap the injected error %v", got, injected)
	}
	if marker.started.Load() != 1 || marker.stopped.Load() != 1 {
		t.Fatalf("health lifecycle markers = started %d stopped %d, want 1/1", marker.started.Load(), marker.stopped.Load())
	}
}

// TestSuperviseDeliveryUnexpectedCleanExitCancelsHost proves an unexpected exit is a
// supervision failure even when the runtime returns nil: a delivery runtime that stops
// draining without an error while the host is running still cancels the host and records a
// cause, so run exits nonzero rather than silently serving with a dead delivery runtime.
func TestSuperviseDeliveryUnexpectedCleanExitCancelsHost(t *testing.T) {
	t.Parallel()
	deliveryCtx, cancelDelivery := context.WithCancel(context.Background())
	defer cancelDelivery()
	hostCtx, cancelHost := context.WithCancel(context.Background())
	defer cancelHost()

	sup := superviseDelivery(deliveryCtx, cancelHost, func(context.Context) error {
		return nil // clean, but unexpected: deliveryCtx was never canceled
	}, &markerSpy{}, quietLog())

	select {
	case <-hostCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not cancel the host after an unexpected clean exit")
	}
	if !sup.wait(2 * time.Second) {
		t.Fatal("supervisor goroutine did not return")
	}
	if got := sup.exitErr(); !errors.Is(got, errDeliveryExitedClean) {
		t.Fatalf("exitErr = %v, want errDeliveryExitedClean", got)
	}
}

// TestSuperviseDeliveryNormalShutdownIsQuiet proves the normal path: when the host cancels
// the delivery context (expected shutdown), the supervisor does NOT cancel the host and
// records NO exit error — run returns web.Run's own result, not a supervision failure.
func TestSuperviseDeliveryNormalShutdownIsQuiet(t *testing.T) {
	t.Parallel()
	deliveryCtx, cancelDelivery := context.WithCancel(context.Background())
	hostCtx, cancelHost := context.WithCancel(context.Background())
	defer cancelHost()

	started := make(chan struct{})
	// The runtime blocks until its own context is canceled, then returns nil — the real
	// runtime's shutdown contract.
	sup := superviseDelivery(deliveryCtx, cancelHost, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	}, &markerSpy{}, quietLog())

	<-started
	// Simulate the host-driven ordered shutdown: cancel the delivery context.
	cancelDelivery()
	if !sup.wait(2 * time.Second) {
		t.Fatal("supervisor goroutine did not return after expected shutdown")
	}
	if got := sup.exitErr(); got != nil {
		t.Fatalf("exitErr = %v, want nil (expected shutdown is the quiet path)", got)
	}
	select {
	case <-hostCtx.Done():
		t.Fatal("supervisor canceled the host on an EXPECTED shutdown — the failure path fired wrongly")
	default:
	}
}
