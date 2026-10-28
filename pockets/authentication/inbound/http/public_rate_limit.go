package authenticationhttp

import (
	"context"
	"fmt"
	"net/http"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Default per-client-IP budgets, per minute, of the bundled public routes.
const (
	defaultRegisterPerMinute          = 10
	defaultVerifyPerMinute            = 20
	defaultPasswordResetPerMinute     = 20
	defaultVerifyLinkPerMinute        = 20
	defaultOAuthNativePerMinute       = 40
	defaultRefreshPerMinute           = 30
	defaultInvitationDeclinePerMinute = 10
	defaultPasskeyPerMinute           = 30
)

// PublicRouteLimits sets the per-client-IP budgets of the bundled public
// credential-establishment routes. Each key is "<prefix>:<client IP>", the IP
// from the client-info carrier (web.TrustProxies-resolved, else the peer).
// A zero Limit selects that route's default; Validate refuses a non-zero limit
// that ratelimiter.Limit.Normalize refuses. There is no off switch: a host
// behind its own edge throttle sets a large budget.
//
// POST /auth/password/forgot and POST /auth/token carry no edge budget here:
// their per-IP budgets are AuthenticationLimits.PasswordReset and
// AuthenticationLimits.Login in the service.
type PublicRouteLimits struct {
	// Register budgets POST /auth/register (key "register"). Zero → 10/min.
	Register ratelimiter.Limit
	// Verify budgets POST /auth/verify (key "verify"). Zero → 20/min.
	Verify ratelimiter.Limit
	// PasswordReset budgets POST /auth/password/reset, the token-consume step
	// (key "password_reset"). Zero → 20/min.
	PasswordReset ratelimiter.Limit
	// VerifyLink budgets POST /auth/oauth/verify-link and
	// POST /auth/oauth/native/verify-link (one key, "verify_link"). Zero → 20/min.
	VerifyLink ratelimiter.Limit
	// OAuthNative budgets POST /auth/oauth/{provider}/native/start, /complete and
	// /link/start (one key shared by the three, "oauth_native"). Zero → 40/min.
	OAuthNative ratelimiter.Limit
	// Refresh budgets POST /auth/refresh per IP (key "refresh"). Zero → 30/min.
	// It is the IP arm only; the per-session arm in the service stays a fixed
	// 30/min.
	Refresh ratelimiter.Limit
	// InvitationDecline budgets both public decline routes, POST
	// /auth/invitations/{id}/decline and POST /auth/invitations/decline (one key
	// shared by both, "invitation_decline"). Zero → 10/min.
	InvitationDecline ratelimiter.Limit
	// Passkey budgets the public passkey sign-in pair, POST
	// /auth/passkeys/assertion/options and /auth/passkeys/assertion (one key
	// shared by both, "passkey"). Zero → 30/min.
	Passkey ratelimiter.Limit
}

// Validate reports the first limit, defaults applied, that
// ratelimiter.Limit.Normalize refuses; the error matches sdk.ErrInvalidInput.
func (l PublicRouteLimits) Validate() error {
	l = l.withDefaults()
	for _, item := range []struct {
		name  string
		limit ratelimiter.Limit
	}{
		{"Register", l.Register},
		{"Verify", l.Verify},
		{"PasswordReset", l.PasswordReset},
		{"VerifyLink", l.VerifyLink},
		{"OAuthNative", l.OAuthNative},
		{"Refresh", l.Refresh},
		{"InvitationDecline", l.InvitationDecline},
		{"Passkey", l.Passkey},
	} {
		if _, err := item.limit.Normalize(); err != nil {
			return fmt.Errorf("authentication: PublicRouteLimits.%s: %w", item.name, err)
		}
	}
	return nil
}

func (l PublicRouteLimits) withDefaults() PublicRouteLimits {
	for _, item := range []struct {
		limit     *ratelimiter.Limit
		perMinute int
	}{
		{&l.Register, defaultRegisterPerMinute},
		{&l.Verify, defaultVerifyPerMinute},
		{&l.PasswordReset, defaultPasswordResetPerMinute},
		{&l.VerifyLink, defaultVerifyLinkPerMinute},
		{&l.OAuthNative, defaultOAuthNativePerMinute},
		{&l.Refresh, defaultRefreshPerMinute},
		{&l.InvitationDecline, defaultInvitationDeclinePerMinute},
		{&l.Passkey, defaultPasskeyPerMinute},
	} {
		if *item.limit == (ratelimiter.Limit{}) {
			*item.limit = ratelimiter.PerMinute(item.perMinute)
		}
	}
	return l
}

// RateLimitByIP throttles a public route using the client-info carrier's IP,
// or, on a route ClientInfoMiddleware did not stamp, the TrustProxies-resolved
// or peer address. A limiter dependency failure fails CLOSED with a JSON 503 +
// Retry-After unless the adapter was built with WithLimiterFailOpen(true);
// either way it is logged. Invalid limits remain configuration errors (500);
// exhausted quotas return JSON 429 + Retry-After.
func (s *Adapter) RateLimitByIP(keyPrefix string, perMinute int) web.Middleware {
	return s.rateLimitByIP(keyPrefix, ratelimiter.PerMinute(perMinute))
}

// rateLimitByKey is rateLimitByIP keyed on "<prefix>:<key>" instead of the
// client IP — a per-actor budget, applied after the actor is known.
func (s *Adapter) rateLimitByKey(keyPrefix, key string, limit ratelimiter.Limit) web.Middleware {
	return ratelimiter.Middleware(s.limiter, ratelimiter.MiddlewareConfig{
		Limit:    limit,
		Key:      func(*http.Request) string { return keyPrefix + ":" + key },
		FailOpen: s.limiterFailOpen,
		OnError: func(ctx context.Context, err error) {
			s.logger.WarnContext(ctx, "actor rate limiter failed", "error_kind", authlogic.ErrorKind(err), "fail_open", s.limiterFailOpen)
		},
	})
}

func (s *Adapter) rateLimitByIP(keyPrefix string, limit ratelimiter.Limit) web.Middleware {
	return ratelimiter.Middleware(s.limiter, ratelimiter.MiddlewareConfig{
		Limit: limit,
		Key: func(r *http.Request) string {
			return keyPrefix + ":" + clientIPFor(r)
		},
		FailOpen: s.limiterFailOpen,
		OnError: func(ctx context.Context, err error) {
			s.logger.WarnContext(ctx, "public route rate limiter failed", "error_kind", authlogic.ErrorKind(err), "fail_open", s.limiterFailOpen)
		},
	})
}
