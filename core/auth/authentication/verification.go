package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// VerifyEmail marks a user's email as verified after validating a 6-digit code.
func (a *Authenticator) VerifyEmail(ctx context.Context, email, code string) error {
	// Look up the stored code.
	stored, err := a.repositories.codes.Get(ctx, email, PurposeEmailVerify)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return ErrCodeInvalid
		}
		return fmt.Errorf("auth verify email: lookup code: %w", err)
	}

	if time.Now().UTC().After(stored.ExpiresAt) {
		_ = a.repositories.codes.Delete(ctx, email, PurposeEmailVerify)
		return ErrCodeExpired
	}

	// Compare hash (constant-time to prevent timing side-channels).
	codeHash, err := hashToken(code)
	if err != nil {
		return ErrCodeInvalid
	}
	if !hashEquals(codeHash, stored.CodeHash) {
		// Increment attempt counter; delete code if max attempts exceeded.
		if a.config.MaxVerificationAttempts > 0 {
			count, err := a.repositories.codes.IncrementAttempts(ctx, email, PurposeEmailVerify)
			if err != nil {
				a.log.WarnContext(ctx, "failed to increment verification attempts", "error", err)
			} else if count >= a.config.MaxVerificationAttempts {
				_ = a.repositories.codes.Delete(ctx, email, PurposeEmailVerify)
				a.log.WarnContext(ctx, "verification code locked out", "attempts", count)
				return ErrTooManyAttempts
			}
		}
		return ErrCodeInvalid
	}

	// Look up user to get ID.
	user, err := a.repositories.users.GetByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("auth verify email: lookup user: %w", err)
	}

	// Mark email as verified.
	if err := a.repositories.users.SetEmailVerified(ctx, user.UserID); err != nil {
		return fmt.Errorf("auth verify email: set verified: %w", err)
	}

	// Mark password as verified — email ownership proves the credential.
	// Best-effort: user might not have a password (OAuth-only accounts).
	if err := a.repositories.passwords.SetVerified(ctx, user.UserID); err != nil {
		if !errors.Is(err, errs.ErrNotFound) {
			a.log.WarnContext(ctx, "failed to set password verified", "user_id", user.UserID, "error", err)
		}
	}

	// Clean up the code.
	_ = a.repositories.codes.Delete(ctx, email, PurposeEmailVerify)

	a.log.InfoContext(ctx, "email verified", "user_id", user.UserID)
	a.logSecurityEvent(ctx, user.UserID, SecEventEmailVerified, SecStatusSuccess, map[string]any{
		"email": email,
	})

	return nil
}

// ResendVerificationCode generates and stores a new 6-digit verification code.
//
// Returns the plaintext code. A [VerificationCodeRequestedEvent] is emitted
// for email delivery.
func (a *Authenticator) ResendVerificationCode(ctx context.Context, email string) (string, error) {
	user, err := a.repositories.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			// Silent fail — don't reveal whether the email exists.
			return "", nil
		}
		return "", fmt.Errorf("auth resend code: lookup user: %w", err)
	}

	if user.EmailVerified {
		return "", ErrEmailAlreadyVerified
	}

	code, err := a.generateAndStoreVerificationCode(ctx, user, PurposeEmailVerify)
	if err != nil {
		return "", fmt.Errorf("auth resend code: %w", err)
	}

	a.log.InfoContext(ctx, "verification code resent", "user_id", user.UserID)
	a.logSecurityEvent(ctx, user.UserID, SecEventVerificationResent, SecStatusSuccess, map[string]any{
		"email": email,
	})
	return code, nil
}

// sendVerificationCode is a fire-and-forget helper that generates a new
// verification code and emits an event for email delivery. Errors are logged
// but never propagated — this must never fail the caller's auth flow.
//
// Used by Login and OAuth flows to auto-send verification when credentials
// are correct but the account or credential is unverified.
func (a *Authenticator) sendVerificationCode(ctx context.Context, user User, purpose string) {
	if _, err := a.generateAndStoreVerificationCode(ctx, user, purpose); err != nil {
		a.log.WarnContext(ctx, "failed to send verification code",
			"user_id", user.UserID, "purpose", purpose, "error", err)
	}
}

// generateAndStoreVerificationCode creates a new 6-digit code, stores it, and
// emits a [VerificationCodeRequestedEvent]. Returns the plaintext code.
//
// The code is keyed by the user's email — appropriate for signup/email-verify
// flows where the user may not yet have a session. For in-session destructive
// flows, use [Authenticator.IssueVerificationCode] which keys by user_id and
// supports binding the code to per-operation context.
func (a *Authenticator) generateAndStoreVerificationCode(ctx context.Context, user User, purpose string) (string, error) {
	// Delete any existing code for this email/purpose.
	_ = a.repositories.codes.Delete(ctx, user.Email, purpose)

	code, err := generateVerificationCode()
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}

	if err := a.repositories.codes.Create(ctx, VerificationCode{
		Identifier: user.Email,
		CodeHash:   mustHashToken(code),
		Purpose:    purpose,
		ExpiresAt:  time.Now().UTC().Add(a.config.VerificationCodeExpiry),
	}); err != nil {
		return "", fmt.Errorf("store code: %w", err)
	}

	if err := a.bus.Emit(ctx, VerificationCodeRequestedEvent{
		UserID:      user.UserID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Code:        code,
		ExpiresIn:   a.config.VerificationCodeExpiry.String(),
	}); err != nil {
		a.log.WarnContext(ctx, "failed to emit verification code event",
			"user_id", user.UserID, "error", err)
	}

	return code, nil
}
