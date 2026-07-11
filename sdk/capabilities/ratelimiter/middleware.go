package ratelimiter

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Allower is the one method Middleware consumes; Limiter satisfies it. The HTTP
// seam never needs Reset/Close, so it accepts this narrow port rather than the
// full Limiter (or the resolver-backed *RateLimiter): a host can wrap its own
// Allower — for example a logging/metrics decorator — around whatever backend it
// wires.
type Allower interface {
	Allow(ctx context.Context, key string, limit Limit) (Result, error)
}

// Middleware returns web.Middleware that throttles requests on key(r) against
// limit, using l. reject is called on a denied request; a nil reject sends the
// default JSON 429 via web.RespondJSONError (code "rate_limited"), optionally
// carrying Retry-After from the Result.
//
// This is a capability×foundation composition (a ratelimiter capability
// producing a web.Middleware), so it lives with the ratelimiter semantics
// rather than in web: web stays agnostic of rate limiting, ratelimiter legally
// depends on web (capability → foundation).
//
// A limiter ERROR fails OPEN — the request proceeds; reject fires ONLY on
// err == nil && !res.Allowed. Availability of the guarded route beats a limiter
// outage, and this mirrors the exact posture the authentication feature relied
// on before it delegated here. (This is the deliberate opposite of an
// authorization gate, which fails closed.)
//
// The fail-open path is SILENT by design: the middleware takes no logger and
// emits nothing on the err != nil branch. A host that needs visibility into
// limiter failures wraps its Allower with a logging/metrics decorator — the
// Allower seam is the observability point — and/or relies on limiter-side
// alerting; silent fail-open must never be mistaken for a monitored state.
func Middleware(l Allower, limit Limit, key func(*http.Request) string, reject func(http.ResponseWriter, *http.Request, Result)) web.Middleware {
	if reject == nil {
		reject = defaultReject
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res, err := l.Allow(r.Context(), key(r), limit)
			if err == nil && !res.Allowed {
				reject(w, r, res)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// defaultReject writes the FS9 JSON 429, adding Retry-After (whole seconds,
// rounded up) when the Result carries one.
func defaultReject(w http.ResponseWriter, _ *http.Request, res Result) {
	if res.RetryAfter > 0 {
		secs := int((res.RetryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many requests").WithCode("rate_limited"))
}
