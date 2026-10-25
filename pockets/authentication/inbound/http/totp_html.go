package authenticationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The TOTP HTML pages and the outcome codes their PRG redirects carry.
const (
	totpPagePath       = "/auth/account/totp"
	totpEnrollPagePath = "/auth/account/totp/enroll"
	mfaStepUpPagePath  = "/auth/step-up/mfa"

	outcomeTOTPRemoved = "totp_removed"

	// totpAlreadySetUpMsg is the manage page's copy after a re-POSTed confirm:
	// the codes were shown once and are not shown again.
	totpAlreadySetUpMsg = "Your authenticator app is already set up. If you didn't save your recovery codes, generate new ones."
	// totpExpiredMsg is the copy for a lapsed or missing enrollment.
	totpExpiredMsg = "That setup expired. Start again to get a new key."
	// totpStaleMsg is the copy for a regenerate whose codes changed meanwhile.
	totpStaleMsg = "Your recovery codes changed since this page loaded. Reload it before generating new ones."
)

// TOTPViews renders the TOTP pages (authentication.TOTPConfig.Views). It is
// separate from Views, as PasskeyViews is, so the Views interface never grows.
// Nil leaves the TOTP routes JSON-only.
type TOTPViews interface {
	// TOTP is the manage page: set up, remove, recovery-code status and
	// regeneration.
	TOTP(m TOTPPage) web.Renderer
	// TOTPEnroll is the enrollment page: the key, the otpauth:// link (and a QR
	// code where the view renders one) and the confirm form.
	TOTPEnroll(m TOTPEnrollPage) web.Renderer
	// RecoveryCodes shows a freshly generated recovery-code set, once.
	RecoveryCodes(m RecoveryCodesPage) web.Renderer
	// MFAStepUp is the "Confirm it's you" chooser a Browser() gate with
	// RequireMFA() redirects an AAL1 cookie session to while TOTP views are wired.
	MFAStepUp(m MFAStepUpPage) web.Renderer
}

// TOTPLinks carries every URL the TOTP controls use, so a template hard-codes
// none of them. Manage, ChooserPage and Remove (the form lane of DELETE
// /auth/totp) are empty unless TOTPViews is wired.
type TOTPLinks struct {
	Manage             string
	Enroll             string
	Confirm            string
	Remove             string
	RecoveryCodes      string
	StepUp             string
	RecoveryCodeStepUp string
	ChooserPage        string
}

// TOTPPage models the manage page. RecoveryCodesGeneratedAt is the current
// set's generation (RFC 3339); the regenerate form carries it back as the
// hidden expected_generated_at.
type TOTPPage struct {
	PageContext
	Links                    TOTPLinks
	Enrolled                 bool
	Pending                  bool
	EnrolledAt               string
	LastUsedAt               string
	RecoveryCodesRemaining   int
	RecoveryCodesGeneratedAt string
}

// TOTPEnrollPage models the enrollment page. Secret is the key in
// space-separated groups of four; OTPAuthURI is the otpauth:// URI a view links
// and may render as a QR code. The page re-reads the pending enrollment, so a
// reload or a wrong code shows the same key.
type TOTPEnrollPage struct {
	PageContext
	Links      TOTPLinks
	Secret     string
	OTPAuthURI string
	Account    string
	Issuer     string
	ExpiresAt  string
}

// RecoveryCodesPage shows a recovery-code set once. Continue is where the "I
// saved these" link goes.
type RecoveryCodesPage struct {
	PageContext
	Links       TOTPLinks
	Codes       []string
	GeneratedAt string
	Continue    string
}

// MFAStepUpPage models the chooser, filtered by MFAConfig.Methods: the passkey
// control first when an AAL2-capable passkey is held (Passkeys carries its
// endpoints), the authenticator-app form when one is confirmed, the
// recovery-code form beside it, and otherwise the "Add a passkey" /
// "Set up an authenticator app" links.
type MFAStepUpPage struct {
	PageContext
	Passkeys          *PasskeyLinks
	TOTP              TOTPLinks
	OfferPasskey      bool
	OfferTOTP         bool
	OfferRecoveryCode bool
	OfferAddPasskey   bool
	OfferSetUpTOTP    bool
}

// totpLinks returns the page-model links, or nil when TOTP is off.
func (h *handlers) totpLinks() *TOTPLinks {
	if h.totp == nil {
		return nil
	}
	links := &TOTPLinks{
		Enroll:             "/auth/totp/enrollment",
		Confirm:            "/auth/totp/enrollment/confirm",
		RecoveryCodes:      "/auth/recovery-codes",
		StepUp:             "/auth/step-up/totp",
		RecoveryCodeStepUp: "/auth/step-up/recovery-code",
	}
	if h.totpViews != nil {
		links.Manage, links.ChooserPage, links.Remove = totpPagePath, mfaStepUpPagePath, "/auth/totp"
	}
	return links
}

// mountTOTPHTML registers the TOTP pages and the manage page's remove form lane
// (an HTML form cannot emit DELETE).
func mountTOTPHTML(r pockets.RouteRegistrar, h *handlers, browserAccount, browserSafeForm web.Middleware) {
	live := htmlSecured(h.htmlPolicy, browserAccount)
	r.Handle("GET", totpPagePath, h.totpPage, live)
	r.Handle("GET", totpEnrollPagePath, h.totpEnrollPage, live)
	r.Handle("GET", mfaStepUpPagePath, h.mfaStepUpPage, live)
	r.Handle("POST", "/auth/totp", h.removeTOTPForm, live, browserSafeForm)
}

// totpForm runs fn as a TOTP form arm: only when the TOTP pages are mounted
// (else 415, as an unmounted form lane is), after the double-submit CSRF check.
func (h *handlers) totpForm(w http.ResponseWriter, r *http.Request, fn func(form url.Values)) {
	if h.totpViews == nil {
		unsupportedMediaType(w)
		return
	}
	h.accountForm(w, r, fn)
}

func (h *handlers) totpPage(w http.ResponseWriter, r *http.Request) {
	h.renderTOTP(w, r, http.StatusOK, "")
}

// renderTOTP renders the manage page at status with message.
func (h *handlers) renderTOTP(w http.ResponseWriter, r *http.Request, status int, message string) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		h.renderError(w, r, http.StatusUnauthorized, "Please sign in to continue.")
		return
	}
	st, err := h.totp.TOTPStatus(r.Context(), userID)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong. Please try again.")
		return
	}
	pc := h.newPageContext(w)
	pc.ReturnTo = h.validatedReturnTo(r)
	pc.Message = message
	m := TOTPPage{
		PageContext: pc, Links: *h.totpLinks(), Enrolled: st.Enrolled, Pending: st.Pending,
		EnrolledAt: rfc3339(st.EnrolledAt), LastUsedAt: rfc3339(st.LastUsedAt),
		RecoveryCodesRemaining: st.RecoveryCodesRemaining, RecoveryCodesGeneratedAt: rfc3339(st.RecoveryCodesGeneratedAt),
	}
	h.renderForm(w, r, status, pc.CSPNonce, h.totpViews.TOTP(m))
}

// totpEnrollPage re-reads the caller's pending enrollment (the PRG target of
// the enrollment POST). No live enrollment lands on the manage page.
func (h *handlers) totpEnrollPage(w http.ResponseWriter, r *http.Request) {
	h.renderTOTPEnroll(w, r, http.StatusOK, "")
}

func (h *handlers) renderTOTPEnroll(w http.ResponseWriter, r *http.Request, status int, message string) {
	userID, ok := h.svc.CurrentUser(r.Context())
	sessionID, sok := h.svc.CurrentSessionID(r.Context())
	if !ok || !sok {
		h.renderError(w, r, http.StatusUnauthorized, "Please sign in to continue.")
		return
	}
	e, err := h.totp.PendingTOTPEnrollment(r.Context(), sessionID, userID)
	switch {
	case errors.Is(err, authlogic.ErrTOTPExists):
		h.renderTOTP(w, r, http.StatusConflict, totpAlreadySetUpMsg)
		return
	case errors.Is(err, authlogic.ErrTOTPEnrollmentExpired):
		h.renderTOTP(w, r, http.StatusGone, totpExpiredMsg)
		return
	case err != nil:
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong. Please try again.")
		return
	}
	pc := h.newPageContext(w)
	pc.Message = message
	grouped := e.Secret
	if raw, err := totp.DecodeSecret(e.Secret); err == nil {
		grouped = totp.FormatSecret(raw)
	}
	m := TOTPEnrollPage{
		PageContext: pc, Links: *h.totpLinks(), Secret: grouped, OTPAuthURI: e.KeyURI,
		Account: e.Account, Issuer: e.Issuer, ExpiresAt: e.ExpiresAt.UTC().Format(time.RFC3339),
	}
	h.renderForm(w, r, status, pc.CSPNonce, h.totpViews.TOTPEnroll(m))
}

// beginTOTPEnrollmentForm starts an enrollment and PRGs to the enrollment page.
// A missing step-up goes to the chooser for an AAL2-factor holder, else to the
// code or password step-up page bound to register_totp.
func (h *handlers) beginTOTPEnrollmentForm(w http.ResponseWriter, r *http.Request) {
	h.totpForm(w, r, func(url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		_, err := h.totp.BeginTOTPEnrollment(r.Context(), sessionID, userID)
		switch {
		case err == nil:
			h.prgTo(w, r, totpEnrollPagePath)
		case errors.Is(err, authlogic.ErrStepUpRequired):
			h.secondFactorStepUpRedirect(w, r, userID, authgrant.PurposeRegisterTOTP, "set up an authenticator app")
		case errors.Is(err, authlogic.ErrTOTPExists):
			h.renderTOTP(w, r, http.StatusConflict, totpAlreadySetUpMsg)
		default:
			status, msg := accountFailure(err)
			h.renderTOTP(w, r, status, msg)
		}
	})
}

// confirmTOTPEnrollmentForm confirms the enrollment and renders the recovery
// codes DIRECTLY — no PRG, since they are shown once. A wrong code re-renders
// the enrollment page with the same key; a re-POST (refresh) lands on the
// manage page with the "already set up" copy.
func (h *handlers) confirmTOTPEnrollmentForm(w http.ResponseWriter, r *http.Request) {
	h.totpForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		c, err := h.totp.ConfirmTOTPEnrollment(r.Context(), authlogic.TOTPConfirmInput{SessionID: sessionID, UserID: userID, Code: form.Get("code")})
		switch {
		case err == nil:
			h.renderRecoveryCodes(w, r, c.RecoveryCodes)
		case errors.Is(err, authlogic.ErrTOTPExists), errors.Is(err, authlogic.ErrTOTPEnrollmentExpired):
			status, _ := accountFailure(err)
			h.renderTOTP(w, r, status, totpAlreadySetUpMsg)
		case errors.Is(err, authlogic.ErrStepUpRequired):
			h.secondFactorStepUpRedirect(w, r, userID, authgrant.PurposeRegisterTOTP, "set up an authenticator app")
		default:
			status, msg := accountFailure(err)
			writeRetryAfter(w, err)
			h.renderTOTPEnroll(w, r, status, msg)
		}
	})
}

// regenerateRecoveryCodesForm replaces the recovery codes and renders them once.
// The form carries the generation it read as expected_generated_at, so a re-POST
// is refused as stale.
func (h *handlers) regenerateRecoveryCodesForm(w http.ResponseWriter, r *http.Request) {
	h.totpForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		var expected *time.Time
		if raw := form.Get("expected_generated_at"); raw != "" {
			at, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				h.renderTOTP(w, r, http.StatusBadRequest, accountErrMsg)
				return
			}
			expected = &at
		}
		set, err := h.totp.RegenerateRecoveryCodes(r.Context(), authlogic.RegenerateRecoveryCodesInput{SessionID: sessionID, UserID: userID, ExpectedGeneratedAt: expected})
		switch {
		case err == nil:
			h.renderRecoveryCodes(w, r, set)
		case errors.Is(err, authlogic.ErrStepUpRequired):
			h.secondFactorStepUpRedirect(w, r, userID, authgrant.PurposeRegenerateRecoveryCodes, "generate new recovery codes")
		case errors.Is(err, authlogic.ErrRecoveryCodesStale):
			h.renderTOTP(w, r, http.StatusConflict, totpStaleMsg)
		default:
			status, msg := accountFailure(err)
			h.renderTOTP(w, r, status, msg)
		}
	})
}

// renderRecoveryCodes renders a fresh set once.
func (h *handlers) renderRecoveryCodes(w http.ResponseWriter, r *http.Request, set authlogic.RecoveryCodeSet) {
	pc := h.newPageContext(w)
	m := RecoveryCodesPage{PageContext: pc, Links: *h.totpLinks(), Codes: set.Codes, GeneratedAt: set.GeneratedAt.UTC().Format(time.RFC3339), Continue: totpPagePath}
	h.renderForm(w, r, http.StatusOK, pc.CSPNonce, h.totpViews.RecoveryCodes(m))
}

// removeTOTPForm is the manage page's remove form lane. A completed removal has
// revoked every session, so it clears the cookies and lands on sign-in; a
// missing step-up goes to the chooser.
func (h *handlers) removeTOTPForm(w http.ResponseWriter, r *http.Request) {
	if classifyContent(r) == contentJSON {
		h.removeTOTP(w, r)
		return
	}
	h.totpForm(w, r, func(url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		signedOut, err := h.totp.RemoveTOTP(r.Context(), authlogic.TOTPRemoveInput{SessionID: sessionID, UserID: userID})
		switch {
		case err == nil && signedOut:
			h.svc.ClearSessionCookies(w)
			h.prgTo(w, r, loginDone(outcomeTOTPRemoved))
		case err == nil:
			h.prgTo(w, r, totpPagePath)
		case errors.Is(err, authlogic.ErrStepUpRequired):
			h.secondFactorStepUpRedirect(w, r, userID, authgrant.PurposeRemoveTOTP, "remove your authenticator app")
		default:
			status, msg := accountFailure(err)
			h.renderTOTP(w, r, status, msg)
		}
	})
}

// stepUpCodeForm completes a TOTP or recovery-code step-up from the chooser:
// the new cookies are set and the caller PRGs to the validated return_to. A
// refusal re-renders the chooser with the error (and Retry-After on a lockout).
func (h *handlers) stepUpCodeForm(w http.ResponseWriter, r *http.Request, step func(context.Context, authlogic.StepUpCodeInput) (authlogic.StepUpResult, error)) {
	h.totpForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		result, err := step(r.Context(), authlogic.StepUpCodeInput{SessionID: sessionID, UserID: userID, Code: form.Get("code")})
		if err != nil {
			status, msg := accountFailure(err)
			writeRetryAfter(w, err)
			h.renderMFAStepUp(w, r, userID, status, msg, h.safeReturnTo(form.Get("return_to")))
			return
		}
		h.svc.SetSessionCookies(w, result.Tokens)
		h.prgRedirect(w, r, form.Get("return_to"))
	})
}

// secondFactorStepUpRedirect sends a caller whose second-factor mutation needs
// a step-up to the right page: the chooser (AAL2 is the gate) for a holder of
// an AAL2 factor, else the code or password step-up page bound to purpose. The
// manage page is the return_to either way; both paths keep the mount prefix the
// browser login path carries, as the Browser() redirect does.
func (h *handlers) secondFactorStepUpRedirect(w http.ResponseWriter, r *http.Request, userID, purpose, operation string) {
	manage := h.svc.pagePath(totpPagePath)
	if holds, err := h.totp.HoldsAAL2Factor(r.Context(), userID); err == nil && holds {
		h.prgTo(w, r, h.svc.pagePath(mfaStepUpPagePath)+"?return_to="+url.QueryEscape(manage))
		return
	}
	h.stepUpRedirect(w, r, purpose, "", operation, manage)
}

// mfaStepUpPage renders the chooser. Its return_to is honoured on GET and HEAD
// only, validated like the login page's.
func (h *handlers) mfaStepUpPage(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		h.renderError(w, r, http.StatusUnauthorized, "Please sign in to continue.")
		return
	}
	h.renderMFAStepUp(w, r, userID, http.StatusOK, "", h.validatedReturnTo(r))
}

// renderMFAStepUp builds the chooser from the caller's inventory, filtered by
// MFAConfig.Methods.
func (h *handlers) renderMFAStepUp(w http.ResponseWriter, r *http.Request, userID string, status int, message, returnTo string) {
	pc := h.newPageContext(w)
	pc.ReturnTo, pc.Message = returnTo, message
	m := MFAStepUpPage{PageContext: pc, TOTP: *h.totpLinks()}
	allowed := h.svc.mfaAllows
	if h.passkeys != nil && allowed(authlogic.MFAMethodPasskey) {
		m.Passkeys = h.passkeyLinks()
		if list, err := h.passkeys.ListPasskeys(r.Context(), userID); err == nil && slices.ContainsFunc(list, passkey.Passkey.AAL2Capable) {
			m.OfferPasskey = true
		} else {
			m.OfferAddPasskey = h.passkeyViews != nil
		}
	}
	if allowed(authlogic.MFAMethodTOTP) {
		if st, err := h.totp.TOTPStatus(r.Context(), userID); err == nil && st.Enrolled {
			m.OfferTOTP, m.OfferRecoveryCode = true, st.RecoveryCodesRemaining > 0
		} else {
			m.OfferSetUpTOTP = true
		}
	}
	h.renderForm(w, r, status, pc.CSPNonce, h.totpViews.MFAStepUp(m))
}

// writeRetryAfter sets Retry-After on a second-factor lockout or exhausted
// budget.
func writeRetryAfter(w http.ResponseWriter, err error) {
	if _, retryAfter, ok := totpErrorFor(err); ok && retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
}
