package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// authService is the narrow surface the handlers consume. *authsvc.Service
// satisfies it. Login, ChangePassword, IssueToken, and Refresh return the
// access/refresh TokenPair; the session row holds only the refresh token's hash
// (design §7.3). Accept interfaces, return structs.
type authService interface {
	Register(ctx context.Context, email, password, displayName string) (user.User, error)
	Verify(ctx context.Context, email, code string) error
	Login(ctx context.Context, email, password string) (pair authsvc.TokenPair, u user.User, err error)
	Logout(ctx context.Context, refreshToken, accessToken string) error
	Refresh(ctx context.Context, refreshToken string) (authsvc.TokenPair, error)
	ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (pair authsvc.TokenPair, err error)
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, token, newPassword string) error

	// Credential-suite password mutations (design §5.2/§5.3). SetPassword sets an
	// initial password behind a consumed set_password grant; the remove pair delivers
	// a remove_password code to a verified recovery identifier and completes through
	// the revision-serialized credential rail. Set and remove both revoke every
	// session and mint a fresh caller pair.
	SetPassword(ctx context.Context, sessionID, userID, newPassword string) (pair authsvc.TokenPair, err error)
	StartRemovePassword(ctx context.Context, userID string) (authsvc.StepUpReceipt, error)
	RemovePassword(ctx context.Context, userID, code string) (pair authsvc.TokenPair, err error)

	// DeliveryStatus is the live-session-gated delivery-status read (design
	// §6.1.1): a session-gated caller polls the durable outbox with its receipt key
	// to learn that delivery failed without holding the start request open.
	DeliveryStatus(ctx context.Context, receiptKey string) (delivery.Status, error)
	CurrentUser(ctx context.Context) (userID string, ok bool)
	CurrentSessionID(ctx context.Context) (sessionID string, ok bool)
	ActiveVerifiedIdentifier(ctx context.Context, userID, kind string) (string, error)

	// ResolveRedirect validates an HTML form's return-to against the exact redirect
	// allowlist, returning the safe destination or the same-origin default "/" (design
	// §9.2). The form dispatch calls it before every 303 so a browser flow can never be
	// bounced to a Host-derived or attacker-controlled URL.
	ResolveRedirect(target string) string

	// Methods is the live-session-gated masked method inventory (design §5.1):
	// password presence, typed OAuth methods, and the caller's active identifiers
	// with masked values, proof time, uses, and advisory removable hints.
	Methods(ctx context.Context, userID string) (authsvc.MethodsView, error)

	// Recent-authentication / step-up grant flow (design §5.0). BeginStepUp delivers
	// a code to an existing verified identifier; the completions earn a single-use,
	// operation-bound grant the later sensitive mutation consumes.
	BeginStepUp(ctx context.Context, in authsvc.StepUpStart) (authsvc.StepUpReceipt, error)
	CompleteStepUpWithPassword(ctx context.Context, in authsvc.StepUpCompletion, password string) (authgrant.Grant, error)
	CompleteStepUpWithIdentifierCode(ctx context.Context, in authsvc.StepUpCompletion, code string) (authgrant.Grant, error)
	SetSessionCookies(w http.ResponseWriter, pair authsvc.TokenPair)
	ClearSessionCookies(w http.ResponseWriter)
	SessionCookieName() string
	RefreshCookieName() string
	RequireUser(next http.Handler) http.Handler
	RequireLiveSession(next http.Handler) http.Handler
	RateLimitByIP(keyPrefix string, perMinute int) web.Middleware

	// OAuth flow (design §3). OAuthEnabled gates whether the OAuth routes are
	// registered at all (deny-by-absence).
	OAuthEnabled() bool
	StartOAuth(ctx context.Context, provider, redirectTo string) (authURL string, err error)
	StartLink(ctx context.Context, userID, provider, redirectTo string) (authURL string, err error)
	OAuthCallback(ctx context.Context, provider, code, state string) (authsvc.OAuthResult, error)
	VerifyLink(ctx context.Context, token string) (authsvc.OAuthResult, error)

	// Code-gated OAuth unlink (design §5.4). StartUnlinkOAuth delivers a
	// provider-bound unlink_oauth code to a verified recovery identifier; UnlinkOAuth
	// consumes it and unlinks through the revision-serialized credential rail.
	StartUnlinkOAuth(ctx context.Context, userID, provider string) (authsvc.StepUpReceipt, error)
	UnlinkOAuth(ctx context.Context, userID, provider, code string) error

	// Identifier management (design §5.5). Start delivers an ownership-proof code to
	// the proposed NEW address; Confirm consumes challenge + pending value and applies
	// the verified change under the revision-CAS. Remove and SetUses route through the
	// policy-guarded revision-serialized credential rail.
	StartIdentifierChange(ctx context.Context, in authsvc.IdentifierChangeStart) (authsvc.StepUpReceipt, error)
	ConfirmIdentifierChange(ctx context.Context, in authsvc.IdentifierChangeConfirm) error
	RemoveIdentifier(ctx context.Context, in authsvc.IdentifierRemoveInput) error
	SetIdentifierUses(ctx context.Context, in authsvc.IdentifierUsesInput) error

	// Machine identity (design §4.1). MachineEnabled gates whether the lifecycle
	// routes are registered at all (deny-by-absence).
	MachineEnabled() bool
	CreateServiceAccount(ctx context.Context, createdBy, name, description string, actAsUser bool, ownerUserID string) (serviceaccount.ServiceAccount, error)
	ListServiceAccounts(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error)
	MintAPIKey(ctx context.Context, serviceAccountID, name string, expiresAt time.Time) (apikey.APIKey, string, error)
	ListAPIKeys(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error)
	RevokeAPIKey(ctx context.Context, keyID string) error

	// Token endpoint (§1.1). TokenEnabled gates whether POST /auth/token is
	// registered at all (always true now the signer is required, D3).
	TokenEnabled() bool
	IssueToken(ctx context.Context, email, password string) (pair authsvc.TokenPair, err error)

	// Passwordless login (design §4). PasswordlessEnabled gates whether the
	// passwordless routes are registered at all (deny-by-absence). StartPasswordless
	// is the enumeration-safe asynchronous start: it never resolves the account or
	// calls a provider on the request path.
	PasswordlessEnabled() bool
	// PasswordlessKinds lists the enabled kinds in deterministic order; the HTML
	// start page renders the kind select/hidden-field from it so the form POST
	// always carries the kind the service validates.
	PasswordlessKinds() []string
	StartPasswordless(ctx context.Context, kind, identifier, method string) error
	// VerifyPasswordless completes the OTP rail: it re-resolves the current identifier,
	// consumes the login_otp bound to it, and mints a session. Every failure is one
	// generic 401; an exhausted verify budget is the only distinct outcome (429).
	VerifyPasswordless(ctx context.Context, kind, identifier, code string) (authsvc.TokenPair, error)
	// RedeemPasswordless completes the magic-link rail: it consumes the
	// login_magic_link token, reloads/validates the bound current identifier, and mints
	// a session. Every failure is one generic 401; an exhausted redeem budget is 429.
	RedeemPasswordless(ctx context.Context, token string) (authsvc.TokenPair, error)
}

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type forgotRequest struct {
	Email string `json:"email"`
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// deliveryStatusResponse is the live-session-gated delivery-status projection
// (design §6.1.1): lifecycle only, never a destination or secret.
type deliveryStatusResponse struct {
	State   string `json:"state"`
	Attempt int    `json:"attempt"`
	Pending bool   `json:"pending"`
	Failed  bool   `json:"failed"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// tokenResponse is the POST /auth/token and POST /auth/refresh success body
// (§1.1, breaking change from AV6): the session-backed access JWT, its absolute
// expiry (RFC3339), and the opaque refresh token. RefreshToken is omitted on the
// grace refresh lane (empty → omitempty), where the client keeps its existing
// refresh token.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresAt    string `json:"expires_at"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// refreshRequest is the optional POST /auth/refresh body for non-cookie (API)
// clients: the opaque refresh token. Browser clients present it via the refresh
// cookie instead and send no body.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// newTokenResponse renders a minted pair. AccessExpiresAt is formatted RFC3339.
func newTokenResponse(pair authsvc.TokenPair) tokenResponse {
	return tokenResponse{
		AccessToken:  pair.AccessToken,
		ExpiresAt:    pair.AccessExpiresAt.Format(time.RFC3339),
		RefreshToken: pair.RefreshToken,
	}
}

// userResponse is the compatibility user DTO (design V9): the public register/
// login signatures stay email-shaped, so the response keeps the email/verified
// fields even though identity now lives in user_identifiers, not on user.User.
type userResponse struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	EmailVerified bool   `json:"email_verified"`
}

// userResponseFor renders the compatibility DTO, sourcing the email/verified
// state from the caller's active verified email identifier — the authoritative
// post-v3 identity. While the primary is still unverified (a just-registered
// account has no verified identifier yet), it falls back to the submitted request
// email with email_verified=false.
func (h *handlers) userResponseFor(ctx context.Context, u user.User, requestEmail string) userResponse {
	email, verified := requestEmail, false
	if v, err := h.svc.ActiveVerifiedIdentifier(ctx, u.ID, identity.KindEmail); err == nil {
		email, verified = v, true
	}
	return userResponse{ID: u.ID, Email: email, DisplayName: u.DisplayName, EmailVerified: verified}
}

// registerJSON is the JSON transport for POST /auth/register. The content-type
// dispatcher (register, dispatch.go) routes an application/json body here; a form
// body goes to registerForm. The JSON DTO/status/body/cookie contract is unchanged.
func (h *handlers) registerJSON(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}
	u, err := h.svc.Register(r.Context(), req.Email, req.Password, req.DisplayName)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONCreated(w, h.userResponseFor(r.Context(), u, req.Email))
}

// loginJSON is the JSON transport for POST /auth/login (dispatched by login,
// dispatch.go). The JSON contract is unchanged; a form body goes to loginForm.
func (h *handlers) loginJSON(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	pair, u, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, authsvc.ErrRateLimited) {
			web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many login attempts").WithCode("rate_limited"))
			return
		}
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, pair)
	web.RespondJSONOK(w, h.userResponseFor(r.Context(), u, req.Email))
}

// token authenticates login-shaped credentials and returns the session-backed
// pair {access_token, expires_at, refresh_token} (§1.1, breaking change from
// AV6's stateless-only token). It shares /auth/login's request shape,
// pre-credential rate-limit discipline, and verified-email gating.
func (h *handlers) token(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	pair, err := h.svc.IssueToken(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, authsvc.ErrRateLimited) {
			web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many login attempts").WithCode("rate_limited"))
			return
		}
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newTokenResponse(pair))
}

// refresh rotates the caller's refresh token (§1.3). The token is taken from the
// refresh cookie (browser) or the JSON body (API); on success it sets both
// cookies (the refresh cookie only when a new refresh token was issued — the
// grace lane issues none) and returns the pair. Every denial (no token, unknown,
// expired, reuse) is a generic 401.
func (h *handlers) refresh(w http.ResponseWriter, r *http.Request) {
	presented := ""
	if c, err := r.Cookie(h.svc.RefreshCookieName()); err == nil {
		presented = c.Value
	}
	if presented == "" && r.ContentLength != 0 {
		var req refreshRequest
		if !decode(w, r, &req) {
			return
		}
		presented = req.RefreshToken
	}
	pair, err := h.svc.Refresh(r.Context(), presented)
	if err != nil {
		if errors.Is(err, authsvc.ErrRateLimited) {
			web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many requests").WithCode("rate_limited"))
			return
		}
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, pair)
	web.RespondJSONOK(w, newTokenResponse(pair))
}

// verifyJSON is the JSON transport for POST /auth/verify (dispatched by verify,
// dispatch.go). The JSON contract is unchanged; a form body goes to verifyForm.
func (h *handlers) verifyJSON(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.Verify(r.Context(), req.Email, req.Code); err != nil {
		// The challenge rail's stable sentinels surface here; respondDomainError emits
		// the named §5.8 codes (challenge_expired/invalid/too_many_attempts) and falls
		// back to the generic sdk-kind mapping for anything else.
		respondDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "verified"})
}

// deliveryUnavailable reports whether err is a bounded in-process delivery admission
// rejection — the queue is at capacity or the runtime is shutting down
// (delivery.ErrDeliveryCapacity / delivery.ErrDeliveryClosed). Both mean the work was
// NOT accepted, so the honest response is 503 Service Unavailable, never a
// 202-accepted lie after dropping work. The classification is by error KIND, identical
// for a known and an unknown identifier (admission precedes any account lookup), so it
// introduces no enumeration signal.
func deliveryUnavailable(err error) bool {
	return errors.Is(err, delivery.ErrDeliveryCapacity) || errors.Is(err, delivery.ErrDeliveryClosed)
}

// forgotPasswordJSON is the JSON transport for POST /auth/password/forgot
// (dispatched by forgotPassword, dispatch.go). The JSON contract is unchanged; a
// form body goes to forgotPasswordForm.
func (h *handlers) forgotPasswordJSON(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if !decode(w, r, &req) {
		return
	}
	// The service returns nil for unknown emails; a non-nil error here is a failure
	// class that is identical for registered and unregistered emails alike (the error
	// KIND never depends on existence), so the response still cannot enumerate. A
	// delivery admission rejection (bounded queue full / runtime shutting down) is an
	// honest 503 — never a 202 accepted after dropping the work; every other failure is
	// a 500.
	if err := h.svc.ForgotPassword(r.Context(), req.Email); err != nil {
		if deliveryUnavailable(err) {
			web.RespondJSONError(w, web.ErrUnavailable("could not process request"))
			return
		}
		web.RespondJSONError(w, web.ErrInternal("could not process request"))
		return
	}
	web.RespondJSONAccepted(w, map[string]string{"status": "accepted"})
}

// resetPasswordJSON is the JSON transport for POST /auth/password/reset
// (dispatched by resetPassword, dispatch.go). The JSON contract is unchanged; a
// form body goes to resetPasswordForm.
func (h *handlers) resetPasswordJSON(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "reset"})
}

// deliveryStatus is the live-session-gated delivery-status read (design §6.1.1). A
// session-gated caller polls the durable outbox with the receipt key it was handed
// to learn whether delivery is still pending, succeeded, or failed — without holding
// the original start request open. RequireLiveSession has already validated the
// caller; possession of the opaque, PII-free receipt is the rest of the
// authorization (it names no account and reveals only that caller's own delivery
// state). An unknown receipt is 404; the outbox being off is 403.
func (h *handlers) deliveryStatus(w http.ResponseWriter, r *http.Request) {
	receipt := strings.TrimSpace(r.URL.Query().Get("receipt"))
	if receipt == "" {
		web.RespondJSONError(w, web.ErrBadRequest("receipt is required"))
		return
	}
	st, err := h.svc.DeliveryStatus(r.Context(), receipt)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, deliveryStatusResponse{
		State:   st.State,
		Attempt: st.Attempt,
		Pending: st.Pending,
		Failed:  st.Failed,
	})
}

// changePassword dispatches POST /auth/password/change by Content-Type: the JSON
// arm keeps the existing contract, a form body renders/redirects through the HTML
// surface (only when Views is wired). Both call the same ChangePassword service.
func (h *handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r, h.changePasswordJSON, h.changePasswordForm)
}

// changePasswordJSON is session-gated (RequireLiveSession has already validated the
// caller and stashed the user id). It verifies the current password, sets the new
// one, revokes ALL the user's sessions, and sets a fresh session cookie for the
// caller (design §7.2). A wrong current password surfaces as 401.
func (h *handlers) changePasswordJSON(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	pair, err := h.svc.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, pair)
	web.RespondJSONOK(w, map[string]string{"status": "password_changed"})
}

// logoutJSON is the JSON transport for POST /auth/logout (dispatched by logout,
// dispatch.go). It revokes the session behind the caller's credentials and clears
// BOTH cookies (§1.5). It is NOT session-gated: an expired access JWT must still be
// able to log out (never a no-op). The service resolves the session id from the
// refresh token (primary) or the access JWT's session_id ignoring expiry
// (fallback); logout is idempotent regardless of the delete outcome. A form body
// goes to logoutForm.
func (h *handlers) logoutJSON(w http.ResponseWriter, r *http.Request) {
	refreshToken := ""
	if c, err := r.Cookie(h.svc.RefreshCookieName()); err == nil {
		refreshToken = c.Value
	}
	accessToken := ""
	if raw, ok := bearerToken(r); ok {
		accessToken = raw
	} else if c, err := r.Cookie(h.svc.SessionCookieName()); err == nil {
		accessToken = c.Value
	}
	_ = h.svc.Logout(r.Context(), refreshToken, accessToken)
	h.svc.ClearSessionCookies(w)
	web.RespondJSONOK(w, map[string]string{"status": "logged_out"})
}

// bearerToken extracts the token from an Authorization: Bearer header (the logout
// fallback lane reads the access JWT from it).
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	return tok, tok != ""
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// decode reads a JSON request body into dst, rejecting unknown fields. On
// failure it writes a 400 and returns false.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		web.RespondJSONError(w, web.ErrBadRequest("invalid request body"))
		return false
	}
	return true
}
