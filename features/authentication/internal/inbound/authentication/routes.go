// Package authentication is the auth feature's JSON transport: request/response DTOs, the
// handlers over the domain service, and the route table. v1 is JSON-API only
// (no server-rendered views), so a host that wants login pages renders its own
// form and calls these endpoints, exactly as a SPA or mobile client would.
// Mounted only through feature.RouteRegistrar (see auth.Register).
package authentication

import (
	"net"
	"net/http"
	"strings"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// handlers holds the services the route handlers delegate to. inv is nil when no
// Granter is wired (invitations off); its routes are then never registered.
// listStrategy is the transport-edge DefaultStrategy the list handlers pass to
// crud.ParseListRequest (host-configured via authentication.Config.ListStrategy).
type handlers struct {
	svc          authService
	inv          InvitationService
	listStrategy crud.Strategy
}

// Mount registers the auth feature's routes on the registrar. The route surface
// is POST /auth/{register,login,verify,password/forgot,password/reset} plus the
// session-gated POST /auth/logout and POST /auth/password/change.
//
// It wraps the registrar so the client-info middleware rides EVERY route,
// unauthenticated ones included (design §5.1 WI4): the ONE blanket write point
// that stamps the request IP + User-Agent onto the context for login's
// rate-limit key and the security-event audit rows.
func Mount(r feature.RouteRegistrar, svc authService, inv InvitationService, listStrategy crud.Strategy) {
	r = clientInfoRegistrar{inner: r}
	h := &handlers{svc: svc, inv: inv, listStrategy: listStrategy}
	r.Handle("POST", "/auth/register", h.register)
	r.Handle("POST", "/auth/login", h.login)
	r.Handle("POST", "/auth/verify", h.verify)
	r.Handle("POST", "/auth/password/forgot", h.forgotPassword)
	r.Handle("POST", "/auth/password/reset", h.resetPassword)
	r.Handle("POST", "/auth/logout", h.logout, svc.RequireUser)
	r.Handle("POST", "/auth/password/change", h.changePassword, svc.RequireUser)

	// OAuth routes are registered only when at least one provider is wired
	// (deny-by-absence, design §3): an unwired host returns 404 for them.
	if svc.OAuthEnabled() {
		mountOAuth(r, h, svc.RequireUser)
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

	// Invitation routes are registered only when a Granter is wired (deny-by-
	// absence, design §6): an unwired host returns 404 for the entire surface.
	if inv != nil {
		mountInvitations(r, h, svc.RequireUser, svc.RateLimitByIP("invitation_decline", declineAttemptsPerMinute))
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

// clientIP derives the client IP, preferring the first X-Forwarded-For hop and
// falling back to the request's remote address.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
