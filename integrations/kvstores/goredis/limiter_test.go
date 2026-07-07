// These tests are hermetic: they exercise construction, option handling, and the
// Lua-reply coercion helper without any Redis connection. The live
// ratelimiter.Limiter contract is verified by conformance_test.go
// (ratelimitertest.Run) under REDIS_TEST_ADDR.
package goredis

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

func TestLimiterAppliesDefaultKeyPrefix(t *testing.T) {
	l := NewLimiter(dummyClient())
	if l.keyPrefix != defaultLimiterKeyPrefix {
		t.Errorf("keyPrefix = %q, want %q", l.keyPrefix, defaultLimiterKeyPrefix)
	}
}

func TestLimiterKeyPrefixOption(t *testing.T) {
	l := NewLimiter(dummyClient(), WithLimiterKeyPrefix("api:"))
	if l.keyPrefix != "api:" {
		t.Errorf("keyPrefix = %q, want %q", l.keyPrefix, "api:")
	}
}

func TestLimiterCloseIsIdempotentWithoutRedis(t *testing.T) {
	l := NewLimiter(dummyClient())
	if err := l.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func TestLimiterPortSatisfaction(t *testing.T) {
	var _ ratelimiter.Limiter = (*Limiter)(nil)
}

func TestToInt64(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int64
	}{
		{"int64", int64(7), 7},
		{"int", 7, 7},
		{"float64", float64(7), 7},
		{"string", "7", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toInt64(tc.in)
			if err != nil {
				t.Fatalf("toInt64(%v) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("toInt64(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}

	if _, err := toInt64(struct{}{}); err == nil {
		t.Error("toInt64(unsupported) error = nil, want non-nil")
	}
}
