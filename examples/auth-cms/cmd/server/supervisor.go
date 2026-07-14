package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// errDeliveryExitedClean is the recorded cause when the delivery runtime returns nil while
// the host is still running — an unexpected clean exit is as much a supervision failure as
// an errored one (the runtime stopped draining work though shutdown was never requested).
var errDeliveryExitedClean = errors.New("delivery runtime exited unexpectedly (returned without an error) while the host was running")

// deliveryHealthMarker is the narrow lifecycle seam the supervisor brackets the delivery
// runtime with, so the health surface reports not_started vs running. *deliveryhealth.Health
// satisfies it; the interface keeps superviseDelivery unit-testable without the full surface.
type deliveryHealthMarker interface {
	MarkStarted()
	MarkStopped()
}

// deliverySupervisor runs the host-owned delivery runtime as part of the host lifecycle
// (IX-02) and supervises its exit. An exit while the runtime's own context is NOT canceled is
// UNEXPECTED — the runtime stopped on its own (error or clean return) though the host never
// requested shutdown — and must not leave HTTP admitting work against a dead runtime, so the
// supervisor records the cause and cancels the host so web.Run drains and run returns nonzero.
// An exit while the context IS canceled is the normal quiet shutdown path.
type deliverySupervisor struct {
	done chan struct{}

	mu  sync.Mutex
	err error
}

// superviseDelivery starts the delivery runtime under supervision. It brackets the runtime
// with health MarkStarted/MarkStopped and launches the supervising goroutine, returning
// immediately with a handle the shutdown tail waits on. deliveryCtx is the runtime's own
// Background-derived context (canceled after HTTP drains); cancelHost cancels the context
// web.Run blocks on so an unexpected exit drives the ordered shutdown.
func superviseDelivery(deliveryCtx context.Context, cancelHost context.CancelFunc, run func(context.Context) error, health deliveryHealthMarker, log *slog.Logger) *deliverySupervisor {
	s := &deliverySupervisor{done: make(chan struct{})}
	health.MarkStarted()
	go func() {
		defer close(s.done)
		defer health.MarkStopped()

		err := run(deliveryCtx)

		if deliveryCtx.Err() != nil {
			// Expected shutdown: the host canceled deliveryCtx. Quiet path — a stop-time error
			// is logged but is not a supervision failure.
			if err != nil {
				log.ErrorContext(deliveryCtx, "delivery runtime stopped with error", "error", err)
			}
			return
		}

		// Unexpected exit while the host is running.
		if err == nil {
			err = errDeliveryExitedClean
		}
		s.setErr(err)
		log.ErrorContext(deliveryCtx,
			"delivery runtime exited unexpectedly while the host was running; initiating host shutdown",
			"error", err)
		cancelHost()
	}()
	return s
}

// wait blocks until the supervised runtime's goroutine returns or the bound elapses. It
// reports true when the goroutine finished within the bound.
func (s *deliverySupervisor) wait(within time.Duration) bool {
	timer := time.NewTimer(within)
	defer timer.Stop()
	select {
	case <-s.done:
		return true
	case <-timer.C:
		return false
	}
}

// exitErr returns the recorded unexpected-exit cause, or nil for the normal shutdown path.
// It is safe to read after wait has returned (the goroutine's write happens-before its
// close of done).
func (s *deliverySupervisor) exitErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *deliverySupervisor) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}
