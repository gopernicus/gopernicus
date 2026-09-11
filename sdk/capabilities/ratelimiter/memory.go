package ratelimiter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// MemoryOption configures an in-process limiter at construction.
type MemoryOption func(*memoryConfig)

type memoryConfig struct{ maxEntries int }

// WithMaxEntries bounds tracked keys. Nonpositive values select 10,000.
// This is a key count, not a byte budget; hosts still control key sizes.
// Repeated options replace the limit.
func WithMaxEntries(n int) MemoryOption {
	return func(c *memoryConfig) { c.maxEntries = n }
}

// Memory enforces the common two-window approximation in one process. Construct
// it with NewMemory. It owns no goroutine or shutdown work. Expired state is
// collected on access and at capacity; active budgets are never evicted.
type Memory struct {
	mu         sync.Mutex
	windows    map[string]window
	maxEntries int
	now        func() time.Time
}

type window struct {
	start    int64
	count    int64
	previous int64
	millis   int64
	updated  int64
}

var _ Limiter = (*Memory)(nil)

// NewMemory returns an empty bounded limiter. A nil option panics.
func NewMemory(opts ...MemoryOption) *Memory {
	var cfg memoryConfig
	for _, opt := range opts {
		if opt == nil {
			panic("ratelimiter.NewMemory: nil option")
		}
		opt(&cfg)
	}
	if cfg.maxEntries <= 0 {
		cfg.maxEntries = 10_000
	}
	return &Memory{windows: make(map[string]window), maxEntries: cfg.maxEntries, now: time.Now}
}

func (m *Memory) Allow(ctx context.Context, key string, limit Limit) (Result, error) {
	if err := checkKey(ctx, key); err != nil {
		return Result{}, err
	}
	limit, err := limit.Normalize()
	if err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	now := m.now().UnixMilli()
	w, found := m.windows[key]
	if found && now >= w.expires() {
		delete(m.windows, key)
		found = false
	}
	if !found {
		if len(m.windows) >= m.maxEntries {
			for key, entry := range m.windows {
				if now >= entry.expires() {
					delete(m.windows, key)
				}
			}
			if len(m.windows) >= m.maxEntries {
				return Result{}, ErrCapacity
			}
		}
		w = window{start: now, millis: limit.Window.Milliseconds(), updated: now}
	} else if w.millis != limit.Window.Milliseconds() {
		return Result{}, fmt.Errorf("ratelimiter: a live key's window cannot change: %w", sdk.ErrInvalidInput)
	}
	now = max(now, w.updated)
	elapsed := now - w.start
	if elapsed >= w.millis {
		buckets := elapsed / w.millis
		w.previous = 0
		if buckets == 1 {
			w.previous = w.count
		}
		w.count = 0
		w.start += buckets * w.millis
	}
	remainingMillis := w.start + w.millis - now
	effective := w.count + w.previous*remainingMillis/w.millis
	ceiling := int64(limit.Requests + limit.Burst)
	result := Result{Allowed: effective < ceiling, ResetAt: time.UnixMilli(w.start + w.millis)}
	if result.Allowed {
		w.count++
		result.Remaining = int(ceiling - effective - 1)
	} else {
		result.RetryAfter = time.Duration(remainingMillis) * time.Millisecond
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	w.updated = now
	m.windows[key] = w
	return result, nil
}

func (w window) expires() int64 { return w.start + 2*w.millis }

func (m *Memory) Reset(ctx context.Context, key string) error {
	if err := checkKey(ctx, key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(m.windows, key)
	return nil
}
