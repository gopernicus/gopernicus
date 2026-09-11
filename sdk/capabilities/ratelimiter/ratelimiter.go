// Package ratelimiter provides keyed admission, HTTP rejection and blocking
// worker waits. Hosts choose budgets, key scopes and dependency failure policy.
package ratelimiter

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// MaxCeiling is the largest Requests+Burst supported on every Go architecture.
	MaxCeiling = 1<<31 - 1
	// MaxWindow is the largest whole millisecond representable by time.Duration.
	MaxWindow = time.Duration((1<<63-1)/int64(time.Millisecond)) * time.Millisecond
	// Keep weighted-count multiplication and division exact across Go, SQL and
	// Redis Lua numbers. This is an arithmetic bound, not an application rate cap.
	maxWindowProduct int64 = 1<<52 - 1
)

// Allower atomically checks a nonempty key and consumes one unit only if allowed.
// Caller cancellation and malformed limits return errors; a denial is a Result.
// Implementations must not use application wall time to derive remote retry waits.
type Allower interface {
	Allow(context.Context, string, Limit) (Result, error)
}

// Limiter adds administrative reset to admission. Underlying client/pool lifetime
// belongs to the host. Reset of an absent key succeeds; empty keys are invalid.
type Limiter interface {
	Allower
	Reset(context.Context, string) error
}

// Limit describes a two-window counter approximation. The effective ceiling is
// Requests+Burst; Burst is extra allowance, not a continuously refilling bucket.
// Bundled implementations use anchored windows and decay the preceding count by
// the fraction of this window remaining. This is not an exact rolling event log.
//
// A live key's normalized Window cannot change without Reset or expiration; use
// a new policy key for independent windows. Ceiling changes retain consumed quota.
// Expired state is absent even before physical collection. Reset discards quota.
type Limit struct {
	Requests int
	Window   time.Duration
	Burst    int
}

// Normalize validates the limit and rounds its window up to whole milliseconds.
// Requests and Window must be positive; Burst must be nonnegative. The ceiling
// must fit MaxCeiling, and ceiling*windowMilliseconds must not exceed 2^52-1.
// An overflow or unsupported numerical range matches sdk.ErrInvalidInput.
func (l Limit) Normalize() (Limit, error) {
	if l.Requests <= 0 || l.Burst < 0 || l.Burst > MaxCeiling || l.Requests > MaxCeiling-l.Burst {
		return Limit{}, fmt.Errorf("ratelimiter: positive requests and nonnegative burst must total at most %d: %w", MaxCeiling, sdk.ErrInvalidInput)
	}
	if l.Window <= 0 || l.Window > MaxWindow {
		return Limit{}, fmt.Errorf("ratelimiter: window must be positive and at most %s: %w", MaxWindow, sdk.ErrInvalidInput)
	}
	millis := int64(l.Window / time.Millisecond)
	if l.Window%time.Millisecond != 0 {
		millis++
	}
	if int64(l.Requests+l.Burst) > maxWindowProduct/millis {
		return Limit{}, fmt.Errorf("ratelimiter: ceiling times window milliseconds exceeds exact arithmetic range: %w", sdk.ErrInvalidInput)
	}
	l.Window = time.Duration(millis) * time.Millisecond
	return l, nil
}

// Result reports admission and the current bucket's end. RetryAfter is a backend-
// computed duration to that checkpoint on denial, not a reservation or a promise
// of the earliest possible admission. It is zero when allowed. Remaining is
// nonnegative. A caller's clock need not agree with a remote ResetAt timestamp.
type Result struct {
	Allowed    bool
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration
}

func PerSecond(n int) Limit           { return Limit{Requests: n, Window: time.Second} }
func PerMinute(n int) Limit           { return Limit{Requests: n, Window: time.Minute} }
func PerHour(n int) Limit             { return Limit{Requests: n, Window: time.Hour} }
func (l Limit) WithBurst(n int) Limit { l.Burst = n; return l }

func checkKey(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("ratelimiter: key must not be empty: %w", sdk.ErrInvalidInput)
	}
	return nil
}
