package authentication

import (
	"net/http"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// defaultRedirect is the same-origin fallback the callback redirects to when a
// flow carried no validated destination.
const defaultRedirect = "/"

// verifyLinkRequest is the body of POST /auth/oauth/verify-link.
type verifyLinkRequest struct {
	Token string `json:"token"`
}

// unlinkStartRequest starts the code-gated OAuth unlink; it carries no fields (the
// destination is the account's verified recovery identifier, chosen by policy, and
// the provider is a path parameter).
type unlinkStartRequest struct{}

// unlinkRequest completes the unlink with the delivered provider-bound unlink_oauth
// code.
type unlinkRequest struct {
	Code string `json:"code"`
}

// mountOAuth registers the OAuth route surface (design §3). Called from Mount
// only when a provider is wired. The link-start route takes requireUser; the
// code-gated unlink pair (design §5.4) is a sensitive mutation gated by
// liveSession (immediate revocation) plus the browser-safe-mutation Origin/CSRF
// gate, replacing the plain DELETE /auth/oauth/{provider}/link (pre-tag route
// break). The caller's link inventory is no longer a route here: GET
// /auth/oauth/linked is subsumed by the masked GET /auth/methods (design §5.1).
func mountOAuth(r feature.RouteRegistrar, h *handlers, requireUser, liveSession, browserSafe web.Middleware) {
	r.Handle("GET", "/auth/oauth/{provider}/start", h.oauthStart)
	r.Handle("GET", "/auth/oauth/{provider}/callback", h.oauthCallback)
	r.Handle("POST", "/auth/oauth/verify-link", h.oauthVerifyLink)
	r.Handle("GET", "/auth/oauth/{provider}/link/start", h.oauthLinkStart, requireUser)
	r.Handle("POST", "/auth/oauth/{provider}/unlink/start", h.startUnlinkOAuth, liveSession, browserSafe)
	r.Handle("POST", "/auth/oauth/{provider}/unlink", h.unlinkOAuth, liveSession, browserSafe)
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
		h.svc.SetSessionCookies(w, authsvc.TokenPair{AccessToken: res.Token, RefreshToken: res.RefreshToken})
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
	h.svc.SetSessionCookies(w, authsvc.TokenPair{AccessToken: res.Token, RefreshToken: res.RefreshToken})
	// The linked user's verified email identifier is the authoritative address; the
	// helper resolves it (no request email is available on this callback lane).
	web.RespondJSONOK(w, h.userResponseFor(r.Context(), res.User, ""))
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

// startUnlinkOAuth / unlinkOAuth dispatch their POST by Content-Type: the JSON arm
// keeps the existing contract, a form body renders or redirects through the HTML
// surface (only when Views is wired). Both arms call the same code-gated unlink
// service methods (design §5.4/§9.2).
func (h *handlers) startUnlinkOAuth(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.startUnlinkOAuthJSON, h.startUnlinkOAuthForm)
}

func (h *handlers) unlinkOAuth(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.unlinkOAuthJSON, h.unlinkOAuthForm)
}

// startUnlinkOAuthJSON issues a provider-bound unlink_oauth code to the caller's
// verified recovery identifier and returns the PII-free delivery receipt (design
// §5.4). The provider is a path parameter; the code binds it so it cannot unlink
// another.
func (h *handlers) startUnlinkOAuthJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req unlinkStartRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, _, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	receipt, err := h.svc.StartUnlinkOAuth(r.Context(), userID, web.Param(r, "provider"))
	if err != nil {
		writePasswordError(w, err)
		return
	}
	web.RespondJSONOK(w, stepUpBeginResponse{Status: "sent", Receipt: receipt.Receipt})
}

// unlinkOAuthJSON consumes the delivered provider-bound code and unlinks the provider
// through the revision-serialized credential rail (design §5.4). A code issued for a
// different provider is consumed and rejected without unlinking.
func (h *handlers) unlinkOAuthJSON(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var req unlinkRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, _, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.svc.UnlinkOAuth(r.Context(), userID, web.Param(r, "provider"), req.Code); err != nil {
		writePasswordError(w, err)
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
