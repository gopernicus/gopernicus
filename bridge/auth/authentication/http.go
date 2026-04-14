package authentication

import (
	"net/http"

	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/errs"
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
	group.POST("/password-change", b.httpChangePassword, rl("auth:password-change", ratelimiter.PerMinute(10)), sessionMid)
	group.POST("/password/remove/send-code", b.httpRemovePasswordSendCode, rl("auth:remove-password-send", ratelimiter.PerMinute(10)), authMid)
	group.POST("/password/remove", b.httpRemovePassword, rl("auth:remove-password", ratelimiter.PerMinute(10)), authMid)
	group.POST("/logout", b.httpLogout, rl("auth:logout", ratelimiter.PerMinute(30)), sessionMid)
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
	group.POST("/oauth/unlink/{provider}/send-code", b.httpOAuthUnlinkSendCode, rl("auth:unlink-oauth-send", ratelimiter.PerMinute(10)), authMid)
	group.DELETE("/oauth/unlink/{provider}", b.httpOAuthUnlink, rl("auth:unlink-oauth", ratelimiter.PerMinute(10)), authMid)
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
		b.log.WarnContext(r.Context(), "login failed", "error", err)
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
		// so any non-nil error is a genuine infrastructure failure.
		b.log.ErrorContext(r.Context(), "registration failed", "error", err)
		web.RespondJSONError(w, web.ErrInternal("registration failed"))
		return
	}

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
		web.RespondJSONError(w, httpErrFor(err))
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
		if !errs.IsExpected(err) {
			// Unexpected errors (DB down, etc.) are genuine failures — return 500.
			b.log.ErrorContext(r.Context(), "resend verification failed", "error", err)
			web.RespondJSONError(w, web.ErrInternal("resend verification failed"))
			return
		}
		// Expected domain errors (not found, already verified) are swallowed to
		// prevent account enumeration.
	}

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
		// ErrTokenReuse intentionally stays generic ("unauthorized") via httpErrFor's
		// fallback to ErrFromDomain.
		web.RespondJSONError(w, httpErrFor(err))
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
		if !errs.IsExpected(err) {
			b.log.ErrorContext(r.Context(), "password reset initiation failed", "error", err)
			web.RespondJSONError(w, web.ErrInternal("password reset failed"))
			return
		}
		// Expected domain errors (email not found, etc.) are swallowed to
		// prevent account enumeration.
	}

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
		web.RespondJSONError(w, httpErrFor(err))
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
		web.RespondJSONError(w, httpErrFor(err))
		return
	}

	web.RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "password changed",
	})
}

// httpRemovePasswordSendCode handles POST /auth/password/remove/send-code.
//
// Authenticated. Issues a 6-digit verification code dedicated to the
// remove-password flow and emits a [authentication.RemovePasswordCodeRequestedEvent]
// for email delivery. The code must be presented on POST /auth/password/remove
// to confirm the operation.
//
// Codes are one-shot per (user, purpose) — issuing a new code invalidates
// any prior remove-password code for the same user. Verification codes
// issued for other sensitive operations (e.g. unlink OAuth) cannot be used
// here, and vice versa, because they're keyed by purpose at the core layer.
func (b *Bridge) httpRemovePasswordSendCode(w http.ResponseWriter, r *http.Request) {
	userID := httpmid.GetSubjectID(r.Context())
	if _, err := b.authenticator.SendRemovePasswordCode(r.Context(), userID); err != nil {
		b.log.ErrorContext(r.Context(), "send remove password code failed", "error", err)
		web.RespondJSONError(w, httpErrFor(err))
		return
	}
	web.RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Message: "verification code sent",
	})
}

// httpRemovePassword handles POST /auth/password/remove.
//
// Authenticated. Requires a verification code in the request body, obtained
// from POST /auth/password/remove/send-code. Removes the user's password
// credential — the user must have at least one linked OAuth account or this
// returns cannot_remove_last_method.
func (b *Bridge) httpRemovePassword(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[RemovePasswordRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	userID := httpmid.GetSubjectID(r.Context())

	if err := b.authenticator.VerifyRemovePasswordCode(r.Context(), userID, req.VerificationCode); err != nil {
		b.log.WarnContext(r.Context(), "remove password code verification failed", "error", err)
		web.RespondJSONError(w, httpErrFor(err))
		return
	}

	if err := b.authenticator.RemovePassword(r.Context(), userID); err != nil {
		b.log.ErrorContext(r.Context(), "remove password failed", "error", err)
		web.RespondJSONError(w, httpErrFor(err))
		return
	}

	web.RespondNoContent(w)
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

	hasPassword, err := b.authenticator.HasPassword(ctx, user.UserID)
	if err != nil {
		b.log.ErrorContext(ctx, "failed to check password status", "error", err)
		web.RespondJSONError(w, web.ErrInternal("internal error"))
		return
	}

	web.RespondJSON(w, http.StatusOK, MeResponse{
		User: UserResponse{
			UserID:        user.UserID,
			Email:         user.Email,
			DisplayName:   user.DisplayName,
			EmailVerified: user.EmailVerified,
			HasPassword:   hasPassword,
		},
		Session: SessionResponse{
			SessionID: session.SessionID,
		},
	})
}
