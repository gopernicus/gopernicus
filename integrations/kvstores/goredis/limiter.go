package goredis

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/redis/go-redis/v9"
)

const defaultLimiterKeyPrefix = "ratelimit:"
const limiterKeyVersion = "v2:"

// The reply is {allowed, remaining, reset_at_ms, retry_ms}. Negative allowed
// markers report incompatible state (-2) or a live window-policy mismatch (-1).
const slidingWindowScript = `
local key = KEYS[1]
local window = tonumber(ARGV[1])
local ceiling = tonumber(ARGV[2])
local max_window = tonumber(ARGV[3])
local exists = redis.call('EXISTS', key) == 1
if exists then
    local stored_window = tonumber(redis.call('HGET', key, 'window_ms'))
    if not stored_window or stored_window <= 0 or stored_window > max_window or stored_window ~= math.floor(stored_window) then
        return {-2, 0, 0, 0}
    end
end

local stamp = redis.call('TIME')
local now = tonumber(stamp[1]) * 1000 + math.floor(tonumber(stamp[2]) / 1000)
local data = redis.call('HMGET', key, 'count', 'window_start', 'prev_count', 'window_ms', 'updated_at', 'expires_at')
local count = tonumber(data[1]) or 0
local start = tonumber(data[2]) or now
local previous = tonumber(data[3]) or 0
local stored_window = tonumber(data[4]) or window
local updated = tonumber(data[5]) or now
local expires = tonumber(data[6]) or now
now = math.max(now, updated)

if not exists or now >= expires then
    start = now
    count = 0
    previous = 0
elseif stored_window ~= window then
    return {-1, 0, 0, 0}
else
    local steps = math.floor((now - start) / window)
    if steps >= 1 then
        previous = steps == 1 and count or 0
        count = 0
        start = start + steps * window
    end
end

local remaining_ms = window - (now - start)
local effective = count + math.floor(previous * remaining_ms / window)
local allowed = effective < ceiling
local remaining = 0
local retry = remaining_ms
if allowed then
    count = count + 1
    remaining = math.max(ceiling - effective - 1, 0)
    retry = 0
end
expires = start + 2 * window
redis.call('HSET', key, 'count', count, 'window_start', start, 'prev_count', previous,
    'window_ms', window, 'updated_at', now, 'expires_at', expires)
redis.call('PEXPIREAT', key, expires)
return {allowed and 1 or 0, remaining, start + window, retry}
`

const resetLimiterScript = `
local key = KEYS[1]
if redis.call('EXISTS', key) == 0 then return 0 end
local window = tonumber(redis.call('HGET', key, 'window_ms'))
if not window or window <= 0 or window > tonumber(ARGV[1]) or window ~= math.floor(window) then
    return -1
end
return redis.call('DEL', key)
`

var (
	_                  ratelimiter.Limiter = (*Limiter)(nil)
	limiterAllowScript                     = redis.NewScript(slidingWindowScript)
	limiterResetScript                     = redis.NewScript(resetLimiterScript)
)

// Limiter applies the shared two-window approximation atomically using Redis
// time. The host owns its client; no lifecycle methods are needed here.
type Limiter struct {
	rdb       *redis.Client
	keyPrefix string
}

// LimiterOption configures construction of a Limiter. Options apply in order.
type LimiterOption func(*limiterConfig)

type limiterConfig struct {
	keyPrefix string
}

// WithLimiterKeyPrefix selects the host namespace (default "ratelimit:"). An
// internal "v2:" suffix is always appended, including to custom/empty prefixes.
func WithLimiterKeyPrefix(prefix string) LimiterOption {
	return func(cfg *limiterConfig) { cfg.keyPrefix = prefix }
}

// NewLimiter wraps a caller-owned client. Versioned keys normally separate old
// state; a colliding legacy record is rejected, never migrated or deleted.
// A nil option panics.
func NewLimiter(rdb *redis.Client, opts ...LimiterOption) *Limiter {
	cfg := limiterConfig{keyPrefix: defaultLimiterKeyPrefix}
	for _, opt := range opts {
		if opt == nil {
			panic("goredis: nil LimiterOption")
		}
		opt(&cfg)
	}
	return &Limiter{rdb: rdb, keyPrefix: cfg.keyPrefix + limiterKeyVersion}
}

// Allow normalizes the policy and checks a nonempty key using server-time
// millisecond buckets. RetryAfter is a relative checkpoint, not local-clock math.
func (l *Limiter) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	if err := ctx.Err(); err != nil {
		return ratelimiter.Result{}, err
	}
	if key == "" {
		return ratelimiter.Result{}, fmt.Errorf("goredis: rate limit key is empty: %w", sdk.ErrInvalidInput)
	}
	normalized, err := limit.Normalize()
	if err != nil {
		return ratelimiter.Result{}, err
	}
	raw, err := limiterAllowScript.Run(ctx, l.rdb, []string{l.keyPrefix + key}, normalized.Window.Milliseconds(), normalized.Requests+normalized.Burst, ratelimiter.MaxWindow.Milliseconds()).Result()
	if ctx.Err() != nil {
		return ratelimiter.Result{}, ctx.Err()
	}
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("goredis: evaluating rate limit: %w", err)
	}
	return decodeLimiterResult(raw, normalized)
}

func decodeLimiterResult(raw any, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	values, ok := raw.([]any)
	if !ok || len(values) != 4 {
		return ratelimiter.Result{}, fmt.Errorf("goredis: invalid rate limit reply shape")
	}
	numbers := [4]int64{}
	for i, value := range values {
		n, ok := value.(int64)
		if !ok {
			return ratelimiter.Result{}, fmt.Errorf("goredis: rate limit reply field %d is not an integer", i)
		}
		numbers[i] = n
	}
	allowed, remaining, reset, retry := numbers[0], numbers[1], numbers[2], numbers[3]
	if (allowed == -1 || allowed == -2) && remaining == 0 && reset == 0 && retry == 0 {
		if allowed == -2 {
			return ratelimiter.Result{}, fmt.Errorf("goredis: incompatible rate limit state: %w", sdk.ErrConflict)
		}
		return ratelimiter.Result{}, fmt.Errorf("goredis: active rate limit window changed: %w", sdk.ErrInvalidInput)
	}
	ceiling := int64(limit.Requests + limit.Burst)
	if (allowed != 0 && allowed != 1) || remaining < 0 || remaining >= ceiling || reset <= 0 || reset > 1<<53-1 || retry < 0 || retry > limit.Window.Milliseconds() || (allowed == 1 && retry != 0) || (allowed == 0 && (remaining != 0 || retry == 0)) {
		return ratelimiter.Result{}, fmt.Errorf("goredis: invalid rate limit reply values")
	}
	return ratelimiter.Result{Allowed: allowed == 1, Remaining: int(remaining), ResetAt: time.UnixMilli(reset).UTC(), RetryAfter: time.Duration(retry) * time.Millisecond}, nil
}

// Reset removes compatible state for a nonempty key. Legacy collisions are
// errors and remain untouched; an absent key succeeds.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("goredis: rate limit key is empty: %w", sdk.ErrInvalidInput)
	}
	raw, err := limiterResetScript.Run(ctx, l.rdb, []string{l.keyPrefix + key}, ratelimiter.MaxWindow.Milliseconds()).Result()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("goredis: resetting rate limit: %w", err)
	}
	count, ok := raw.(int64)
	if !ok || count < -1 || count > 1 {
		return fmt.Errorf("goredis: invalid rate limit reset reply")
	}
	if count == -1 {
		return fmt.Errorf("goredis: incompatible rate limit state: %w", sdk.ErrConflict)
	}
	return nil
}
