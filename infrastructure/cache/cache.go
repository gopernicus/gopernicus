// Package cache provides caching for the facilities layer.
// Store implementations are in memorycache/, rediscache/, and noopcache/ subpackages.
package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/telemetry"
)

// =============================================================================
// Cacher Interface (what store implementations satisfy)
// =============================================================================

// Cacher defines the interface for cache store implementations.
// Memory, Redis, and Noop stores satisfy this interface.
type Cacher interface {
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

// =============================================================================
// Cache Service (wraps Cacher with convenience methods)
// =============================================================================

// Cache wraps a Cacher with optional tracing and convenience methods.
//
// Tracing is opt-in via WithTracer(). Spans include hit/miss attributes
// for Get operations and record errors.
type Cache struct {
	cacher Cacher
	tracer telemetry.Tracer
}

// Option configures a Cache instance.
type Option func(*Cache)

// WithTracer enables OTEL tracing for cache operations.
func WithTracer(tracer telemetry.Tracer) Option {
	return func(c *Cache) {
		c.tracer = tracer
	}
}

// New creates a Cache service that wraps a Cacher.
// If cacher is nil, returns nil (callers should check before use).
func New(cacher Cacher, opts ...Option) *Cache {
	if cacher == nil {
		return nil
	}
	c := &Cache{
		cacher: cacher,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get retrieves a raw byte value by key.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if c.tracer == nil {
		return c.cacher.Get(ctx, key)
	}

	ctx, span := telemetry.StartClientSpan(ctx, c.tracer, "cache.get")
	defer span.End()

	telemetry.AddAttribute(span, "cache.key", key)

	data, found, err := c.cacher.Get(ctx, key)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, false, err
	}

	telemetry.AddBoolAttribute(span, "cache.hit", found)
	return data, found, nil
}

// GetMany retrieves multiple values by keys in a single round trip.
func (c *Cache) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	if c.tracer == nil {
		return c.cacher.GetMany(ctx, keys)
	}

	ctx, span := telemetry.StartClientSpan(ctx, c.tracer, "cache.get_many")
	defer span.End()

	telemetry.AddInt64Attribute(span, "cache.key_count", int64(len(keys)))

	result, err := c.cacher.GetMany(ctx, keys)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}

	telemetry.AddInt64Attribute(span, "cache.hit_count", int64(len(result)))
	return result, nil
}

// Set stores a raw byte value with TTL.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c.tracer == nil {
		return c.cacher.Set(ctx, key, value, ttl)
	}

	ctx, span := telemetry.StartClientSpan(ctx, c.tracer, "cache.set")
	defer span.End()

	telemetry.AddAttribute(span, "cache.key", key)
	telemetry.AddInt64Attribute(span, "cache.ttl_ms", ttl.Milliseconds())

	if err := c.cacher.Set(ctx, key, value, ttl); err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	return nil
}

// Delete removes a single key.
func (c *Cache) Delete(ctx context.Context, key string) error {
	if c.tracer == nil {
		return c.cacher.Delete(ctx, key)
	}

	ctx, span := telemetry.StartClientSpan(ctx, c.tracer, "cache.delete")
	defer span.End()

	telemetry.AddAttribute(span, "cache.key", key)

	if err := c.cacher.Delete(ctx, key); err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	return nil
}

// DeletePattern removes all keys matching pattern.
func (c *Cache) DeletePattern(ctx context.Context, pattern string) error {
	if c.tracer == nil {
		return c.cacher.DeletePattern(ctx, pattern)
	}

	ctx, span := telemetry.StartClientSpan(ctx, c.tracer, "cache.delete_pattern")
	defer span.End()

	telemetry.AddAttribute(span, "cache.pattern", pattern)

	if err := c.cacher.DeletePattern(ctx, pattern); err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	return nil
}

// Close shuts down the cache store.
func (c *Cache) Close() error {
	return c.cacher.Close()
}

// =============================================================================
// JSON Convenience Functions
// =============================================================================

// GetJSON retrieves and unmarshals a JSON value.
// Returns (value, found, error). If not found, returns zero value and found=false.
func GetJSON[T any](c *Cache, ctx context.Context, key string) (T, bool, error) {
	var zero T
	if c == nil {
		return zero, false, nil
	}

	data, found, err := c.Get(ctx, key)
	if err != nil || !found {
		return zero, found, err
	}

	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return zero, false, err
	}
	return result, true, nil
}

// SetJSON marshals and stores a value as JSON.
func SetJSON[T any](c *Cache, ctx context.Context, key string, value T, ttl time.Duration) error {
	if c == nil {
		return nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.Set(ctx, key, data, ttl)
}

// =============================================================================
// Status Check
// =============================================================================

// StatusCheck verifies the cache is operational.
// Returns nil if healthy, error otherwise.
func StatusCheck(ctx context.Context, c *Cache) error {
	if c == nil {
		return nil
	}

	testKey := "__health_check__"
	testValue := []byte("ok")

	if err := c.Set(ctx, testKey, testValue, time.Second); err != nil {
		return err
	}

	_, found, err := c.Get(ctx, testKey)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	_ = c.Delete(ctx, testKey)
	return nil
}
