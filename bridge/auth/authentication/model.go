package authentication

import (
	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/sdk/validation"
)

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// LoginRequest is the JSON body for POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r *LoginRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("email", r.Email))
	errs.Add(validation.Required("password", r.Password))
	return errs.Err()
}

// RegisterRequest is the JSON body for POST /auth/register.
type RegisterRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

func (r *RegisterRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("email", r.Email))
	errs.Add(validation.Email("email", r.Email))
	errs.Add(authentication.ValidatePassword(r.Password))
	return errs.Err()
}

// VerifyEmailRequest is the JSON body for POST /auth/verify-email.
type VerifyEmailRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func (r *VerifyEmailRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("email", r.Email))
	errs.Add(validation.Email("email", r.Email))

	errs.Add(validation.Required("code", r.Code))
	return errs.Err()
}

// ResendVerificationRequest is the JSON body for POST /auth/verify-email/resend.
type ResendVerificationRequest struct {
	Email string `json:"email"`
}

func (r *ResendVerificationRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("email", r.Email))
	errs.Add(validation.Email("email", r.Email))

	return errs.Err()
}

// RefreshTokenRequest is the JSON body for POST /auth/refresh.
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (r *RefreshTokenRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("refresh_token", r.RefreshToken))
	return errs.Err()
}

// ChangePasswordRequest is the JSON body for POST /auth/password-change.
type ChangePasswordRequest struct {
	CurrentPassword     string `json:"current_password"`
	NewPassword         string `json:"new_password"`
	RevokeOtherSessions bool   `json:"revoke_other_sessions"`
}

func (r *ChangePasswordRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("current_password", r.CurrentPassword))
	errs.Add(authentication.ValidatePassword(r.NewPassword))
	return errs.Err()
}

// InitiatePasswordResetRequest is the JSON body for POST /auth/password-reset/initiate.
type InitiatePasswordResetRequest struct {
	Email    string `json:"email"`
	ResetURL string `json:"reset_url"` // Frontend URL; token is appended as ?token=X
}

func (r *InitiatePasswordResetRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("email", r.Email))
	errs.Add(validation.Email("email", r.Email))
	return errs.Err()
}

// ResetPasswordRequest is the JSON body for POST /auth/password-reset/confirm.
type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (r *ResetPasswordRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("token", r.Token))
	errs.Add(authentication.ValidatePassword(r.NewPassword))
	return errs.Err()
}

// RemovePasswordRequest is the JSON body for POST /auth/password/remove.
// Requires a verification code obtained from POST /auth/password/remove/send-code.
type RemovePasswordRequest struct {
	VerificationCode string `json:"verification_code"`
}

func (r *RemovePasswordRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("verification_code", r.VerificationCode))
	return errs.Err()
}

// UnlinkOAuthRequest is the JSON body for DELETE /auth/oauth/unlink/{provider}.
// Requires a verification code obtained from
// POST /auth/oauth/unlink/{provider}/send-code. The provider in the URL path
// must match the provider the code was issued for; mismatches are rejected
// at the core layer with an invalid-code response.
type UnlinkOAuthRequest struct {
	VerificationCode string `json:"verification_code"`
}

func (r *UnlinkOAuthRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("verification_code", r.VerificationCode))
	return errs.Err()
}

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

// TokenResponse is the token portion of login/refresh responses.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// LoginResponse is the JSON response for POST /auth/login and POST /auth/refresh.
type LoginResponse struct {
	User      UserResponse  `json:"user"`
	SessionID string        `json:"session_id"`
	Tokens    TokenResponse `json:"tokens"`
}

// MeResponse is the JSON response for GET /auth/me.
type MeResponse struct {
	User    UserResponse    `json:"user"`
	Session SessionResponse `json:"session"`
}

// UserResponse is the user portion of the /me response.
type UserResponse struct {
	UserID        string `json:"user_id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	EmailVerified bool   `json:"email_verified"`
	HasPassword   bool   `json:"has_password"`
}

// SessionResponse is the session portion of the /me response.
type SessionResponse struct {
	SessionID string `json:"session_id"`
}

// SuccessResponse is the JSON response for operations that succeed
// without returning domain data.
type SuccessResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}
