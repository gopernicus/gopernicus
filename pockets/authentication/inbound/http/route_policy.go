package authenticationhttp

import (
	"slices"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// RoutePrincipalStrategy is a host's authentication posture for ONE bundled
// route group (WithRouteAuthentication). It is opaque so "unset" stays
// distinguishable from an explicit zero-option PrincipalStrategy(), which
// deliberately means RequirePrincipal(): every wired credential, both
// transports, the LiveOnMutation tier.
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
// Every bundled group retains a non-overridable delegated-token denial; resource
// routes that accept Audience belong on the host's own resource surface.
//
// A host override owns the credential set and transports; the liveness floor
// of a sensitive group — SessionHydration, SessionSecurityReads,
// CredentialManagement, MachineLifecycle and UserAdministration — is Live()
// unless relaxed (WithLivenessFloorRelaxed). An override lowered to
// Stateless() or LiveOnMutation() there still proves a live session on every
// call (one session read; for an external token, the provider revocation check,
// which fails closed during a provider outage even under RevocationFailOpen).
// OAuthLinkStart, CSRFBootstrap, Invitations and BrowserAccount keep a
// Stateless() floor, so their override owns the tier.
//
// Under MFAConfig.Required all beside passkeys or TOTP, OAuthLinkStart,
// SessionSecurityReads, CSRFBootstrap, SessionHydration, CredentialManagement
// and BrowserAccount are the enrollment surface: their defaults, their floors
// and any override carry AllowMFAEnrollment(), so a session past its enrollment
// grace can still enrol. MachineLifecycle, UserAdministration and Invitations
// do not.
//
// An override also owns the inbound context contract of its surface. Handlers
// that read CurrentUser — and the session-bound credential/step-up handlers that
// also read CurrentSessionID — still fail closed when the configured strategy
// authenticates a caller that cannot supply them (a self-acting service account
// has no user; a stateless strategy proves no live session).
type BundledRouteAuthentication struct {
	// OAuthLinkStart gates GET /auth/oauth/{provider}/link/start.
	// Default: RequirePrincipal(Accept(CredentialAccessToken), FirstParty()).
	OAuthLinkStart RoutePrincipalStrategy
	// SessionSecurityReads gates GET /auth/delivery/status and /auth/methods.
	// Default: RequirePrincipal(Accept(CredentialAccessToken), FirstParty(), Live()).
	SessionSecurityReads RoutePrincipalStrategy
	// CSRFBootstrap gates GET /auth/csrf. Default: RequirePrincipal(Live()) —
	// every wired kind, so a provider-cookie user can obtain the double-submit
	// token the bundled mutations require.
	CSRFBootstrap RoutePrincipalStrategy
	// SessionHydration gates GET /auth/me. Default: RequirePrincipal(Live()) —
	// every wired kind.
	SessionHydration RoutePrincipalStrategy
	// CredentialManagement gates the password change/set/remove routes, the
	// step-up pair, identifier add/confirm/update/delete, and the OAuth unlink
	// pair. Default: RequirePrincipal(Accept(CredentialAccessToken),
	// FirstParty(), Live()) — they need a native session, so an external token
	// is denied (step-up included).
	CredentialManagement RoutePrincipalStrategy
	// MachineLifecycle gates service-account creation/listing and key
	// minting/listing/revocation. Default: RequirePrincipal(Accept(CredentialAccessToken),
	// FirstParty(), Live()) — a key never creates, reads, mints, or revokes keys.
	MachineLifecycle RoutePrincipalStrategy
	// UserAdministration gates every /auth/admin/users route and
	// GET /auth/admin/security-events.
	// Default: RequirePrincipal(Live(), RefuseActAsUserKeys()) — every wired
	// kind except an act-as-user API key; a self-acting machine principal
	// reaches WithUserAdminCheck and the host decides. An override that admits
	// act-as-user keys is logged at every boot, and the check then reads
	// UserAdminCheckRequest.Credential to tell the key from its owner.
	UserAdministration RoutePrincipalStrategy
	// Invitations gates the authenticated invitation routes.
	// Default: RequirePrincipal(Live(), RefuseActAsUserKeys()) — every wired
	// kind except an act-as-user API key. A self-acting service account fails
	// the handlers' CurrentUser requirement, so no machine principal reaches an
	// invitation handler by default.
	Invitations RoutePrincipalStrategy
	// BrowserAccount gates the bundled HTML account pages and the form-only
	// identifier edit POST. Default: RequirePrincipal(Accept(CredentialAccessToken),
	// FirstParty(), Transports(TransportCookie), Live(), Browser()) — the set the
	// OAuth2 authorize and sessions pages also use.
	BrowserAccount RoutePrincipalStrategy
}

// middleware resolves one bundled-route strategy over svc, or nil when the host
// left the slot unset — which Mount reads as "keep this group's audited
// default". An explicit PrincipalStrategy() with no options is CONFIGURED and
// resolves to the primitive defaults, which is why the configured bit exists.
// enrollment appends AllowMFAEnrollment() for the enrollment surface's groups,
// so an override there inherits it; group labels the act-as-user refusal row.
func (r RoutePrincipalStrategy) middleware(svc *Adapter, group string, enrollment bool) web.Middleware {
	if !r.configured {
		return nil
	}
	opts := append(slices.Clip(r.opts), bundledGroup(group))
	if enrollment {
		opts = append(opts, AllowMFAEnrollment())
	}
	return svc.RequirePrincipal(opts...)
}

// warnActAsUserAdmission logs, at every boot, each host override of
// UserAdministration or Invitations whose credential set admits an act-as-user
// API key at the authenticator while machine identity is wired: the host
// policy behind it sees the key's human owner. With MFAConfig.APIKeysExempt
// also set it logs a second line per group, since the admin MFA gate then
// admits that key unverified. An override's own Check cannot be introspected,
// so both lines speak of admission at the authenticator only. It warns, never
// refuses.
func (s *Adapter) warnActAsUserAdmission(cfg BundledRouteAuthentication) {
	if !s.service.MachineEnabled() {
		return
	}
	actAsUserKey := Credential{Kind: CredentialAPIKey, Transport: TransportHeader, ActAsUser: true}
	for _, g := range []struct {
		name     string
		strategy RoutePrincipalStrategy
		next     string
	}{
		{"UserAdministration", cfg.UserAdministration, "add RefuseActAsUserKeys() to the strategy, or read UserAdminCheckRequest.Credential"},
		{"Invitations", cfg.Invitations, "add RefuseActAsUserKeys() to the strategy, or read InviteCheckRequest.Credential; InviteCheck sees Credential on create only"},
	} {
		if !g.strategy.configured || !s.resolveSet(g.strategy.opts).admits(actAsUserKey) {
			continue
		}
		s.logger.Warn("authentication: a bundled route group override admits act-as-user API keys at the authenticator, so its host policy sees the key's human owner; "+g.next,
			"posture_warning", "bundled_group_admits_act_as_user_keys", "group", g.name)
		if s.apiKeysExempt {
			s.logger.Warn("authentication: a bundled route group override admits act-as-user API keys at the authenticator and MFAConfig.APIKeysExempt is set, so the admin MFA gate admits that key unverified",
				"posture_warning", "bundled_group_act_as_user_mfa_exempt", "group", g.name)
		}
	}
}

// resolveBundledRouteAuth freezes WithRouteAuthentication into concrete
// middleware over the constructed service (the "no half-constructed Service"
// contract: a host names a posture through WithRouteAuthentication).
func resolveBundledRouteAuth(cfg BundledRouteAuthentication, svc *Adapter) RouteAuthentication {
	return RouteAuthentication{
		OAuthLinkStart:       cfg.OAuthLinkStart.middleware(svc, "OAuthLinkStart", true),
		SessionSecurityReads: cfg.SessionSecurityReads.middleware(svc, "SessionSecurityReads", true),
		CSRFBootstrap:        cfg.CSRFBootstrap.middleware(svc, "CSRFBootstrap", true),
		SessionHydration:     cfg.SessionHydration.middleware(svc, "SessionHydration", true),
		CredentialManagement: cfg.CredentialManagement.middleware(svc, "CredentialManagement", true),
		MachineLifecycle:     cfg.MachineLifecycle.middleware(svc, "MachineLifecycle", false),
		UserAdministration:   cfg.UserAdministration.middleware(svc, "UserAdministration", false),
		Invitations:          cfg.Invitations.middleware(svc, "Invitations", false),
		BrowserAccount:       cfg.BrowserAccount.middleware(svc, "BrowserAccount", true),
	}
}
