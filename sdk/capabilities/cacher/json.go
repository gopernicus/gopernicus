package cacher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// GetJSON decodes a present entry into T. Missing values return zero, false, nil.
// Storage and JSON errors are reported and returned; false/zero/null are hits.
func GetJSON[T any](ctx context.Context, cache *Cache, key string) (T, bool, error) {
	var value T
	data, found, err := cache.Get(ctx, key)
	if err != nil || !found {
		return value, false, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		var zero T
		return zero, false, cache.finish(ctx, "decode_json", fmt.Errorf("cacher: decode JSON: %w", err))
	}
	if err := cache.finish(ctx, "decode_json", nil); err != nil {
		var zero T
		return zero, false, err
	}
	return value, true, nil
}

// SetJSON encodes value and stores it with an explicit TTL. Serialization and
// storage errors are reported and returned. Zero TTL means no expiration.
func SetJSON[T any](ctx context.Context, cache *Cache, key string, value T, ttl time.Duration) error {
	if err := validateSet(ctx, ttl); err != nil {
		return cache.report(ctx, "set", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return cache.finish(ctx, "encode_json", fmt.Errorf("cacher: encode JSON: %w", err))
	}
	return cache.Set(ctx, key, data, ttl)
}

// GetOrLoadJSON returns a cached value or calls load synchronously with ctx.
// Reported cache/codec failures fall back to the loader; a successful load is
// returned even if encoding or storage fails. Loader and caller-context errors
// are returned and never cached. Negative TTL is rejected before loading.
// Concurrent misses can invoke load independently; no coalescing is promised.
func GetOrLoadJSON[T any](ctx context.Context, cache *Cache, key string, ttl time.Duration, load func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := validateSet(ctx, ttl); err != nil {
		return zero, cache.report(ctx, "set", err)
	}
	value, found, err := GetJSON[T](ctx, cache, key)
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	if err == nil && found {
		return value, nil
	}
	value, err = load(ctx)
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	if err != nil {
		return zero, err
	}
	_ = SetJSON(ctx, cache, key, value, ttl) // The strict helper reports cache/codec failures.
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	return value, nil
}
