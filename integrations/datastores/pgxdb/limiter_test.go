// These tests are hermetic: they exercise construction, option handling, and the
// pre-flight guards that run before any statement is issued (so a nil *DB is
// never dereferenced). The live ratelimiter.Limiter contract — conformance,
// atomic admission, server time — is verified by limiter_live_test.go under
// POSTGRES_TEST_DSN.
package pgxdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

func TestLimiterAppliesDefaultKeyPrefix(t *testing.T) {
	l := NewLimiter(nil)
	if l.keyPrefix != defaultLimiterKeyPrefix+limiterKeyVersion {
		t.Errorf("keyPrefix = %q, want %q", l.keyPrefix, defaultLimiterKeyPrefix+limiterKeyVersion)
	}
}

func TestLimiterKeyPrefixOption(t *testing.T) {
	l := NewLimiter(nil, WithLimiterKeyPrefix("api:"))
	if l.keyPrefix != "api:v2:" {
		t.Errorf("keyPrefix = %q, want %q", l.keyPrefix, "api:v2:")
	}
}

func TestLimiterPortSatisfaction(t *testing.T) {
	var _ ratelimiter.Limiter = (*Limiter)(nil)
}

// A limiter with no usable connection must fail its boot probe rather than
// report healthy. StatusCheck catches missing dependencies before traffic
// reaches the host's configured middleware failure policy.
func TestLimiterStatusCheckWithoutDatabase(t *testing.T) {
	err := NewLimiter(nil).StatusCheck(context.Background())
	if err == nil {
		t.Fatal("StatusCheck() with a nil *DB error = nil, want non-nil")
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("StatusCheck() with a nil *DB error = %v, want sdk.ErrInvalidInput", err)
	}
}

// The boot path must refuse an unreachable database somewhere. Today Open's own
// ping fails first (nothing listens on loopback port 1), so StatusCheck is not
// even reached; if Open ever becomes lazy about connecting, the probe is the next
// gate and must fail instead. The one outcome this forbids is both succeeding.
func TestLimiterBootRefusesUnreachableDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := Open(context.Background(), Config{DSN: "postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable", ConnectTimeout: 2 * time.Second})
	if err != nil {
		return
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := NewLimiter(db).StatusCheck(ctx); err == nil {
		t.Error("Open and StatusCheck both succeeded against an unreachable database, want a boot failure from one of them")
	}
}

func TestLimiterAllowRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewLimiter(nil).Allow(ctx, "k", ratelimiter.PerMinute(1)); !errors.Is(err, context.Canceled) {
		t.Errorf("Allow() with canceled ctx error = %v, want context.Canceled", err)
	}
}

func TestLimiterResetRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := NewLimiter(nil).Reset(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Reset() with canceled ctx error = %v, want context.Canceled", err)
	}
}

func TestLimiterAllowRejectsNonPositiveWindow(t *testing.T) {
	for _, window := range []time.Duration{0, -time.Second} {
		_, err := NewLimiter(nil).Allow(context.Background(), "k", ratelimiter.Limit{Requests: 1, Window: window})
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("Allow() with window %v error = %v, want sdk.ErrInvalidInput", window, err)
		}
	}
}

// A zero (or negative) ceiling is a misconfiguration — a limiter that admits
// nothing on a login path. It must be a loud config error, never a silent
// deny-all that looks like normal throttling in production.
func TestLimiterAllowRejectsNonPositiveCeiling(t *testing.T) {
	limits := []ratelimiter.Limit{
		{Requests: 0, Window: time.Minute},
		{Requests: 0, Burst: 0, Window: time.Minute},
		{Requests: -5, Burst: 5, Window: time.Minute},
		{Requests: 1, Burst: -3, Window: time.Minute},
	}
	for _, limit := range limits {
		_, err := NewLimiter(nil).Allow(context.Background(), "k", limit)
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("Allow() with %+v error = %v, want sdk.ErrInvalidInput", limit, err)
		}
	}
}

func TestLimiterEmptyKeysAndNormalizedBounds(t *testing.T) {
	l := NewLimiter(nil)
	if _, err := l.Allow(context.Background(), "", ratelimiter.PerSecond(1)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if err := l.Reset(context.Background(), ""); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	for _, limit := range []ratelimiter.Limit{{Requests: 1, Window: ratelimiter.MaxWindow + 1}, {Requests: ratelimiter.MaxCeiling, Window: time.Hour}} {
		if _, err := l.Allow(context.Background(), "key", limit); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("limit %+v: %v", limit, err)
		}
	}
	for _, prefix := range []string{"", "custom:", "custom:v2:"} {
		if got := NewLimiter(nil, WithLimiterKeyPrefix(prefix)).keyPrefix; got != prefix+"v2:" {
			t.Fatal(got)
		}
	}
}
