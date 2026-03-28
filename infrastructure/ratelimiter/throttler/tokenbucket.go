package throttler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// tokenBucketScript meters requests evenly over time using a Redis-backed token bucket.
// Uses redis.call('TIME') for server-side timestamps to avoid clock skew.
//
// KEYS[1] = bucket key
// ARGV[1] = capacity (max tokens)
// ARGV[2] = refill_rate (tokens per nanosecond, as float string)
// ARGV[3] = ttl_ms (PEXPIRE for auto-cleanup)
//
// Returns: {allowed (1/0), wait_ns}
const tokenBucketScript = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])

local redis_time = redis.call('TIME')
local now_ns = tonumber(redis_time[1]) * 1000000000 + tonumber(redis_time[2]) * 1000

local data = redis.call('HMGET', key, 'tokens', 'last_refill_ns')
local tokens = tonumber(data[1])
local last_refill_ns = tonumber(data[2])

if tokens == nil or last_refill_ns == nil then
    redis.call('HMSET', key, 'tokens', tostring(capacity - 1), 'last_refill_ns', now_ns)
    redis.call('PEXPIRE', key, ttl_ms)
    return {1, 0}
end

local elapsed_ns = now_ns - last_refill_ns
if elapsed_ns > 0 then
    local refill = elapsed_ns * refill_rate
    tokens = tokens + refill
    if tokens > capacity then
        tokens = capacity
    end
end

if tokens >= 1 then
    tokens = tokens - 1
    redis.call('HMSET', key, 'tokens', tostring(tokens), 'last_refill_ns', now_ns)
    redis.call('PEXPIRE', key, ttl_ms)
    return {1, 0}
end

local deficit = 1 - tokens
local wait_ns = math.ceil(deficit / refill_rate)

redis.call('HMSET', key, 'tokens', tostring(tokens), 'last_refill_ns', now_ns)
redis.call('PEXPIRE', key, ttl_ms)

return {0, wait_ns}
`

// tokenBucket implements Throttler using a Redis-backed token bucket.
// Unlike the sliding window throttler, this meters requests evenly over time
// (e.g. one every ~66ms for 15/sec) instead of allowing bursts then starving.
type tokenBucket struct {
	rdb       *redis.Client
	keyPrefix string
	log       *slog.Logger

	scriptSHA    string
	scriptSHAMu  sync.RWMutex
	scriptLoaded bool
}

// TokenBucketOption configures the token bucket throttler.
type TokenBucketOption func(*tokenBucket)

// WithTokenBucketKeyPrefix sets a Redis key prefix for all bucket keys. Default: "throttle:tb:".
func WithTokenBucketKeyPrefix(prefix string) TokenBucketOption {
	return func(tb *tokenBucket) {
		tb.keyPrefix = prefix
	}
}

// NewTokenBucket creates a Redis-backed token bucket Throttler.
// Use this when you need even request metering rather than burst-then-starve behavior —
// e.g. when respecting external API rate limits from background workers.
func NewTokenBucket(rdb *redis.Client, log *slog.Logger, opts ...TokenBucketOption) Throttler {
	tb := &tokenBucket{
		rdb:       rdb,
		keyPrefix: "throttle:tb:",
		log:       log,
	}
	for _, opt := range opts {
		opt(tb)
	}
	return tb
}

// Acquire blocks until the token bucket allows the request.
func (tb *tokenBucket) Acquire(ctx context.Context, key string, limit ratelimiter.Limit) error {
	fullKey := tb.keyPrefix + key
	capacity := 1 + limit.Burst
	refillRate := float64(limit.Requests) / float64(limit.Window.Nanoseconds())
	ttlMs := limit.Window.Milliseconds() * 3

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		allowed, waitNs, err := tb.evalScript(ctx, fullKey, capacity, refillRate, ttlMs)
		if err != nil {
			return fmt.Errorf("throttler: token bucket script: %w", err)
		}

		if allowed {
			return nil
		}

		wait := time.Duration(waitNs)
		if wait < time.Millisecond {
			wait = time.Millisecond
		}

		tb.log.DebugContext(ctx, "throttler: token bucket waiting",
			"key", key,
			"wait", wait,
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Close is a no-op — Redis keys auto-expire via TTL.
func (tb *tokenBucket) Close() error { return nil }

func (tb *tokenBucket) evalScript(ctx context.Context, key string, capacity int, refillRate float64, ttlMs int64) (allowed bool, waitNs int64, err error) {
	refillRateStr := fmt.Sprintf("%.20e", refillRate)

	result, err := tb.evalWithSHAFallback(ctx, key, capacity, refillRateStr, ttlMs)
	if err != nil {
		return false, 0, err
	}

	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return false, 0, fmt.Errorf("unexpected script result: %v", result)
	}

	allowedVal, err := tbToInt64(values[0])
	if err != nil {
		return false, 0, fmt.Errorf("parsing allowed: %w", err)
	}

	waitVal, err := tbToInt64(values[1])
	if err != nil {
		return false, 0, fmt.Errorf("parsing wait_ns: %w", err)
	}

	return allowedVal == 1, waitVal, nil
}

func (tb *tokenBucket) evalWithSHAFallback(ctx context.Context, key string, capacity int, refillRateStr string, ttlMs int64) (any, error) {
	tb.scriptSHAMu.RLock()
	sha := tb.scriptSHA
	loaded := tb.scriptLoaded
	tb.scriptSHAMu.RUnlock()

	if loaded && sha != "" {
		result, err := tb.rdb.EvalSha(ctx, sha, []string{key}, capacity, refillRateStr, ttlMs).Result()
		if err == nil {
			return result, nil
		}
		if !strings.Contains(err.Error(), "NOSCRIPT") {
			return nil, err
		}
		tb.scriptSHAMu.Lock()
		tb.scriptLoaded = false
		tb.scriptSHAMu.Unlock()
	}

	newSHA, err := tb.rdb.ScriptLoad(ctx, tokenBucketScript).Result()
	if err != nil {
		return tb.rdb.Eval(ctx, tokenBucketScript, []string{key}, capacity, refillRateStr, ttlMs).Result()
	}

	tb.scriptSHAMu.Lock()
	tb.scriptSHA = newSHA
	tb.scriptLoaded = true
	tb.scriptSHAMu.Unlock()

	return tb.rdb.EvalSha(ctx, newSHA, []string{key}, capacity, refillRateStr, ttlMs).Result()
}

// tbToInt64 is package-local to avoid collision with goredislimiter's toInt64.
func tbToInt64(v interface{}) (int64, error) {
	switch val := v.(type) {
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	case float64:
		return int64(val), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

// Compile-time interface check.
var _ Throttler = (*tokenBucket)(nil)
