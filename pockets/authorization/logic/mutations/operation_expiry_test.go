package mutations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

type stubExpirer struct {
	before time.Time
	limit  int
	calls  int
}

func (e *stubExpirer) Expire(_ context.Context, before time.Time, limit int) (int, error) {
	e.calls++
	e.before, e.limit = before, limit
	return 3, nil
}

func TestExpireOperationsCutoffIsNowMinusRetention(t *testing.T) {
	for _, tc := range []struct {
		name      string
		opts      []Option
		retention time.Duration
	}{
		{"default", nil, defaultOperationRetention},
		{"minimum", []Option{WithOperationRetention(7 * 24 * time.Hour)}, 7 * 24 * time.Hour},
		{"longer", []Option{WithOperationRetention(400 * 24 * time.Hour)}, 400 * 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expirer := &stubExpirer{}
			svc, err := NewService(&stubMutationRepo{}, append([]Option{WithOperationExpiry(expirer)}, tc.opts...)...)
			if err != nil {
				t.Fatal(err)
			}
			lower := time.Now().Add(-tc.retention)
			n, err := svc.ExpireOperations(t.Context(), 25)
			upper := time.Now().Add(-tc.retention)
			if err != nil || n != 3 || expirer.calls != 1 || expirer.limit != 25 {
				t.Fatalf("ExpireOperations = %d %v; expirer saw %d calls, limit %d", n, err, expirer.calls, expirer.limit)
			}
			if expirer.before.Before(lower) || expirer.before.After(upper) {
				t.Fatalf("cutoff %v outside [%v, %v]", expirer.before, lower, upper)
			}
		})
	}
	if defaultOperationRetention != 180*24*time.Hour || minOperationRetention != 7*24*time.Hour {
		t.Fatalf("retention defaults drifted: default %s, minimum %s", defaultOperationRetention, minOperationRetention)
	}
}

func TestExpireOperationsRefusesBeforeCallingTheExpirer(t *testing.T) {
	expirer := &stubExpirer{}
	svc, err := NewService(&stubMutationRepo{}, WithOperationExpiry(expirer))
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, -1} {
		if n, err := svc.ExpireOperations(t.Context(), limit); n != 0 || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("limit %d: %d %v", limit, n, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.ExpireOperations(ctx, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
	if expirer.calls != 0 {
		t.Fatalf("a refused call reached the expirer %d times", expirer.calls)
	}
	unconfigured, err := NewService(&stubMutationRepo{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unconfigured.ExpireOperations(t.Context(), 10); !errors.Is(err, ErrOperationExpiryNotConfigured) {
		t.Fatalf("without an expirer: %v", err)
	}
}

func TestOperationRetentionConstruction(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
		want error
	}{
		{"below the minimum", []Option{WithOperationExpiry(&stubExpirer{}), WithOperationRetention(minOperationRetention - time.Nanosecond)}, ErrOperationRetentionTooShort},
		{"explicit zero", []Option{WithOperationExpiry(&stubExpirer{}), WithOperationRetention(0)}, ErrOperationRetentionTooShort},
		{"negative", []Option{WithOperationExpiry(&stubExpirer{}), WithOperationRetention(-time.Hour)}, ErrOperationRetentionTooShort},
		{"without an expirer", []Option{WithOperationRetention(30 * 24 * time.Hour)}, ErrOperationRetentionWithoutExpirer},
		{"typed-nil expirer", []Option{WithOperationExpiry((*stubExpirer)(nil))}, sdk.ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if svc, err := NewService(&stubMutationRepo{}, tc.opts...); svc != nil || !errors.Is(err, tc.want) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("NewService = %v %v, want %v", svc, err, tc.want)
			}
		})
	}
}
