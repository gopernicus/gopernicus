package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// ChangePassword updates a user's password after verifying the current one.
//
// If revokeOtherSessions is true, all sessions except the given session are
// revoked, forcing re-authentication on other devices.
func (a *Authenticator) ChangePassword(ctx context.Context, userID, sessionID, currentPassword, newPassword string, revokeOtherSessions bool) error {
	// Verify the current password.
	pw, err := a.repositories.passwords.GetByUserID(ctx, userID)
	if err != nil {
		return ErrInvalidCredentials
	}
	if err := a.hasher.Compare(pw.Hash, currentPassword); err != nil {
		a.log.WarnContext(ctx, "failed password change — invalid current password", "user_id", userID)
		a.logSecurityEvent(ctx, userID, SecEventPasswordChange, SecStatusFailure, map[string]any{
			"reason": "invalid_current_password",
		})
		return ErrInvalidCredentials
	}

	// Hash and store the new password.
	hash, err := a.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("auth change password: hash: %w", err)
	}
	if err := a.repositories.passwords.Update(ctx, userID, hash); err != nil {
		return fmt.Errorf("auth change password: update: %w", err)
	}

	// Mark verified — user proved current password ownership.
	if err := a.repositories.passwords.SetVerified(ctx, userID); err != nil {
		a.log.WarnContext(ctx, "failed to set password verified", "user_id", userID, "error", err)
	}

	// Optionally revoke other sessions.
	if revokeOtherSessions && sessionID != "" {
		if err := a.repositories.sessions.DeleteAllForUserExcept(ctx, userID, sessionID); err != nil {
			a.log.WarnContext(ctx, "failed to revoke other sessions after password change",
				"user_id", userID, "error", err)
		}
	}

	a.log.InfoContext(ctx, "password changed", "user_id", userID, "revoke_others", revokeOtherSessions)
	a.logSecurityEvent(ctx, userID, SecEventPasswordChange, SecStatusSuccess, map[string]any{
		"revoke_others": revokeOtherSessions,
	})
	return nil
}

// InitiatePasswordReset generates a password reset token and emits a
// [PasswordResetRequestedEvent] for email delivery.
//
// Returns the plaintext token. If the email is not found, returns ("", nil)
// to prevent email enumeration.
// PasswordResetOption configures optional behavior for password reset initiation.
type PasswordResetOption func(*passwordResetConfig)

type passwordResetConfig struct {
	resetURL string
}

// WithResetURL sets the frontend URL for the password reset link.
// The subscriber appends ?token=X to construct the full link.
func WithResetURL(url string) PasswordResetOption {
	return func(c *passwordResetConfig) { c.resetURL = url }
}

func (a *Authenticator) InitiatePasswordReset(ctx context.Context, email string, opts ...PasswordResetOption) (string, error) {
	var cfg passwordResetConfig
	for _, o := range opts {
		o(&cfg)
	}
	user, err := a.repositories.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			// Perform dummy work to equalize timing with the known-email path.
			// Without this, an attacker can distinguish known vs unknown emails
			// by measuring response time.
			_, _ = generateToken()
			_ = mustHashToken("dummy")
			a.log.InfoContext(ctx, "password reset requested for unknown email")
			return "", nil
		}
		return "", fmt.Errorf("auth password reset: lookup user: %w", err)
	}

	// Invalidate any previous reset tokens for this user.
	_ = a.repositories.tokens.DeleteByUserIDAndPurpose(ctx, user.UserID, PurposePasswordReset)

	// Generate an opaque reset token.
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("auth password reset: generate token: %w", err)
	}

	if err := a.repositories.tokens.Create(ctx, VerificationToken{
		Identifier: mustHashToken(token),
		TokenHash:  mustHashToken(token),
		UserID:     user.UserID,
		Purpose:    PurposePasswordReset,
		ExpiresAt:  time.Now().UTC().Add(a.config.PasswordResetExpiry),
	}); err != nil {
		return "", fmt.Errorf("auth password reset: store token: %w", err)
	}

	// Emit event for email delivery.
	if err := a.bus.Emit(ctx, PasswordResetRequestedEvent{
		UserID:      user.UserID,
		Email:       email,
		DisplayName: user.DisplayName,
		Token:       token,
		ResetURL:    cfg.resetURL,
		ExpiresIn:   a.config.PasswordResetExpiry.String(),
	}); err != nil {
		a.log.WarnContext(ctx, "failed to emit password reset event", "user_id", user.UserID, "error", err)
	}

	a.log.InfoContext(ctx, "password reset initiated", "user_id", user.UserID)
	a.logSecurityEvent(ctx, user.UserID, SecEventPasswordResetInit, SecStatusSuccess, map[string]any{
		"email": email,
	})
	return token, nil
}

// ResetPassword validates a reset token and sets a new password.
//
// All sessions for the user are revoked, forcing re-authentication.
func (a *Authenticator) ResetPassword(ctx context.Context, token, newPassword string) error {
	tokenHash, err := hashToken(token)
	if err != nil {
		return ErrCodeInvalid
	}
	stored, err := a.repositories.tokens.Get(ctx, tokenHash, PurposePasswordReset)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return ErrCodeInvalid
		}
		return fmt.Errorf("auth reset password: lookup token: %w", err)
	}

	if time.Now().UTC().After(stored.ExpiresAt) {
		_ = a.repositories.tokens.Delete(ctx, tokenHash, PurposePasswordReset)
		return ErrCodeExpired
	}

	// Hash and store the new password.
	hash, err := a.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("auth reset password: hash: %w", err)
	}
	if err := a.repositories.passwords.Update(ctx, stored.UserID, hash); err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			// OAuth-only user with no existing password — create one.
			if err := a.repositories.passwords.Create(ctx, stored.UserID, hash); err != nil {
				return fmt.Errorf("auth reset password: create: %w", err)
			}
		} else {
			return fmt.Errorf("auth reset password: update: %w", err)
		}
	}

	// Mark verified — reset token proves email ownership.
	if err := a.repositories.passwords.SetVerified(ctx, stored.UserID); err != nil {
		a.log.WarnContext(ctx, "failed to set password verified after reset",
			"user_id", stored.UserID, "error", err)
	}

	// Also mark email as verified — user proved ownership via reset token.
	if err := a.repositories.users.SetEmailVerified(ctx, stored.UserID); err != nil {
		a.log.WarnContext(ctx, "failed to set email verified after reset",
			"user_id", stored.UserID, "error", err)
	}

	// Consume the token.
	_ = a.repositories.tokens.Delete(ctx, tokenHash, PurposePasswordReset)

	// Revoke all sessions — user must re-authenticate.
	if err := a.repositories.sessions.DeleteAllForUser(ctx, stored.UserID); err != nil {
		a.log.WarnContext(ctx, "failed to revoke sessions after password reset",
			"user_id", stored.UserID, "error", err)
	}

	a.log.InfoContext(ctx, "password reset completed", "user_id", stored.UserID)
	a.logSecurityEvent(ctx, stored.UserID, SecEventPasswordReset, SecStatusSuccess, nil)
	return nil
}
