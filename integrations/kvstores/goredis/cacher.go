package goredis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// defaultCacheKeyPrefix namespaces cache keys in a shared Redis instance.
const defaultCacheKeyPrefix = "cache:"

// scanBatch is the COUNT hint for the SCAN cursor DeletePrefix walks.
const scanBatch = 100

var (
	_ cacher.Storer        = (*Cacher)(nil)
	_ cacher.PrefixDeleter = (*Cacher)(nil)
)

// Cacher is a Redis-backed cacher.Storer for multi-instance deployments that
// share one cache. Values are opaque bytes; keys are namespaced by an optional
// prefix. The caller supplies and owns the *redis.Client and its lifecycle.
type Cacher struct {
	rdb       *redis.Client
	keyPrefix string
}

// CacheOption configures construction of a Cacher. Options apply in order.
type CacheOption func(*cacheConfig)

type cacheConfig struct{ keyPrefix string }

// WithCacheKeyPrefix sets the prefix prepended to every cache key, for
// namespacing in a shared Redis instance. Default: "cache:".
func WithCacheKeyPrefix(prefix string) CacheOption {
	return func(c *cacheConfig) {
		c.keyPrefix = prefix
	}
}

// NewCacher creates a Redis cache over the caller's client (which may be the
// same client feeding the Bus and Limiter). The caller owns the client.
// A nil option panics.
func NewCacher(rdb *redis.Client, opts ...CacheOption) *Cacher {
	cfg := cacheConfig{keyPrefix: defaultCacheKeyPrefix}
	for _, opt := range opts {
		if opt == nil {
			panic("goredis: nil CacheOption")
		}
		opt(&cfg)
	}
	return &Cacher{rdb: rdb, keyPrefix: cfg.keyPrefix}
}

// Get retrieves a value by key. found is false (with a nil value and nil error)
// when the key is absent.
func (c *Cacher) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	data, err := c.rdb.Get(ctx, c.keyPrefix+key).Bytes()
	err = cacheCommandError(ctx, err)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return make(map[string][]byte), nil
	}

	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = c.keyPrefix + key
	}

	vals, err := c.rdb.MGet(ctx, fullKeys...).Result()
	err = cacheCommandError(ctx, err)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]byte, len(keys))
	for i, val := range vals {
		switch v := val.(type) {
		case string:
			result[keys[i]] = []byte(v)
		case []byte:
			result[keys[i]] = bytes.Clone(v)
		}
	}
	return result, nil
}

// Set replaces a value and its TTL. Zero means no expiration; negative TTL is
// invalid input. Positive durations round up to Redis's millisecond resolution.
func (c *Cacher) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl < 0 {
		return fmt.Errorf("cacher: TTL must not be negative: %w", sdk.ErrInvalidInput)
	}
	if ttl == 0 {
		return cacheCommandError(ctx, c.rdb.Set(ctx, c.keyPrefix+key, value, 0).Err())
	}
	// Round the integer millisecond count, not the duration: rounding a maximum
	// time.Duration up and converting it back to nanoseconds would overflow.
	millis := int64(ttl / time.Millisecond)
	if ttl%time.Millisecond != 0 {
		millis++
	}
	return cacheCommandError(ctx, c.rdb.Do(ctx, "set", c.keyPrefix+key, value, "px", millis).Err())
}

// Delete removes a single key, returning nil when the key does not exist.
func (c *Cacher) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return cacheCommandError(ctx, c.rdb.Del(ctx, c.keyPrefix+key).Err())
}

// DeletePrefix removes a literal prefix within this adapter's namespace. Empty
// prefix clears only that namespace. SCAN deletion is not atomic with writers.
func (c *Cacher) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fullPattern := escapeCachePrefix(c.keyPrefix+prefix) + "*"

	var cursor uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		keys, next, err := c.rdb.Scan(ctx, cursor, fullPattern, scanBatch).Result()
		err = cacheCommandError(ctx, err)
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := cacheCommandError(ctx, c.rdb.Del(ctx, keys...).Err()); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func cacheCommandError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func escapeCachePrefix(prefix string) string {
	var escaped strings.Builder
	for i := 0; i < len(prefix); i++ {
		switch prefix[i] {
		case '*', '?', '[', ']', '\\':
			escaped.WriteByte('\\')
		}
		escaped.WriteByte(prefix[i])
	}
	return escaped.String()
}
