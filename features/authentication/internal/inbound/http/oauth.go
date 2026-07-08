package http

import (
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// defaultRedirect is the same-origin fallback the callback redirects to when a
// flow carried no validated destination.
const defaultRedirect = "/"

// verifyLinkRequest is the body of POST /auth/oauth/verify-link.
type verifyLinkRequest struct {
	Token string `json:"token"`
}

// linkedAccountResponse is one entry in GET /auth/oauth/linked.
type linkedAccountResponse struct {
	Provider      string `json:"provider"`
	ProviderEmail string `json:"provider_email"`
	LinkedAt      string `json:"linked_at"`
}

func newLinkedResponse(accts []oauthaccount.OAuthAccount) []linkedAccountResponse {
	out := make([]linkedAccountResponse, 0, len(accts))
	for _, a := range accts {
		out = append(out, linkedAccountResponse{
			Provider:      a.Provider,
			ProviderEmail: a.ProviderEmail,
			LinkedAt:      a.LinkedAt.Format(time.RFC3339),
		})
	}
	return out
}

// mountOAuth registers the OAuth route surface (design §3). Called from Mount
// only when a provider is wired. The session-gated routes take requireUser.
func mountOAuth(r feature.RouteRegistrar, h *handlers, requireUser web.Middleware) {
	r.Handle("GET", "/auth/oauth/{provider}/start", h.oauthStart)
	r.Handle("GET", "/auth/oauth/{provider}/callback", h.oauthCallback)
	r.Handle("POST", "/auth/oauth/verify-link", h.oauthVerifyLink)
	r.Handle("GET", "/auth/oauth/linked", h.oauthLinked, requireUser)
	r.Handle("GET", "/auth/oauth/{provider}/link/start", h.oauthLinkStart, requireUser)
	r.Handle("DELETE", "/auth/oauth/{provider}/link", h.oauthUnlink, requireUser)
}

// oauthStart redirects the browser to the provider authorization URL.
func (h *handlers) oauthStart(w http.ResponseWriter, r *http.Request) {
	url, err := h.svc.StartOAuth(r.Context(), web.Param(r, "provider"), r.URL.Query().Get("redirect"))
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// oauthCallback processes the provider redirect: it resolves the anti-takeover
// branch, sets a session cookie on login/register, and redirects the browser to
// the flow's validated destination. A pending link (email sent) or a completed
// explicit link redirects without a new cookie.
func (h *handlers) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.svc.OAuthCallback(r.Context(), web.Param(r, "provider"), q.Get("code"), q.Get("state"))
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	if res.Action == authsvc.ActionLogin || res.Action == authsvc.ActionRegister {
		h.svc.SetSessionCookie(w, res.Token)
	}
	http.Redirect(w, r, redirectOrDefault(res.RedirectTo), http.StatusFound)
}

// oauthVerifyLink completes a pending link from the mailed secret and logs the
// user in (fresh session cookie).
func (h *handlers) oauthVerifyLink(w http.ResponseWriter, r *http.Request) {
	var req verifyLinkRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.svc.VerifyLink(r.Context(), req.Token)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookie(w, res.Token)
	web.RespondJSONOK(w, newUserResponse(res.User))
}

// oauthLinked lists the caller's provider links (session-gated).
func (h *handlers) oauthLinked(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	accts, err := h.svc.ListLinked(r.Context(), userID)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newLinkedResponse(accts))
}

// oauthLinkStart begins a session-gated link round-trip for the caller.
func (h *handlers) oauthLinkStart(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	url, err := h.svc.StartLink(r.Context(), userID, web.Param(r, "provider"), r.URL.Query().Get("redirect"))
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// oauthUnlink removes the caller's link to a provider (session-gated), enforcing
// last-authentication-method protection in the service (→ 409).
func (h *handlers) oauthUnlink(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	if err := h.svc.Unlink(r.Context(), userID, web.Param(r, "provider")); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "unlinked"})
}

// redirectOrDefault falls back to the same-origin default when a flow carried no
// destination.
func redirectOrDefault(target string) string {
	if target == "" {
		return defaultRedirect
	}
	return target
}
