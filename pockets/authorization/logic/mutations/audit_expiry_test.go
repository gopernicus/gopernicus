package mutations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

type stubAuditExpirer struct {
	before time.Time
	limit  int
	calls  int
}

func (e *stubAuditExpirer) Expire(_ context.Context, before time.Time, limit int) (int, error) {
	e.calls++
	e.before, e.limit = before, limit
	return 4, nil
}

func TestExpireAuditCutoffIsNowMinusRetention(t *testing.T) {
	for _, retention := range []time.Duration{MinAuditRetention, 7 * 365 * 24 * time.Hour} {
		expirer := &stubAuditExpirer{}
		svc, err := NewService(&stubMutationRepo{}, WithAuditExpiry(expirer), WithAuditRetention(retention))
		if err != nil {
			t.Fatal(err)
		}
		lower := time.Now().Add(-retention)
		n, err := svc.ExpireAudit(t.Context(), 25)
		upper := time.Now().Add(-retention)
		if err != nil || n != 4 || expirer.calls != 1 || expirer.limit != 25 {
			t.Fatalf("ExpireAudit = %d %v; expirer saw %d calls, limit %d", n, err, expirer.calls, expirer.limit)
		}
		if expirer.before.Before(lower) || expirer.before.After(upper) {
			t.Fatalf("cutoff %v outside [%v, %v]", expirer.before, lower, upper)
		}
	}
	if MinAuditRetention != 366*24*time.Hour {
		t.Fatalf("MinAuditRetention drifted: %s", MinAuditRetention)
	}
}

func TestExpireAuditRefusesBeforeCallingTheExpirer(t *testing.T) {
	expirer := &stubAuditExpirer{}
	svc, err := NewService(&stubMutationRepo{}, WithAuditExpiry(expirer), WithAuditRetention(MinAuditRetention))
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, -1} {
		if n, err := svc.ExpireAudit(t.Context(), limit); n != 0 || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("limit %d: %d %v", limit, n, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.ExpireAudit(ctx, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
	unbounded, err := NewService(&stubMutationRepo{}, WithAuditExpiry(expirer))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unbounded.ExpireAudit(t.Context(), 10); !errors.Is(err, ErrAuditRetentionNotConfigured) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("without a retention: %v", err)
	}
	if expirer.calls != 0 {
		t.Fatalf("a refused call reached the expirer %d times", expirer.calls)
	}
	unconfigured, err := NewService(&stubMutationRepo{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unconfigured.ExpireAudit(t.Context(), 10); !errors.Is(err, ErrAuditExpiryNotConfigured) {
		t.Fatalf("without an expirer: %v", err)
	}
	var nilService *Service
	if _, err := nilService.ExpireAudit(t.Context(), 10); !errors.Is(err, ErrAuditExpiryNotConfigured) {
		t.Fatalf("nil service: %v", err)
	}
}

func TestAuditRetentionConstruction(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
		want error
	}{
		{"below the minimum", []Option{WithAuditExpiry(&stubAuditExpirer{}), WithAuditRetention(MinAuditRetention - time.Nanosecond)}, ErrAuditRetentionTooShort},
		{"the store floor", []Option{WithAuditExpiry(&stubAuditExpirer{}), WithAuditRetention(365 * 24 * time.Hour)}, ErrAuditRetentionTooShort},
		{"explicit zero", []Option{WithAuditExpiry(&stubAuditExpirer{}), WithAuditRetention(0)}, ErrAuditRetentionTooShort},
		{"negative", []Option{WithAuditExpiry(&stubAuditExpirer{}), WithAuditRetention(-time.Hour)}, ErrAuditRetentionTooShort},
		{"without an expirer", []Option{WithAuditRetention(MinAuditRetention)}, ErrAuditRetentionWithoutExpirer},
		{"typed-nil expirer", []Option{WithAuditExpiry((*stubAuditExpirer)(nil))}, sdk.ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if svc, err := NewService(&stubMutationRepo{}, tc.opts...); svc != nil || !errors.Is(err, tc.want) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("NewService = %v %v, want %v", svc, err, tc.want)
			}
		})
	}
}
