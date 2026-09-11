package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

func TestAuthenticationBudgetsAreIndependent(t *testing.T) {
	ctx := func(ip string) context.Context { return WithClientInfo(context.Background(), ip, "") }
	t.Run("subject across IPs", func(t *testing.T) {
		h := newHarness(t, nil)
		budget := RequestBudget{PerSubject: ratelimiter.PerMinute(1), PerIP: ratelimiter.PerMinute(10)}
		if err := h.svc.allowAuthenticationRequest(ctx("ip1"), "test", "user", budget); err != nil {
			t.Fatal(err)
		}
		if err := h.svc.allowAuthenticationRequest(ctx("ip2"), "test", "user", budget); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("IP rotation reset subject allowance: %v", err)
		}
	})
	t.Run("IP across subjects", func(t *testing.T) {
		h := newHarness(t, nil)
		budget := RequestBudget{PerSubject: ratelimiter.PerMinute(10), PerIP: ratelimiter.PerMinute(1)}
		if err := h.svc.allowAuthenticationRequest(ctx("ip"), "test", "user1", budget); err != nil {
			t.Fatal(err)
		}
		if err := h.svc.allowAuthenticationRequest(ctx("ip"), "test", "user2", budget); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("subject rotation reset IP allowance: %v", err)
		}
	})
}

func TestAuthenticationLimitConfiguration(t *testing.T) {
	if err := (AuthenticationLimits{}).Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for _, invalid := range []ratelimiter.Limit{{Requests: 1}, {Window: time.Minute}, {Requests: 1, Window: -time.Second}, {Requests: 1, Window: time.Minute, Burst: -1}} {
		for _, field := range []string{"login subject", "reset IP", "code subject", "password IP"} {
			t.Run(field+invalid.Window.String(), func(t *testing.T) {
				var l AuthenticationLimits
				switch field {
				case "login subject":
					l.Login.PerSubject = invalid
				case "reset IP":
					l.PasswordReset.PerIP = invalid
				case "code subject":
					l.SensitiveCode.PerSubject = invalid
				case "password IP":
					l.SensitivePassword.PerIP = invalid
				}
				if err := l.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("partial/negative host limit accepted: %v", err)
				}
			})
		}
	}
}

type failedAuthenticationLimiter struct {
	calls  int
	failAt int
	err    error
}

func (l *failedAuthenticationLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	l.calls++
	if l.calls == l.failAt {
		return ratelimiter.Result{}, l.err
	}
	return ratelimiter.Result{Allowed: true}, nil
}
func (*failedAuthenticationLimiter) Reset(context.Context, string) error { return nil }

func TestSensitiveBudgetsFailBeforeIssuance(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			uid, _, sid := h.mustVerifiedLogin(t, "limits@example.com", "password123456789")
			cause := errors.New("limiter unavailable")
			h.svc.limiter = &failedAuthenticationLimiter{failAt: failAt, err: cause}
			before := h.ch.countRows()
			sent := h.mailer.count()
			if _, err := h.svc.BeginStepUp(ctx, StepUpStart{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeSetPassword}); !errors.Is(err, cause) {
				t.Fatalf("code start swallowed limiter failure: %v", err)
			}
			if h.ch.countRows() != before || h.mailer.count() != sent {
				t.Fatal("failed limiter still issued/delivered code")
			}
			h.svc.limiter = &failedAuthenticationLimiter{failAt: failAt, err: cause}
			if _, err := h.svc.CompleteStepUpWithPassword(ctx, StepUpCompletion{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeSetPassword}, "password123456789"); !errors.Is(err, cause) {
				t.Fatalf("password proof swallowed limiter failure: %v", err)
			}
			if h.grants.unconsumed() != 0 {
				t.Fatal("failed limiter still admitted grant")
			}
		})
	}
}

func TestSensitiveCodeBudgetCannotBeResetByChangingPurpose(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "purpose-limit@example.com", "password123456789")
	h.svc.authenticationLimits.SensitiveCode = RequestBudget{PerSubject: ratelimiter.PerMinute(1), PerIP: ratelimiter.PerMinute(10)}
	if _, err := h.svc.BeginStepUp(ctx, StepUpStart{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeSetPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BeginStepUp(ctx, StepUpStart{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeUnlinkOAuth, Context: "another"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("purpose cycling bypassed subject budget: %v", err)
	}
}
