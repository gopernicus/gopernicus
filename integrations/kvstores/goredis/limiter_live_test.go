package goredis

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/redis/go-redis/v9"
)

func openLimiterRedis(t *testing.T) (*redis.Client, context.Context) {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set — live rate limiter behavior not verified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	rdb, err := Open(ctx, Config{Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, ctx
}

func TestLive_LimiterAnchoredBuckets(t *testing.T) {
	rdb, ctx := openLimiterRedis(t)
	now, err := rdb.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	// A future updated time freezes each decision through the required clock
	// rollback clamp. This pins exact boundaries without replacing server TIME.
	start := now.Add(time.Hour).UnixMilli()
	for _, tc := range []struct {
		name                 string
		elapsed, count, prev int64
		allowed              bool
		wantCount, wantPrev  int64
		advance, retry       int64
	}{
		{"exact boundary", 1000, 2, 0, false, 0, 2, 1000, 1000},
		{"weighted midpoint", 1500, 2, 0, true, 1, 2, 1000, 0},
		{"denial keeps anchor", 250, 1, 2, false, 1, 2, 0, 750},
		{"expiry clears both", 2000, 2, 2, true, 1, 0, 2000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := fmt.Sprintf("rlstate:%d:%s:", time.Now().UnixNano(), tc.name)
			key := prefix + limiterKeyVersion + "key"
			t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })
			if err := rdb.HSet(ctx, key, "count", tc.count, "prev_count", tc.prev,
				"window_start", start, "updated_at", start+tc.elapsed,
				"expires_at", start+2000, "window_ms", 1000).Err(); err != nil {
				t.Fatal(err)
			}
			l := NewLimiter(rdb, WithLimiterKeyPrefix(prefix))
			got, err := l.Allow(ctx, "key", ratelimiter.Limit{Requests: 2, Window: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			remaining := 0
			if tc.elapsed == 2000 {
				remaining = 1
			}
			want := ratelimiter.Result{Allowed: tc.allowed, Remaining: remaining,
				ResetAt: time.UnixMilli(start + tc.advance + 1000).UTC(), RetryAfter: time.Duration(tc.retry) * time.Millisecond}
			if got != want {
				t.Fatalf("decision = %+v, want %+v", got, want)
			}
			state, err := rdb.HGetAll(ctx, key).Result()
			if err != nil {
				t.Fatal(err)
			}
			wantState := map[string]int64{"count": tc.wantCount, "prev_count": tc.wantPrev,
				"window_start": start + tc.advance, "updated_at": start + tc.elapsed,
				"expires_at": start + tc.advance + 2000, "window_ms": 1000}
			for field, value := range wantState {
				if state[field] != strconv.FormatInt(value, 10) {
					t.Errorf("%s = %s, want %d", field, state[field], value)
				}
			}
			expires, err := rdb.PExpireTime(ctx, key).Result()
			if err != nil || expires != time.Duration(wantState["expires_at"])*time.Millisecond {
				t.Errorf("physical expiry = %v, %v; want epoch ms %d", expires, err, wantState["expires_at"])
			}
		})
	}
}

func TestLive_LimiterPolicyChangesPreserveCounts(t *testing.T) {
	rdb, ctx := openLimiterRedis(t)
	prefix := fmt.Sprintf("rlpolicy:%d:", time.Now().UnixNano())
	key := prefix + limiterKeyVersion + "key"
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })
	l := NewLimiter(rdb, WithLimiterKeyPrefix(prefix))
	if got, err := l.Allow(ctx, "key", ratelimiter.PerMinute(1)); err != nil || !got.Allowed {
		t.Fatalf("initial Allow = %+v, %v", got, err)
	}
	before, err := rdb.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Allow(ctx, "key", ratelimiter.PerSecond(1)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("active window change = %v", err)
	}
	after, err := rdb.HGetAll(ctx, key).Result()
	if err != nil || !maps.Equal(before, after) {
		t.Fatalf("invalid policy changed state: before %v, after %v, err %v", before, after, err)
	}
	if got, err := l.Allow(ctx, "key", ratelimiter.PerMinute(2)); err != nil || !got.Allowed || got.Remaining != 0 {
		t.Fatalf("raised ceiling = %+v, %v; want retained count plus one", got, err)
	}
	if got, err := l.Allow(ctx, "key", ratelimiter.PerMinute(1)); err != nil || got.Allowed {
		t.Fatalf("lowered ceiling = %+v, %v; want denial", got, err)
	}
	// Leave the expired hash physically present. Expiry and pruning must have
	// identical admission semantics, including accepting a new window.
	if err := rdb.HSet(ctx, key, "expires_at", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Allow(ctx, "key", ratelimiter.PerSecond(1)); err != nil || !got.Allowed {
		t.Fatalf("expired window change = %+v, %v", got, err)
	}
}

func TestLive_LimiterLegacyKeyCollisionIsPreserved(t *testing.T) {
	rdb, ctx := openLimiterRedis(t)
	prefix := fmt.Sprintf("rllegacy:%d:", time.Now().UnixNano())
	// This was a valid old caller key. It collides with the new internal v2:
	// suffix; versioning alone cannot distinguish arbitrary logical keys.
	physical := prefix + "v2:key"
	t.Cleanup(func() { _ = rdb.Del(context.Background(), physical).Err() })
	for _, window := range []any{nil, 0, -1, "1.5", "not-a-number"} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			if err := rdb.Del(ctx, physical).Err(); err != nil {
				t.Fatal(err)
			}
			if err := rdb.HSet(ctx, physical, "count", 7, "prev_count", 2,
				"window_start", 1234567890123456789, "expires_at", 1).Err(); err != nil {
				t.Fatal(err)
			}
			if window != nil {
				if err := rdb.HSet(ctx, physical, "window_ms", window).Err(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := rdb.HGetAll(ctx, physical).Result()
			if err != nil {
				t.Fatal(err)
			}
			l := NewLimiter(rdb, WithLimiterKeyPrefix(prefix))
			if _, err := l.Allow(ctx, "key", ratelimiter.PerMinute(1)); !errors.Is(err, sdk.ErrConflict) {
				t.Errorf("Allow collision = %v, want conflict", err)
			}
			if err := l.Reset(ctx, "key"); !errors.Is(err, sdk.ErrConflict) {
				t.Errorf("Reset collision = %v, want conflict", err)
			}
			after, err := rdb.HGetAll(ctx, physical).Result()
			if err != nil || !maps.Equal(before, after) {
				t.Fatalf("legacy state changed: before %v, after %v, err %v", before, after, err)
			}
		})
	}
}
