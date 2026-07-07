package goredis

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

// defaultLimiterKeyPrefix namespaces rate-limit keys in a shared Redis instance.
const defaultLimiterKeyPrefix = "ratelimit:"

// slidingWindowScript is an atomic Lua sliding-window rate limiter. It reads
// Redis server time (not the caller's clock) so distributed application
// instances agree on the window regardless of clock skew, and lets Redis expire
// idle keys via PEXPIRE. It returns [allowed (0/1), remaining, reset_at_unix_ns].
const slidingWindowScript = `
local key = KEYS[1]
local window_ns = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])

-- Use Redis server time to avoid clock skew across distributed servers.
local redis_time = redis.call('TIME')
local now = tonumber(redis_time[1]) * 1000000000 + tonumber(redis_time[2]) * 1000

local data = redis.call('HMGET', key, 'count', 'window_start', 'prev_count', 'prev_window_start')
local count = tonumber(data[1]) or 0
local window_start = tonumber(data[2]) or 0
local prev_count = tonumber(data[3]) or 0
local prev_window_start = tonumber(data[4]) or 0

-- New key or expired window.
if window_start == 0 then
    redis.call('HMSET', key, 'count', 1, 'window_start', now, 'prev_count', 0, 'prev_window_start', 0)
    redis.call('PEXPIRE', key, math.ceil(window_ns / 1000000 * 2.5))
    return {1, limit - 1, now + window_ns}
end

local window_end = window_start + window_ns

if now > window_end then
    -- Window expired: slide forward, carrying the previous window only if it is
    -- recent enough to still weigh on the sliding estimate.
    if now < window_end + window_ns then
        prev_count = count
        prev_window_start = window_start
    else
        prev_count = 0
        prev_window_start = 0
    end
    count = 0
    window_start = now
    window_end = now + window_ns
end

-- Sliding-window approximation: blend the decaying tail of the previous window.
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

count = count + 1
redis.call('HMSET', key, 'count', count, 'window_start', window_start, 'prev_count', prev_count, 'prev_window_start', prev_window_start)
redis.call('PEXPIRE', key, math.ceil(window_ns / 1000000 * 2.5))

local remaining = limit - effective_count - 1
if remaining < 0 then
    remaining = 0
end

return {1, remaining, window_end}
`

var _ ratelimiter.Limiter = (*Limiter)(nil)

// Limiter is a Redis-backed ratelimiter.Limiter that enforces one sliding window
// across every application instance sharing the client. The window is evaluated
// by an atomic Lua script (EVALSHA with an EVAL/reload fallback) keyed off Redis
// server time. The caller supplies and owns the *redis.Client — Close is a no-op
// and never closes it.
type Limiter struct {
	rdb       *redis.Client
	keyPrefix string

	// scriptSHA is loaded lazily on first use and cached; scriptLoaded guards it
	// against a NOSCRIPT reload after a server-side SCRIPT FLUSH.
	scriptMu     sync.RWMutex
	scriptSHA    string
	scriptLoaded bool
}

// LimiterOption configures a Limiter.
type LimiterOption func(*Limiter)

// WithLimiterKeyPrefix sets the prefix prepended to every rate-limit key, for
// namespacing in a shared Redis instance. Default: "ratelimit:".
func WithLimiterKeyPrefix(prefix string) LimiterOption {
	return func(l *Limiter) {
		l.keyPrefix = prefix
	}
}

// NewLimiter creates a Redis rate limiter over the caller's client (which may be
// the same client feeding the Bus and Cacher). The caller owns the client.
func NewLimiter(rdb *redis.Client, opts ...LimiterOption) *Limiter {
	l := &Limiter{
		rdb:       rdb,
		keyPrefix: defaultLimiterKeyPrefix,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Allow checks and records a request against key's sliding window. Limit.Burst
// is added to Limit.Requests to form the effective ceiling.
func (l *Limiter) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	if err := ctx.Err(); err != nil {
		return ratelimiter.Result{}, err
	}

	fullKey := l.keyPrefix + key
	windowNs := limit.Window.Nanoseconds()
	effectiveLimit := limit.Requests + limit.Burst

	raw, err := l.evalScript(ctx, fullKey, windowNs, effectiveLimit)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredis: executing rate limit script: %w", err)
	}

	values, ok := raw.([]any)
	if !ok || len(values) != 3 {
		return ratelimiter.Result{}, fmt.Errorf("goredis: unexpected rate limit script result: %v", raw)
	}

	allowed, err := toInt64(values[0])
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredis: parsing allowed: %w", err)
	}
	remaining, err := toInt64(values[1])
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredis: parsing remaining: %w", err)
	}
	resetAtNs, err := toInt64(values[2])
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredis: parsing reset_at: %w", err)
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

// Reset clears the sliding-window state for key.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return l.rdb.Del(ctx, l.keyPrefix+key).Err()
}

// Close is a no-op: Redis expires keys via PEXPIRE and the client lifecycle
// belongs to the caller. It is idempotent, so repeated calls remain safe.
func (l *Limiter) Close() error {
	return nil
}

// evalScript runs the sliding-window script via EVALSHA, loading and caching the
// SHA on first use and reloading after a NOSCRIPT (server-side SCRIPT FLUSH). If
// ScriptLoad itself fails it falls back to a plain EVAL.
func (l *Limiter) evalScript(ctx context.Context, key string, windowNs int64, limit int) (any, error) {
	l.scriptMu.RLock()
	sha := l.scriptSHA
	loaded := l.scriptLoaded
	l.scriptMu.RUnlock()

	if loaded && sha != "" {
		result, err := l.rdb.EvalSha(ctx, sha, []string{key}, windowNs, limit).Result()
		if err == nil {
			return result, nil
		}
		if !isNoScriptError(err) {
			return nil, err
		}
		l.scriptMu.Lock()
		l.scriptLoaded = false
		l.scriptMu.Unlock()
	}

	newSHA, err := l.rdb.ScriptLoad(ctx, slidingWindowScript).Result()
	if err != nil {
		return l.rdb.Eval(ctx, slidingWindowScript, []string{key}, windowNs, limit).Result()
	}

	l.scriptMu.Lock()
	l.scriptSHA = newSHA
	l.scriptLoaded = true
	l.scriptMu.Unlock()

	return l.rdb.EvalSha(ctx, newSHA, []string{key}, windowNs, limit).Result()
}

func isNoScriptError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOSCRIPT")
}

// toInt64 normalizes the Lua reply elements, which go-redis may surface as any
// of int64/int/float64/string depending on the RESP protocol version.
func toInt64(v any) (int64, error) {
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
