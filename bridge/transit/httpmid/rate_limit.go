package httpmid

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// =============================================================================
// Rate Limiting Middleware - Functional Options API
// =============================================================================

// rateLimitOptions holds configuration for rate limiting middleware.
type rateLimitOptions struct {
	limit     *ratelimiter.Limit                          // Explicit limit (nil = use resolver)
	keyFunc   func(context.Context, *http.Request) string // Key extraction function
	keyPrefix string                                      // Optional key prefix
	failOpen  bool                                        // Allow requests on limiter errors
	skipFunc  func(*http.Request) bool                    // Skip rate limiting for certain requests
}

// RateLimitOption configures rate limiting behavior.
type RateLimitOption func(*rateLimitOptions)

// WithLimit sets an explicit rate limit, bypassing the resolver.
// Use for route-specific limits (e.g., stricter limits for auth routes).
func WithLimit(limit ratelimiter.Limit) RateLimitOption {
	return func(o *rateLimitOptions) {
		o.limit = &limit
	}
}

// WithKeyFunc sets a custom key extraction function.
// Default: auth-aware key (user/service account ID when authenticated, IP when not).
func WithKeyFunc(fn func(context.Context, *http.Request) string) RateLimitOption {
	return func(o *rateLimitOptions) {
		o.keyFunc = fn
	}
}

// WithIPKey forces IP-based rate limiting instead of auth-aware keying.
// Use for unauthenticated endpoints or when you want shared limits per IP.
func WithIPKey() RateLimitOption {
	return func(o *rateLimitOptions) {
		o.keyFunc = ipKeyFunc
	}
}

// WithKeyPrefix adds a prefix to rate limit keys.
// Useful for namespacing limits (e.g., "login:", "api:").
func WithKeyPrefix(prefix string) RateLimitOption {
	return func(o *rateLimitOptions) {
		o.keyPrefix = prefix
	}
}

// WithFailOpen allows requests when the rate limiter fails.
// Default is fail-closed (reject on errors).
func WithFailOpen() RateLimitOption {
	return func(o *rateLimitOptions) {
		o.failOpen = true
	}
}

// WithSkipFunc sets a function to skip rate limiting for certain requests.
func WithSkipFunc(fn func(*http.Request) bool) RateLimitOption {
	return func(o *rateLimitOptions) {
		o.skipFunc = fn
	}
}

// RateLimit creates rate limiting middleware.
//
// Default behavior uses auth-aware keying: rate limits are tracked per authenticated
// user/service account, falling back to IP for unauthenticated requests.
//
// Usage patterns:
//
//	// Default: auth-aware keying with resolver-based limits
//	httpmid.RateLimit(limiter, log)
//
//	// Explicit limit for specific routes
//	httpmid.RateLimit(limiter, log, httpmid.WithLimit(ratelimiter.PerMinute(5)))
//
//	// IP-based keying (for unauthenticated endpoints)
//	httpmid.RateLimit(limiter, log, httpmid.WithIPKey())
//
//	// Key prefix for namespaced limits
//	httpmid.RateLimit(limiter, log, httpmid.WithKeyPrefix("login"), httpmid.WithLimit(ratelimiter.PerMinute(5)))
func RateLimit(limiter *ratelimiter.RateLimiter, log *slog.Logger, opts ...RateLimitOption) func(http.Handler) http.Handler {
	// Handle nil limiter gracefully (no-op middleware).
	if limiter == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	options := &rateLimitOptions{
		keyFunc: authAwareKeyFunc,
	}
	for _, opt := range opts {
		opt(options)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Check if we should skip rate limiting.
			if options.skipFunc != nil && options.skipFunc(r) {
				next.ServeHTTP(w, r)
				return
			}

			// Extract rate limit key (ctx contains auth info from previous middleware).
			key := options.keyFunc(ctx, r)
			if key == "" {
				ip := GetClientIP(ctx)
				if ip == "" {
					ip = extractRemoteAddrIP(r)
				}
				key = "unknown:" + ip
				log.WarnContext(ctx, "rate limit key extraction failed, using fallback",
					"fallback_key", key,
				)
			}

			// Add prefix if configured.
			if options.keyPrefix != "" {
				key = options.keyPrefix + ":" + key
			}

			// Check rate limit.
			var result ratelimiter.Result
			var limit ratelimiter.Limit
			var err error

			if options.limit != nil {
				limit = *options.limit
				result, err = limiter.AllowWithLimit(ctx, key, limit)
			} else {
				resolveReq := buildResolveRequest(ctx, r)
				result, err = limiter.Allow(ctx, key, resolveReq)
				limit = limiter.Resolver().Resolve(ctx, resolveReq)
			}

			if err != nil {
				log.ErrorContext(ctx, "rate limiter error",
					"error", err,
					"key", key,
				)

				if options.failOpen {
					next.ServeHTTP(w, r)
					return
				}

				web.RespondJSONError(w, web.ErrUnavailable("rate limiter unavailable"))
				return
			}

			// Set rate limit headers.
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit.Requests))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))

			if !result.Allowed {
				retryAfter := int(result.RetryAfter.Seconds())

				log.WarnContext(ctx, "rate limit exceeded",
					"key", key,
					"limit", limit.Requests,
					"window", limit.Window,
					"retry_after", result.RetryAfter,
				)

				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				web.RespondJSONError(w, web.ErrTooManyRequests(
					fmt.Sprintf("Rate limit exceeded. Try again in %d seconds.", retryAfter),
				))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// =============================================================================
// Key Functions
// =============================================================================

// authAwareKeyFunc returns a rate limit key based on the authenticated subject.
// Authenticated: "user:{user_id}" or "service_account:{sa_id}"
// Anonymous: "ip:{client_ip}"
func authAwareKeyFunc(ctx context.Context, r *http.Request) string {
	if subject := GetSubject(ctx); subject != "" {
		return subject
	}

	ip := GetClientIP(ctx)
	if ip == "" {
		ip = extractRemoteAddrIP(r)
	}
	return "ip:" + ip
}

// ipKeyFunc extracts the client IP from context (set by TrustProxies middleware).
func ipKeyFunc(ctx context.Context, r *http.Request) string {
	ip := GetClientIP(ctx)
	if ip == "" {
		ip = extractRemoteAddrIP(r)
	}
	return "ip:" + ip
}

// buildResolveRequest creates a ResolveRequest from the request context.
func buildResolveRequest(ctx context.Context, r *http.Request) ratelimiter.ResolveRequest {
	ip := GetClientIP(ctx)
	if ip == "" {
		ip = extractRemoteAddrIP(r)
	}

	req := ratelimiter.ResolveRequest{
		SubjectType: "anonymous",
		ClientIP:    ip,
	}

	subject := GetSubject(ctx)
	if subject != "" {
		subjectType := GetSubjectType(ctx)
		switch subjectType {
		case SubjectTypeUser:
			req.SubjectType = "user"
		case SubjectTypeServiceAccount:
			req.SubjectType = "service_account"
		}
		if idx := strings.Index(subject, ":"); idx != -1 {
			req.SubjectID = subject[idx+1:]
		}
	}

	return req
}
