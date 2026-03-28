// Package memorylimiter provides an in-memory rate limiter implementation.
// Suitable for single-instance deployments, development, and testing.
//
// Memory management:
//   - Entries are cleaned up after 2x their window duration
//   - Optional max entries cap with LRU eviction
//   - Background cleanup runs periodically
package memorylimiter

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// Store implements ratelimiter.Storer using in-memory storage.
// It uses a sliding window algorithm for accurate rate limiting.
type Store struct {
	mu      sync.RWMutex
	entries map[string]*entry
	closed  bool

	// LRU tracking for max entries eviction.
	lru    *list.List
	lruMap map[string]*list.Element

	// Configuration.
	maxEntries      int
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
}

// entry tracks request counts for a key within a time window.
type entry struct {
	key        string
	count      int
	startTime  time.Time
	windowSize time.Duration

	// For sliding window, track previous window.
	prevCount     int
	prevStartTime time.Time
}

// Option configures the memory store.
type Option func(*Store)

// WithCleanupInterval sets how often expired entries are cleaned up.
// Default is 30 seconds.
func WithCleanupInterval(d time.Duration) Option {
	return func(s *Store) {
		s.cleanupInterval = d
	}
}

// WithMaxEntries sets the maximum number of entries to keep.
// When exceeded, least recently used entries are evicted.
// Default is 10000. Set to 0 for unlimited (not recommended for production).
func WithMaxEntries(n int) Option {
	return func(s *Store) {
		s.maxEntries = n
	}
}

// New creates a new in-memory rate limiter store.
func New(opts ...Option) *Store {
	s := &Store{
		entries:         make(map[string]*entry),
		lru:             list.New(),
		lruMap:          make(map[string]*list.Element),
		maxEntries:      10000,
		cleanupInterval: 30 * time.Second,
		stopCleanup:     make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	go s.cleanup()

	return s
}

// Allow checks if a request should be allowed for the given key.
func (s *Store) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	if err := ctx.Err(); err != nil {
		return ratelimiter.Result{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ratelimiter.Result{}, ratelimiter.ErrLimiterClosed
	}

	now := time.Now()
	effectiveLimit := limit.Requests + limit.Burst

	e, exists := s.entries[key]
	if !exists {
		// Evict before adding if at capacity.
		if s.maxEntries > 0 && len(s.entries) >= s.maxEntries {
			s.evictOldest()
		}

		e = &entry{
			key:        key,
			count:      1,
			startTime:  now,
			windowSize: limit.Window,
		}
		s.entries[key] = e
		s.touchLRU(key)

		return ratelimiter.Result{
			Allowed:   true,
			Remaining: effectiveLimit - 1,
			ResetAt:   now.Add(limit.Window),
		}, nil
	}

	s.touchLRU(key)
	e.windowSize = limit.Window

	// Check if we need to slide the window.
	windowEnd := e.startTime.Add(limit.Window)
	if now.After(windowEnd) {
		if now.Before(windowEnd.Add(limit.Window)) {
			// Within one window of the previous, keep prev for sliding.
			e.prevCount = e.count
			e.prevStartTime = e.startTime
		} else {
			// More than one window ago, reset completely.
			e.prevCount = 0
			e.prevStartTime = time.Time{}
		}
		e.count = 0
		e.startTime = now
		windowEnd = now.Add(limit.Window)
	}

	// Calculate effective count using sliding window approximation.
	count := e.count
	if !e.prevStartTime.IsZero() {
		elapsed := now.Sub(e.startTime)
		weight := float64(limit.Window-elapsed) / float64(limit.Window)
		if weight > 0 {
			count += int(float64(e.prevCount) * weight)
		}
	}

	if count >= effectiveLimit {
		retryAfter := windowEnd.Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return ratelimiter.Result{
			Allowed:    false,
			Remaining:  0,
			ResetAt:    windowEnd,
			RetryAfter: retryAfter,
		}, nil
	}

	e.count++
	remaining := effectiveLimit - count - 1
	if remaining < 0 {
		remaining = 0
	}

	return ratelimiter.Result{
		Allowed:   true,
		Remaining: remaining,
		ResetAt:   windowEnd,
	}, nil
}

// Reset clears the rate limit state for a key.
func (s *Store) Reset(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ratelimiter.ErrLimiterClosed
	}

	s.removeEntry(key)
	return nil
}

// Close releases resources and stops the cleanup goroutine.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	close(s.stopCleanup)
	s.entries = nil
	s.lru = nil
	s.lruMap = nil

	return nil
}

// Stats returns current store statistics for monitoring.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return Stats{
		EntryCount: len(s.entries),
		MaxEntries: s.maxEntries,
	}
}

// Stats contains store statistics.
type Stats struct {
	EntryCount int
	MaxEntries int
}

// touchLRU moves or adds a key to the front of the LRU list.
// Must be called with mu held.
func (s *Store) touchLRU(key string) {
	if elem, exists := s.lruMap[key]; exists {
		s.lru.MoveToFront(elem)
	} else {
		elem := s.lru.PushFront(key)
		s.lruMap[key] = elem
	}
}

// evictOldest removes the least recently used entry.
// Must be called with mu held.
func (s *Store) evictOldest() {
	if s.lru.Len() == 0 {
		return
	}

	elem := s.lru.Back()
	if elem == nil {
		return
	}

	key := elem.Value.(string)
	s.removeEntry(key)
}

// removeEntry removes an entry and its LRU tracking.
// Must be called with mu held.
func (s *Store) removeEntry(key string) {
	delete(s.entries, key)
	if elem, exists := s.lruMap[key]; exists {
		s.lru.Remove(elem)
		delete(s.lruMap, key)
	}
}

// cleanup periodically removes expired entries.
func (s *Store) cleanup() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCleanup:
			return
		case <-ticker.C:
			s.cleanupExpired()
		}
	}
}

// cleanupExpired removes entries whose windows have fully expired.
func (s *Store) cleanupExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	now := time.Now()
	for key, e := range s.entries {
		expiresAt := e.startTime.Add(2 * e.windowSize)
		if now.After(expiresAt) {
			s.removeEntry(key)
		}
	}
}

// nextID is an atomic counter for unique store identification.
var nextID uint64

func init() {
	atomic.AddUint64(&nextID, 1)
}

// Compile-time interface check.
var _ ratelimiter.Storer = (*Store)(nil)
