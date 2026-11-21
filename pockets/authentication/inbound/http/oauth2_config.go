package authenticationhttp

import (
	"context"
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type SessionManagementService interface {
	ListUserSessions(context.Context, string, list.Request) (list.Page[session.Session], error)
	RevokeUserSession(context.Context, string, string) error
	RevokeAllUserSessions(context.Context, string) error
	RevokeDelegatedUserSessions(context.Context, string) error
}

type OAuth2Config struct {
	Service  *oauth2.Service
	Sessions SessionManagementService
	// Views renders consent and the sessions page on the pocket (server-rendered
	// lane, which also needs the browser Views). Set exactly one of Views and
	// ConsentPath.
	Views OAuthViews
	// ConsentPath is the host's own consent page (SPA lane): a safe relative
	// path with no query or fragment. GET /auth/oauth2/authorize validates the
	// request and redirects there with ?request=<consent request token>; the
	// page reads GET /auth/oauth2/consent and posts POST /auth/oauth2/consent.
	// The browser Views are then optional, and no OAuth2 HTML page is mounted.
	ConsentPath           string
	Limiter               ratelimiter.Limiter
	CapabilityDescription string
}

func WithOAuth2(value *OAuth2Config) Option {
	if value == nil {
		return func(c *adapterConfig) { c.OAuth2 = nil }
	}
	copy := *value
	if copy.CapabilityDescription == "" {
		copy.CapabilityDescription = "This app can use the tools exposed by this service with your current account permissions. This includes reading data and making changes where your account permits them. Changes are attributed to your account."
	}
	return func(c *adapterConfig) { snapshot := copy; c.OAuth2 = &snapshot }
}

// validateOAuth2Consent requires exactly one consent lane: OAuthViews (which
// also needs the browser Views, as the server-rendered pages share them) or a
// safe relative ConsentPath with no query or fragment.
func validateOAuth2Consent(cfg *OAuth2Config, views Views) error {
	hasViews, hasPath := !nilDependency(cfg.Views), cfg.ConsentPath != ""
	switch {
	case hasViews == hasPath:
		return fmt.Errorf("authentication HTTP: OAuth2 requires exactly one of Views and ConsentPath: %w", sdk.ErrInvalidInput)
	case hasViews && nilDependency(views):
		return fmt.Errorf("authentication HTTP: OAuth2 Views require the browser Views: %w", sdk.ErrInvalidInput)
	case hasPath && (redirect.SafeRelativePath(cfg.ConsentPath) != cfg.ConsentPath || strings.ContainsAny(cfg.ConsentPath, "?#")):
		return fmt.Errorf("authentication HTTP: OAuth2 ConsentPath must be a safe relative path with no query or fragment: %w", sdk.ErrInvalidInput)
	}
	return nil
}
