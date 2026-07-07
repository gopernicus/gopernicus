package cacher

import (
	"context"
	"time"
)

// =============================================================================
// Storer Interface (what store implementations satisfy)
// =============================================================================

type Storer interface {
	// Get retrieves a value by key.
	// Returns (value, found, error) — found is false if key doesn't exist.
	Get(ctx context.Context, key string) ([]byte, bool, error)

	// GetMany retrieves multiple values by keys in a single round trip.
	// Returns a map of key -> value for keys that exist.
	// Keys not found are simply not included in the result map.
	GetMany(ctx context.Context, keys []string) (map[string][]byte, error)

	// Set stores a value with TTL.
	// TTL of 0 means no expiration.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes a single key.
	// Returns nil if the key doesn't exist.
	Delete(ctx context.Context, key string) error

	// DeletePattern removes all keys matching pattern (e.g., "users:*").
	// Pattern syntax depends on backend (Redis uses glob-style).
	DeletePattern(ctx context.Context, pattern string) error

	// Close releases any resources held by the store.
	Close() error
}

// Cache wraps a Cacher with convenience methods.
type Cache struct {
	storer Storer
}

// CacheOption configures a Cache instance.
type CacheOption func(*Cache)

// New creates a Cache service that wraps a Cacher.
// If cacher is nil, returns nil (callers should check before use).
func New(storer Storer, opts ...CacheOption) *Cache {
	if storer == nil {
		return nil
	}
	c := &Cache{
		storer: storer,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}
