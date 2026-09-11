package goredis

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/redis/go-redis/v9"
)

func TestLimiterKeyPrefixes(t *testing.T) {
	if got := NewLimiter(nil).keyPrefix; got != "ratelimit:v2:" {
		t.Fatal(got)
	}
	for _, prefix := range []string{"api:", "", "already:v2:"} {
		if got := NewLimiter(nil, WithLimiterKeyPrefix(prefix)).keyPrefix; got != prefix+"v2:" {
			t.Fatal(got)
		}
	}
}

func TestLimiterInvalidInputsDoNotCallRedis(t *testing.T) {
	l := NewLimiter(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Allow(ctx, "k", ratelimiter.PerSecond(1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := l.Reset(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := l.Allow(context.Background(), "", ratelimiter.PerSecond(1)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if err := l.Reset(context.Background(), ""); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	for _, limit := range []ratelimiter.Limit{{Requests: 0, Window: time.Second}, {Requests: 1, Window: 0}, {Requests: 1, Window: time.Second, Burst: -1}, {Requests: 1, Window: time.Duration(math.MaxInt64)}, {Requests: ratelimiter.MaxCeiling, Window: time.Hour}} {
		if _, err := l.Allow(context.Background(), "k", limit); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("limit %+v: %v", limit, err)
		}
	}
}

func TestLimiterScriptRunFallbackAndRelativeRetry(t *testing.T) {
	rdb := dummyClient()
	t.Cleanup(func() { rdb.Close() })
	commands := []string{}
	rdb.AddHook(cacheCommandHook{run: func(cmd redis.Cmder) error {
		commands = append(commands, cmd.Name())
		if cmd.Name() == "evalsha" {
			return limiterRedisError("NOSCRIPT No matching script")
		}
		if cmd.Name() != "eval" {
			t.Fatalf("unexpected command %s", cmd.Name())
		}
		args := cmd.Args()
		if args[3] != "host:v2:k" || args[4] != int64(2) || args[5] != 3 {
			t.Fatalf("key/normalization args=%#v", args)
		}
		cmd.(*redis.Cmd).SetVal([]any{int64(0), int64(0), int64(1), int64(2)})
		return nil
	}})
	l := NewLimiter(rdb, WithLimiterKeyPrefix("host:"))
	result, err := l.Allow(context.Background(), "k", ratelimiter.Limit{Requests: 2, Burst: 1, Window: time.Millisecond + 1})
	if err != nil || result.Allowed || result.RetryAfter != 2*time.Millisecond || !result.ResetAt.Equal(time.UnixMilli(1)) {
		t.Fatalf("decision=%+v,%v", result, err)
	}
	if !reflect.DeepEqual(commands, []string{"evalsha", "eval"}) {
		t.Fatal(commands)
	}
}

func TestLimiterStrictReply(t *testing.T) {
	limit := ratelimiter.PerSecond(2)
	good := []any{int64(1), int64(1), int64(1000), int64(0)}
	if _, err := decodeLimiterResult(good, limit); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []any{nil, []any{1, 1, 1000, 0}, []any{int64(1), float64(1.5), int64(1000), int64(0)}, []any{int64(2), int64(0), int64(1000), int64(0)}, []any{int64(1), int64(2), int64(1000), int64(0)}, []any{int64(0), int64(0), int64(1000), int64(0)}, []any{int64(0), int64(0), int64(1000), int64(1001)}, []any{int64(1), int64(0), int64(0), int64(0)}} {
		if _, err := decodeLimiterResult(raw, limit); err == nil {
			t.Fatalf("accepted malformed reply %#v", raw)
		}
	}
	for marker, want := range map[int64]error{-1: sdk.ErrInvalidInput, -2: sdk.ErrConflict} {
		if _, err := decodeLimiterResult([]any{marker, int64(0), int64(0), int64(0)}, limit); !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
}

func TestLimiterCancellationAfterSuccess(t *testing.T) {
	for _, operation := range []string{"allow", "reset"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rdb := dummyClient()
			t.Cleanup(func() { rdb.Close() })
			rdb.AddHook(cacheCommandHook{run: func(cmd redis.Cmder) error {
				if operation == "allow" {
					cmd.(*redis.Cmd).SetVal([]any{int64(1), int64(0), int64(1000), int64(0)})
				} else {
					cmd.(*redis.Cmd).SetVal(int64(1))
				}
				cancel()
				return nil
			}})
			l := NewLimiter(rdb)
			var err error
			if operation == "allow" {
				_, err = l.Allow(ctx, "key", ratelimiter.PerSecond(1))
			} else {
				err = l.Reset(ctx, "key")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("success hid cancellation: %v", err)
			}
		})
	}
}

type limiterRedisError string

func (e limiterRedisError) Error() string { return string(e) }
func (limiterRedisError) RedisError()     {}
