package ratelimiter_test

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

type fakeAllower struct {
	res    ratelimiter.Result
	err    error
	gotKey string
	calls  int
	cancel context.CancelFunc
}

func (f *fakeAllower) Allow(_ context.Context, key string, _ ratelimiter.Limit) (ratelimiter.Result, error) {
	f.calls++
	f.gotKey = key
	if f.cancel != nil {
		f.cancel()
	}
	return f.res, f.err
}
func staticKey(k string) func(*http.Request) string { return func(*http.Request) string { return k } }

func TestMiddlewareFailurePolicy(t *testing.T) {
	for _, tc := range []struct {
		name          string
		err           error
		allowed, open bool
		want          int
		wantNext      bool
	}{
		{"allowed", nil, true, false, 200, true},
		{"denied", nil, false, false, 429, false},
		{"outage closed", io.ErrUnexpectedEOF, false, false, 503, false},
		{"outage open", io.ErrUnexpectedEOF, false, true, 200, true},
		{"capacity closed", ratelimiter.ErrCapacity, false, false, 503, false},
		{"invalid even open", sdk.ErrInvalidInput, false, true, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allower := &fakeAllower{res: ratelimiter.Result{Allowed: tc.allowed}, err: tc.err}
			reports := 0
			nextRan := false
			cfg := ratelimiter.MiddlewareConfig{Limit: ratelimiter.PerMinute(5), Key: staticKey("ip:test"), FailOpen: tc.open, OnError: func(_ context.Context, err error) {
				reports++
				if !errors.Is(err, tc.err) {
					t.Fatal(err)
				}
			}}
			h := ratelimiter.Middleware(allower, cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextRan = true; w.WriteHeader(200) }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != tc.want || nextRan != tc.wantNext || allower.gotKey != "ip:test" {
				t.Fatalf("status=%d next=%v key=%q", w.Code, nextRan, allower.gotKey)
			}
			wantReports := 0
			if tc.err != nil {
				wantReports = 1
			}
			if reports != wantReports {
				t.Fatalf("reports=%d", reports)
			}
		})
	}
}

func TestMiddlewareInvalidConfiguration(t *testing.T) {
	for _, kind := range []string{"limit", "key function", "empty key", "nil limiter"} {
		t.Run(kind, func(t *testing.T) {
			fake := &fakeAllower{res: ratelimiter.Result{Allowed: true}}
			var allower ratelimiter.Allower = fake
			cfg := ratelimiter.MiddlewareConfig{Limit: ratelimiter.PerMinute(1), Key: staticKey("k"), FailOpen: true}
			switch kind {
			case "limit":
				cfg.Limit = ratelimiter.Limit{}
			case "key function":
				cfg.Key = nil
			case "empty key":
				cfg.Key = staticKey("")
			case "nil limiter":
				allower = nil
			}
			reports := 0
			cfg.OnError = func(_ context.Context, err error) {
				reports++
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatal(err)
				}
			}
			h := ratelimiter.Middleware(allower, cfg)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid configuration bypassed enforcement") }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != 500 || fake.calls != 0 || reports != 1 {
				t.Fatalf("%d calls=%d reports=%d", w.Code, fake.calls, reports)
			}
		})
	}
}

func TestMiddlewareCustomRejectAndRetryRounding(t *testing.T) {
	for _, tc := range []struct {
		retry  time.Duration
		header string
	}{{0, ""}, {1, "1"}, {time.Second, "1"}, {time.Second + 1, "2"}, {time.Duration(math.MaxInt64), "9223372037"}} {
		fake := &fakeAllower{res: ratelimiter.Result{RetryAfter: tc.retry}}
		h := ratelimiter.Middleware(fake, ratelimiter.MiddlewareConfig{Limit: ratelimiter.PerMinute(1), Key: staticKey("k")})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("denial ran next") }))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 429 || w.Header().Get("Retry-After") != tc.header {
			t.Fatalf("%s: %d %v", tc.retry, w.Code, w.Header())
		}
	}
	fake := &fakeAllower{}
	h := ratelimiter.Middleware(fake, ratelimiter.MiddlewareConfig{Limit: ratelimiter.PerMinute(1), Key: staticKey("k"), Reject: func(w http.ResponseWriter, _ *http.Request, _ ratelimiter.Result) { w.WriteHeader(418) }})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("denial ran next") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 418 {
		t.Fatal(w.Code)
	}
}

func TestMiddlewareCanceledCallerDoesNotContinue(t *testing.T) {
	for _, when := range []string{"before", "during", "hook"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := &fakeAllower{res: ratelimiter.Result{Allowed: true}}
			if when == "before" {
				cancel()
			}
			if when == "during" {
				fake.cancel = cancel
			}
			if when == "hook" {
				fake.err = io.ErrUnexpectedEOF
			}
			reported := false
			cfg := ratelimiter.MiddlewareConfig{Limit: ratelimiter.PerMinute(1), Key: staticKey("k"), FailOpen: true, OnError: func(context.Context, error) {
				reported = true
				if when == "hook" {
					cancel()
				}
			}}
			h := ratelimiter.Middleware(fake, cfg)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("canceled caller ran next") }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
			if !reported || w.Body.Len() != 0 || (when == "before" && fake.calls != 0) {
				t.Fatalf("reported=%v calls=%d body=%s", reported, fake.calls, w.Body.String())
			}
		})
	}
}

func TestMiddlewareRealHTTPBudget(t *testing.T) {
	h := ratelimiter.Middleware(ratelimiter.NewMemory(), ratelimiter.MiddlewareConfig{Limit: ratelimiter.PerMinute(1).WithBurst(1), Key: staticKey("public:catalog")})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }))
	server := httptest.NewServer(h)
	defer server.Close()
	for i := 0; i < 3; i++ {
		res, err := server.Client().Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if res.StatusCode != 200 || string(body) != "ok" {
				t.Fatalf("%d %q", res.StatusCode, body)
			}
		} else if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" {
			t.Fatalf("%d %v", res.StatusCode, res.Header)
		}
	}
}
