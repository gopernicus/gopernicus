package authentication

import (
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// AddHttpRoutes registers authentication endpoints on the given route group.
//
// authMid is the Authenticate middleware for protected endpoints.
// Public endpoints (login, register, etc.) don't use it.
func (b *Bridge) AddHttpRoutes(group *web.RouteGroup, authMid web.Middleware) {
	// Rate limit helper — unauthenticated routes fall back to IP-based keying
	// via the default auth-aware key function. Limits are set generously to
	// avoid locking out users behind shared IPs (corporate NAT, VPN) while
	// still blocking automated tooling. Per-account protections (too-many-attempts,
	// token reuse detection) are enforced at the core layer.
	rl := func(prefix string, limit ratelimiter.Limit) web.Middleware {
		return httpmid.RateLimit(b.rateLimiter, b.log, httpmid.WithKeyPrefix(prefix), httpmid.WithLimit(limit))
	}

	// Public routes — rate limited (falls back to IP for unauthenticated callers).
	group.POST("/login", b.httpLogin, rl("auth:login", ratelimiter.PerMinute(120)))
	group.POST("/register", b.httpRegister, rl("auth:register", ratelimiter.PerMinute(60)))
	group.POST("/verify-email", b.httpVerifyEmail, rl("auth:verify", ratelimiter.PerMinute(60)))
	group.POST("/verify-email/resend", b.httpResendVerification, rl("auth:verify-resend", ratelimiter.PerMinute(30)))
	group.POST("/refresh", b.httpRefreshToken, rl("auth:refresh", ratelimiter.PerMinute(300)))
	group.POST("/password-reset/initiate", b.httpInitiatePasswordReset, rl("auth:reset-init", ratelimiter.PerMinute(30)))
	group.POST("/password-reset/confirm", b.httpResetPassword, rl("auth:reset-confirm", ratelimiter.PerMinute(60)))

	// Session-aware middleware for routes that need full user/session context.
	// Uses WithUserSession() to load user + session from DB, unlike authMid
	// which only validates the JWT signature (no DB lookup).
	sessionMid := httpmid.Authenticate(b.authenticator, b.log, httpmid.JSONErrors{}, httpmid.WithUserSession())

	// Protected routes.
	group.POST("/password-change", b.httpChangePassword, authMid)
	group.POST("/logout", b.httpLogout, authMid)
	group.GET("/me", b.httpMe, sessionMid)

	// Public OAuth routes — rate limited.
	group.POST("/oauth/initiate", b.httpOAuthInitiate, rl("auth:oauth-init", ratelimiter.PerMinute(60)))
	group.POST("/oauth/verify-link", b.httpOAuthVerifyLink, rl("auth:oauth-verify", ratelimiter.PerMinute(60)))
	group.POST("/oauth/callback/mobile/{provider}", b.httpOAuthMobileCallback, rl("auth:oauth-mobile", ratelimiter.PerMinute(60)))
	group.GET("/oauth/mobile-redirect/{provider}", b.httpOAuthMobileRedirect, rl("auth:oauth-redirect", ratelimiter.PerMinute(120)))
	group.GET("/oauth/start/{provider}", b.httpOAuthStart, rl("auth:oauth-start", ratelimiter.PerMinute(60)))
	group.GET("/oauth/callback/{provider}", b.httpOAuthCallback, rl("auth:oauth-callback", ratelimiter.PerMinute(60)))

	// Protected OAuth routes.
	group.POST("/oauth/link", b.httpOAuthLink, authMid)
	group.GET("/oauth/link/start/{provider}", b.httpOAuthLinkStart, authMid)
	group.DELETE("/oauth/unlink/{provider}", b.httpOAuthUnlink, authMid)
	group.GET("/oauth/linked", b.httpOAuthGetLinked, authMid)
}

// ---------------------------------------------------------------------------
// Public handlers
// ---------------------------------------------------------------------------

func (b *Bridge) httpLogin(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[LoginRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	result, err := b.authenticator.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		// All login failures return the same 401 to prevent account enumeration.
		// A 403 for "email not verified" vs 401 for "bad password" would reveal
		// both that the email exists and its verification state.
		web.RespondJSONError(w, web.ErrUnauthorized("invalid credentials"))
		return
	}

	// Set session cookies (for web clients).
	b.setSessionCookies(w, result.AccessToken, result.RefreshToken)

	// Return JSON response with user data and tokens (for API clients).
	web.RespondJSON(w, http.StatusOK, LoginResponse{
		User: UserResponse{
			UserID:        result.User.UserID,
			Email:         result.User.Email,
			DisplayName:   result.User.DisplayName,
			EmailVerified: result.User.EmailVerified,
		},
		SessionID: result.SessionID,
		Tokens: TokenResponse{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
		},
	})
}

func (b *Bridge) httpRegister(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[RegisterRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	_, err = b.authenticator.Register(r.Context(), req.Email, req.Password, req.DisplayName)
	if err != nil {
		// Register returns nil for duplicate emails (anti-enumeration),
		// so errors here are genuine failures.
		b.log.ErrorContext(r.Context(), "registration failed", "error", err)
	}

	// Always return the same response to prevent account enumeration.
	web.RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "check your email for verification",
	})
}

func (b *Bridge) httpVerifyEmail(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[VerifyEmailRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	if err := b.authenticator.VerifyEmail(r.Context(), req.Email, req.Code); err != nil {
		switch {
		case errors.Is(err, authentication.ErrCodeExpired):
			web.RespondJSONError(w, web.ErrGone("verification code expired").WithCode("verification_code_expired"))
		case errors.Is(err, authentication.ErrTooManyAttempts):
			web.RespondJSONError(w, web.ErrForbidden("too many attempts").WithCode("too_many_attempts"))
		case errors.Is(err, authentication.ErrCodeInvalid):
			web.RespondJSONError(w, web.ErrBadRequest("invalid verification code").WithCode("verification_code_invalid"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	web.RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "email verified",
	})
}

func (b *Bridge) httpResendVerification(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[ResendVerificationRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	_, err = b.authenticator.ResendVerificationCode(r.Context(), req.Email)
	if err != nil {
		// Swallow errors to prevent account enumeration. A "not found" or
		// "already verified" response would reveal whether the email exists.
		b.log.ErrorContext(r.Context(), "resend verification failed", "error", err)
	}

	// Always return success regardless of outcome.
	web.RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "if the email exists, a verification code has been sent",
	})
}

func (b *Bridge) httpRefreshToken(w http.ResponseWriter, r *http.Request) {
	var refreshToken string

	// Try to get refresh token from cookie first (web clients).
	if cookie, err := r.Cookie(b.refreshCookieName); err == nil && cookie.Value != "" {
		refreshToken = cookie.Value
	} else if bearer := httpmid.ExtractBearerToken(r); bearer != "" {
		// Fall back to Authorization header.
		refreshToken = bearer
	} else {
		// Fall back to request body (API clients).
		req, err := web.DecodeJSON[RefreshTokenRequest](r)
		if err != nil {
			web.RespondJSONError(w, web.ErrValidation(err))
			return
		}
		refreshToken = req.RefreshToken
	}

	if refreshToken == "" {
		web.RespondJSONError(w, web.ErrUnauthorized("refresh token required"))
		return
	}

	result, err := b.authenticator.RefreshToken(r.Context(), refreshToken)
	if err != nil {
		if errors.Is(err, authentication.ErrTokenExpired) {
			web.RespondJSONError(w, web.ErrGone("token expired").WithCode("refresh_token_expired"))
			return
		}
		// ErrTokenReuse intentionally stays generic ("unauthorized").
		web.RespondJSONDomainError(w, err)
		return
	}

	// Set new session cookies (for web clients).
	b.setSessionCookies(w, result.AccessToken, result.RefreshToken)

	web.RespondJSON(w, http.StatusOK, LoginResponse{
		User: UserResponse{
			UserID:        result.User.UserID,
			Email:         result.User.Email,
			DisplayName:   result.User.DisplayName,
			EmailVerified: result.User.EmailVerified,
		},
		SessionID: result.SessionID,
		Tokens: TokenResponse{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
		},
	})
}

func (b *Bridge) httpInitiatePasswordReset(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[InitiatePasswordResetRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	// InitiatePasswordReset returns the token for the caller to deliver.
	// The HTTP response never reveals whether the email exists.
	var resetOpts []authentication.PasswordResetOption
	if req.ResetURL != "" {
		resetOpts = append(resetOpts, authentication.WithResetURL(req.ResetURL))
	}
	_, err = b.authenticator.InitiatePasswordReset(r.Context(), req.Email, resetOpts...)
	if err != nil {
		b.log.ErrorContext(r.Context(), "password reset initiation failed", "error", err)
	}

	// Always return success to prevent account enumeration.
	web.RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "if the email exists, a reset link has been sent",
	})
}

func (b *Bridge) httpResetPassword(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[ResetPasswordRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	if err := b.authenticator.ResetPassword(r.Context(), req.Token, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, authentication.ErrTokenExpired):
			web.RespondJSONError(w, web.ErrGone("reset token expired").WithCode("reset_token_expired"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	web.RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "password reset successful",
	})
}

// ---------------------------------------------------------------------------
// Protected handlers
// ---------------------------------------------------------------------------

func (b *Bridge) httpChangePassword(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[ChangePasswordRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	userID := httpmid.GetSubjectID(r.Context())
	sessionID := httpmid.GetSessionID(r.Context())

	if err := b.authenticator.ChangePassword(r.Context(), userID, sessionID, req.CurrentPassword, req.NewPassword, req.RevokeOtherSessions); err != nil {
		switch {
		case errors.Is(err, authentication.ErrInvalidCredentials):
			web.RespondJSONError(w, web.ErrBadRequest("current password is incorrect").WithCode("invalid_credentials"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	web.RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "password changed",
	})
}

func (b *Bridge) httpLogout(w http.ResponseWriter, r *http.Request) {
	userID := httpmid.GetSubjectID(r.Context())
	sessionID := httpmid.GetSessionID(r.Context())
	if userID == "" || sessionID == "" {
		// Even without session info, clear cookies.
		b.clearSessionCookies(w)
		web.RespondJSONError(w, web.ErrBadRequest("session required"))
		return
	}

	if err := b.authenticator.Logout(r.Context(), userID, sessionID); err != nil {
		// Even if logout fails, clear cookies.
		b.clearSessionCookies(w)
		web.RespondJSONDomainError(w, err)
		return
	}

	// Clear session cookies (for web clients).
	b.clearSessionCookies(w)

	web.RespondNoContent(w)
}

func (b *Bridge) httpMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := httpmid.GetUser(ctx)
	if user == nil {
		b.log.ErrorContext(ctx, "user not found in context")
		web.RespondJSONError(w, web.ErrInternal("internal error"))
		return
	}

	session := httpmid.GetSession(ctx)
	if session == nil {
		b.log.ErrorContext(ctx, "session not found in context")
		web.RespondJSONError(w, web.ErrInternal("internal error"))
		return
	}

	web.RespondJSON(w, http.StatusOK, MeResponse{
		User: UserResponse{
			UserID:        user.UserID,
			Email:         user.Email,
			DisplayName:   user.DisplayName,
			EmailVerified: user.EmailVerified,
		},
		Session: SessionResponse{
			SessionID: session.SessionID,
		},
	})
}
