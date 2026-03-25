package authentication

import (
	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// Event type constants.
const (
	// EventTypeVerificationCodeRequested is emitted when a 6-digit verification
	// code should be sent to the user (registration, resend).
	EventTypeVerificationCodeRequested = "auth.verification_code_requested"

	// EventTypePasswordResetRequested is emitted when a password reset token
	// should be sent to the user.
	EventTypePasswordResetRequested = "auth.password_reset_requested"

	// EventTypeOAuthLinkVerificationRequested is emitted when an OAuth account
	// link needs email verification.
	EventTypeOAuthLinkVerificationRequested = "auth.oauth_link_verification_requested"

	// EventTypeEmailVerified is emitted after a user's email is successfully
	// verified. Subscribers can use this to resolve pending invitations,
	// grant default permissions, or trigger onboarding flows.
	EventTypeEmailVerified = "auth.email_verified"

	// EventTypeUserDeletionRequested is emitted when a user deletion is
	// initiated via [Authenticator.DeleteUser]. Subscribers handle the actual
	// data cleanup: deleting passwords, OAuth links, verification codes,
	// security event pseudonymization, domain data, and the user record itself.
	EventTypeUserDeletionRequested = "auth.user_deletion_requested"
)

// VerificationCodeRequestedEvent is emitted after registration or when
// a verification code is resent.
type VerificationCodeRequestedEvent struct {
	events.BaseEvent
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Code        string `json:"code"`
	ExpiresIn   string `json:"expires_in"`
}

func (e VerificationCodeRequestedEvent) Type() string {
	return EventTypeVerificationCodeRequested
}

// PasswordResetRequestedEvent is emitted when a user initiates a password reset.
type PasswordResetRequestedEvent struct {
	events.BaseEvent
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Token       string `json:"token"`
	ResetURL    string `json:"reset_url,omitempty"` // Frontend base URL; subscriber appends ?token=X
	ExpiresIn   string `json:"expires_in"`
}

func (e PasswordResetRequestedEvent) Type() string {
	return EventTypePasswordResetRequested
}

// OAuthLinkVerificationRequestedEvent is emitted when an OAuth account link
// needs email verification before completing.
type OAuthLinkVerificationRequestedEvent struct {
	events.BaseEvent
	UserID      string `json:"user_id,omitempty"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Provider    string `json:"provider"`
	Code        string `json:"code"`
	ExpiresIn   string `json:"expires_in"`
}

func (e OAuthLinkVerificationRequestedEvent) Type() string {
	return EventTypeOAuthLinkVerificationRequested
}

// EmailVerifiedEvent is emitted after a user's email is successfully verified.
type EmailVerifiedEvent struct {
	events.BaseEvent
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

func (e EmailVerifiedEvent) Type() string {
	return EventTypeEmailVerified
}

// UserDeletionRequestedEvent is emitted when a user deletion is initiated.
// Subscribers are responsible for cascading the deletion across all data stores:
// passwords, OAuth accounts, verification codes/tokens, security event
// pseudonymization, and the user record itself.
type UserDeletionRequestedEvent struct {
	events.BaseEvent
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

func (e UserDeletionRequestedEvent) Type() string {
	return EventTypeUserDeletionRequested
}
