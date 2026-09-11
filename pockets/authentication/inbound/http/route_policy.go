package authenticationhttp

import (
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// RoutePrincipalStrategy is a host's authentication posture for ONE bundled
// route group (WithRouteAuthentication). It is opaque so "unset" stays
// distinguishable from an explicit zero-option PrincipalStrategy(), which
// deliberately means RequirePrincipal(): every wired credential, both
// transports, stateless.
type RoutePrincipalStrategy struct {
	configured bool
	opts       []PrincipalOption
}

// PrincipalStrategy builds the posture for one bundled route group out of the
// same option vocabulary RequirePrincipal takes. Calling it with no options is
// an explicit choice of the primitive defaults, NOT "leave the audited default
// in place" — leave the field zero for that.
func PrincipalStrategy(opts ...PrincipalOption) RoutePrincipalStrategy {
	return RoutePrincipalStrategy{configured: true, opts: append([]PrincipalOption(nil), opts...)}
}

// BundledRouteAuthentication overrides the authentication posture of the bundled
// route groups, one semantic surface at a time (WithRouteAuthentication). A zero
// field keeps that group's audited default; a set field replaces only that
// group. It configures AUTHENTICATION only: it cannot unmount the authenticator
// from a protected surface, and it never replaces the separate host
// authorization seams (MachineRoutesGate, UserAdminCheck, InviteCheck).
//
// An override also owns the inbound context contract of its surface. Handlers
// that read CurrentUser — and the session-bound credential/step-up handlers that
// also read CurrentSessionID — still fail closed when the configured strategy
// authenticates a caller that cannot supply them (a self-acting service account
// has no user; a stateless strategy proves no live session).
type BundledRouteAuthentication struct {
	// OAuthLinkStart gates GET /auth/oauth/{provider}/link/start.
	// Default: RequireAccessToken().
	OAuthLinkStart RoutePrincipalStrategy
	// SessionSecurityReads gates GET /auth/delivery/status, /auth/methods, and
	// /auth/csrf. Default: RequireAccessTokenLive().
	SessionSecurityReads RoutePrincipalStrategy
	// SessionHydration gates GET /auth/me. Default: RequireAccessTokenOrAPIKeyLive().
	SessionHydration RoutePrincipalStrategy
	// CredentialManagement gates the password change/set/remove routes, the
	// step-up pair, identifier add/confirm/update/delete, and the OAuth unlink
	// pair. Default: RequireAccessTokenLive().
	CredentialManagement RoutePrincipalStrategy
	// MachineLifecycle gates service-account creation/listing and key
	// minting/listing/revocation. Default: RequireAccessTokenLive() — a key
	// never creates, reads, mints, or revokes keys.
	MachineLifecycle RoutePrincipalStrategy
	// UserAdministration gates every /auth/admin/users route.
	// Default: RequireAccessTokenOrAPIKeyLive() — a machine principal reaches
	// authlogic.WithUserAdminCheck and the host decides.
	UserAdministration RoutePrincipalStrategy
	// Invitations gates the authenticated invitation routes.
	// Default: RequireAccessTokenOrAPIKeyLive().
	Invitations RoutePrincipalStrategy
	// BrowserAccount gates the bundled HTML account pages and the form-only
	// identifier edit POST. Default: RequirePrincipal(Accept(CredentialAccessToken),
	// Transports(TransportCookie), Live(), Browser()).
	BrowserAccount RoutePrincipalStrategy
}

// middleware resolves one bundled-route strategy over svc, or nil when the host
// left the slot unset — which Mount reads as "keep this group's audited
// default". An explicit PrincipalStrategy() with no options is CONFIGURED and
// resolves to the primitive defaults, which is why the configured bit exists.
func (r RoutePrincipalStrategy) middleware(svc *Adapter) web.Middleware {
	if !r.configured {
		return nil
	}
	return svc.RequirePrincipal(r.opts...)
}

// resolveBundledRouteAuth freezes WithRouteAuthentication into concrete
// middleware over the constructed service (the "no half-constructed Service"
// contract: a host names a posture through WithRouteAuthentication).
func resolveBundledRouteAuth(cfg BundledRouteAuthentication, svc *Adapter) RouteAuthentication {
	return RouteAuthentication{
		OAuthLinkStart:       cfg.OAuthLinkStart.middleware(svc),
		SessionSecurityReads: cfg.SessionSecurityReads.middleware(svc),
		SessionHydration:     cfg.SessionHydration.middleware(svc),
		CredentialManagement: cfg.CredentialManagement.middleware(svc),
		MachineLifecycle:     cfg.MachineLifecycle.middleware(svc),
		UserAdministration:   cfg.UserAdministration.middleware(svc),
		Invitations:          cfg.Invitations.middleware(svc),
		BrowserAccount:       cfg.BrowserAccount.middleware(svc),
	}
}
