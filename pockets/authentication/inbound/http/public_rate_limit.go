package authenticationhttp

import (
	"context"
	"net/http"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// RateLimitByIP throttles a public route using the client-info carrier's IP.
// Dependency failures deliberately fail open and are logged. Invalid limits
// remain configuration errors; exhausted quotas return JSON 429 + Retry-After.
func (s *Adapter) RateLimitByIP(keyPrefix string, perMinute int) web.Middleware {
	return ratelimiter.Middleware(s.limiter, ratelimiter.MiddlewareConfig{
		Limit: ratelimiter.PerMinute(perMinute),
		Key: func(r *http.Request) string {
			return keyPrefix + ":" + clientIPFromContext(r.Context())
		},
		FailOpen: true,
		OnError: func(ctx context.Context, err error) {
			s.logger.WarnContext(ctx, "public route rate limiter failed", "error_kind", authlogic.ErrorKind(err))
		},
	})
}
