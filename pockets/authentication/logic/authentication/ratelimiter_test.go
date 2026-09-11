package authentication

import (
	"bytes"
	"context"
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

func TestRefreshReportsLimiterOutageAndRotates(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "limiter@example.com", "password123456789")
	var logs bytes.Buffer
	h.svc.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	h.svc.limiter = failedLimiter{sdk.ErrUnavailable}
	next, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil || next.RefreshToken == "" || next.RefreshToken == pair.RefreshToken {
		t.Fatalf("refresh during limiter outage: pair=%+v err=%v", next, err)
	}
	if !strings.Contains(logs.String(), "refresh rate limiter failed") {
		t.Fatalf("limiter failure unreported: %s", logs.String())
	}
}
