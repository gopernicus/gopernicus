// Package rediscache provides a Redis-backed cache implementation using go-redis.
// Suitable for multi-instance deployments with shared cache.
package rediscache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/infrastructure/cache"
)

// Store is a Redis-backed cache.
type Store struct {
	rdb       *redis.Client
	keyPrefix string
}

// Option configures the Redis store.
type Option func(*Store)

// WithKeyPrefix sets a prefix for all cache keys.
// Useful for namespacing in shared Redis instances.
func WithKeyPrefix(prefix string) Option {
	return func(s *Store) {
		s.keyPrefix = prefix
	}
}

// New creates a new Redis cache store.
// Takes *redis.Client directly (goredisdb.New() returns this).
func New(rdb *redis.Client, opts ...Option) *Store {
	s := &Store{
		rdb:       rdb,
		keyPrefix: "cache:",
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Get retrieves a value by key.
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	data, err := s.rdb.Get(ctx, s.keyPrefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// GetMany retrieves multiple values by keys using MGET.
func (s *Store) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return make(map[string][]byte), nil
	}

	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = s.keyPrefix + key
	}

	vals, err := s.rdb.MGet(ctx, fullKeys...).Result()
	if err != nil {
		return nil, err
	}

	result := make(map[string][]byte, len(keys))
	for i, val := range vals {
		if val != nil {
			switch v := val.(type) {
			case string:
				result[keys[i]] = []byte(v)
			case []byte:
				result[keys[i]] = v
			}
		}
	}

	return result, nil
}

// Set stores a value with TTL.
func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.rdb.Set(ctx, s.keyPrefix+key, value, ttl).Err()
}

// Delete removes a single key.
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.rdb.Del(ctx, s.keyPrefix+key).Err()
}

// DeletePattern removes all keys matching pattern using SCAN.
func (s *Store) DeletePattern(ctx context.Context, pattern string) error {
	fullPattern := s.keyPrefix + pattern

	var cursor uint64
	for {
		keys, newCursor, err := s.rdb.Scan(ctx, cursor, fullPattern, 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = newCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

// Close is a no-op for Redis store.
// The Redis client lifecycle is managed externally.
func (s *Store) Close() error {
	return nil
}

// Compile-time interface check.
var _ cache.Cacher = (*Store)(nil)
