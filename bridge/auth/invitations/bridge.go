// Package invitations provides HTTP handlers for invitation endpoints.
//
// It bridges the [invitations.Inviter] to HTTP. Routes are registered
// via [Bridge.AddHttpRoutes] onto a [*web.RouteGroup]:
//
//	ib := invitations.New(log, inviter, authorizer, authenticator, rateLimiter)
//	group := handler.Group("/invitations")
//	ib.AddHttpRoutes(group)
package invitations

import (
	"log/slog"

	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/auth/authorization"
	invitationscore "github.com/gopernicus/gopernicus/core/auth/invitations"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
)

// Bridge is the HTTP handler bridge for invitation endpoints.
type Bridge struct {
	log           *slog.Logger
	invitations   *invitationscore.Inviter
	authorizer    *authorization.Authorizer
	authenticator *authentication.Authenticator
	rateLimiter   *ratelimiter.RateLimiter
	jsonErrors    httpmid.ErrorRenderer
	htmlErrors    httpmid.ErrorRenderer
}

// BridgeOption configures optional Bridge dependencies.
type BridgeOption func(*Bridge)

// WithJSONErrorRenderer overrides the default JSON error renderer used by
// API middleware (Authenticate, AuthorizeParam). The default is [httpmid.JSONErrors].
func WithJSONErrorRenderer(r httpmid.ErrorRenderer) BridgeOption {
	return func(b *Bridge) { b.jsonErrors = r }
}

// WithHTMLErrorRenderer sets the HTML error renderer for server-rendered
// routes. When set, HTML middleware uses this renderer instead of the JSON
// default.
func WithHTMLErrorRenderer(r httpmid.ErrorRenderer) BridgeOption {
	return func(b *Bridge) { b.htmlErrors = r }
}

// New creates a new invitations bridge.
func New(
	log *slog.Logger,
	invitations *invitationscore.Inviter,
	authorizer *authorization.Authorizer,
	authenticator *authentication.Authenticator,
	rateLimiter *ratelimiter.RateLimiter,
	opts ...BridgeOption,
) *Bridge {
	b := &Bridge{
		log:           log,
		invitations:   invitations,
		authorizer:    authorizer,
		authenticator: authenticator,
		rateLimiter:   rateLimiter,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}
