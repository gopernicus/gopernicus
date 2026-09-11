package goredis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/redis/go-redis/v9"
)

func TestConstructorsRejectNilOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		construct func()
		panic     string
	}{
		{"bus", func() { New(nil, nil) }, "goredis: nil BusOption"},
		{"cache", func() { NewCacher(nil, nil) }, "goredis: nil CacheOption"},
		{"limiter", func() { NewLimiter(nil, nil) }, "goredis: nil LimiterOption"},
		{"logging", func() { LoggingHook(nil, nil) }, "goredis: nil LoggingOption"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v, want %s", got, tc.panic)
				}
			}()
			tc.construct()
		})
	}
	for _, opts := range [][]ClientOption{{nil}, {WithLogging(nil, nil)}} {
		client, err := Open(context.Background(), Config{}, opts...)
		if client != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Open invalid options = %v, %v", client, err)
		}
	}
}

func TestLoggingOptionsSnapshotAndAppend(t *testing.T) {
	opts := []LoggingOption{WithSlowThreshold(time.Second)}
	option := WithLogging(nil, opts...)
	opts[0] = nil
	var first, second clientOptions
	for _, cfg := range []*clientOptions{&first, &second} {
		option(cfg)
		WithTracing(nil)(cfg)
		WithLogging(nil, WithSlowThreshold(2*time.Second))(cfg)
		if cfg.err != nil || len(cfg.hooks) != 3 {
			t.Fatalf("hooks = %d, err = %v", len(cfg.hooks), cfg.err)
		}
		if got := cfg.hooks[0].(*loggingHook).slowThreshold; got != time.Second {
			t.Fatalf("captured threshold = %v", got)
		}
		if _, ok := cfg.hooks[1].(*tracingHook); !ok {
			t.Fatalf("middle hook = %T", cfg.hooks[1])
		}
		if got := cfg.hooks[2].(*loggingHook).slowThreshold; got != 2*time.Second {
			t.Fatalf("appended threshold = %v", got)
		}
	}
	first.hooks[0] = nil
	if second.hooks[0] == nil {
		t.Fatal("construction reused hook slice storage")
	}
}

func TestCacheAndLimiterOptionReusePreservesNamespaces(t *testing.T) {
	rdb := dummyClient()
	t.Cleanup(func() { _ = rdb.Close() })
	var keys []string
	rdb.AddHook(cacheCommandHook{run: func(cmd redis.Cmder) error { keys = append(keys, cmd.Args()[1].(string)); return nil }})
	cacheOption := WithCacheKeyPrefix("")
	limiterOption := WithLimiterKeyPrefix("")
	for range 2 {
		cache := NewCacher(rdb, WithCacheKeyPrefix("ignored:"), cacheOption)
		if err := cache.Set(context.Background(), "key", nil, 0); err != nil {
			t.Fatal(err)
		}
		limiter := NewLimiter(rdb, WithLimiterKeyPrefix("ignored:"), limiterOption)
		if limiter.keyPrefix != "v2:" {
			t.Fatalf("limiter prefix = %q", limiter.keyPrefix)
		}
	}
	if len(keys) != 2 || keys[0] != "key" || keys[1] != "key" {
		t.Fatalf("physical cache keys = %v", keys)
	}
}
