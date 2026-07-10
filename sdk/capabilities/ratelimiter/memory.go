package ratelimiter

import (
	"context"
	"sync"
	"time"
)

// Memory is the in-process, fixed-window default Limiter that ships with sdk
// — no external dependency, handy for development and single-node
// deployments (a distributed Limiter like Redis is a separate integration
// module). Limit.Burst is ignored: Memory implements plain fixed-window
// counting, not a token bucket, so there is no continuous refill to burst
// against — a future backend can add burst semantics without changing this
// port.
//
// Eviction: window state for a key is retained until it is naturally
// overwritten by a new window for that same key (on the first Allow call
// after the previous window has expired). Keys that are never queried again
// are never evicted, so a Memory limiter run indefinitely with an
// ever-growing key space (e.g. keying by request IP) will grow unbounded;
// this mirrors the guidance on cacher.Memory (dev/single-node use) and is
// acceptable for the currently-unwired state of this package (D6).
type Memory struct {
	mu      sync.Mutex
	windows map[string]*window
	now     func() time.Time
}

type window struct {
	start time.Time
	count int
}

var _ Limiter = (*Memory)(nil)

// NewMemory returns an empty in-memory Limiter.
func NewMemory() *Memory {
	return &Memory{windows: map[string]*window{}, now: time.Now}
}

// Allow checks and increments the fixed-window counter for key. A request
// starts a new window when none exists yet or the current one has elapsed;
// within a window, requests beyond limit.Requests are denied.
func (m *Memory) Allow(_ context.Context, key string, limit Limit) (Result, error) {
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.windows[key]
	if !ok || now.Sub(w.start) >= limit.Window {
		w = &window{start: now}
		m.windows[key] = w
	}
	resetAt := w.start.Add(limit.Window)

	if w.count >= limit.Requests {
		return Result{
			Allowed:    false,
			Remaining:  0,
			ResetAt:    resetAt,
			RetryAfter: resetAt.Sub(now),
		}, nil
	}

	w.count++
	remaining := limit.Requests - w.count
	if remaining < 0 {
		remaining = 0
	}
	return Result{
		Allowed:   true,
		Remaining: remaining,
		ResetAt:   resetAt,
	}, nil
}

// Reset clears the window state for key, so its next Allow call starts fresh.
func (m *Memory) Reset(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.windows, key)
	m.mu.Unlock()
	return nil
}

// Close releases resources (none for the in-memory backend).
func (m *Memory) Close() error { return nil }
