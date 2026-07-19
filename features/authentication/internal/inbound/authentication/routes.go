// Package authentication is the auth feature's JSON transport: request/response DTOs, the
// handlers over the domain service, and the route table. v1 is JSON-API only
// (no server-rendered views), so a host that wants login pages renders its own
// form and calls these endpoints, exactly as a SPA or mobile client would.
// Mounted only through feature.RouteRegistrar (see auth.Register).
package authentication

import (
	"net"
	"net/http"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/invitationsvc"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// refreshAttemptsPerMinute caps POST /auth/refresh attempts per client IP (the
// by-IP arm of the §6 refresh rate limit; the by-session arm lives in the
// service). A per-process memory limiter is N× budget across N instances and NAT
// users share an IP bucket — multi-instance hosts wire a shared limiter.
const refreshAttemptsPerMinute = 30

// handlers holds the services the route handlers delegate to. inv is nil when no
// Granter is wired (invitations off); its routes are then never registered.
// listStrategy is the transport-edge DefaultStrategy the list handlers pass to
// crud.ParseListRequest (host-configured via authentication.Config.ListStrategy).
type handlers struct {
	svc authService
	inv InvitationService
	// inviteCheck is the host's relation-aware invitation authorization seam
	// (design §6/D3), invoked by the create/list handlers after live-session
	// validation, principal resolution, and request parsing. It is non-nil exactly
	// when inv is non-nil: package auth requires it at construction whenever a
	// Granter enables invitations (ErrInviteCheckRequired), so a mounted invitation
	// surface always carries a check.
	inviteCheck  invitationsvc.InviteCheck
	listStrategy crud.Strategy
	// mutation is the browser-safe-mutation policy (design §9.1) applied to
	// cookie-authenticated sensitive routes (step-up and, in later phase-6 tasks,
	// credential/identifier management).
	mutation MutationSecurity
	// views is the optional HTML rendering port (design §9.2). Nil → the HTML GET
	// surface is not registered and only the JSON API mounts; non-nil → the HTML GET
	// pages render through it. The core never imports templ; this is a technology-
	// neutral web.Renderer port a host (or the bundled views/templ module) satisfies.
	views Views
	// htmlPolicy is the optional, technology-neutral HTML resource policy (design
	// §9.2, GOTH-0.4) writeHTMLSecurity applies to every HTML page and redirect. Nil
	// → the historical asset-free CSP (script-src nonce-only); non-nil → the fixed
	// protections plus the policy's validated widening resource directives. It only
	// widens; it can never remove a fixed protection.
	htmlPolicy *HTMLResourcePolicy
}

// Mount registers the auth feature's routes on the registrar. The route surface
// is POST /auth/{register,login,verify,refresh,logout,password/forgot,
// password/reset} plus the RequireLiveSession-gated POST /auth/password/change.
// /auth/refresh and /auth/logout are credential-driven, not middleware-gated
// (§1.3/§1.5).
//
// It wraps the registrar so the client-info middleware rides EVERY route,
// unauthenticated ones included (design §5.1 WI4): the ONE blanket write point
// that stamps the request IP + User-Agent onto the context for login's
// rate-limit key and the security-event audit rows.
//
// The JSON API route surface is registered unconditionally. The optional HTML GET
// surface (design §9.2) is registered only when a Views port is wired (views !=
// nil): mountHTML adds the HTML pages while the JSON contracts stay byte-compatible.
// A nil views leaves the feature API-only, uniformly.
func Mount(r feature.RouteRegistrar, svc authService, inv InvitationService, inviteCheck invitationsvc.InviteCheck, listStrategy crud.Strategy, mutation MutationSecurity, views Views, htmlPolicy *HTMLResourcePolicy) {
	r = clientInfoRegistrar{inner: r}
	h := &handlers{svc: svc, inv: inv, inviteCheck: inviteCheck, listStrategy: listStrategy, mutation: mutation, views: views, htmlPolicy: htmlPolicy}
	r.Handle("POST", "/auth/register", h.register)
	r.Handle("POST", "/auth/login", h.login)
	r.Handle("POST", "/auth/verify", h.verify)
	r.Handle("POST", "/auth/password/forgot", h.forgotPassword)
	r.Handle("POST", "/auth/password/reset", h.resetPassword)
	// /auth/refresh is rate-limited by IP (the by-session arm is enforced in the
	// service once the session resolves — §6). It is cookie- or body-driven and
	// not credential-gated: rotation IS the credential.
	r.Handle("POST", "/auth/refresh", h.refresh, svc.RateLimitByIP("refresh", refreshAttemptsPerMinute))
	// /auth/logout is NOT session-gated (§1.5): an expired access JWT must still be
	// able to log out, so gating it on a live credential would make logout a no-op
	// exactly when the shared-computer hazard matters most. It IS cookie-driven,
	// though, so it carries the ORIGIN-ONLY browser gate (requireBrowserSafeOrigin,
	// design §9.1): a same-origin browser passes and a same-site sibling is rejected,
	// while a native/bearer client sending neither Origin nor Sec-Fetch-Site passes.
	// The double-submit CSRF token is deliberately NOT required here — an
	// expired-session logout has no live auth_csrf cookie to double-submit, so a hard
	// double-submit gate would break exactly the shared-computer logout §1.5 protects.
	r.Handle("POST", "/auth/logout", h.logout, requireBrowserSafeOrigin(h.mutation.csrf()))
	// /auth/password/change is a sensitive route: RequireLiveSession revokes
	// immediately (§1.4), not RequireUser's ≤AccessTokenTTL stale window. Like every
	// other cookie-authenticated credential mutation it also carries the browser-safe
	// gate (allowlisted Origin + double-submit CSRF, design §9.1); bearer-only API
	// callers skip the gate. The gate is added below once browserSafe is built so the
	// change route reaches parity with set/remove-password.
	// /auth/delivery/status is the live-session-gated delivery-status read (design
	// §6.1.1): a session-gated caller polls the durable outbox with its receipt to
	// learn that delivery failed without holding the start request open.
	r.Handle("GET", "/auth/delivery/status", h.deliveryStatus, svc.RequireLiveSession)
	// /auth/methods is the live-session-gated masked method inventory (design §5.1):
	// it returns sensitive credential/contact inventory, so RequireLiveSession denies
	// a revoked access JWT within one round-trip. It is a bearer-safe GET read with no
	// request body, so it skips the browser-safe-mutation CSRF gate (which guards
	// state changes); the handler sets Cache-Control: no-store. It subsumes and
	// replaces GET /auth/oauth/linked (removed, pre-tag route break — design §9).
	r.Handle("GET", "/auth/methods", h.methods, svc.RequireLiveSession)

	// Step-up (recent-authentication grant) routes (design §5.0). Each is a
	// cookie-authenticated sensitive mutation: RequireLiveSession proves revocation
	// state and stamps the session id, and the browser-safe-mutation gate adds the
	// allowlisted-Origin + double-submit CSRF protection (design §9.1). The handlers
	// themselves enforce the strict JSON body and set Cache-Control: no-store.
	browserSafe := requireBrowserSafeMutation(h.mutation.csrf())
	r.Handle("POST", "/auth/password/change", h.changePassword, svc.RequireLiveSession, browserSafe)
	r.Handle("POST", "/auth/step-up/begin", h.beginStepUp, svc.RequireLiveSession, browserSafe)
	r.Handle("POST", "/auth/step-up/password", h.completeStepUpPassword, svc.RequireLiveSession, browserSafe)
	r.Handle("POST", "/auth/step-up/code", h.completeStepUpCode, svc.RequireLiveSession, browserSafe)

	// Credential-suite password routes (design §5.2/§5.3). Each is a
	// cookie-authenticated sensitive mutation gated by RequireLiveSession (immediate
	// revocation) plus the browser-safe-mutation Origin/CSRF gate; the handlers add
	// strict JSON hardening and Cache-Control: no-store. /auth/password/set consumes a
	// set_password grant; the remove pair delivers a remove_password code to a verified
	// recovery identifier and completes through the revision-serialized credential rail.
	r.Handle("POST", "/auth/password/set", h.setPassword, svc.RequireLiveSession, browserSafe)
	r.Handle("POST", "/auth/password/remove/start", h.startRemovePassword, svc.RequireLiveSession, browserSafe)
	r.Handle("POST", "/auth/password/remove", h.removePassword, svc.RequireLiveSession, browserSafe)

	// Identifier-management routes (design §5.5). Each is a cookie-authenticated
	// sensitive mutation gated by RequireLiveSession (immediate revocation) plus the
	// browser-safe-mutation Origin/CSRF gate; the handlers add strict JSON hardening
	// and Cache-Control: no-store. The add/change start delivers an ownership-proof
	// code to the proposed NEW address and the confirm applies the verified change;
	// PATCH/DELETE route through the policy-guarded revision-serialized credential rail.
	r.Handle("POST", "/auth/identifiers/email", h.startEmailIdentifier, svc.RequireLiveSession, browserSafe)
	r.Handle("POST", "/auth/identifiers/email/confirm", h.confirmEmailIdentifier, svc.RequireLiveSession, browserSafe)
	r.Handle("POST", "/auth/identifiers/phone", h.startPhoneIdentifier, svc.RequireLiveSession, browserSafe)
	r.Handle("POST", "/auth/identifiers/phone/confirm", h.confirmPhoneIdentifier, svc.RequireLiveSession, browserSafe)
	r.Handle("PATCH", "/auth/identifiers/{id}", h.patchIdentifier, svc.RequireLiveSession, browserSafe)
	r.Handle("DELETE", "/auth/identifiers/{id}", h.deleteIdentifier, svc.RequireLiveSession, browserSafe)

	// OAuth routes are registered only when at least one provider is wired
	// (deny-by-absence, design §3): an unwired host returns 404 for them.
	if svc.OAuthEnabled() {
		mountOAuth(r, h, svc.RequireUser, svc.RequireLiveSession, browserSafe)
	}

	// Machine-identity lifecycle routes are registered only when both machine
	// repositories are wired (deny-by-absence, design §4.1); an unwired host
	// returns 404 for them.
	if svc.MachineEnabled() {
		mountMachine(r, h, svc.RequireUser)
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
	// Every authenticated invitation route rides RequireLiveSession (design §6/D3):
	// a revoked session's outstanding access JWT cannot create, list, read-mine,
	// accept, cancel, or resend an invitation. User-only semantics are preserved by
	// the handlers, which resolve CurrentUser (Type=user) and reject a
	// service-account principal — RequireLiveSession admits an API-key caller that
	// RequireUser's JWT-only gate did not, so the invitation surface is NOT a
	// service-account administration API. Decline stays public and IP-rate-limited.
	if inv != nil {
		mountInvitations(r, h, svc.RequireLiveSession, svc.RateLimitByIP("invitation_decline", declineAttemptsPerMinute))
	}

	// The optional HTML GET surface is registered only when a Views port is wired
	// (design §9.2): with a nil Views the feature is API-only and every HTML page
	// path 404s, while the JSON API above stays fully mounted. Account-security HTML
	// pages ride RequireLiveSession, exactly like their JSON twins.
	if views != nil {
		mountHTML(r, h, browserSafe)
	}
}

// clientInfoRegistrar wraps a RouteRegistrar so the client-info middleware is
// prepended to EVERY route it registers — the single blanket write point for the
// request's IP + User-Agent (design §5.1 WI4). Being outermost, it runs before
// any auth middleware, so unauthenticated routes (failed login, register, the
// OAuth callback) are attributed too.
type clientInfoRegistrar struct {
	inner feature.RouteRegistrar
}

func (c clientInfoRegistrar) Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	all := append([]web.Middleware{clientInfoMiddleware}, middleware...)
	c.inner.Handle(method, path, handler, all...)
}

// clientInfoMiddleware stamps the request IP and User-Agent onto the context via
// the feature's exported carrier (authsvc.WithClientInfo). It is the ONE write
// point; login/token read the IP from it and the audit rail reads IP + UA.
func clientInfoMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := authsvc.WithClientInfo(r.Context(), clientIP(r), r.UserAgent())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
