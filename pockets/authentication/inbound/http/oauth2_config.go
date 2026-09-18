package authenticationhttp

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type SessionManagementService interface {
	ListUserSessions(context.Context, string, list.Request) (list.Page[session.Session], error)
	RevokeUserSession(context.Context, string, string) error
	RevokeAllUserSessions(context.Context, string) error
}

type OAuth2Config struct {
	Service               *oauth2.Service
	Sessions              SessionManagementService
	Views                 OAuthViews
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
