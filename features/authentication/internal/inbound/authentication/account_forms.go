package authentication

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Account-security HTML form handlers (design §5.0–§5.5, §9.2, task AV3-8.4). These
// are the form arm of the content-type dispatch for the live-session-gated account
// routes. Each is reached ONLY when Views is wired and the request carried a
// urlencoded/multipart body (dispatch.go). They call the SAME service methods as the
// JSON arm — the HTML layer never recalculates method safety, assurance, rate limits,
// context binding, or policy; the service stays authoritative. On success they use
// 303 Post/Redirect/Get; on failure they re-render the form with generic,
// enumeration-resistant copy and never repopulate a password/code/token. A sensitive
// mutation that needs a recent-authentication grant the caller lacks redirects to the
// step-up page bound to that operation.

const (
	// accountPath is the masked-inventory landing every completed account mutation
	// PRGs to.
	accountPath = "/auth/account"

	// accountErrMsg is the generic re-render copy for a failed account mutation. It
	// never distinguishes an unknown identifier, method, or policy detail.
	accountErrMsg = "That change couldn't be completed. Please check your details and try again."

	// accountPolicyMsg is the generic copy for a policy-refused removal (e.g. removing
	// the last authentication method). It is actionable without exposing which method
	// or contact value is involved.
	accountPolicyMsg = "That change would leave your account without a way to sign in, so it was not applied."

	// csrfFailedMsg is the generic copy for a failed form double-submit CSRF check.
	csrfFailedMsg = "That request could not be verified. Please reload the page and try again."
)

// ---------------------------------------------------------------------------
// Shared account-form plumbing
// ---------------------------------------------------------------------------

// accountForm is the wrapper every account-security form handler runs through. It
// parses a bounded body, performs the double-submit CSRF compare against the body's
// csrf_token field (the form lane of the browser-safe-mutation gate — a form POST
// cannot set the X-CSRF-Token header, design §9.1), and only then invokes fn. The
// route middleware has already enforced the allowlisted Origin and the live session;
// this closes the CSRF token lane the middleware deferred.
func (h *handlers) accountForm(w http.ResponseWriter, r *http.Request, fn func(form url.Values)) {
	form, ok := h.parseForm(w, r)
	if !ok {
		return
	}
	if !h.formCSRFOK(w, r, form) {
		return
	}
	fn(form)
}

// formCSRFOK performs the double-submit CSRF compare for a form POST: the body's
// csrf_token field must equal the auth_csrf cookie, compared in constant time. On a
// missing/mismatched token it renders a generic 403 page and returns false. This is
// the form-lane twin of the header double-submit the middleware applies to
// JSON/fetch callers; neither lane is weakened.
func (h *handlers) formCSRFOK(w http.ResponseWriter, r *http.Request, form url.Values) bool {
	cookie, err := r.Cookie(csrfCookieName)
	token := form.Get("csrf_token")
	if err != nil || cookie.Value == "" || token == "" ||
		subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) != 1 {
		h.renderError(w, r, http.StatusForbidden, csrfFailedMsg)
		return false
	}
	return true
}

// formPrincipal resolves the caller's user id and live session id that
// RequireLiveSession stamped (the same binding the JSON stepUpPrincipal uses). A
// step-up grant is always bound to the proven session, never a body field; a missing
// principal redirects to login rather than leaking a JSON 401 onto an HTML page.
func (h *handlers) formPrincipal(w http.ResponseWriter, r *http.Request) (userID, sessionID string, ok bool) {
	userID, uok := h.svc.CurrentUser(r.Context())
	sessionID, sok := h.svc.CurrentSessionID(r.Context())
	if !uok || !sok {
		h.redirectToLogin(w, r)
		return "", "", false
	}
	return userID, sessionID, true
}

// redirectToLogin 303s a browser to the login page, reflecting the current request
// path back as a validated safe relative return-to (never an absolute/attacker URL,
// design §9.2).
func (h *handlers) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	loc := "/auth/login"
	if rt := safeRelativePath(r.URL.Path); rt != "" {
		loc += "?return_to=" + url.QueryEscape(rt)
	}
	h.prgTo(w, r, loc)
}

// stepUpRedirect 303s a browser to the step-up page bound to the exact operation a
// sensitive mutation requires (design §5.0). purpose/context bind the grant the
// completion will earn; operation is the generic label; returnTo is the action page
// to retry once the grant exists (validated as a safe relative path).
func (h *handlers) stepUpRedirect(w http.ResponseWriter, r *http.Request, purpose, context, operation, returnTo string) {
	h.prgTo(w, r, stepUpPath(stepUpParams{purpose: purpose, context: context, operation: operation, returnTo: returnTo}))
}

// accountFailure maps a service error to the re-render status and generic copy: rate
// limits are 429; a last-method policy refusal keeps its generic actionable copy;
// every other failure maps through the shared domain-error status with the generic
// account message (never the raw error, preserving enumeration resistance).
func accountFailure(err error) (int, string) {
	switch {
	case errors.Is(err, authsvc.ErrRateLimited),
		errors.Is(err, authsvc.ErrIdentifierChangeRateLimited),
		errors.Is(err, authsvc.ErrPasswordlessRateLimited):
		return http.StatusTooManyRequests, tooManyErrMsg
	case errors.Is(err, credential.ErrNoLoginMethod):
		return web.ErrFromDomain(err).Status, accountPolicyMsg
	default:
		return web.ErrFromDomain(err).Status, accountErrMsg
	}
}

// formUses reads the login/recovery/notification role checkboxes ("true" when
// checked) into the domain Uses value.
func formUses(form url.Values) identifier.Uses {
	return identifier.Uses{
		Login:        form.Get("login") == "true",
		Recovery:     form.Get("recovery") == "true",
		Notification: form.Get("notification") == "true",
	}
}

// ---------------------------------------------------------------------------
// Password form handlers
// ---------------------------------------------------------------------------

func (h *handlers) changePasswordForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, ok := h.svc.CurrentUser(r.Context())
		if !ok {
			h.redirectToLogin(w, r)
			return
		}
		pair, err := h.svc.ChangePassword(r.Context(), userID, form.Get("current_password"), form.Get("password"))
		if err != nil {
			h.renderPasswordForm(w, r, "change", true, err)
			return
		}
		h.svc.SetSessionCookies(w, pair)
		h.prgTo(w, r, accountPath)
	})
}

func (h *handlers) setPasswordForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		pair, err := h.svc.SetPassword(r.Context(), sessionID, userID, form.Get("password"))
		if err != nil {
			if errors.Is(err, authsvc.ErrStepUpRequired) {
				h.stepUpRedirect(w, r, authgrant.PurposeSetPassword, "", "set a password", "/auth/password/set")
				return
			}
			h.renderPasswordForm(w, r, "set", false, err)
			return
		}
		h.svc.SetSessionCookies(w, pair)
		h.prgTo(w, r, accountPath)
	})
}

func (h *handlers) startRemovePasswordForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, _, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		if _, err := h.svc.StartRemovePassword(r.Context(), userID); err != nil {
			h.renderPasswordForm(w, r, "remove", false, err)
			return
		}
		// Generic PRG back to the remove page where the delivered code is entered.
		h.prgTo(w, r, "/auth/password/remove?sent=1")
	})
}

func (h *handlers) removePasswordForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, _, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		pair, err := h.svc.RemovePassword(r.Context(), userID, form.Get("code"))
		if err != nil {
			h.renderPasswordForm(w, r, "remove", false, err)
			return
		}
		h.svc.SetSessionCookies(w, pair)
		h.prgTo(w, r, accountPath)
	})
}

// renderPasswordForm re-renders the set/change/remove password page with generic copy
// at the mapped status. No password or code is ever repopulated (the model carries no
// such field).
func (h *handlers) renderPasswordForm(w http.ResponseWriter, r *http.Request, mode string, showCurrent bool, err error) {
	status, msg := accountFailure(err)
	pc := h.newPageContext(w)
	pc.Message = msg
	m := PasswordFormPage{PageContext: pc, Mode: mode, ShowCurrentPassword: showCurrent}
	if mode == "remove" {
		if userID, ok := h.svc.CurrentUser(r.Context()); ok {
			m.MaskedDestination = h.maskedRecovery(r.Context(), userID)
		}
	}
	h.renderForm(w, r, status, pc.CSPNonce, h.views.PasswordForm(m))
}

// ---------------------------------------------------------------------------
// Step-up (recent-authentication) form handlers
// ---------------------------------------------------------------------------

func (h *handlers) beginStepUpForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		purpose, context := form.Get("purpose"), form.Get("context")
		if _, err := h.svc.BeginStepUp(r.Context(), authsvc.StepUpStart{
			SessionID: sessionID, UserID: userID, Purpose: purpose, Context: context, Kind: form.Get("kind"),
		}); err != nil {
			h.renderStepUp(w, r, userID, stepUpParams{purpose: purpose, context: context, operation: form.Get("operation"), returnTo: form.Get("return_to")}, err)
			return
		}
		h.prgTo(w, r, stepUpPath(stepUpParams{
			purpose: purpose, context: context, operation: form.Get("operation"), returnTo: form.Get("return_to"), sent: true,
		}))
	})
}

func (h *handlers) completeStepUpPasswordForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		purpose, context := form.Get("purpose"), form.Get("context")
		if _, err := h.svc.CompleteStepUpWithPassword(r.Context(), authsvc.StepUpCompletion{
			SessionID: sessionID, UserID: userID, Purpose: purpose, Context: context,
		}, form.Get("password")); err != nil {
			h.renderStepUp(w, r, userID, stepUpParams{purpose: purpose, context: context, operation: form.Get("operation"), returnTo: form.Get("return_to")}, err)
			return
		}
		h.prgRedirect(w, r, form.Get("return_to"))
	})
}

func (h *handlers) completeStepUpCodeForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		purpose, context := form.Get("purpose"), form.Get("context")
		if _, err := h.svc.CompleteStepUpWithIdentifierCode(r.Context(), authsvc.StepUpCompletion{
			SessionID: sessionID, UserID: userID, Purpose: purpose, Context: context,
		}, form.Get("code")); err != nil {
			h.renderStepUp(w, r, userID, stepUpParams{purpose: purpose, context: context, operation: form.Get("operation"), returnTo: form.Get("return_to")}, err)
			return
		}
		h.prgRedirect(w, r, form.Get("return_to"))
	})
}

// renderStepUp re-renders the step-up page for the given operation binding with
// generic copy at the mapped status. No password/code is repopulated.
func (h *handlers) renderStepUp(w http.ResponseWriter, r *http.Request, userID string, p stepUpParams, err error) {
	status, msg := accountFailure(err)
	pc := h.newPageContext(w)
	pc.ReturnTo, pc.Message = h.safeReturnTo(p.returnTo), msg
	m := h.stepUpModel(r.Context(), userID, pc, p)
	h.renderForm(w, r, status, pc.CSPNonce, h.views.StepUp(m))
}

// ---------------------------------------------------------------------------
// Identifier form handlers
// ---------------------------------------------------------------------------

func (h *handlers) startEmailIdentifierForm(w http.ResponseWriter, r *http.Request) {
	h.startIdentifierForm(w, r, identifier.KindEmail, authgrant.PurposeChangeEmail, "add an email")
}

func (h *handlers) startPhoneIdentifierForm(w http.ResponseWriter, r *http.Request) {
	h.startIdentifierForm(w, r, identifier.KindPhone, authgrant.PurposeChangePhone, "add a phone")
}

// startIdentifierForm begins an identifier add/change from a form: it delivers an
// ownership-proof code to the proposed new address, or redirects to step-up when the
// caller lacks the required recent-auth grant. A collision (address already claimed)
// re-renders generically without confirming the address exists.
func (h *handlers) startIdentifierForm(w http.ResponseWriter, r *http.Request, kind identifier.Kind, purpose, operation string) {
	h.accountForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		value := form.Get("value")
		if _, err := h.svc.StartIdentifierChange(r.Context(), authsvc.IdentifierChangeStart{
			SessionID: sessionID, UserID: userID, Kind: kind, Value: value,
			Uses: formUses(form), MakePrimary: form.Get("primary") == "true",
		}); err != nil {
			if errors.Is(err, authsvc.ErrStepUpRequired) {
				h.stepUpRedirect(w, r, purpose, "", operation, "/auth/identifiers/new?kind="+url.QueryEscape(string(kind)))
				return
			}
			h.renderIdentifierAdd(w, r, string(kind), value, form, err)
			return
		}
		// The ownership-proof code was sent to the proposed address; land on the confirm
		// page bound to this kind, where the caller enters it. The confirm form posts the
		// same kind-specific /auth/identifiers/{kind}/confirm edge the JSON API uses.
		h.prgTo(w, r, "/auth/identifiers/confirm?kind="+url.QueryEscape(string(kind)))
	})
}

func (h *handlers) confirmEmailIdentifierForm(w http.ResponseWriter, r *http.Request) {
	h.confirmIdentifierForm(w, r, identifier.KindEmail)
}

func (h *handlers) confirmPhoneIdentifierForm(w http.ResponseWriter, r *http.Request) {
	h.confirmIdentifierForm(w, r, identifier.KindPhone)
}

// confirmIdentifierForm completes an identifier add/change by consuming the delivered
// proof code and applying the verified change, then PRGs to the account page.
func (h *handlers) confirmIdentifierForm(w http.ResponseWriter, r *http.Request, kind identifier.Kind) {
	h.accountForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		if err := h.svc.ConfirmIdentifierChange(r.Context(), authsvc.IdentifierChangeConfirm{
			SessionID: sessionID, UserID: userID, Kind: kind, Code: form.Get("code"),
		}); err != nil {
			h.renderIdentifierConfirm(w, r, string(kind), err)
			return
		}
		h.prgTo(w, r, accountPath)
	})
}

// identifierEditForm is the HTML edit twin of the JSON PATCH/DELETE {id} edges: a
// form cannot emit those verbs, so the edit form POSTs /auth/identifiers/{id} and an
// action field selects update-uses (default, PATCH) or remove (DELETE). Both route to
// the same service methods and consume the same recent-auth grant; a missing grant
// redirects to step-up bound to the identifier.
func (h *handlers) identifierEditForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		id := web.Param(r, "id")
		if form.Get("action") == "remove" {
			err := h.svc.RemoveIdentifier(r.Context(), authsvc.IdentifierRemoveInput{
				SessionID: sessionID, UserID: userID, IdentifierID: id, ReplacementID: form.Get("replacement"),
			})
			if err != nil {
				if errors.Is(err, authsvc.ErrStepUpRequired) {
					h.stepUpRedirect(w, r, authgrant.PurposeRemoveIdentifier, id, "remove an identifier", "/auth/identifiers/"+url.PathEscape(id)+"/edit")
					return
				}
				h.renderIdentifierEdit(w, r, id, err)
				return
			}
			h.prgTo(w, r, accountPath)
			return
		}
		err := h.svc.SetIdentifierUses(r.Context(), authsvc.IdentifierUsesInput{
			SessionID: sessionID, UserID: userID, IdentifierID: id,
			Uses: formUses(form), MakePrimary: form.Get("primary") == "true",
		})
		if err != nil {
			if errors.Is(err, authsvc.ErrStepUpRequired) {
				h.stepUpRedirect(w, r, authgrant.PurposeChangeIdentifierUses, id, "update an identifier", "/auth/identifiers/"+url.PathEscape(id)+"/edit")
				return
			}
			h.renderIdentifierEdit(w, r, id, err)
			return
		}
		h.prgTo(w, r, accountPath)
	})
}

// renderIdentifierAdd re-renders the add-identifier form. The entered address is a
// contact value (echoed), never a secret; no ownership code is repopulated.
func (h *handlers) renderIdentifierAdd(w http.ResponseWriter, r *http.Request, kind, value string, form url.Values, err error) {
	status, msg := accountFailure(err)
	pc := h.newPageContext(w)
	pc.Message = msg
	m := IdentifierFormPage{
		PageContext: pc, Mode: "add", Kind: kind, Value: value,
		LoginEnabled: form.Get("login") == "true", RecoveryEnabled: form.Get("recovery") == "true",
		NotificationEnabled: form.Get("notification") == "true", MakePrimary: form.Get("primary") == "true",
	}
	h.renderForm(w, r, status, pc.CSPNonce, h.views.IdentifierForm(m))
}

// renderIdentifierConfirm re-renders the ownership-proof code entry form with generic
// copy at the mapped status. No code is repopulated (the model carries no such field);
// the entered address is not echoed on a confirm failure.
func (h *handlers) renderIdentifierConfirm(w http.ResponseWriter, r *http.Request, kind string, err error) {
	status, msg := accountFailure(err)
	pc := h.newPageContext(w)
	pc.Message = msg
	m := IdentifierFormPage{PageContext: pc, Mode: "confirm", Kind: kind}
	h.renderForm(w, r, status, pc.CSPNonce, h.views.IdentifierForm(m))
}

// renderIdentifierEdit re-renders the edit-identifier form from the current masked
// inventory (the existing address is shown masked).
func (h *handlers) renderIdentifierEdit(w http.ResponseWriter, r *http.Request, id string, err error) {
	status, msg := accountFailure(err)
	pc := h.newPageContext(w)
	pc.Message = msg
	m := IdentifierFormPage{PageContext: pc, Mode: "edit", ID: id}
	if userID, ok := h.svc.CurrentUser(r.Context()); ok {
		h.populateIdentifierEdit(r.Context(), userID, id, &m)
	}
	h.renderForm(w, r, status, pc.CSPNonce, h.views.IdentifierForm(m))
}

// ---------------------------------------------------------------------------
// OAuth unlink form handlers
// ---------------------------------------------------------------------------

func (h *handlers) startUnlinkOAuthForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, _, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		provider := web.Param(r, "provider")
		if _, err := h.svc.StartUnlinkOAuth(r.Context(), userID, provider); err != nil {
			h.renderOAuthUnlink(w, r, provider, err)
			return
		}
		h.prgTo(w, r, "/auth/oauth/"+url.PathEscape(provider)+"/unlink?sent=1")
	})
}

func (h *handlers) unlinkOAuthForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, _, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		provider := web.Param(r, "provider")
		// A code minted for a different provider is consumed and rejected without
		// unlinking — the form re-renders generically, never revealing the mismatch.
		if err := h.svc.UnlinkOAuth(r.Context(), userID, provider, form.Get("code")); err != nil {
			h.renderOAuthUnlink(w, r, provider, err)
			return
		}
		h.prgTo(w, r, accountPath)
	})
}

// renderOAuthUnlink re-renders the provider-bound unlink page with generic copy. The
// unlink code is delivered out-of-band and never echoed.
func (h *handlers) renderOAuthUnlink(w http.ResponseWriter, r *http.Request, provider string, err error) {
	status, msg := accountFailure(err)
	pc := h.newPageContext(w)
	pc.Message = msg
	m := OAuthUnlinkPage{PageContext: pc, Provider: provider}
	if userID, ok := h.svc.CurrentUser(r.Context()); ok {
		m.MaskedDestination = h.maskedRecovery(r.Context(), userID)
	}
	h.renderForm(w, r, status, pc.CSPNonce, h.views.OAuthUnlink(m))
}
