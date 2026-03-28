// Package goredislimiter provides a Redis-backed rate limiter implementation using go-redis.
// Suitable for distributed systems where rate limits need to be enforced across multiple
// application instances.
//
// Uses a Lua script for atomic sliding window operations. Keys expire automatically via
// Redis TTL. Redis server time is used for all timestamp calculations to avoid clock skew
// between application servers.
//
// For single-instance deployments, memorylimiter or sqlitelimiter are simpler alternatives.
package goredislimiter

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// slidingWindowScript is an atomic Lua script for sliding window rate limiting.
// Uses Redis server time to avoid clock skew across distributed servers.
// Returns: [allowed (0/1), remaining, reset_at_unix_ns]
const slidingWindowScript = `
local key = KEYS[1]
local window_ns = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])

-- Use Redis server time to avoid clock skew across distributed servers
local redis_time = redis.call('TIME')
local now = tonumber(redis_time[1]) * 1000000000 + tonumber(redis_time[2]) * 1000

local data = redis.call('HMGET', key, 'count', 'window_start', 'prev_count', 'prev_window_start')
local count = tonumber(data[1]) or 0
local window_start = tonumber(data[2]) or 0
local prev_count = tonumber(data[3]) or 0
local prev_window_start = tonumber(data[4]) or 0

-- Check if this is a new key or window has expired
if window_start == 0 then
    -- First request
    redis.call('HMSET', key, 'count', 1, 'window_start', now, 'prev_count', 0, 'prev_window_start', 0)
    redis.call('PEXPIRE', key, math.ceil(window_ns / 1000000 * 2.5))
    return {1, limit - 1, now + window_ns}
end

local window_end = window_start + window_ns

if now > window_end then
    -- Window expired, slide forward
    if now < window_end + window_ns then
        -- Within one window of previous
        prev_count = count
        prev_window_start = window_start
    else
        -- More than one window ago
        prev_count = 0
        prev_window_start = 0
    end
    count = 0
    window_start = now
    window_end = now + window_ns
end

-- Calculate effective count with sliding window approximation
local effective_count = count
if prev_window_start > 0 then
    local elapsed = now - window_start
    local weight = (window_ns - elapsed) / window_ns
    if weight > 0 then
        effective_count = effective_count + math.floor(prev_count * weight)
    end
end

if effective_count >= limit then
    redis.call('HMSET', key, 'count', count, 'window_start', window_start, 'prev_count', prev_count, 'prev_window_start', prev_window_start)
    redis.call('PEXPIRE', key, math.ceil(window_ns / 1000000 * 2.5))
    return {0, 0, window_end}
end

-- Allow request
count = count + 1
redis.call('HMSET', key, 'count', count, 'window_start', window_start, 'prev_count', prev_count, 'prev_window_start', prev_window_start)
redis.call('PEXPIRE', key, math.ceil(window_ns / 1000000 * 2.5))

local remaining = limit - effective_count - 1
if remaining < 0 then
    remaining = 0
end

return {1, remaining, window_end}
`

// Limiter implements ratelimiter.Storer using Redis.
type Limiter struct {
	rdb       *redis.Client
	keyPrefix string

	// Script SHA is loaded lazily on first use and cached.
	scriptSHA    string
	scriptSHAMu  sync.RWMutex
	scriptLoaded bool
}

// Option configures the Limiter.
type Option func(*Limiter)

// WithKeyPrefix sets a prefix for all rate limit keys.
// Useful for namespacing in shared Redis instances. Default: "ratelimit:".
func WithKeyPrefix(prefix string) Option {
	return func(l *Limiter) {
		l.keyPrefix = prefix
	}
}

// New creates a new Redis-backed rate limiter.
func New(rdb *redis.Client, opts ...Option) *Limiter {
	l := &Limiter{
		rdb:       rdb,
		keyPrefix: "ratelimit:",
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Allow checks if a request should be allowed for the given key.
func (l *Limiter) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	if err := ctx.Err(); err != nil {
		return ratelimiter.Result{}, err
	}

	fullKey := l.keyPrefix + key
	windowNs := limit.Window.Nanoseconds()
	effectiveLimit := limit.Requests + limit.Burst

	result, err := l.evalScript(ctx, fullKey, windowNs, effectiveLimit)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredislimiter: executing rate limit script: %w", err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) != 3 {
		return ratelimiter.Result{}, fmt.Errorf("goredislimiter: unexpected script result format: %v", result)
	}

	allowed, err := toInt64(values[0])
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredislimiter: parsing allowed: %w", err)
	}

	remaining, err := toInt64(values[1])
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredislimiter: parsing remaining: %w", err)
	}

	resetAtNs, err := toInt64(values[2])
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredislimiter: parsing reset_at: %w", err)
	}

	resetAt := time.Unix(0, resetAtNs)
	var retryAfter time.Duration
	if allowed == 0 {
		retryAfter = time.Until(resetAt)
		if retryAfter < 0 {
			retryAfter = 0
		}
	}

	return ratelimiter.Result{
		Allowed:    allowed == 1,
		Remaining:  int(remaining),
		ResetAt:    resetAt,
		RetryAfter: retryAfter,
	}, nil
}

// Reset clears the rate limit state for a key.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return l.rdb.Del(ctx, l.keyPrefix+key).Err()
}

// Close is a no-op — Redis manages key expiry via TTL.
func (l *Limiter) Close() error { return nil }

// evalScript executes the rate limiting script using EVALSHA when available,
// falling back to EVAL and caching the SHA on first load or after a script flush.
func (l *Limiter) evalScript(ctx context.Context, key string, windowNs int64, limit int) (any, error) {
	l.scriptSHAMu.RLock()
	sha := l.scriptSHA
	loaded := l.scriptLoaded
	l.scriptSHAMu.RUnlock()

	if loaded && sha != "" {
		result, err := l.rdb.EvalSha(ctx, sha, []string{key}, windowNs, limit).Result()
		if err == nil {
			return result, nil
		}
		if !isNoScriptError(err) {
			return nil, err
		}
		l.scriptSHAMu.Lock()
		l.scriptLoaded = false
		l.scriptSHAMu.Unlock()
	}

	newSHA, err := l.rdb.ScriptLoad(ctx, slidingWindowScript).Result()
	if err != nil {
		return l.rdb.Eval(ctx, slidingWindowScript, []string{key}, windowNs, limit).Result()
	}

	l.scriptSHAMu.Lock()
	l.scriptSHA = newSHA
	l.scriptLoaded = true
	l.scriptSHAMu.Unlock()

	return l.rdb.EvalSha(ctx, newSHA, []string{key}, windowNs, limit).Result()
}

func isNoScriptError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOSCRIPT")
}

func toInt64(v interface{}) (int64, error) {
	switch val := v.(type) {
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	case float64:
		return int64(val), nil
	case string:
		return strconv.ParseInt(val, 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

// Compile-time interface check.
var _ ratelimiter.Storer = (*Limiter)(nil)
