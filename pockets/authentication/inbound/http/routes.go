// Package authenticationhttp provides authentication middleware, cookie policy
// and optional bundled JSON/HTML routes. New builds the full route adapter;
// NewAuthenticator accepts the narrower credential port for host-owned routes.
// Register mounts routes through pockets.Mount and starts no background work.
package authenticationhttp

import (
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// handlers holds services and inbound host policies. listStrategy controls list
// request parsing; domain services enforce their own business invariants.
type handlers struct {
	oauth2         *OAuth2Config
	inviteCheck    InviteCheck
	resourceRule   InvitationResourceRule
	userAdminCheck UserAdminCheck
	adminMFA       bool
	eraser         userEraser
	events         securityEventReader
	syncer         invitationSyncer
	claims         claimIdentifierLister
	svc            authService
	inv            InvitationService
	listStrategy   list.Strategy
	// mutation is the browser-safe-mutation policy (design §9.1) applied to
	// cookie-authenticated sensitive routes (step-up and, in later phase-6 tasks,
	// credential/identifier management).
	mutation MutationSecurity
	// views is the optional HTML rendering port (design §9.2). Nil → the HTML GET
	// surface is not registered and only the JSON API mounts; non-nil → the HTML GET
	// pages render through it. The core never imports templ; this is a technology-
	// neutral web.Renderer port a host (or the bundled views/templ module) satisfies.
	views Views
	// passkeys is the passkey capability, nil when the service has none wired;
	// passkeyViews renders the passkey pages, nil leaving the routes JSON-only.
	passkeys     passkeyService
	passkeyViews PasskeyViews
	// totp is the TOTP capability, nil when the service has none wired;
	// totpViews renders the TOTP pages, nil leaving the routes JSON-only.
	totp      totpService
	totpViews TOTPViews
	// machineMFA requires AAL2 on the machine lifecycle routes after the host's
	// MachineGate (MFAConfig admins beside passkeys or TOTP).
	machineMFA bool
	// globalRevoker drives the opt-in revoke-all route; nil leaves it unmounted.
	globalRevoker globalSessionRevoker
	// accountStatus is the service-account status, nil unless wired: the
	// machine DTOs then report every account active.
	accountStatus serviceAccountStatusManager
	// keyFilter is the filtered key listing; nil answers a filtered call 400.
	keyFilter apiKeyFilterLister
	// registration is IdentityConfig.Registration: closed unmounts the
	// registration routes and hides the login page's link.
	registration authlogic.RegistrationMode
	// htmlPolicy is the optional, technology-neutral HTML resource policy (design
	// §9.2, GOTH-0.4) writeHTMLSecurity applies to every HTML page and redirect. Nil
	// → the historical asset-free CSP (script-src nonce-only); non-nil → the fixed
	// protections plus the policy's validated widening resource directives. It only
	// widens; it can never remove a fixed protection.
	htmlPolicy *HTMLResourcePolicy
}

// mountDeps is the struct input Mount takes: the services, the transport-edge
// policies, and the optional host seams the route table needs. auth.Register is
// its only production caller — a struct keeps the optional members (invitations,
// views, the HTML policy, the machine gate) named at the call site instead of
// growing another positional parameter each release.
type mountDeps struct {
	OAuth2         *OAuth2Config
	InviteCheck    InviteCheck
	ResourceRule   InvitationResourceRule
	UserAdminCheck UserAdminCheck
	// Auth is the domain service every handler delegates to. Required.
	Auth authService
	// Invitations is the invitation service, a GENUINE nil interface when no
	// Granter is wired: nil → none of the invitation routes register
	// (deny-by-absence, design §6).
	Invitations InvitationService
	// ListStrategy is the transport-edge DefaultStrategy the list handlers pass
	// to list.ParseRequest (authentication.WithListStrategy).
	ListStrategy list.Strategy
	// Mutation is the browser-safe-mutation policy (design §9.1) applied to
	// cookie-authenticated sensitive routes.
	Mutation MutationSecurity
	// Views is the optional HTML rendering port (design §9.2): nil → only the
	// JSON API mounts.
	Views Views
	// HTMLPolicy is the optional HTML resource policy (design §9.2, GOTH-0.4):
	// nil → the historical asset-free CSP.
	HTMLPolicy *HTMLResourcePolicy
	// MachineGate is the host authorization the machine-identity lifecycle
	// routes run behind (authentication.AdministrationConfig.MachineRoutesGate). Nil → NONE of
	// them mount (deny-by-absence); key authentication is unaffected.
	MachineGate web.Middleware
	// RouteAuth carries the host's resolved AUTHENTICATION posture for the
	// bundled route groups (authentication.BrowserConfig.BundledRouteAuth, resolved to
	// concrete middleware at NewService). A nil field takes that group's audited
	// default below, so a host overrides one semantic surface without restating
	// the others.
	RouteAuth RouteAuthentication
	// LivenessFloorRelaxed lowers the sensitive groups' liveness floor from
	// Live() to Stateless() (authentication.BrowserConfig.
	// BundledRouteAuthLivenessFloorRelaxed).
	LivenessFloorRelaxed bool
	// Proxies logs the one production trusted-proxy warning; nil outside
	// production.
	Proxies *proxyPosture
	// Eraser drives POST /auth/admin/users/{id}/erase. Nil unless the host
	// opted in (WithUserErasureRoute) and the service's erasure is wired.
	Eraser userEraser
	// SecurityEvents drives GET /auth/admin/security-events. Nil unless the host
	// opted in (WithSecurityEventsRoute) and the service's repository is wired.
	SecurityEvents securityEventReader
	// InvitationSync drives POST /auth/invitations/sync; nil leaves it unmounted.
	InvitationSync invitationSyncer
	// ClaimIdentifiers keys GET /auth/invitations/mine on claim-holding
	// identifiers; nil falls back to ActiveVerifiedIdentifier.
	ClaimIdentifiers claimIdentifierLister
	// AdministrationMFA requires AAL2 of every caller UserAdminCheck admits on
	// /auth/admin/users* (WithAdministrationMFA).
	AdministrationMFA bool
	// RouteLimits are the per-client-IP budgets of the public routes; a zero
	// field takes its default at mount.
	RouteLimits PublicRouteLimits
	// Passkeys is the service's passkey capability when it implements one and
	// has a relying party wired; nil → none of the passkey routes mount.
	Passkeys passkeyService
	// PasskeyViews renders the passkey pages; nil → the passkey routes are
	// JSON-only.
	PasskeyViews PasskeyViews
	// TOTP is the service's TOTP capability when it implements one and has TOTP
	// wired; nil → none of the TOTP routes mount.
	TOTP totpService
	// TOTPViews renders the TOTP pages; nil → the TOTP routes are JSON-only.
	TOTPViews TOTPViews
	// GlobalRevoker drives POST /auth/admin/sessions/revoke-all. Nil unless
	// the host opted in (WithGlobalSessionRevocationRoute) and the service's
	// global revocation is wired.
	GlobalRevoker globalSessionRevoker
	// AccountStatus is the service-account status when the service wires it;
	// nil leaves the disable/enable routes unmounted.
	AccountStatus serviceAccountStatusManager
	// KeyFilter is the service's filtered key listing; nil answers a filtered
	// GET /auth/service-accounts/{id}/keys with 400 filter_unsupported.
	KeyFilter apiKeyFilterLister
	// Registration is the service's IdentityConfig.Registration.
	Registration authlogic.RegistrationMode
}

// RouteAuthentication is the authenticator each bundled route group runs behind
// — one middleware per SEMANTIC surface, not per route, because the posture is a
// property of what the surface does (read a session's security inventory, mutate
// a human credential, administer machines) rather than of its path. A nil field
// means "the audited default", which withDefaults materializes from the service.
type RouteAuthentication struct {
	OAuthLinkStart       web.Middleware
	SessionSecurityReads web.Middleware
	CSRFBootstrap        web.Middleware
	SessionHydration     web.Middleware
	CredentialManagement web.Middleware
	MachineLifecycle     web.Middleware
	UserAdministration   web.Middleware
	Invitations          web.Middleware
	BrowserAccount       web.Middleware
}

// withDefaults fills every unset group with its audited default posture. This is
// the ONE place the bundled route audit is expressed, so a default and the route
// it guards can never drift apart:
//
//   - OAuthLinkStart — Accept(CredentialAccessToken), FirstParty(): an API key
//     or an external token cannot start a human OAuth link, but either
//     transport may carry the person's token;
//   - SessionSecurityReads — Accept(CredentialAccessToken), FirstParty(),
//     Live(): explicitly established-session surfaces (the masked credential
//     inventory, the delivery-status read);
//   - CSRFBootstrap — Live(), every wired kind: a provider-cookie user must be
//     able to obtain the double-submit token every bundled cookie mutation needs;
//   - SessionHydration — Live(), every wired kind: preserves the documented
//     act-as-user-key hydration and hydrates external users (a self-acting
//     service account still fails the handler's CurrentUser requirement);
//   - CredentialManagement — Accept(CredentialAccessToken), FirstParty(), Live():
//     these mutate or prove a HUMAN credential and several bind work to
//     CurrentSessionID, so neither an API key nor an external token (which has
//     no native session, so no step-up) substitutes for a session;
//   - MachineLifecycle — Accept(CredentialAccessToken), FirstParty(), Live(): a
//     key never creates, reads, mints, or revokes keys;
//   - UserAdministration — Live(), every wired kind: a machine principal
//     deliberately REACHES WithUserAdminCheck, and the host decides;
//   - Invitations — Live(), every wired kind: an act-as-user key acts as its
//     effective human principal, while a self-acting service account fails the
//     handlers' CurrentUser requirement;
//   - BrowserAccount — cookie-only, live, redirecting on denial: the bundled
//     browser UI reads only its own cookie and never a header.
//
// Every group runs inside a floor over the default credential set, so it
// admits whatever kinds are wired (external included) while denying delegated
// tokens. A host override owns the credential set and transports; the liveness
// floor of a sensitive group — SessionHydration, SessionSecurityReads,
// CredentialManagement, MachineLifecycle, UserAdministration — is
// RequirePrincipal(Live()) unless relaxed, so an override lowered to
// Stateless() or LiveOnMutation() still proves a live session there (one
// session read per call; the default postures reuse their own Live() read).
// OAuthLinkStart, CSRFBootstrap, Invitations and BrowserAccount, and every
// group when relaxed, keep the RequirePrincipal(Stateless()) floor.
func (a RouteAuthentication) withDefaults(svc authService, relaxed bool) RouteAuthentication {
	if a.OAuthLinkStart == nil {
		a.OAuthLinkStart = svc.RequirePrincipal(Accept(CredentialAccessToken), FirstParty())
	}
	if a.SessionSecurityReads == nil {
		a.SessionSecurityReads = svc.RequirePrincipal(Accept(CredentialAccessToken), FirstParty(), Live())
	}
	if a.CSRFBootstrap == nil {
		a.CSRFBootstrap = svc.RequirePrincipal(Live())
	}
	if a.SessionHydration == nil {
		a.SessionHydration = svc.RequirePrincipal(Live())
	}
	if a.CredentialManagement == nil {
		a.CredentialManagement = svc.RequirePrincipal(Accept(CredentialAccessToken), FirstParty(), Live())
	}
	if a.MachineLifecycle == nil {
		a.MachineLifecycle = svc.RequirePrincipal(Accept(CredentialAccessToken), FirstParty(), Live())
	}
	if a.UserAdministration == nil {
		a.UserAdministration = svc.RequirePrincipal(Live())
	}
	if a.Invitations == nil {
		a.Invitations = svc.RequirePrincipal(Live())
	}
	if a.BrowserAccount == nil {
		a.BrowserAccount = svc.RequirePrincipal(
			Accept(CredentialAccessToken),
			FirstParty(),
			Transports(authlogic.TransportCookie),
			Live(),
			Browser(),
		)
	}
	// Bundled authentication/account routes cannot become OAuth resource routes
	// through a host override. Preserve the existing API-key policy seams while
	// requiring verified first-party/key proof after every configured strategy.
	stateless := svc.RequirePrincipal(Stateless())
	live := stateless
	if !relaxed {
		live = svc.RequirePrincipal(Live())
	}
	protect := func(strategy, floor web.Middleware) web.Middleware {
		return func(next http.Handler) http.Handler { return strategy(floor(next)) }
	}
	a.OAuthLinkStart = protect(a.OAuthLinkStart, stateless)
	a.SessionSecurityReads = protect(a.SessionSecurityReads, live)
	a.CSRFBootstrap = protect(a.CSRFBootstrap, stateless)
	a.SessionHydration = protect(a.SessionHydration, live)
	a.CredentialManagement = protect(a.CredentialManagement, live)
	a.MachineLifecycle = protect(a.MachineLifecycle, live)
	a.UserAdministration = protect(a.UserAdministration, live)
	a.Invitations = protect(a.Invitations, stateless)
	a.BrowserAccount = protect(a.BrowserAccount, stateless)
	return a
}

// Mount registers the auth pocket's routes on the registrar. The route surface
// is POST /auth/{register,login,verify,refresh,logout,password/forgot,
// password/reset} plus the credential-management-gated POST /auth/password/change.
// /auth/refresh and /auth/logout are credential-driven, not middleware-gated
// (§1.3/§1.5).
//
// It wraps the registrar so the client-info middleware rides EVERY route,
// unauthenticated ones included (design §5.1 WI4): the ONE blanket write point
// that stamps the request IP + User-Agent onto the context for login's
// rate-limit key and the security-event audit rows. A second wrap prepends
// web.NoStore() to every route, so no /auth response — a token-bearing JSON
// arm, an HTML page or a gate's rejection — is retained by any cache.
//
// The JSON API route surface is registered unconditionally. The optional HTML GET
// surface (design §9.2) is registered only when a Views port is wired (views !=
// nil): mountHTML adds the HTML pages while the JSON contracts stay byte-compatible.
// A nil views leaves the pocket API-only, uniformly.
func mount(r pockets.RouteRegistrar, d mountDeps) {
	svc, inv, views := d.Auth, d.Invitations, d.Views
	// The bundled route groups' authenticators: the host's override where it set
	// one (authentication.BrowserConfig.BundledRouteAuth), the audited default otherwise.
	auth := d.RouteAuth.withDefaults(svc, d.LivenessFloorRelaxed)
	limits := d.RouteLimits.withDefaults()
	r = clientInfoRegistrar{inner: noStoreRegistrar{inner: r}, proxies: d.Proxies}
	h := &handlers{oauth2: d.OAuth2, inviteCheck: d.InviteCheck, resourceRule: d.ResourceRule, userAdminCheck: d.UserAdminCheck, adminMFA: d.AdministrationMFA, eraser: d.Eraser, events: d.SecurityEvents, syncer: d.InvitationSync, claims: d.ClaimIdentifiers, svc: svc, inv: inv, listStrategy: d.ListStrategy, mutation: d.Mutation, views: views, htmlPolicy: d.HTMLPolicy,
		passkeys: d.Passkeys, passkeyViews: d.PasskeyViews, totp: d.TOTP, totpViews: d.TOTPViews,
		machineMFA:    d.AdministrationMFA && (d.Passkeys != nil || d.TOTP != nil),
		globalRevoker: d.GlobalRevoker, accountStatus: d.AccountStatus, keyFilter: d.KeyFilter, registration: d.Registration}
	if d.OAuth2 != nil {
		mountOAuth2(r, h)
		mountSessionManagement(r, h)
	}
	// The password credential's routes register only when the posture is on
	// (deny-by-absence, like machine identity and the token endpoint): a
	// PasswordFlowsDisabled host answers 404 for every one of them.
	pw := svc.PasswordFlowsEnabled()
	// IdentityConfig.Registration closed unmounts registration; verify and
	// resend stay for accounts already awaiting verification.
	if pw && h.registration != authlogic.RegistrationClosed {
		r.Handle("POST", "/auth/register", h.register, svc.rateLimitByIP("register", limits.Register))
	}
	if pw {
		r.Handle("POST", "/auth/login", h.login, requireBrowserSafeOrigin(h.mutation.csrf()))
	}
	if pw {
		r.Handle("POST", "/auth/verify", h.verify, svc.rateLimitByIP("verify", limits.Verify))
	}
	if pw {
		r.Handle("POST", "/auth/password/forgot", h.forgotPassword)
	}
	// The public registration-verification resend (CHAU-2.3) is an UNAUTHENTICATED
	// credential-establishment endpoint, exactly like the passwordless starts: it
	// carries the allowlisted-Origin gate but NOT the double-submit CSRF gate,
	// because the population it serves — users who cannot log in because they never
	// verified — has no session and therefore no __Host-auth_csrf cookie to compare.
	// The gate is added below with the other origin-gated routes, once the shared
	// middleware is built.
	if pw {
		r.Handle("POST", "/auth/password/reset", h.resetPassword, svc.rateLimitByIP("password_reset", limits.PasswordReset))
	}
	// /auth/refresh is rate-limited by IP (the by-session arm is enforced in the
	// service once the session resolves — §6). It is cookie- or body-driven and
	// not credential-gated: rotation IS the credential.
	r.Handle("POST", "/auth/refresh", h.refresh, requireBrowserSafeOrigin(h.mutation.csrf()), svc.rateLimitByIP("refresh", limits.Refresh))
	// /auth/logout is NOT session-gated (§1.5): an expired access JWT must still be
	// able to log out, so gating it on a live credential would make logout a no-op
	// exactly when the shared-computer hazard matters most. It IS cookie-driven,
	// though, so it carries the ORIGIN-ONLY browser gate (requireBrowserSafeOrigin,
	// design §9.1): a same-origin browser passes and a same-site sibling is rejected,
	// while a native/bearer client sending neither Origin nor Sec-Fetch-Site passes.
	// The double-submit CSRF token is deliberately NOT required here — an
	// expired-session logout has no live __Host-auth_csrf cookie to double-submit, so a hard
	// double-submit gate would break exactly the shared-computer logout §1.5 protects.
	r.Handle("POST", "/auth/logout", h.logout, requireBrowserSafeOrigin(h.mutation.csrf()))
	// /auth/password/change is a sensitive route: the CredentialManagement
	// authenticator revokes immediately (§1.4) rather than admitting a stateless
	// token for up to AccessTokenTTL. Like every other cookie-authenticated
	// credential mutation it also carries the browser-safe gate (allowlisted Origin
	// + double-submit CSRF, design §9.1); bearer-only API callers skip the gate. The
	// gate is added below once browserSafe is built so the change route reaches
	// parity with set/remove-password.
	// /auth/delivery/status is a session-security read (design §6.1.1): a
	// session-gated caller polls the durable outbox with its receipt to learn that
	// delivery failed without holding the start request open.
	r.Handle("GET", "/auth/delivery/status", h.deliveryStatus, auth.SessionSecurityReads)
	// /auth/methods is the masked method inventory (design §5.1): it returns a
	// human's credential/contact inventory, so it is a session-security read — an
	// API key is refused and a revoked access JWT denies within one round-trip. It
	// is a bearer-safe GET read with no request body, so it skips the
	// browser-safe-mutation CSRF gate (which guards state changes); the handler sets
	// Cache-Control: no-store. It subsumes and replaces GET /auth/oauth/linked
	// (removed, pre-tag route break — design §9).
	r.Handle("GET", "/auth/methods", h.methods, auth.SessionSecurityReads)
	// /auth/csrf is the JSON double-submit bootstrap (upstream evidence §1a): a
	// cookie-authenticated SPA served from a DIFFERENT origin than the API cannot
	// read the API-origin __Host-auth_csrf cookie, so it reads the matching token from
	// this body instead and echoes it in X-CSRF-Token. It rides its own
	// CSRFBootstrap group (only a live authenticated caller bootstraps, a
	// provider-cookie user included; a credential-establishment endpoint needs no
	// token) plus the ORIGIN-ONLY
	// browser gate — the double-submit gate cannot protect the very endpoint that
	// hands out the token. The handler sets Cache-Control: no-store and reuses an
	// existing well-formed token rather than rotating another tab's out from under
	// it. The host must allow X-CSRF-Token in its CORS request-header policy
	// (web.CORSWithConfig) for the browser to send the echo header.
	r.Handle("GET", "/auth/csrf", h.csrfBootstrap, auth.CSRFBootstrap, requireBrowserSafeOrigin(h.mutation.csrf()))
	// /auth/me is session hydration: the cookie-authenticated client that cannot
	// read its own session asks who it is signed in as, and gets the SAME
	// userResponse body login and register return. Hydration intentionally pays one
	// revocation lookup (ruling 6) so a revoked access JWT cannot hydrate, and it
	// keeps admitting an act-as-user API key, which hydrates as its human owner.
	// Like /auth/methods it is a bearer-safe GET with no body, so it skips the
	// browser-safe-mutation CSRF gate; the handler sets Cache-Control: no-store and
	// denies a machine principal (see me).
	r.Handle("GET", "/auth/me", h.me, auth.SessionHydration)

	// Step-up (recent-authentication grant) routes (design §5.0). Each is a
	// cookie-authenticated sensitive mutation: the CredentialManagement authenticator
	// proves revocation state and stamps the session id, and the browser-safe-mutation gate adds the
	// allowlisted-Origin + double-submit CSRF protection (design §9.1). The handlers
	// themselves enforce the strict JSON body and set Cache-Control: no-store.
	browserSafe := requireBrowserSafeMutation(h.mutation.csrf())
	browserSafeForm := requireBrowserSafeFormMutation(h.mutation.csrf())
	if pw {
		r.Handle("POST", "/auth/verification/resend", h.resendVerification, requireBrowserSafeOrigin(h.mutation.csrf()))
	}
	if pw {
		r.Handle("POST", "/auth/password/change", h.changePassword, auth.CredentialManagement, browserSafeForm)
	}
	r.Handle("POST", "/auth/step-up/begin", h.beginStepUp, auth.CredentialManagement, browserSafeForm)
	if pw {
		r.Handle("POST", "/auth/step-up/password", h.completeStepUpPassword, auth.CredentialManagement, browserSafeForm)
	}
	r.Handle("POST", "/auth/step-up/code", h.completeStepUpCode, auth.CredentialManagement, browserSafeForm)

	// Credential-suite password routes (design §5.2/§5.3). Each is a
	// cookie-authenticated sensitive mutation gated by the CredentialManagement
	// authenticator (immediate revocation, human credential only) plus the
	// browser-safe-mutation Origin/CSRF gate; the handlers add
	// strict JSON hardening and Cache-Control: no-store. /auth/password/set consumes a
	// set_password grant; the remove pair delivers a remove_password code to a verified
	// recovery identifier and completes through the revision-serialized credential rail.
	if pw {
		r.Handle("POST", "/auth/password/set", h.setPassword, auth.CredentialManagement, browserSafeForm)
	}
	if pw {
		r.Handle("POST", "/auth/password/remove/start", h.startRemovePassword, auth.CredentialManagement, browserSafeForm)
	}
	if pw {
		r.Handle("POST", "/auth/password/remove", h.removePassword, auth.CredentialManagement, browserSafeForm)
	}

	// Identifier-management routes (design §5.5). Each is a cookie-authenticated
	// sensitive mutation gated by the CredentialManagement authenticator (immediate
	// revocation, human credential only) plus the
	// browser-safe-mutation Origin/CSRF gate; the handlers add strict JSON hardening
	// and Cache-Control: no-store. The add/change start delivers an ownership-proof
	// code to the proposed NEW address and the confirm applies the verified change;
	// PATCH/DELETE route through the policy-guarded revision-serialized credential rail.
	r.Handle("POST", "/auth/identifiers/email", h.startEmailIdentifier, auth.CredentialManagement, browserSafeForm)
	r.Handle("POST", "/auth/identifiers/email/confirm", h.confirmEmailIdentifier, auth.CredentialManagement, browserSafeForm)
	r.Handle("POST", "/auth/identifiers/phone", h.startPhoneIdentifier, auth.CredentialManagement, browserSafeForm)
	r.Handle("POST", "/auth/identifiers/phone/confirm", h.confirmPhoneIdentifier, auth.CredentialManagement, browserSafeForm)
	r.Handle("PATCH", "/auth/identifiers/{id}", h.patchIdentifier, auth.CredentialManagement, browserSafe)
	r.Handle("DELETE", "/auth/identifiers/{id}", h.deleteIdentifier, auth.CredentialManagement, browserSafe)

	// The administrative user surface (CHAU-1.6) is registered only when the host
	// made an explicit authorization decision AND the capability is wired
	// (deny-by-absence). Repository presence alone is NOT enough: a bundled store
	// adapter returns the administration ports for every host, and turning them
	// into an HTTP surface must be the host's choice, not the store's. Package
	// auth already rejected a check wired without the repositories
	// (ErrUserAdminReposRequired), so reaching here with one and not the other is
	// impossible — the belt-and-braces conjunction keeps the invariant local.
	if svc.UserAdminEnabled() && d.UserAdminCheck != nil {
		mountUserAdmin(r, h, auth.UserAdministration, browserSafe)
		// Erasure is irreversible, so it mounts only on its own explicit opt-in:
		// a host whose UserAdminCheck ignores Action must not receive a
		// destructive route by upgrading.
		if d.Eraser != nil {
			r.Handle("POST", "/auth/admin/users/{id}/erase", h.adminEraseUser, auth.UserAdministration, browserSafe)
		}
		// The security-event listing is an explicit opt-in too: a host whose
		// UserAdminCheck ignores Action must not expose the rail by upgrading.
		if d.SecurityEvents != nil {
			r.Handle("GET", "/auth/admin/security-events", h.adminListSecurityEvents, auth.UserAdministration)
		}
		// The fleet-wide revoke is an explicit opt-in, behind its own
		// authenticator: a first-party access token or an external token, Live —
		// whatever a host overrides UserAdministration to admit, an API key (which
		// the revoke leaves standing) never fires it, and a delegated token, which
		// names no audience here, is refused.
		if d.GlobalRevoker != nil {
			r.Handle("POST", "/auth/admin/sessions/revoke-all", h.adminRevokeAllSessions, svc.RequirePrincipal(Accept(CredentialAccessToken, CredentialExternalToken), Live()), browserSafe)
		}
		if d.Passkeys != nil {
			r.Handle("POST", "/auth/admin/users/{id}/passkeys/reset", h.adminResetPasskeys, auth.UserAdministration, browserSafe)
		}
		if d.TOTP != nil {
			r.Handle("POST", "/auth/admin/users/{id}/totp/reset", h.adminResetTOTP, auth.UserAdministration, browserSafe)
		}
	}

	// Passkey routes (SOC 2 S4b) mount only when the service wires a relying
	// party (deny-by-absence); the pages additionally need PasskeyViews.
	if d.Passkeys != nil {
		mountPasskeys(r, h, auth, browserSafe, requireBrowserSafeOrigin(h.mutation.csrf()), svc.rateLimitByIP("passkey", limits.Passkey))
		if d.PasskeyViews != nil {
			mountPasskeyHTML(r, h, auth.BrowserAccount, browserSafeForm)
		}
	}

	// TOTP routes (SOC 2 S4c) mount only when the service wires TOTP
	// (deny-by-absence); the pages additionally need TOTPViews.
	if d.TOTP != nil {
		mountTOTP(r, h, auth, browserSafe, browserSafeForm)
		if d.TOTPViews != nil {
			mountTOTPHTML(r, h, auth.BrowserAccount, browserSafeForm)
		}
	}

	// OAuth routes are registered only when at least one provider is wired
	// (deny-by-absence, design §3): an unwired host returns 404 for them.
	if svc.OAuthEnabled() {
		mountOAuth(r, h, auth.OAuthLinkStart, auth.CredentialManagement, browserSafeForm,
			svc.rateLimitByIP("verify_link", limits.VerifyLink), svc.rateLimitByIP("oauth_native", limits.OAuthNative))
	}

	// Machine-identity lifecycle routes are registered only when both machine
	// repositories are wired AND the host named an authorization gate
	// (deny-by-absence, design §4.1 + D1): they issue and revoke credentials, so
	// the pocket never guesses a policy. A nil gate leaves them unmounted (404)
	// while key AUTHENTICATION keeps working, and authentication.New WARNs about the
	// posture at boot. A host that wants its own lifecycle surface leaves the
	// gate nil and serves it over the Service methods.
	if svc.MachineEnabled() && d.MachineGate != nil {
		mountMachine(r, h, auth.MachineLifecycle, browserSafe, d.MachineGate)
	}

	// The bearer-JWT token endpoint is registered only when a TokenSigner is
	// wired (deny-by-absence, design §4.4); an unwired host returns 404 for it.
	if svc.TokenEnabled() {
		r.Handle("POST", "/auth/token", h.token)
	}

	// Passwordless login routes are registered only when the host enabled at least
	// one passwordless kind (deny-by-absence, design §4.2); an unwired host returns
	// 404 for the entire surface.
	if svc.PasswordlessEnabled() {
		mountPasswordless(r, h)
	}

	// Invitation routes are registered only when a Granter is wired (deny-by-
	// absence, design §6): an unwired host returns 404 for the entire surface.
	// Every authenticated invitation route rides the Invitations authenticator
	// (design §6/D3): a revoked session's outstanding access JWT cannot create,
	// list, read-mine, accept, cancel, or resend an invitations. User-only semantics
	// are preserved by the handlers, which resolve CurrentUser (Type=user) and
	// reject a service-account principal — the authenticator admits an act-as-user
	// API key, which acts as its human owner, so the invitation surface is NOT a
	// service-account administration API. Decline stays public and IP-rate-limited.
	if inv != nil {
		mountInvitations(r, h, auth.Invitations, svc.rateLimitByIP("invitation_decline", limits.InvitationDecline))
	}

	// The optional HTML GET surface is registered only when a Views port is wired
	// (design §9.2): with a nil Views the pocket is API-only and every HTML page
	// path 404s, while the JSON API above stays fully mounted. Account-security HTML
	// pages ride the BrowserAccount authenticator: cookie-only and live, redirecting
	// on denial instead of leaking a JSON 401 onto an HTML page.
	if views != nil {
		mountHTML(r, h, auth.BrowserAccount, browserSafeForm)
	}
}

// clientInfoRegistrar wraps a RouteRegistrar so the client-info middleware is
// prepended to EVERY route it registers — the single blanket write point for the
// request's IP + User-Agent (design §5.1 WI4). Being outermost, it runs before
// any auth middleware, so unauthenticated routes (failed login, register, the
// OAuth callback) are attributed too.
type clientInfoRegistrar struct {
	inner pockets.RouteRegistrar
	// proxies, when set, logs the one production trusted-proxy warning.
	proxies *proxyPosture
}

func (c clientInfoRegistrar) Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	all := append([]web.Middleware{clientInfoMiddleware(c.proxies)}, middleware...)
	c.inner.Handle(method, path, handler, all...)
}

// noStoreRegistrar wraps a RouteRegistrar so web.NoStore() is prepended to
// every route it registers: Cache-Control: no-store is set before any gate or
// handler runs, so a rejection carries it too. A handler that writes its own
// no-store (or adds Pragma) is unaffected.
type noStoreRegistrar struct {
	inner pockets.RouteRegistrar
}

func (n noStoreRegistrar) Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	all := append([]web.Middleware{web.NoStore()}, middleware...)
	n.inner.Handle(method, path, handler, all...)
}

// ClientInfoMiddleware stamps every request's client IP and User-Agent onto
// the pocket's client-info carrier, so a host route's audit rows — a denial
// recorded through a host bridge, an external_login row — and RateLimitByIP
// see the client. It is a stamp, not a gate: it admits every request. The IP
// is web.ClientIP when web.TrustProxies ran, else the peer address — never a
// raw X-Forwarded-For — so the host installs it with router.Use AFTER the
// TrustProxies Use, once the adapter exists. The pocket's own routes carry the
// stamp regardless.
func (a *Adapter) ClientInfoMiddleware() web.Middleware {
	return clientInfoMiddleware(a.proxies)
}

// clientInfoMiddleware stamps the request IP and User-Agent onto the context via
// the pocket's exported carrier (authlogic.WithClientInfo). It is the ONE write
// point; login/token read the IP from it and the audit rail reads IP + UA. Each
// request is first shown to proxies, which may log the production warning.
func clientInfoMiddleware(proxies *proxyPosture) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proxies.observe(r)
			ctx := authlogic.WithClientInfo(r.Context(), clientIP(r), r.UserAgent())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// proxyPosture logs one production WARN the first time a request shows that
// client IPs are not being attributed: web.TrustProxies did not run, or the
// request carries X-Forwarded-For yet its client IP resolved to the peer
// address (TrustProxies(0) behind a proxy). Every rate limit and audit row would
// then key to the proxy. A request carrying only X-Real-IP whose client IP is
// the peer logs its own one WARN naming web.TrustProxiesConfig.TrustRealIPHeader.
// A nil *proxyPosture (development, or a route table mounted without the
// adapter) observes nothing.
type proxyPosture struct {
	log        *slog.Logger
	once       sync.Once
	realIPOnce sync.Once
}

// newProxyPosture returns the production observer, or nil in any other mode.
func newProxyPosture(mode environment.Mode, log *slog.Logger) *proxyPosture {
	if !mode.IsProduction() {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &proxyPosture{log: log}
}

func (p *proxyPosture) observe(r *http.Request) {
	if p == nil {
		return
	}
	ip, resolved := web.ClientIP(r.Context())
	switch {
	case !resolved:
		p.once.Do(func() {
			p.log.Warn("authentication: web.TrustProxies is not wired, so client IPs are the peer address; behind a proxy every rate limit and audit row keys to the proxy — wire web.TrustProxies outermost")
		})
	case r.Header.Get("X-Forwarded-For") != "" && ip == peerAddress(r):
		p.once.Do(func() {
			p.log.Warn("authentication: a request carried X-Forwarded-For but its client IP resolved to the peer address; behind a proxy set web.TrustProxies to the proxy hop count (TRUSTED_PROXY_COUNT), or every rate limit and audit row keys to the proxy")
		})
	case r.Header.Get("X-Real-IP") != "" && r.Header.Get("X-Forwarded-For") == "" && ip == peerAddress(r):
		p.realIPOnce.Do(func() {
			p.log.Warn("authentication: a request carried X-Real-IP but no X-Forwarded-For and its client IP resolved to the peer address; X-Real-IP is ignored unless web.TrustProxiesWithConfig sets web.TrustProxiesConfig.TrustRealIPHeader (with TrustedProxyCount >= 1), so every rate limit and audit row keys to the proxy")
		})
	}
}

// clientIP derives the client IP. The TrustProxies-resolved IP wins when the
// host wired web.TrustProxies (web.ClientIP present) — the correct, spoof-safe
// source. Absent that trusted-proxy context it falls back to RemoteAddr and
// NEVER trusts a raw X-Forwarded-For header: a client can forge that header to
// rotate rate-limit keys or poison audit rows, so trusted-proxy configuration
// is the only source of forwarded client IPs (design §9.1). Wire
// web.TrustProxies to attribute forwarded requests.
func clientIP(r *http.Request) string {
	if ip, ok := web.ClientIP(r.Context()); ok {
		return ip
	}
	return peerAddress(r)
}

// peerAddress is the host part of RemoteAddr, or RemoteAddr when it has none.
func peerAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
