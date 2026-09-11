package ratelimiter

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestLimitNormalize(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   Limit
		want    time.Duration
		invalid bool
	}{
		{"zero", Limit{}, 0, true}, {"negative requests", PerMinute(-1), 0, true},
		{"negative burst", PerMinute(2).WithBurst(-1), 0, true},
		{"overflow", Limit{Requests: math.MaxInt, Burst: 1, Window: time.Millisecond}, 0, true},
		{"ceiling overflow", PerMinute(MaxCeiling).WithBurst(1), 0, true},
		{"zero window", Limit{Requests: 1}, 0, true},
		{"negative window", Limit{Requests: 1, Window: -1}, 0, true},
		{"round overflow", Limit{Requests: 1, Window: time.Duration(math.MaxInt64)}, 0, true},
		{"product overflow", Limit{Requests: MaxCeiling, Window: MaxWindow}, 0, true},
		{"nanosecond", Limit{Requests: 1, Window: 1}, time.Millisecond, false},
		{"fractional", Limit{Requests: 1, Window: time.Millisecond + 1}, 2 * time.Millisecond, false},
		{"maximum window", Limit{Requests: 1, Window: MaxWindow}, MaxWindow, false},
		{"maximum ceiling", Limit{Requests: MaxCeiling, Window: time.Millisecond}, time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.limit.Normalize()
			if tc.invalid {
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("%+v: %v", got, err)
				}
				return
			}
			if err != nil || got.Window != tc.want {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestMemoryAnchoredWindowAndCeilingChanges(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1_000_000)
	m := NewMemory()
	m.now = func() time.Time { return now }
	limit := Limit{Requests: 4, Window: time.Second}
	for i := 0; i < 4; i++ {
		r, e := m.Allow(ctx, "k", limit)
		if e != nil || !r.Allowed {
			t.Fatal(r, e)
		}
	}
	now = now.Add(1500 * time.Millisecond)
	// Halfway through the next anchored bucket, two old requests still count.
	r, e := m.Allow(ctx, "k", limit)
	if e != nil || !r.Allowed || r.Remaining != 1 || !r.ResetAt.Equal(time.UnixMilli(1_002_000)) {
		t.Fatal(r, e)
	}
	// A lower ceiling preserves both current and decaying previous counts.
	r, e = m.Allow(ctx, "k", Limit{Requests: 2, Window: time.Second})
	if e != nil || r.Allowed || r.RetryAfter != 500*time.Millisecond {
		t.Fatal(r, e)
	}
	// A live window change fails without consuming/resetting quota.
	_, e = m.Allow(ctx, "k", PerMinute(4))
	if !errors.Is(e, sdk.ErrInvalidInput) {
		t.Fatal(e)
	}
	r, e = m.Allow(ctx, "k", limit)
	if e != nil || !r.Allowed || r.Remaining != 0 {
		t.Fatal(r, e)
	}
}

func TestMemoryExactBoundaryAndClockRollback(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1_000_000)
	m := NewMemory()
	m.now = func() time.Time { return now }
	limit := Limit{Requests: 1, Window: time.Second}
	m.Allow(ctx, "k", limit)
	now = now.Add(time.Second)
	r, e := m.Allow(ctx, "k", limit)
	if e != nil || r.Allowed || r.RetryAfter != time.Second || !r.ResetAt.Equal(time.UnixMilli(1_002_000)) {
		t.Fatal(r, e)
	}
	now = now.Add(-500 * time.Millisecond)
	r, e = m.Allow(ctx, "k", limit)
	if e != nil || r.Allowed || r.RetryAfter != time.Second {
		t.Fatal(r, e)
	}
}

func TestMemoryCapacityKeepsActiveBudgets(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1_000_000)
	m := NewMemory(WithMaxEntries(2), WithMaxEntries(1))
	m.now = func() time.Time { return now }
	limit := Limit{Requests: 1, Window: time.Second}
	m.Allow(ctx, "existing", limit)
	_, err := m.Allow(ctx, "new", limit)
	if !errors.Is(err, ErrCapacity) || !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatal(err)
	}
	r, err := m.Allow(ctx, "existing", limit)
	if err != nil || r.Allowed {
		t.Fatalf("active budget lost: %+v %v", r, err)
	}
	now = now.Add(3 * time.Second)
	r, err = m.Allow(ctx, "new", limit)
	if err != nil || !r.Allowed || len(m.windows) != 1 {
		t.Fatalf("expiry reclamation: %+v %v entries=%d", r, err, len(m.windows))
	}
}

func TestMemoryExpiredKeyAcceptsNewWindowAndLargeEndpoints(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1_000_000)
	m := NewMemory()
	m.now = func() time.Time { return now }
	m.Allow(ctx, "k", Limit{Requests: 1, Window: time.Millisecond})
	now = now.Add(2 * time.Millisecond)
	r, e := m.Allow(ctx, "k", PerHour(1))
	if e != nil || !r.Allowed {
		t.Fatal(r, e)
	}
	r, e = m.Allow(ctx, "large", Limit{Requests: 1, Window: MaxWindow})
	if e != nil || !r.Allowed || !r.ResetAt.Equal(now.Add(MaxWindow)) {
		t.Fatal(r, e)
	}
	r, e = m.Allow(ctx, "large", Limit{Requests: 1, Window: MaxWindow})
	if e != nil || r.Allowed || r.RetryAfter != MaxWindow {
		t.Fatal(r, e)
	}
}

func TestMemoryRejectsNilOption(t *testing.T) {
	defer func() {
		if got := recover(); got != "ratelimiter.NewMemory: nil option" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewMemory(nil)
}
