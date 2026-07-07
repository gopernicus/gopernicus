// These tests are hermetic: they exercise Config defaulting via the env tags,
// ClientOption wiring, and Open's fail-fast path against an unreachable address.
// The live Open path is exercised by conformance_test.go under REDIS_TEST_ADDR.
package goredis

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/environment"
	"github.com/gopernicus/gopernicus/sdk/tracing"
)

// TestConfigDefaultsFromTags proves the `default:` struct tags populate a zero
// Config through sdk/environment.ParseEnvTags — the host-facing convenience path.
func TestConfigDefaultsFromTags(t *testing.T) {
	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags() error = %v", err)
	}

	if cfg.Addr != defaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, defaultAddr)
	}
	if cfg.MaxRetries != defaultMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, defaultMaxRetries)
	}
	if cfg.DialTimeout != defaultDialTimeout {
		t.Errorf("DialTimeout = %v, want %v", cfg.DialTimeout, defaultDialTimeout)
	}
	if cfg.ReadTimeout != defaultReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", cfg.ReadTimeout, defaultReadTimeout)
	}
	if cfg.WriteTimeout != defaultWriteTimeout {
		t.Errorf("WriteTimeout = %v, want %v", cfg.WriteTimeout, defaultWriteTimeout)
	}
	if cfg.PoolSize != defaultPoolSize {
		t.Errorf("PoolSize = %d, want %d", cfg.PoolSize, defaultPoolSize)
	}
	if cfg.MinIdleConns != defaultMinIdleConns {
		t.Errorf("MinIdleConns = %d, want %d", cfg.MinIdleConns, defaultMinIdleConns)
	}
}

// TestConfigEnvOverride proves an env var overrides the default tag value.
func TestConfigEnvOverride(t *testing.T) {
	t.Setenv("REDIS_ADDR", "redis.internal:6380")
	t.Setenv("REDIS_DB", "3")

	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags() error = %v", err)
	}

	if cfg.Addr != "redis.internal:6380" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, "redis.internal:6380")
	}
	if cfg.DB != 3 {
		t.Errorf("DB = %d, want 3", cfg.DB)
	}
}

// TestClientOptionsInstallHooks proves WithLogging/WithTracing each append a hook
// of the expected concrete type to the clientOptions Open threads into AddHook.
func TestClientOptionsInstallHooks(t *testing.T) {
	var co clientOptions
	WithLogging(nil)(&co)
	WithTracing(nil)(&co)

	if len(co.hooks) != 2 {
		t.Fatalf("hooks len = %d, want 2", len(co.hooks))
	}
	if _, ok := co.hooks[0].(*loggingHook); !ok {
		t.Errorf("hook[0] type = %T, want *loggingHook", co.hooks[0])
	}
	if _, ok := co.hooks[1].(*tracingHook); !ok {
		t.Errorf("hook[1] type = %T, want *tracingHook", co.hooks[1])
	}
}

// TestWithLoggingThreshold proves the LoggingOption reaches the installed hook.
func TestWithLoggingThreshold(t *testing.T) {
	var co clientOptions
	WithLogging(nil, WithSlowThreshold(42*time.Millisecond))(&co)

	h, ok := co.hooks[0].(*loggingHook)
	if !ok {
		t.Fatalf("hook[0] type = %T, want *loggingHook", co.hooks[0])
	}
	if h.slowThreshold != 42*time.Millisecond {
		t.Errorf("slowThreshold = %v, want 42ms", h.slowThreshold)
	}
}

// TestWithTracingNilUsesNoop proves a nil tracer is replaced by tracing.Noop so
// installation is always safe.
func TestWithTracingNilUsesNoop(t *testing.T) {
	var co clientOptions
	WithTracing(nil)(&co)

	h, ok := co.hooks[0].(*tracingHook)
	if !ok {
		t.Fatalf("hook[0] type = %T, want *tracingHook", co.hooks[0])
	}
	if _, ok := h.tracer.(tracing.Noop); !ok {
		t.Errorf("tracer type = %T, want tracing.Noop", h.tracer)
	}
}

// TestOpenFailsFastOnUnreachableAddr proves Open's construction-time PING returns
// an error (rather than a live client) when the server is unreachable and the
// ctx deadline elapses. No Redis is required: 192.0.2.1 is RFC 5737 TEST-NET-1,
// which is guaranteed unroutable, so the dial is bounded by the short ctx.
func TestOpenFailsFastOnUnreachableAddr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	rdb, err := Open(ctx, Config{
		Addr:        "192.0.2.1:6379",
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
	if err == nil {
		_ = rdb.Close()
		t.Fatal("Open() error = nil, want a fail-fast ping error")
	}
	if rdb != nil {
		t.Errorf("Open() client = %v, want nil on failure", rdb)
	}
}

// TestStatusCheckFailsOnUnreachableAddr proves StatusCheck surfaces a ping error
// (rather than nil) when Redis is unreachable. Hermetic: 192.0.2.1 is RFC 5737
// TEST-NET-1, guaranteed unroutable, and the short client DialTimeout bounds the
// dial so the check returns without a live Redis.
func TestStatusCheckFailsOnUnreachableAddr(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:        "192.0.2.1:6379",
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	if err := StatusCheck(ctx, rdb); err == nil {
		t.Fatal("StatusCheck() error = nil, want a ping error on an unreachable address")
	}
}
