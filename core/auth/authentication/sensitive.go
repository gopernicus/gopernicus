package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// SendSensitiveOpCode generates a 6-digit verification code, stores it under
// [PurposeSensitiveOp] keyed to the user's ID, and emits a
// [VerificationCodeRequestedEvent] for delivery. Used to gate destructive
// in-session operations (unlink OAuth, remove password, delete account, etc).
//
// The code shares the same expiry, attempt-counter, and rate-limit machinery
// as other verification codes. Callers should call this from a handler that
// requires authentication, then prompt the user for the code on a follow-up
// request to [Authenticator.VerifySensitiveOpCode].
//
// Codes are keyed by user_id (not email) because the user is already
// authenticated and email may change between issuing and consuming the code.
//
// Returns the plaintext code so callers can deliver it directly if desired —
// the canonical delivery path is via the emitted event subscriber.
func (a *Authenticator) SendSensitiveOpCode(ctx context.Context, userID string) (string, error) {
	user, err := a.repositories.users.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return "", ErrUserInactive
		}
		return "", fmt.Errorf("auth send sensitive op code: lookup user: %w", err)
	}

	// Delete any existing sensitive-op code for this user — one-shot per request.
	_ = a.repositories.codes.Delete(ctx, userID, PurposeSensitiveOp)

	code, err := generateVerificationCode()
	if err != nil {
		return "", fmt.Errorf("auth send sensitive op code: generate: %w", err)
	}

	if err := a.repositories.codes.Create(ctx, VerificationCode{
		Identifier: userID,
		CodeHash:   mustHashToken(code),
		Purpose:    PurposeSensitiveOp,
		ExpiresAt:  time.Now().UTC().Add(a.config.VerificationCodeExpiry),
	}); err != nil {
		return "", fmt.Errorf("auth send sensitive op code: store: %w", err)
	}

	if err := a.bus.Emit(ctx, VerificationCodeRequestedEvent{
		UserID:      user.UserID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Code:        code,
		ExpiresIn:   a.config.VerificationCodeExpiry.String(),
		Purpose:     PurposeSensitiveOp,
	}); err != nil {
		a.log.WarnContext(ctx, "failed to emit sensitive op code event",
			"user_id", userID, "error", err)
	}

	a.log.InfoContext(ctx, "sensitive op code sent", "user_id", userID)
	a.logSecurityEvent(ctx, userID, SecEventSensitiveOpCodeSent, SecStatusSuccess, nil)

	return code, nil
}

// VerifySensitiveOpCode validates a 6-digit code previously issued by
// [Authenticator.SendSensitiveOpCode] for the given user. Returns nil if the
// code is valid; the code is consumed (deleted) on success. On failure, the
// attempt counter is incremented and the code is deleted after
// MaxVerificationAttempts.
//
// This is intentionally a separate primitive from [Authenticator.VerifyEmail]
// — that method also marks the email as verified and updates user state,
// which would be wrong here. VerifySensitiveOpCode only validates and consumes.
//
// Callers should invoke this immediately before performing the destructive
// operation it gates. The code is one-shot: a successful verify destroys it,
// so callers cannot reuse the same code for multiple operations.
func (a *Authenticator) VerifySensitiveOpCode(ctx context.Context, userID, code string) error {
	stored, err := a.repositories.codes.Get(ctx, userID, PurposeSensitiveOp)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return ErrCodeInvalid
		}
		return fmt.Errorf("auth verify sensitive op code: lookup: %w", err)
	}

	if time.Now().UTC().After(stored.ExpiresAt) {
		_ = a.repositories.codes.Delete(ctx, userID, PurposeSensitiveOp)
		return ErrCodeExpired
	}

	codeHash, err := hashToken(code)
	if err != nil {
		return ErrCodeInvalid
	}
	if !hashEquals(codeHash, stored.CodeHash) {
		if a.config.MaxVerificationAttempts > 0 {
			count, err := a.repositories.codes.IncrementAttempts(ctx, userID, PurposeSensitiveOp)
			if err != nil {
				a.log.WarnContext(ctx, "failed to increment sensitive op attempts", "error", err)
			} else if count >= a.config.MaxVerificationAttempts {
				_ = a.repositories.codes.Delete(ctx, userID, PurposeSensitiveOp)
				a.log.WarnContext(ctx, "sensitive op code locked out", "user_id", userID, "attempts", count)
				return ErrTooManyAttempts
			}
		}
		return ErrCodeInvalid
	}

	// Consume the code on success — one-shot.
	_ = a.repositories.codes.Delete(ctx, userID, PurposeSensitiveOp)

	a.log.InfoContext(ctx, "sensitive op code verified", "user_id", userID)
	a.logSecurityEvent(ctx, userID, SecEventSensitiveOpVerified, SecStatusSuccess, nil)

	return nil
}
