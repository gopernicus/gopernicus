package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

type failedLimiter struct{ err error }

func (l failedLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, l.err
}
func (failedLimiter) Reset(context.Context, string) error { return nil }

// TestRefreshLimiterOutageFailsClosed pins the per-session refresh budget's
// default outage policy: a limiter error is sdk.ErrUnavailable (503), the
// session does not rotate, and the failure is reported without its key.
func TestRefreshLimiterOutageFailsClosed(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "limiter@example.com", "password123456789")
	var logs bytes.Buffer
	h.svc.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	h.svc.limiter = failedLimiter{errors.New("limiter backend down")}
	next, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if !errors.Is(err, sdk.ErrUnavailable) || next != (TokenPair{}) {
		t.Fatalf("refresh during limiter outage: pair=%+v err=%v; want sdk.ErrUnavailable and no pair", next, err)
	}
	if !strings.Contains(logs.String(), "refresh rate limiter failed") {
		t.Fatalf("limiter failure unreported: %s", logs.String())
	}
	h.svc.limiter = ratelimiter.NewMemory()
	if _, err := h.svc.Refresh(context.Background(), pair.RefreshToken); err != nil {
		t.Fatalf("the refused refresh consumed the token: %v", err)
	}
}

// TestRefreshLimiterFailOpenRotates proves WithAbuseProtection's
// LimiterFailOpen reaches the service and admits the refresh with a WARN.
func TestRefreshLimiterFailOpenRotates(t *testing.T) {
	var cfg constructorConfig
	WithAbuseProtection(AbuseProtectionConfig{LimiterFailOpen: true})(&cfg)
	if !newService(cfg).limiterFailOpen {
		t.Fatal("WithAbuseProtection did not carry LimiterFailOpen to the service")
	}
	h := newHarness(t, nil)
	pair := h.loginPair(t, "limiter-open@example.com", "password123456789")
	var logs bytes.Buffer
	h.svc.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	h.svc.limiter = failedLimiter{sdk.ErrUnavailable}
	h.svc.limiterFailOpen = cfg.LimiterFailOpen
	next, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil || next.RefreshToken == "" || next.RefreshToken == pair.RefreshToken {
		t.Fatalf("fail-open refresh during limiter outage: pair=%+v err=%v", next, err)
	}
	if !strings.Contains(logs.String(), "refresh rate limiter failed") {
		t.Fatalf("limiter failure unreported: %s", logs.String())
	}
}
