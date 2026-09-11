package authenticationhttp

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	// defaultRedirect is the same-origin fallback the callback redirects to when a
	// flow carried no validated destination.
	defaultRedirect = "/"

	// verifyLinkPath is the pending-link completion edge both transports post to: the
	// JSON API client and the bundled HTML landing page's form.
	verifyLinkPath = "/auth/oauth/verify-link"

	// linkErrMsg is the generic re-render copy for a failed pending-link completion.
	// The secret is single-use and already consumed by the attempt, so the copy points
	// at starting over rather than retrying; it names no account.
	linkErrMsg = "That link is no longer valid. Start linking your account again."
)

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
// only when a provider is wired. The link-start route takes the OAuthLinkStart
// authenticator (a person's access token over either transport — an API key
// cannot start a human OAuth link); the code-gated unlink pair (design §5.4) is
// a credential mutation gated by the CredentialManagement authenticator
// (immediate revocation, human credential only) plus the browser-safe-mutation
// Origin/CSRF gate, replacing the plain DELETE /auth/oauth/{provider}/link
// (pre-tag route break). The caller's link inventory is no longer a route here:
// GET /auth/oauth/linked is subsumed by the masked GET /auth/methods (design §5.1).
func mountOAuth(r pockets.RouteRegistrar, h *handlers, linkStart, credentialManagement, browserSafe web.Middleware) {
	r.Handle("GET", "/auth/oauth/{provider}/start", h.oauthStart)
	r.Handle("GET", "/auth/oauth/{provider}/callback", h.oauthCallback)
	r.Handle("POST", verifyLinkPath, h.oauthVerifyLink)
	if h.svc.OAuthNativeEnabled() {
		r.Handle("POST", "/auth/oauth/{provider}/native/start", h.oauthNativeStart)
		r.Handle("POST", "/auth/oauth/{provider}/native/complete", h.oauthNativeComplete)
		r.Handle("POST", "/auth/oauth/{provider}/native/link/start", h.oauthNativeLinkStart, linkStart)
		r.Handle("POST", "/auth/oauth/native/verify-link", h.oauthNativeVerifyLink)
	}
	r.Handle("GET", "/auth/oauth/{provider}/link/start", h.oauthLinkStart, linkStart)
	r.Handle("POST", "/auth/oauth/{provider}/unlink/start", h.startUnlinkOAuth, credentialManagement, browserSafe)
	r.Handle("POST", "/auth/oauth/{provider}/unlink", h.unlinkOAuth, credentialManagement, browserSafe)
}

// oauthStart redirects the browser to the provider authorization URL.
func (h *handlers) oauthStart(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	flow, err := h.svc.StartOAuth(r.Context(), web.Param(r, "provider"), authlogic.OAuthStartRequest{Mode: authlogic.OAuthBrowser, RedirectTo: r.URL.Query().Get("redirect")})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.setOAuthFlowCookie(w, flow)
	http.Redirect(w, r, flow.AuthorizationURL, http.StatusFound)
}

// oauthCallback processes the provider redirect: it resolves the anti-takeover
// branch, sets a session cookie on login/register, and redirects the browser to
// the flow's validated destination. A pending link (email sent) or a completed
// explicit link redirects without a new cookie.
func (h *handlers) oauthCallback(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	q := r.URL.Query()
	state := q.Get("state")
	if !authlogic.ValidOAuthFlowValue(state) {
		web.RespondJSONDomainError(w, authlogic.ErrInvalidOAuthState)
		return
	}
	cookie, err := r.Cookie(h.oauthFlowCookieName(state))
	if err != nil {
		web.RespondJSONDomainError(w, authlogic.ErrInvalidOAuthState)
		return
	}
	res, err := h.svc.OAuthCallback(r.Context(), web.Param(r, "provider"), authlogic.OAuthCallbackRequest{
		Mode: authlogic.OAuthBrowser, Code: q.Get("code"), State: state, FlowSecret: cookie.Value,
	})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.clearOAuthFlowCookie(w, state)
	if res.Action == authlogic.ActionLogin || res.Action == authlogic.ActionRegister {
		h.svc.SetSessionCookies(w, authlogic.TokenPair{AccessToken: res.Token, RefreshToken: res.RefreshToken})
	}
	target := redirectOrDefault(res.RedirectTo)
	switch res.Action {
	case authlogic.ActionPendingLink:
		// A pending link mints no session; the callback lands the SPA on the flow's
		// destination carrying a legible outcome (oauth-pending-link plan D3) so it can
		// render a "check your email" state instead of a dead end.
		target = pendingLinkRedirect(res.RedirectTo, web.Param(r, "provider"))
	case authlogic.ActionLinked:
		// A completed explicit link mints no session either, and without a marker it
		// landed back on the account page silently. It carries the same legible outcome
		// so the destination can name what happened. ActionLogin/ActionRegister are not
		// marked: they land on the flow's app destination, not an auth page.
		target = linkedRedirect(res.RedirectTo)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// oauthVerifyLink dispatches the pending-link completion by Content-Type: the JSON
// arm keeps the existing contract, a form body completes the link through the bundled
// HTML landing page (only when Views is wired). Both arms call the same VerifyLink
// service method — the anti-takeover model is unchanged by the transport.
func (h *handlers) oauthVerifyLink(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.oauthVerifyLinkJSON, h.oauthVerifyLinkForm)
}

// oauthVerifyLinkJSON completes a pending link from the mailed secret and logs the
// user in (fresh session cookie).
func (h *handlers) oauthVerifyLinkJSON(w http.ResponseWriter, r *http.Request) {
	var req verifyLinkRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	res, err := h.svc.VerifyLink(r.Context(), req.Token)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, authlogic.TokenPair{AccessToken: res.Token, RefreshToken: res.RefreshToken})
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
	writeNoStore(w)
	flow, err := h.svc.StartLink(r.Context(), userID, web.Param(r, "provider"), authlogic.OAuthStartRequest{Mode: authlogic.OAuthBrowser, RedirectTo: r.URL.Query().Get("redirect")})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.setOAuthFlowCookie(w, flow)
	http.Redirect(w, r, flow.AuthorizationURL, http.StatusFound)
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

// pendingLinkRedirect augments the pending-link callback destination with the
// legible outcome the SPA reads (oauth-pending-link plan D3): auth=link_sent plus
// the provider. It parses the (already-validated, else defaulted) destination and
// sets the parameters through url.Values so existing query values and any fragment
// are preserved. An unparseable destination is returned unchanged rather than
// dropped — the flow already validated it, so this is defensive.
func pendingLinkRedirect(target, provider string) string {
	base := redirectOrDefault(target)
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("auth", "link_sent")
	q.Set("provider", provider)
	u.RawQuery = q.Encode()
	return u.String()
}

// linkedRedirect augments the completed-link callback destination with the outcome
// code the account page's closed reader whitelists. It mirrors pendingLinkRedirect —
// url.Values, so existing query values and any fragment survive, and an unparseable
// destination is returned unchanged — but names NO provider: the destination lists the
// linked provider in its own masked inventory, so echoing the path parameter would put
// attacker-authored text on an authenticated page for nothing.
func linkedRedirect(target string) string {
	base := redirectOrDefault(target)
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("auth", outcomeProviderLinked)
	u.RawQuery = q.Encode()
	return u.String()
}

// Flow cookies are host-only and independent per transaction. Session cookie
// domain/path policy must not broaden proof delivery. Secure comes from the
// configured callback origin, never a request's forwarding headers.
func (h *handlers) oauthFlowCookieName(state string) string {
	prefix := "gopernicus_oauth_"
	if h.svc.OAuthBrowserSecure() {
		prefix = "__Host-" + prefix
	}
	return prefix + state
}

func (h *handlers) setOAuthFlowCookie(w http.ResponseWriter, flow authlogic.OAuthStart) {
	http.SetCookie(w, &http.Cookie{Name: h.oauthFlowCookieName(flow.State), Value: flow.FlowSecret,
		Path: "/", HttpOnly: true, Secure: h.svc.OAuthBrowserSecure(), SameSite: http.SameSiteLaxMode,
		Expires: flow.ExpiresAt, MaxAge: int(time.Until(flow.ExpiresAt).Seconds())})
}

func (h *handlers) clearOAuthFlowCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{Name: h.oauthFlowCookieName(state), Path: "/", HttpOnly: true,
		Secure: h.svc.OAuthBrowserSecure(), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

type oauthNativeStartRequest struct {
	RedirectURI string `json:"redirect_uri"`
}

type oauthNativeCompleteRequest struct {
	Code       string `json:"code"`
	State      string `json:"state"`
	FlowSecret string `json:"flow_secret"`
}

type oauthNativeResponse struct {
	Action       string `json:"action"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
}

func (h *handlers) oauthNativeStart(w http.ResponseWriter, r *http.Request) {
	h.startNativeOAuth(w, r, "")
}

func (h *handlers) oauthNativeLinkStart(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	h.startNativeOAuth(w, r, userID)
}

func (h *handlers) startNativeOAuth(w http.ResponseWriter, r *http.Request, userID string) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var body oauthNativeStartRequest
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	req := authlogic.OAuthStartRequest{Mode: authlogic.OAuthNative, RedirectURI: body.RedirectURI}
	var flow authlogic.OAuthStart
	var err error
	if userID == "" {
		flow, err = h.svc.StartOAuth(r.Context(), web.Param(r, "provider"), req)
	} else {
		flow, err = h.svc.StartLink(r.Context(), userID, web.Param(r, "provider"), req)
	}
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, flow)
}

func (h *handlers) oauthNativeComplete(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var body oauthNativeCompleteRequest
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	res, err := h.svc.OAuthCallback(r.Context(), web.Param(r, "provider"), authlogic.OAuthCallbackRequest{
		Mode: authlogic.OAuthNative, Code: body.Code, State: body.State, FlowSecret: body.FlowSecret,
	})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	writeNativeOAuthResult(w, res)
}

func (h *handlers) oauthNativeVerifyLink(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var body verifyLinkRequest
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	res, err := h.svc.VerifyLink(r.Context(), body.Token)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	writeNativeOAuthResult(w, res)
}

func writeNativeOAuthResult(w http.ResponseWriter, res authlogic.OAuthResult) {
	out := oauthNativeResponse{Action: res.Action, AccessToken: res.Token, RefreshToken: res.RefreshToken}
	if res.Token != "" {
		out.TokenType = "Bearer"
	}
	web.RespondJSONOK(w, out)
}
