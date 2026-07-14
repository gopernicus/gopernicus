package authentication

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Public HTML form handlers (design §9.2, task AV3-8.3). These are the form arm of
// the content-type dispatch: each is reached ONLY when Views is wired and the
// request carried a urlencoded/multipart body (dispatch.go). They parse a bounded
// body, apply the credential-establishment origin policy, call the SAME service
// methods the JSON arm calls, and on success use 303 Post/Redirect/Get through the
// exact redirect allowlist. On failure they re-render the form with generic,
// enumeration-resistant copy and NEVER repopulate a password/code/token. Account-
// security form handlers (password change/set/remove, identifiers, step-up, OAuth
// unlink) are AV3-8.4.

const (
	// maxFormBodyBytes bounds an auth HTML form body before ParseForm so an oversized
	// submission is rejected rather than buffered whole (parity with maxJSONBodyBytes).
	maxFormBodyBytes = 1 << 20 // 1 MiB

	// Generic, enumeration-resistant re-render copy. None distinguishes an
	// unknown/unverified account; secret fields are never echoed alongside them.
	loginErrMsg             = "We couldn't sign you in. Check your details and try again."
	registerErrMsg          = "We couldn't create your account. Check your details and try again."
	verifyErrMsg            = "That code didn't work. Request a new one and try again."
	resetErrMsg             = "That reset link is no longer valid. Request a new one."
	passwordlessStartErrMsg = "We couldn't start sign-in. Check the address and try again."
	passwordlessCodeErrMsg  = "That code didn't work. Request a new one."
	magicErrMsg             = "That sign-in link is no longer valid. Request a new one."
	tooManyErrMsg           = "Too many attempts. Please wait a moment and try again."
)

// ---------------------------------------------------------------------------
// Shared form helpers
// ---------------------------------------------------------------------------

// validatedReturnTo returns the request's ?return_to query value only when it is
// exactly allowlisted (design §9.2); otherwise empty so the form omits it and the
// PRG defaults to the same-origin root. A Host-derived or attacker target never
// survives.
func (h *handlers) validatedReturnTo(r *http.Request) string {
	return h.safeReturnTo(r.URL.Query().Get("return_to"))
}

// safeReturnTo echoes raw only when it resolves to itself: a safe same-origin
// relative path or an exactly-allowlisted absolute target.
func (h *handlers) safeReturnTo(raw string) string {
	if raw == "" {
		return ""
	}
	if h.resolveReturnTo(raw) == raw {
		return raw
	}
	return ""
}

// resolveReturnTo returns a safe post-auth destination for a requested value: a
// safe same-origin relative path is honored directly (a relative path is never an
// open-redirect vector), an exactly-allowlisted absolute target is honored through
// the shared OAuth allowlist, and anything else falls back to the same-origin
// default "/". It is the single return-to resolver both the public and the
// account HTML lanes route through.
func (h *handlers) resolveReturnTo(raw string) string {
	if p := safeRelativePath(raw); p != "" {
		return p
	}
	return h.svc.ResolveRedirect(raw)
}

// safeRelativePath returns p when it is a safe same-origin relative path (a single
// leading slash, no scheme, no protocol-relative "//" or backslash trickery a
// browser might normalize into one), else "". It guards the internal return-to the
// stale-session gate reflects into the login redirect.
func safeRelativePath(p string) string {
	if p == "" || p[0] != '/' {
		return ""
	}
	if strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\") || strings.Contains(p, "://") {
		return ""
	}
	return p
}

// formOriginOK enforces the credential-establishment origin policy for a public form
// POST (design §9.1): a browser form submit carries an Origin/Sec-Fetch-Site, so a
// cross-site submit is rejected; a non-browser client that sends neither passes. It
// renders a generic 403 page on rejection. The passwordless routes already carry the
// same policy as route middleware, so their form handlers skip this call.
func (h *handlers) formOriginOK(w http.ResponseWriter, r *http.Request) bool {
	if browserOriginAllowed(r, h.mutation.AllowedOrigins) {
		return true
	}
	h.renderError(w, r, http.StatusForbidden, "That request looked cross-site and was blocked. Please try again.")
	return false
}

// parseForm bounds the body and parses it, rendering a generic HTML error on an
// oversized (413) or malformed (400) submission and returning false.
func (h *handlers) parseForm(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			h.renderError(w, r, http.StatusRequestEntityTooLarge, "That submission was too large.")
			return nil, false
		}
		h.renderError(w, r, http.StatusBadRequest, "We couldn't read that form. Please try again.")
		return nil, false
	}
	return r.PostForm, true
}

// prgRedirect issues the 303 Post/Redirect/Get to a validated return-to (or the
// same-origin default), under the HTML security headers so the redirect is not
// cached and leaks no referrer.
func (h *handlers) prgRedirect(w http.ResponseWriter, r *http.Request, returnTo string) {
	h.prgTo(w, r, h.resolveReturnTo(returnTo))
}

// prgTo issues the 303 Post/Redirect/Get to a server-controlled destination (a fixed
// same-origin path built by the handler — never a client-supplied absolute URL).
func (h *handlers) prgTo(w http.ResponseWriter, r *http.Request, dest string) {
	writeHTMLSecurity(w, "")
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// formFailure maps a service error to the re-render status and a generic message: a
// rate-limit exhaustion is 429 with a wait message; every other failure maps through
// the shared domain-error status with the caller's generic copy (never the raw error,
// preserving enumeration resistance).
func formFailure(err error, generic string) (int, string) {
	if errors.Is(err, authsvc.ErrRateLimited) || errors.Is(err, authsvc.ErrPasswordlessRateLimited) {
		return http.StatusTooManyRequests, tooManyErrMsg
	}
	// A challenge-rail sentinel maps through the SAME §5.8 mapper the JSON arm uses, so
	// both transports agree on the status for one service error (transport parity). The
	// form copy stays generic — the machine code never leaks into the user-facing
	// message.
	if mapped, ok := challengeErrorFor(err); ok {
		return mapped.Status, generic
	}
	return web.ErrFromDomain(err).Status, generic
}

// renderForm re-renders a page at status under the HTML security headers. pc must be
// a freshly minted PageContext so the re-rendered form carries a new CSRF token and
// nonce.
func (h *handlers) renderForm(w http.ResponseWriter, r *http.Request, status int, nonce string, page web.Renderer) {
	writeHTMLSecurity(w, nonce)
	web.Render(r.Context(), w, status, page)
}

// ---------------------------------------------------------------------------
// Public credential form handlers
// ---------------------------------------------------------------------------

func (h *handlers) loginForm(w http.ResponseWriter, r *http.Request) {
	if !h.formOriginOK(w, r) {
		return
	}
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	email := form.Get("email")
	returnTo := form.Get("return_to")
	pair, _, err := h.svc.Login(r.Context(), email, form.Get("password"))
	if err != nil {
		status, msg := formFailure(err, loginErrMsg)
		pc := h.newPageContext(w)
		pc.ReturnTo, pc.Message = h.safeReturnTo(returnTo), msg
		h.renderForm(w, r, status, pc.CSPNonce, h.views.Login(LoginPage{
			PageContext: pc, Email: email, PasswordlessEnabled: h.svc.PasswordlessEnabled(),
		}))
		return
	}
	h.svc.SetSessionCookies(w, pair)
	h.prgRedirect(w, r, returnTo)
}

func (h *handlers) registerForm(w http.ResponseWriter, r *http.Request) {
	if !h.formOriginOK(w, r) {
		return
	}
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	email := form.Get("email")
	displayName := form.Get("display_name")
	if _, err := h.svc.Register(r.Context(), email, form.Get("password"), displayName); err != nil {
		status, msg := formFailure(err, registerErrMsg)
		pc := h.newPageContext(w)
		pc.Message = msg
		h.renderForm(w, r, status, pc.CSPNonce, h.views.Register(RegisterPage{
			PageContext: pc, Email: email, DisplayName: displayName,
		}))
		return
	}
	// Register mints no session (parity with the JSON 201); PRG to verification. The
	// email is a contact address, not a secret, carried so the verify form names it.
	h.prgTo(w, r, "/auth/verify?email="+url.QueryEscape(email))
}

func (h *handlers) verifyForm(w http.ResponseWriter, r *http.Request) {
	if !h.formOriginOK(w, r) {
		return
	}
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	email := form.Get("email")
	if err := h.svc.Verify(r.Context(), email, form.Get("code")); err != nil {
		status, msg := formFailure(err, verifyErrMsg)
		pc := h.newPageContext(w)
		pc.Message = msg
		h.renderForm(w, r, status, pc.CSPNonce, h.views.Verify(VerifyPage{PageContext: pc, Email: email}))
		return
	}
	h.prgTo(w, r, "/auth/login?email="+url.QueryEscape(email))
}

func (h *handlers) forgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	if !h.formOriginOK(w, r) {
		return
	}
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	// Enumeration-safe (parity with the JSON 202): the service returns nil for unknown
	// addresses; a non-nil error is an internal failure. Success and unknown alike land
	// on the same generic PRG confirmation.
	if err := h.svc.ForgotPassword(r.Context(), form.Get("email")); err != nil {
		status := http.StatusInternalServerError
		if deliveryUnavailable(err) {
			// The bounded in-process outbox rejected admission (full / shutting down):
			// an honest 503, never a success redirect after dropping the work. The class
			// is identical for known and unknown addresses (admission precedes lookup).
			status = http.StatusServiceUnavailable
		}
		h.renderError(w, r, status, "Something went wrong. Please try again.")
		return
	}
	h.prgTo(w, r, "/auth/password/forgot?sent=1")
}

func (h *handlers) resetPasswordForm(w http.ResponseWriter, r *http.Request) {
	if !h.formOriginOK(w, r) {
		return
	}
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	if err := h.svc.ResetPassword(r.Context(), form.Get("token"), form.Get("password")); err != nil {
		status, msg := formFailure(err, resetErrMsg)
		pc := h.newPageContext(w)
		pc.Message = msg
		// The initial GET reads the token from the fragment; on this error RE-render the
		// fragment is already scrubbed, so echo the SUBMITTED token back into the hidden
		// field so a corrected retry survives (IX-11). It is the same value this POST
		// carried — no new fragment/query/referrer copy, and the response HTML is not
		// logged.
		h.renderForm(w, r, status, pc.CSPNonce, h.views.ResetPassword(ResetPage{
			PageContext: pc, RedeemPath: "/auth/password/reset", Token: form.Get("token"),
		}))
		return
	}
	// Reset revokes every session and does not auto-login (design §5.9); land on login.
	h.prgTo(w, r, "/auth/login")
}

// ---------------------------------------------------------------------------
// Passwordless form handlers. The routes already carry requireBrowserSafeOrigin as
// middleware (both transports), so these skip the per-handler origin check.
// ---------------------------------------------------------------------------

func (h *handlers) passwordlessStartForm(w http.ResponseWriter, r *http.Request) {
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	kind, identifier, method := form.Get("kind"), form.Get("identifier"), form.Get("method")
	if err := h.svc.StartPasswordless(r.Context(), kind, identifier, method); err != nil {
		status, msg := formFailure(err, passwordlessStartErrMsg)
		pc := h.newPageContext(w)
		pc.ReturnTo, pc.Message = h.safeReturnTo(form.Get("return_to")), msg
		h.renderForm(w, r, status, pc.CSPNonce, h.views.PasswordlessStart(PasswordlessStartPage{
			PageContext: pc, Kind: kind, Identifier: identifier, Method: method,
			Kinds: h.svc.PasswordlessKinds(),
		}))
		return
	}
	// Generic PRG confirmation — identical for known/unknown/unverified (§4.3).
	h.prgTo(w, r, "/auth/passwordless/check?kind="+url.QueryEscape(kind))
}

func (h *handlers) passwordlessVerifyForm(w http.ResponseWriter, r *http.Request) {
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	kind, identifier := form.Get("kind"), form.Get("identifier")
	returnTo := form.Get("return_to")
	pair, err := h.svc.VerifyPasswordless(r.Context(), kind, identifier, form.Get("code"))
	if err != nil {
		status, msg := formFailure(err, passwordlessCodeErrMsg)
		pc := h.newPageContext(w)
		pc.ReturnTo, pc.Message = h.safeReturnTo(returnTo), msg
		h.renderForm(w, r, status, pc.CSPNonce, h.views.PasswordlessCode(PasswordlessCodePage{
			PageContext: pc, Kind: kind, Identifier: identifier,
		}))
		return
	}
	h.svc.SetSessionCookies(w, pair)
	h.prgRedirect(w, r, returnTo)
}

func (h *handlers) passwordlessRedeemForm(w http.ResponseWriter, r *http.Request) {
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	returnTo := form.Get("return_to")
	pair, err := h.svc.RedeemPasswordless(r.Context(), form.Get("token"))
	if err != nil {
		status, msg := formFailure(err, magicErrMsg)
		pc := h.newPageContext(w)
		pc.ReturnTo, pc.Message = h.safeReturnTo(returnTo), msg
		// The magic token is fragment-read and never server-rendered; the landing's
		// nonced script re-reads it, and a manual fallback stays available.
		h.renderForm(w, r, status, pc.CSPNonce, h.views.MagicLinkLanding(MagicLinkPage{
			PageContext: pc, RedeemPath: "/auth/passwordless/redeem",
		}))
		return
	}
	h.svc.SetSessionCookies(w, pair)
	h.prgRedirect(w, r, returnTo)
}
