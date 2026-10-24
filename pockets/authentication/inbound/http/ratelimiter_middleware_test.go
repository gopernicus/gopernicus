package authenticationhttp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

func TestPublicRateLimitPolicy(t *testing.T) {
	var logs bytes.Buffer
	svc := newServiceWithFakes(authenticationFixture{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	svc.limiter = failedLimiter{errors.New("backend secret must not appear")}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	run := func(limit int) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		svc.RateLimitByIP("public", limit)(next).ServeHTTP(r, httptest.NewRequest("GET", "/", nil))
		return r
	}
	if r := run(1); r.Code != http.StatusServiceUnavailable || r.Header().Get("Retry-After") != "5" {
		t.Fatalf("outage = %d Retry-After %q; want the default fail-closed 503 + Retry-After: 5", r.Code, r.Header().Get("Retry-After"))
	}
	if !strings.Contains(logs.String(), "public route rate limiter failed") || strings.Contains(logs.String(), "backend secret") {
		t.Fatalf("expected failure classification without backend details: %s", logs.String())
	}
	svc.limiterFailOpen = true
	logs.Reset()
	if r := run(1); r.Code != http.StatusNoContent {
		t.Fatalf("outage status = %d; want WithLimiterFailOpen to admit", r.Code)
	}
	if n := strings.Count(logs.String(), "public route rate limiter failed"); n != 1 {
		t.Fatalf("fail-open outage logged %d WARNs, want 1: %s", n, logs.String())
	}
	svc.limiterFailOpen = false
	if r := run(0); r.Code != http.StatusInternalServerError {
		t.Fatalf("invalid configuration status = %d", r.Code)
	}
	svc.limiter = ratelimiter.NewMemory()
	if r := run(1); r.Code != http.StatusNoContent {
		t.Fatalf("first admission status = %d", r.Code)
	}
	if r := run(1); r.Code != http.StatusTooManyRequests || r.Header().Get("Retry-After") == "" {
		t.Fatalf("denial missing status/retry guidance: %d %v", r.Code, r.Header())
	}
}

type failedLimiter struct{ err error }

func (l failedLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, l.err
}
func (failedLimiter) Reset(context.Context, string) error { return nil }
