package authenticationhttp

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// rateLimitedRetryAfter is the Retry-After, in seconds, of a second-factor
// request over its budget.
const rateLimitedRetryAfter = 60

var _ totpService = (*authlogic.Service)(nil)

// The TOTP JSON routes (SOC 2 S4c). Every route sits under /auth, so the mount's
// no-store wrap covers it. The wire contract (totp_test.go pins it with
// goldens; SPAs and the goth forms share it):
//
//   - POST /auth/totp/enrollment {} → 200 {secret, otpauth_uri, issuer, account,
//     algorithm, digits, period, expires_at}; 403 when the gate needs a step-up,
//     409 totp_exists;
//   - POST /auth/totp/enrollment/confirm {code} → 201 {enrolled_at,
//     recovery_codes, generated_at} (the codes are shown once); 410
//     enrollment_expired, 401 totp_rejected, 409 totp_exists, 429;
//   - GET /auth/totp → {enrolled, pending, enrolled_at?, last_used_at?,
//     recovery_codes_remaining}; DELETE /auth/totp → {status: "removed"}, the
//     cookies cleared and EVERY session revoked (bearer tokens die too);
//   - GET /auth/recovery-codes → {remaining, generated_at?}; POST
//     {expected_generated_at?} → 201 {recovery_codes, generated_at}; 409
//     recovery_codes_stale;
//   - POST /auth/step-up/totp and /auth/step-up/recovery-code {code} → exactly
//     as POST /auth/login plus assurance (a bearer caller also gets the new token
//     pair); 401 totp_rejected / recovery_code_rejected; 429 too_many_attempts
//     with Retry-After;
//   - POST /auth/admin/users/{id}/totp/reset {} → {removed}.
//
// The secret appears in the enrollment response and nowhere else; no response
// carries a digest, ciphertext or another user's data.

// totpService is the TOTP capability the routes drive. It is resolved from the
// AuthenticationService at New, so the exported service interface does not
// grow a method; *authlogic.Service implements it.
type totpService interface {
	TOTPEnabled() bool
	HoldsAAL2Factor(ctx context.Context, userID string) (bool, error)
	BeginTOTPEnrollment(ctx context.Context, sessionID, userID string) (authlogic.TOTPEnrollment, error)
	PendingTOTPEnrollment(ctx context.Context, sessionID, userID string) (authlogic.TOTPEnrollment, error)
	ConfirmTOTPEnrollment(ctx context.Context, in authlogic.TOTPConfirmInput) (authlogic.TOTPConfirmation, error)
	StepUpWithTOTP(ctx context.Context, in authlogic.StepUpCodeInput) (authlogic.StepUpResult, error)
	StepUpWithRecoveryCode(ctx context.Context, in authlogic.StepUpCodeInput) (authlogic.StepUpResult, error)
	TOTPStatus(ctx context.Context, userID string) (authlogic.TOTPStatus, error)
	RemoveTOTP(ctx context.Context, in authlogic.TOTPRemoveInput) error
	RegenerateRecoveryCodes(ctx context.Context, in authlogic.RegenerateRecoveryCodesInput) (authlogic.RecoveryCodeSet, error)
	ResetUserTOTP(ctx context.Context, actor authlogic.Principal, userID string) (bool, error)
}

// totpEnrollmentResponse is the enrollment body.
type totpEnrollmentResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURI string `json:"otpauth_uri"`
	Issuer     string `json:"issuer"`
	Account    string `json:"account"`
	Algorithm  string `json:"algorithm"`
	Digits     int    `json:"digits"`
	Period     int    `json:"period"`
	ExpiresAt  string `json:"expires_at"`
}

// totpCodeRequest is every {code} body.
type totpCodeRequest struct {
	Code string `json:"code"`
}

// totpConfirmResponse is the confirm body.
type totpConfirmResponse struct {
	EnrolledAt    string   `json:"enrolled_at"`
	RecoveryCodes []string `json:"recovery_codes"`
	GeneratedAt   string   `json:"generated_at"`
}

// totpStatusResponse is the GET /auth/totp body.
type totpStatusResponse struct {
	Enrolled               bool   `json:"enrolled"`
	Pending                bool   `json:"pending"`
	EnrolledAt             string `json:"enrolled_at,omitempty"`
	LastUsedAt             string `json:"last_used_at,omitempty"`
	RecoveryCodesRemaining int    `json:"recovery_codes_remaining"`
}

// recoveryCodesStatusResponse is the GET /auth/recovery-codes body.
type recoveryCodesStatusResponse struct {
	Remaining   int    `json:"remaining"`
	GeneratedAt string `json:"generated_at,omitempty"`
}

// recoveryCodesRequest is the POST /auth/recovery-codes body.
type recoveryCodesRequest struct {
	ExpectedGeneratedAt string `json:"expected_generated_at"`
}

// recoveryCodesResponse is a freshly generated set.
type recoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
	GeneratedAt   string   `json:"generated_at"`
}

// totpResetResponse is the admin reset body.
type totpResetResponse struct {
	Removed bool `json:"removed"`
}

func newTOTPEnrollmentResponse(e authlogic.TOTPEnrollment) totpEnrollmentResponse {
	return totpEnrollmentResponse{
		Secret: e.Secret, OTPAuthURI: e.KeyURI, Issuer: e.Issuer, Account: e.Account,
		Algorithm: totp.Algorithm, Digits: totp.Digits, Period: int(totp.Period / time.Second),
		ExpiresAt: e.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

func rfc3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// mountTOTP registers the TOTP JSON routes. Every route runs the
// CredentialManagement authenticator — reads the SessionSecurityReads one — and
// the mutations the browser-safe gate; the routes with a form arm take the form
// lane of that gate.
func mountTOTP(r pockets.RouteRegistrar, h *handlers, auth RouteAuthentication, browserSafe, browserSafeForm web.Middleware) {
	r.Handle("POST", "/auth/totp/enrollment", h.beginTOTPEnrollment, auth.CredentialManagement, browserSafeForm)
	r.Handle("POST", "/auth/totp/enrollment/confirm", h.confirmTOTPEnrollment, auth.CredentialManagement, browserSafeForm)
	r.Handle("GET", "/auth/totp", h.totpStatus, auth.SessionSecurityReads)
	r.Handle("DELETE", "/auth/totp", h.removeTOTP, auth.CredentialManagement, browserSafe)
	r.Handle("GET", "/auth/recovery-codes", h.recoveryCodesStatus, auth.SessionSecurityReads)
	r.Handle("POST", "/auth/recovery-codes", h.regenerateRecoveryCodes, auth.CredentialManagement, browserSafeForm)
	r.Handle("POST", "/auth/step-up/totp", h.stepUpTOTP, auth.CredentialManagement, browserSafeForm)
	r.Handle("POST", "/auth/step-up/recovery-code", h.stepUpRecoveryCode, auth.CredentialManagement, browserSafeForm)
}

func (h *handlers) beginTOTPEnrollment(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.beginTOTPEnrollmentJSON, h.beginTOTPEnrollmentForm)
}

func (h *handlers) confirmTOTPEnrollment(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.confirmTOTPEnrollmentJSON, h.confirmTOTPEnrollmentForm)
}

func (h *handlers) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.regenerateRecoveryCodesJSON, h.regenerateRecoveryCodesForm)
}

func (h *handlers) stepUpTOTP(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, func(w http.ResponseWriter, r *http.Request) { h.stepUpCodeJSON(w, r, h.totp.StepUpWithTOTP) },
		func(w http.ResponseWriter, r *http.Request) { h.stepUpCodeForm(w, r, h.totp.StepUpWithTOTP) })
}

func (h *handlers) stepUpRecoveryCode(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, func(w http.ResponseWriter, r *http.Request) { h.stepUpCodeJSON(w, r, h.totp.StepUpWithRecoveryCode) },
		func(w http.ResponseWriter, r *http.Request) { h.stepUpCodeForm(w, r, h.totp.StepUpWithRecoveryCode) })
}

func (h *handlers) beginTOTPEnrollmentJSON(w http.ResponseWriter, r *http.Request) {
	if !passkeyEmptyBody(w, r) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	e, err := h.totp.BeginTOTPEnrollment(r.Context(), sessionID, userID)
	if err != nil {
		respondTOTPError(w, err)
		return
	}
	web.RespondJSONOK(w, newTOTPEnrollmentResponse(e))
}

func (h *handlers) confirmTOTPEnrollmentJSON(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req totpCodeRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	c, err := h.totp.ConfirmTOTPEnrollment(r.Context(), authlogic.TOTPConfirmInput{SessionID: sessionID, UserID: userID, Code: req.Code})
	if err != nil {
		respondTOTPError(w, err)
		return
	}
	web.RespondJSONCreated(w, totpConfirmResponse{
		EnrolledAt:    c.EnrolledAt.UTC().Format(time.RFC3339),
		RecoveryCodes: c.RecoveryCodes.Codes,
		GeneratedAt:   c.RecoveryCodes.GeneratedAt.UTC().Format(time.RFC3339),
	})
}

func (h *handlers) totpStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	status, err := h.totp.TOTPStatus(r.Context(), userID)
	if err != nil {
		respondTOTPError(w, err)
		return
	}
	web.RespondJSONOK(w, totpStatusResponse{
		Enrolled: status.Enrolled, Pending: status.Pending,
		EnrolledAt: rfc3339(status.EnrolledAt), LastUsedAt: rfc3339(status.LastUsedAt),
		RecoveryCodesRemaining: status.RecoveryCodesRemaining,
	})
}

// removeTOTP removes the authenticator app behind its step-up grant. The removal
// revokes every session of the user, so it clears the caller's cookies too.
func (h *handlers) removeTOTP(w http.ResponseWriter, r *http.Request) {
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.totp.RemoveTOTP(r.Context(), authlogic.TOTPRemoveInput{SessionID: sessionID, UserID: userID}); err != nil {
		respondTOTPError(w, err)
		return
	}
	h.svc.ClearSessionCookies(w)
	web.RespondJSONOK(w, map[string]string{"status": "removed"})
}

func (h *handlers) recoveryCodesStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	status, err := h.totp.TOTPStatus(r.Context(), userID)
	if err != nil {
		respondTOTPError(w, err)
		return
	}
	web.RespondJSONOK(w, recoveryCodesStatusResponse{Remaining: status.RecoveryCodesRemaining, GeneratedAt: rfc3339(status.RecoveryCodesGeneratedAt)})
}

func (h *handlers) regenerateRecoveryCodesJSON(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req recoveryCodesRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	expected, ok := parseExpectedGeneratedAt(w, req.ExpectedGeneratedAt)
	if !ok {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	set, err := h.totp.RegenerateRecoveryCodes(r.Context(), authlogic.RegenerateRecoveryCodesInput{SessionID: sessionID, UserID: userID, ExpectedGeneratedAt: expected})
	if err != nil {
		respondTOTPError(w, err)
		return
	}
	web.RespondJSONCreated(w, recoveryCodesResponse{RecoveryCodes: set.Codes, GeneratedAt: set.GeneratedAt.UTC().Format(time.RFC3339)})
}

// parseExpectedGeneratedAt reads an optional RFC 3339 precondition; a malformed
// one is a 400.
func parseExpectedGeneratedAt(w http.ResponseWriter, raw string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("expected_generated_at must be an RFC 3339 time"))
		return nil, false
	}
	return &at, true
}

// stepUpCodeJSON completes a TOTP or recovery-code step-up and answers exactly
// as POST /auth/login plus the assurance; a bearer caller, whose old tokens the
// re-mint revoked, also gets the new token pair.
func (h *handlers) stepUpCodeJSON(w http.ResponseWriter, r *http.Request, step func(context.Context, authlogic.StepUpCodeInput) (authlogic.StepUpResult, error)) {
	if !requireJSON(w, r) {
		return
	}
	var req totpCodeRequest
	if !strictJSONBody(w, r, &req, maxJSONBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	cred, _ := h.svc.CurrentCredential(r.Context())
	result, err := step(r.Context(), authlogic.StepUpCodeInput{SessionID: sessionID, UserID: userID, Code: req.Code})
	if err != nil {
		respondTOTPError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, result.Tokens)
	out := passkeyStepUpResponse{userResponse: h.userResponseFor(r.Context(), result.User, ""), Assurance: string(result.Assurance)}
	if cred.Transport == TransportHeader {
		pair := newTokenResponse(result.Tokens)
		out.AccessToken, out.ExpiresAt, out.RefreshToken = pair.AccessToken, pair.ExpiresAt, pair.RefreshToken
	}
	web.RespondJSONOK(w, out)
}

// adminResetTOTP removes the target user's authenticator app behind the admin
// gate chain (UserAdminCheck, then the admins MFA gate; an admin API key passes
// it).
func (h *handlers) adminResetTOTP(w http.ResponseWriter, r *http.Request) {
	if !passkeyEmptyBody(w, r) {
		return
	}
	id := web.Param(r, "id")
	principal, ok := h.adminPrincipal(w, r, UserAdminResetTOTP, id)
	if !ok {
		return
	}
	removed, err := h.totp.ResetUserTOTP(r.Context(), principal, id)
	if err != nil {
		respondTOTPError(w, err)
		return
	}
	web.RespondJSONOK(w, totpResetResponse{Removed: removed})
}

// totpErrorFor maps a second-factor refusal to its wire code: ErrTOTPExists →
// totp_exists (409), ErrTOTPEnrollmentExpired → enrollment_expired (410),
// ErrTOTPRejected → totp_rejected (401), ErrRecoveryCodeRejected →
// recovery_code_rejected (401), ErrRecoveryCodesStale → recovery_codes_stale
// (409), and a lockout or an exhausted budget → too_many_attempts (429) with
// the seconds to wait. It returns (nil, 0, false) for any other error.
func totpErrorFor(err error) (*web.Error, int, bool) {
	var locked *authlogic.SecondFactorLockedError
	switch {
	case errors.As(err, &locked):
		wait := int(math.Ceil(locked.Wait.Seconds()))
		return web.NewError(http.StatusTooManyRequests, "too many attempts").WithCode("too_many_attempts"), max(wait, 1), true
	case errors.Is(err, authlogic.ErrRateLimited):
		return web.NewError(http.StatusTooManyRequests, "too many attempts").WithCode("too_many_attempts"), rateLimitedRetryAfter, true
	case errors.Is(err, authlogic.ErrTOTPExists):
		return web.NewError(http.StatusConflict, "an authenticator app is already set up").WithCode("totp_exists"), 0, true
	case errors.Is(err, authlogic.ErrTOTPEnrollmentExpired):
		return web.NewError(http.StatusGone, "the enrollment expired").WithCode("enrollment_expired"), 0, true
	case errors.Is(err, authlogic.ErrTOTPRejected):
		return web.NewError(http.StatusUnauthorized, "authenticator code rejected").WithCode("totp_rejected"), 0, true
	case errors.Is(err, authlogic.ErrRecoveryCodeRejected):
		return web.NewError(http.StatusUnauthorized, "recovery code rejected").WithCode("recovery_code_rejected"), 0, true
	case errors.Is(err, authlogic.ErrRecoveryCodesStale):
		return web.NewError(http.StatusConflict, "the recovery codes changed").WithCode("recovery_codes_stale"), 0, true
	}
	return nil, 0, false
}

// respondTOTPError writes a TOTP route's error: the TOTP codes first, then the
// shared mapping (a missing step-up is 403).
func respondTOTPError(w http.ResponseWriter, err error) {
	if mapped, retryAfter, ok := totpErrorFor(err); ok {
		if retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		}
		web.RespondJSONError(w, mapped)
		return
	}
	respondDomainError(w, err)
}
