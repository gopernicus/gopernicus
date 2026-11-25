package outbox

import (
	"context"
	"sync"
)

// Compile-time seam: MemoryLock fills the Lock port.
var _ Lock = (*MemoryLock)(nil)

// Lock lets at most one caller per outbox run a batch at a time. Give a Poller
// one with WithLock and arm the poller on as many instances as you like: one
// drains while the others idle. Nothing outlives a batch, so there is nothing to
// release at shutdown. Delivery stays at least once either way, because a holder
// can lose its lock mid-batch; consumers still deduplicate on EventID.
type Lock interface {
	// TryRun runs fn while holding the outbox's lock and releases it when fn
	// returns, errors, or panics (a panic re-raises after release). When another
	// caller holds the lock, it returns (false, nil) without running fn. When
	// taking the lock fails, it returns (false, err) without running fn.
	// Otherwise ran is true and err is fn's error.
	TryRun(ctx context.Context, fn func(ctx context.Context) error) (ran bool, err error)
}

// MemoryLock is the in-process Lock: hand the same *MemoryLock to every poller
// that shares an outbox. It serializes pollers inside one process only, so use
// it for tests and single-binary hosts; a multi-instance host needs a lock its
// store module provides. The zero value is ready to use.
type MemoryLock struct {
	mu sync.Mutex
}

// NewMemoryLock returns an unheld in-process lock.
func NewMemoryLock() *MemoryLock { return &MemoryLock{} }

// TryRun runs fn if no other caller of this lock is inside its own fn.
func (l *MemoryLock) TryRun(ctx context.Context, fn func(ctx context.Context) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !l.mu.TryLock() {
		return false, nil
	}
	defer l.mu.Unlock()
	return true, fn(ctx)
}
