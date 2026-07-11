package ratelimiter_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// fakeAllower records the key it was called with and returns a canned result.
type fakeAllower struct {
	res    ratelimiter.Result
	err    error
	gotKey string
}

func (f *fakeAllower) Allow(_ context.Context, key string, _ ratelimiter.Limit) (ratelimiter.Result, error) {
	f.gotKey = key
	return f.res, f.err
}

func staticKey(k string) func(*http.Request) string {
	return func(*http.Request) string { return k }
}

func TestMiddleware(t *testing.T) {
	t.Run("allowed request runs next", func(t *testing.T) {
		allower := &fakeAllower{res: ratelimiter.Result{Allowed: true}}
		nextRan := false
		mw := ratelimiter.Middleware(allower, ratelimiter.PerMinute(5), staticKey("k"), nil)
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextRan = true
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if !nextRan {
			t.Fatal("expected next to run on an allowed request")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("denied with nil reject writes default 429 JSON", func(t *testing.T) {
		allower := &fakeAllower{res: ratelimiter.Result{Allowed: false}}
		nextRan := false
		mw := ratelimiter.Middleware(allower, ratelimiter.PerMinute(5), staticKey("k"), nil)
		h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextRan = true }))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if nextRan {
			t.Fatal("expected next NOT to run on a denied request")
		}
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
		}
		var body struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Code != "rate_limited" {
			t.Fatalf("code = %q, want %q", body.Code, "rate_limited")
		}
	})

	t.Run("denied with custom reject fires custom, not default", func(t *testing.T) {
		allower := &fakeAllower{res: ratelimiter.Result{Allowed: false}}
		mw := ratelimiter.Middleware(allower, ratelimiter.PerMinute(5), staticKey("k"),
			func(w http.ResponseWriter, _ *http.Request, _ ratelimiter.Result) {
				w.WriteHeader(http.StatusTeapot)
				w.Write([]byte("custom"))
			})
		h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("next must not run on a denied request")
		}))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Code != http.StatusTeapot {
			t.Fatalf("status = %d, want %d (custom reject)", rec.Code, http.StatusTeapot)
		}
		if rec.Body.String() != "custom" {
			t.Fatalf("body = %q, want %q (default reject should not fire)", rec.Body.String(), "custom")
		}
	})

	t.Run("limiter error fails open: request proceeds", func(t *testing.T) {
		allower := &fakeAllower{res: ratelimiter.Result{Allowed: false}, err: errors.New("limiter down")}
		nextRan := false
		mw := ratelimiter.Middleware(allower, ratelimiter.PerMinute(5), staticKey("k"),
			func(http.ResponseWriter, *http.Request, ratelimiter.Result) {
				t.Fatal("reject must not fire on a limiter error (fail open)")
			})
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextRan = true
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if !nextRan {
			t.Fatal("expected next to run when the limiter errors (fail open)")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("key func output reaches the Allower", func(t *testing.T) {
		allower := &fakeAllower{res: ratelimiter.Result{Allowed: true}}
		mw := ratelimiter.Middleware(allower, ratelimiter.PerMinute(5), staticKey("ip:1.2.3.4"), nil)
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		if allower.gotKey != "ip:1.2.3.4" {
			t.Fatalf("Allower key = %q, want %q", allower.gotKey, "ip:1.2.3.4")
		}
	})
}
