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

	"github.com/gopernicus/gopernicus/features/auth/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
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
	Login(ctx context.Context, email, password, clientIP string) (token string, u user.User, err error)
	Logout(ctx context.Context, token string) error
	ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (token string, err error)
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, token, newPassword string) error
	CurrentUser(ctx context.Context) (userID string, ok bool)
	SetSessionCookie(w http.ResponseWriter, token string)
	ClearSessionCookie(w http.ResponseWriter)
	SessionCookieName() string
	RequireUser(next http.Handler) http.Handler

	// OAuth flow (design §3). OAuthEnabled gates whether the OAuth routes are
	// registered at all (deny-by-absence).
	OAuthEnabled() bool
	StartOAuth(ctx context.Context, provider, redirectTo string) (authURL string, err error)
	StartLink(ctx context.Context, userID, provider, redirectTo string) (authURL string, err error)
	OAuthCallback(ctx context.Context, provider, code, state string) (authsvc.OAuthResult, error)
	VerifyLink(ctx context.Context, token string) (authsvc.OAuthResult, error)
	ListLinked(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error)
	Unlink(ctx context.Context, userID, provider string) error
}

// handlers holds the auth service the route handlers delegate to.
type handlers struct {
	svc authService
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
func Mount(r feature.RouteRegistrar, svc authService) {
	h := &handlers{svc: svc}
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
}

func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}
	u, err := h.svc.Register(r.Context(), req.Email, req.Password, req.DisplayName)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newUserResponse(u))
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	token, u, err := h.svc.Login(r.Context(), req.Email, req.Password, clientIP(r))
	if err != nil {
		if errors.Is(err, authsvc.ErrRateLimited) {
			writeError(w, web.NewError(http.StatusTooManyRequests, "too many login attempts").WithCode("rate_limited"))
			return
		}
		writeErr(w, err)
		return
	}
	h.svc.SetSessionCookie(w, token)
	writeJSON(w, http.StatusOK, newUserResponse(u))
}

func (h *handlers) verify(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.Verify(r.Context(), req.Code); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "verified"})
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
		writeError(w, web.ErrInternal("could not process request"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (h *handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
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
		writeError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	token, err := h.svc.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.svc.SetSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]string{"status": "password_changed"})
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	// RequireUser has already validated the session; clear it regardless of the
	// delete outcome (logout is idempotent).
	if c, err := r.Cookie(h.svc.SessionCookieName()); err == nil {
		_ = h.svc.Logout(r.Context(), c.Value)
	}
	h.svc.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
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
		writeError(w, web.ErrBadRequest("invalid request body"))
		return false
	}
	return true
}

// writeErr maps a domain error to its HTTP status via sdk/web and writes it.
func writeErr(w http.ResponseWriter, err error) {
	writeError(w, web.ErrFromDomain(err))
}

// writeError writes a *web.Error as JSON at its mapped status.
func writeError(w http.ResponseWriter, e *web.Error) {
	writeJSON(w, e.Status, e)
}

// writeJSON writes v as a JSON response at status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// clientIP derives the client IP for rate-limit keying, preferring the first
// X-Forwarded-For hop and falling back to the request's remote address.
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
