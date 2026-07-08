// Package http is the auth feature's JSON transport: request/response DTOs, the
// handlers over the domain service, and the route table. v1 is JSON-API only
// (no server-rendered views), so a host that wants login pages renders its own
// form and calls these endpoints, exactly as a SPA or mobile client would.
// Mounted only through feature.RouteRegistrar (see auth.Register).
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/logic/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/logic/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/logic/user"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// authService is the narrow surface the handlers consume. *authsvc.Service
// satisfies it. Login and ChangePassword return the plaintext session cookie
// token to set (the stored session value is that token's hash — design §7.3).
// Accept interfaces, return structs.
type authService interface {
	Register(ctx context.Context, email, password, displayName string) (user.User, error)
	Verify(ctx context.Context, code string) error
	Login(ctx context.Context, email, password string) (token string, u user.User, err error)
	Logout(ctx context.Context, token string) error
	ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (token string, err error)
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, token, newPassword string) error
	CurrentUser(ctx context.Context) (userID string, ok bool)
	EmailForUser(ctx context.Context, userID string) (string, error)
	SetSessionCookie(w http.ResponseWriter, token string)
	ClearSessionCookie(w http.ResponseWriter)
	SessionCookieName() string
	RequireUser(next http.Handler) http.Handler
	RateLimitByIP(keyPrefix string, perMinute int) web.Middleware

	// OAuth flow (design §3). OAuthEnabled gates whether the OAuth routes are
	// registered at all (deny-by-absence).
	OAuthEnabled() bool
	StartOAuth(ctx context.Context, provider, redirectTo string) (authURL string, err error)
	StartLink(ctx context.Context, userID, provider, redirectTo string) (authURL string, err error)
	OAuthCallback(ctx context.Context, provider, code, state string) (authsvc.OAuthResult, error)
	VerifyLink(ctx context.Context, token string) (authsvc.OAuthResult, error)
	ListLinked(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error)
	Unlink(ctx context.Context, userID, provider string) error

	// Machine identity (design §4.1). MachineEnabled gates whether the lifecycle
	// routes are registered at all (deny-by-absence).
	MachineEnabled() bool
	CreateServiceAccount(ctx context.Context, createdBy, name, description string, actAsUser bool, ownerUserID string) (serviceaccount.ServiceAccount, error)
	ListServiceAccounts(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error)
	MintAPIKey(ctx context.Context, serviceAccountID, name string, expiresAt time.Time) (apikey.APIKey, string, error)
	ListAPIKeys(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error)
	RevokeAPIKey(ctx context.Context, keyID string) error

	// JWT bearer mode (design §4.4). TokenEnabled gates whether POST /auth/token
	// is registered at all (deny-by-absence).
	TokenEnabled() bool
	IssueToken(ctx context.Context, email, password string) (token string, expiresAt time.Time, err error)
}

// handlers holds the services the route handlers delegate to. inv is nil when no
// Granter is wired (invitations off); its routes are then never registered.
type handlers struct {
	svc authService
	inv InvitationService
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
	Code string `json:"code"`
}

type forgotRequest struct {
	Email string `json:"email"`
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// tokenResponse is the POST /auth/token success body (design §4.4): the signed
// bearer JWT and its absolute expiry (RFC3339).
type tokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type userResponse struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	EmailVerified bool   `json:"email_verified"`
}

func newUserResponse(u user.User) userResponse {
	return userResponse{
		ID:            u.ID,
		Email:         u.Email,
		DisplayName:   u.DisplayName,
		EmailVerified: u.EmailVerified,
	}
}

// Mount registers the auth feature's routes on the registrar. The route surface
// is POST /auth/{register,login,verify,password/forgot,password/reset} plus the
// session-gated POST /auth/logout and POST /auth/password/change.
//
// It wraps the registrar so the client-info middleware rides EVERY route,
// unauthenticated ones included (design §5.1 WI4): the ONE blanket write point
// that stamps the request IP + User-Agent onto the context for login's
// rate-limit key and the security-event audit rows.
func Mount(r feature.RouteRegistrar, svc authService, inv InvitationService) {
	r = clientInfoRegistrar{inner: r}
	h := &handlers{svc: svc, inv: inv}
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

func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}
	u, err := h.svc.Register(r.Context(), req.Email, req.Password, req.DisplayName)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONCreated(w, newUserResponse(u))
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	token, u, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, authsvc.ErrRateLimited) {
			web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many login attempts").WithCode("rate_limited"))
			return
		}
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookie(w, token)
	web.RespondJSONOK(w, newUserResponse(u))
}

// token issues a stateless bearer JWT for login-shaped credentials (design
// §4.4). It shares /auth/login's request shape, pre-credential rate-limit
// discipline, and verified-email gating; on success it returns {token,
// expires_at}. Registered only when a TokenSigner is wired.
func (h *handlers) token(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	tok, expiresAt, err := h.svc.IssueToken(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, authsvc.ErrRateLimited) {
			web.RespondJSONError(w, web.NewError(http.StatusTooManyRequests, "too many login attempts").WithCode("rate_limited"))
			return
		}
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, tokenResponse{Token: tok, ExpiresAt: expiresAt.Format(time.RFC3339)})
}

func (h *handlers) verify(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.Verify(r.Context(), req.Code); err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, map[string]string{"status": "verified"})
}

func (h *handlers) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if !decode(w, r, &req) {
		return
	}
	// The service returns nil for unknown emails; a non-nil error here is an
	// internal failure (store/mail), which is a 500 for registered and
	// unregistered emails alike — so the response still cannot enumerate.
	if err := h.svc.ForgotPassword(r.Context(), req.Email); err != nil {
		web.RespondJSONError(w, web.ErrInternal("could not process request"))
		return
	}
	web.RespondJSONAccepted(w, map[string]string{"status": "accepted"})
}

func (h *handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
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

// changePassword is session-gated (RequireUser has already validated the caller
// and stashed the user id). It verifies the current password, sets the new one,
// revokes ALL the user's sessions, and sets a fresh session cookie for the
// caller (design §7.2). A wrong current password surfaces as 401.
func (h *handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	token, err := h.svc.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	h.svc.SetSessionCookie(w, token)
	web.RespondJSONOK(w, map[string]string{"status": "password_changed"})
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	// RequireUser has already validated the session; clear it regardless of the
	// delete outcome (logout is idempotent).
	if c, err := r.Cookie(h.svc.SessionCookieName()); err == nil {
		_ = h.svc.Logout(r.Context(), c.Value)
	}
	h.svc.ClearSessionCookie(w)
	web.RespondJSONOK(w, map[string]string{"status": "logged_out"})
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
