package goredis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// defaultCacheKeyPrefix namespaces cache keys in a shared Redis instance.
const defaultCacheKeyPrefix = "cache:"

// scanBatch is the COUNT hint for the SCAN cursor DeletePattern walks.
const scanBatch = 100

var _ cacher.Storer = (*Cacher)(nil)

// Cacher is a Redis-backed cacher.Storer for multi-instance deployments that
// share one cache. Values are opaque bytes; keys are namespaced by an optional
// prefix. The caller supplies and owns the *redis.Client — Close is a no-op and
// never closes the client, whose lifecycle (and shutdown) the caller owns.
type Cacher struct {
	rdb       *redis.Client
	keyPrefix string
}

// CacheOption configures a Cacher.
type CacheOption func(*Cacher)

// WithCacheKeyPrefix sets the prefix prepended to every cache key, for
// namespacing in a shared Redis instance. Default: "cache:".
func WithCacheKeyPrefix(prefix string) CacheOption {
	return func(c *Cacher) {
		c.keyPrefix = prefix
	}
}

// NewCacher creates a Redis cache over the caller's client (which may be the
// same client feeding the Bus and Limiter). The caller owns the client.
func NewCacher(rdb *redis.Client, opts ...CacheOption) *Cacher {
	c := &Cacher{
		rdb:       rdb,
		keyPrefix: defaultCacheKeyPrefix,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get retrieves a value by key. found is false (with a nil value and nil error)
// when the key is absent.
func (c *Cacher) Get(ctx context.Context, key string) ([]byte, bool, error) {
	data, err := c.rdb.Get(ctx, c.keyPrefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// GetMany retrieves multiple values in one MGET round trip. Keys not present are
// simply omitted from the result map.
func (c *Cacher) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return make(map[string][]byte), nil
	}

	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = c.keyPrefix + key
	}

	vals, err := c.rdb.MGet(ctx, fullKeys...).Result()
	if err != nil {
		return nil, err
	}

	result := make(map[string][]byte, len(keys))
	for i, val := range vals {
		switch v := val.(type) {
		case string:
			result[keys[i]] = []byte(v)
		case []byte:
			result[keys[i]] = v
		}
	}
	return result, nil
}

// Set stores a value with TTL. A ttl of 0 means no expiration.
func (c *Cacher) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.rdb.Set(ctx, c.keyPrefix+key, value, ttl).Err()
}

// Delete removes a single key, returning nil when the key does not exist.
func (c *Cacher) Delete(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, c.keyPrefix+key).Err()
}

// DeletePattern removes every key matching the glob pattern (e.g. "users:*"),
// walking the keyspace with SCAN so a large match set never blocks Redis.
func (c *Cacher) DeletePattern(ctx context.Context, pattern string) error {
	fullPattern := c.keyPrefix + pattern

	var cursor uint64
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, fullPattern, scanBatch).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

// Close is a no-op: the *redis.Client lifecycle belongs to the caller. It is
// idempotent, so repeated calls remain safe.
func (c *Cacher) Close() error {
	return nil
}
