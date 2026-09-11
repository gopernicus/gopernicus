package ratelimiter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// MiddlewareConfig keeps budget, keying, rejection and outage policy host-owned.
// Invalid configuration is reported on requests and never bypasses enforcement.
type MiddlewareConfig struct {
	Limit Limit
	Key   func(*http.Request) string
	// Reject replaces the default quota-exhausted JSON 429 response.
	Reject func(http.ResponseWriter, *http.Request, Result)
	// FailOpen permits downstream work after dependency/capacity errors. It does
	// not hide invalid input or caller cancellation. The zero value fails closed.
	FailOpen bool
	// OnError reports failures synchronously without adding keys. It must be safe
	// for concurrent calls. A denied quota is a Result, not an error notification.
	OnError func(context.Context, error)
}

// Middleware rejects exhausted budgets with 429. Dependency failures produce 503
// unless FailOpen is explicit; invalid configuration/keys produce 500. Canceled
// callers do not invoke the limiter/downstream handler or receive a new response.
// Hosts can omit middleware to disable it; nil dependencies are configuration
// errors. Typed-nil adapters are caller misuse and are not reflection-detected.
func Middleware(l Allower, cfg MiddlewareConfig) web.Middleware {
	limit, configErr := cfg.Limit.Normalize()
	if l == nil || cfg.Key == nil {
		configErr = fmt.Errorf("ratelimiter: middleware needs a limiter and key function: %w", sdk.ErrInvalidInput)
	}
	reject := cfg.Reject
	if reject == nil {
		reject = defaultReject
	}
	report := func(ctx context.Context, err error) {
		if cfg.OnError != nil {
			cfg.OnError(ctx, err)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.Context().Err(); err != nil {
				report(r.Context(), err)
				return
			}
			err := configErr
			var result Result
			if err == nil {
				key := cfg.Key(r)
				err = checkKey(r.Context(), key)
				if err == nil {
					result, err = l.Allow(r.Context(), key, limit)
				}
			}
			if canceled := r.Context().Err(); canceled != nil {
				report(r.Context(), canceled)
				return
			}
			if err != nil {
				report(r.Context(), err)
				// An error hook can observe cancellation or finish work that lets the
				// caller cancel. It never authorizes a subsequent canceled invocation.
				if r.Context().Err() != nil {
					return
				}
				if errors.Is(err, sdk.ErrInvalidInput) {
					web.RespondJSONError(w, web.ErrInternal("internal server error"))
					return
				}
				if !cfg.FailOpen {
					web.RespondJSONError(w, web.ErrUnavailable("service unavailable"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if !result.Allowed {
				reject(w, r, result)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func defaultReject(w http.ResponseWriter, _ *http.Request, res Result) {
	if res.RetryAfter > 0 {
		seconds := int64(res.RetryAfter / time.Second)
		if res.RetryAfter%time.Second != 0 {
			seconds++
		}
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many requests").WithCode("rate_limited"))
}
