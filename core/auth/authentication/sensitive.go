package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// ---------------------------------------------------------------------------
// Generic verification code primitives
//
// IssueVerificationCode and ConsumeVerificationCode are the building blocks
// the framework's built-in sensitive operations use, and are also the
// supported extension point for application-defined sensitive operations.
//
// They are intentionally split from event emission: callers (built-in or
// user) call IssueVerificationCode to generate and store a code, then emit
// their OWN strongly-typed event carrying whatever fields the operation
// needs. This keeps event payloads explicit and type-safe end-to-end —
// there is no shared "VerificationCodeRequestedEvent with a Data map" path
// for sensitive ops, by design.
//
// See workshop/documentation/docs/gopernicus/topics/auth/sensitive-operations.md
// for the full extension pattern.
// ---------------------------------------------------------------------------

// issueConfig holds optional configuration for [Authenticator.IssueVerificationCode].
type issueConfig struct {
	storedContext []byte
}

// IssueOption configures a call to [Authenticator.IssueVerificationCode].
type IssueOption func(*issueConfig)

// WithStoredContext attaches arbitrary JSON-serializable context to the
// stored verification code. The context is returned by
// [Authenticator.ConsumeVerificationCode] so the caller can validate that
// the code was issued for the same resource the user is now acting on
// (for example, "this code was issued to unlink Google, not GitHub").
//
// The value must be JSON-marshalable. If marshaling fails the option is
// silently dropped at issue time — callers should validate their own
// context shape before calling IssueVerificationCode.
func WithStoredContext(v any) IssueOption {
	return func(c *issueConfig) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		c.storedContext = b
	}
}

// IssueVerificationCode generates a 6-digit code, stores it under the given
// purpose keyed by user_id, and returns the plaintext code.
//
// The caller is responsible for emitting their own strongly-typed event
// carrying the code (e.g. via the bus). This method does NOT emit any
// event itself — it's the building block, not the full operation.
//
// Codes are keyed by user_id (not email) because the caller is already
// authenticated and email may change between issuing and consuming.
//
// Issuing a new code for the same (user_id, purpose) invalidates any prior
// code for that pair — codes are one-shot.
//
// Use [WithStoredContext] to bind the code to per-operation data the verify
// step will check (e.g. which OAuth provider).
func (a *Authenticator) IssueVerificationCode(ctx context.Context, userID, purpose string, opts ...IssueOption) (string, error) {
	if purpose == "" {
		return "", fmt.Errorf("auth issue verification code: purpose required")
	}

	var cfg issueConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// Confirm user exists. We use this for symmetry with framework methods —
	// custom callers usually already hold the user record.
	if _, err := a.repositories.users.Get(ctx, userID); err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return "", ErrUserInactive
		}
		return "", fmt.Errorf("auth issue verification code: lookup user: %w", err)
	}

	// One-shot per (user_id, purpose) — drop any prior code.
	_ = a.repositories.codes.Delete(ctx, userID, purpose)

	code, err := generateVerificationCode()
	if err != nil {
		return "", fmt.Errorf("auth issue verification code: generate: %w", err)
	}

	if err := a.repositories.codes.Create(ctx, VerificationCode{
		Identifier: userID,
		CodeHash:   mustHashToken(code),
		Purpose:    purpose,
		Data:       cfg.storedContext,
		ExpiresAt:  time.Now().UTC().Add(a.config.VerificationCodeExpiry),
	}); err != nil {
		return "", fmt.Errorf("auth issue verification code: store: %w", err)
	}

	return code, nil
}

// ConsumeVerificationCode validates a code previously issued by
// [Authenticator.IssueVerificationCode] for the given user and purpose.
// On success the code is consumed (deleted) and any stored context bytes
// are returned for caller-side validation. On failure the attempt counter
// is incremented and the code is deleted after MaxVerificationAttempts.
//
// Codes are one-shot: a successful consume destroys the code, so callers
// cannot reuse the same code for multiple operations. A wrong-context
// validation by the caller (e.g. provider mismatch on unlink) should be
// treated as a failure — the user must request a new code, which is the
// safer default than allowing retry-with-different-context.
func (a *Authenticator) ConsumeVerificationCode(ctx context.Context, userID, code, purpose string) (storedContext []byte, err error) {
	if purpose == "" {
		return nil, fmt.Errorf("auth consume verification code: purpose required")
	}

	stored, err := a.repositories.codes.Get(ctx, userID, purpose)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil, ErrCodeInvalid
		}
		return nil, fmt.Errorf("auth consume verification code: lookup: %w", err)
	}

	if time.Now().UTC().After(stored.ExpiresAt) {
		_ = a.repositories.codes.Delete(ctx, userID, purpose)
		return nil, ErrCodeExpired
	}

	codeHash, err := hashToken(code)
	if err != nil {
		return nil, ErrCodeInvalid
	}
	if !hashEquals(codeHash, stored.CodeHash) {
		if a.config.MaxVerificationAttempts > 0 {
			count, err := a.repositories.codes.IncrementAttempts(ctx, userID, purpose)
			if err != nil {
				a.log.WarnContext(ctx, "failed to increment verification attempts",
					"user_id", userID, "purpose", purpose, "error", err)
			} else if count >= a.config.MaxVerificationAttempts {
				_ = a.repositories.codes.Delete(ctx, userID, purpose)
				a.log.WarnContext(ctx, "verification code locked out",
					"user_id", userID, "purpose", purpose, "attempts", count)
				return nil, ErrTooManyAttempts
			}
		}
		return nil, ErrCodeInvalid
	}

	// Consume on success — one-shot.
	_ = a.repositories.codes.Delete(ctx, userID, purpose)

	return stored.Data, nil
}

// ---------------------------------------------------------------------------
// Built-in sensitive operations
// ---------------------------------------------------------------------------

// SendRemovePasswordCode issues a verification code that gates the
// [Authenticator.RemovePassword] operation, and emits a
// [RemovePasswordCodeRequestedEvent] for email delivery.
//
// Pair with [Authenticator.VerifyRemovePasswordCode] in the bridge handler
// before calling [Authenticator.RemovePassword].
func (a *Authenticator) SendRemovePasswordCode(ctx context.Context, userID string) (string, error) {
	user, err := a.repositories.users.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return "", ErrUserInactive
		}
		return "", fmt.Errorf("auth send remove password code: lookup user: %w", err)
	}

	code, err := a.IssueVerificationCode(ctx, userID, PurposeRemovePassword)
	if err != nil {
		return "", err
	}

	if err := a.bus.Emit(ctx, RemovePasswordCodeRequestedEvent{
		UserID:      user.UserID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Code:        code,
		ExpiresIn:   a.config.VerificationCodeExpiry.String(),
	}); err != nil {
		a.log.WarnContext(ctx, "failed to emit remove password code event",
			"user_id", userID, "error", err)
	}

	a.log.InfoContext(ctx, "remove password code issued", "user_id", userID)
	a.logSecurityEvent(ctx, userID, SecEventRemovePasswordCodeSent, SecStatusSuccess, nil)

	return code, nil
}

// VerifyRemovePasswordCode validates a code issued by
// [Authenticator.SendRemovePasswordCode]. On success the code is consumed
// and the bridge handler may proceed to call [Authenticator.RemovePassword].
func (a *Authenticator) VerifyRemovePasswordCode(ctx context.Context, userID, code string) error {
	if _, err := a.ConsumeVerificationCode(ctx, userID, code, PurposeRemovePassword); err != nil {
		return err
	}

	a.log.InfoContext(ctx, "remove password code verified", "user_id", userID)
	a.logSecurityEvent(ctx, userID, SecEventRemovePasswordCodeVerified, SecStatusSuccess, nil)

	return nil
}

// unlinkOAuthCodeContext is the JSON shape stored in [VerificationCode.Data]
// for [PurposeUnlinkOAuth] codes. Binds the code to a specific provider.
type unlinkOAuthCodeContext struct {
	Provider string `json:"provider"`
}

// SendUnlinkOAuthCode issues a verification code that gates the
// [Authenticator.UnlinkOAuthAccount] operation for a specific provider, and
// emits an [UnlinkOAuthCodeRequestedEvent] for email delivery.
//
// The provider is bound to the code's stored context — a code issued for
// "unlink Google" cannot later be used to unlink GitHub. The bound provider
// is enforced by [Authenticator.VerifyUnlinkOAuthCode].
func (a *Authenticator) SendUnlinkOAuthCode(ctx context.Context, userID, provider string) (string, error) {
	if provider == "" {
		return "", fmt.Errorf("auth send unlink oauth code: provider required")
	}

	user, err := a.repositories.users.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return "", ErrUserInactive
		}
		return "", fmt.Errorf("auth send unlink oauth code: lookup user: %w", err)
	}

	code, err := a.IssueVerificationCode(ctx, userID, PurposeUnlinkOAuth,
		WithStoredContext(unlinkOAuthCodeContext{Provider: provider}),
	)
	if err != nil {
		return "", err
	}

	if err := a.bus.Emit(ctx, UnlinkOAuthCodeRequestedEvent{
		UserID:      user.UserID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Provider:    provider,
		Code:        code,
		ExpiresIn:   a.config.VerificationCodeExpiry.String(),
	}); err != nil {
		a.log.WarnContext(ctx, "failed to emit unlink oauth code event",
			"user_id", userID, "provider", provider, "error", err)
	}

	a.log.InfoContext(ctx, "unlink oauth code issued", "user_id", userID, "provider", provider)
	a.logSecurityEvent(ctx, userID, SecEventUnlinkOAuthCodeSent, SecStatusSuccess, map[string]any{
		"provider": provider,
	})

	return code, nil
}

// VerifyUnlinkOAuthCode validates a code issued by
// [Authenticator.SendUnlinkOAuthCode] AND enforces that the code was issued
// for the same provider being unlinked. A code issued for one provider
// cannot be used to unlink a different provider — the consume succeeds but
// the provider check returns [ErrCodeInvalid] (the code is consumed either
// way: a wrong-provider attempt is treated as a failed verification, so the
// user must request a new code).
//
// On success the bridge handler may proceed to call
// [Authenticator.UnlinkOAuthAccount].
func (a *Authenticator) VerifyUnlinkOAuthCode(ctx context.Context, userID, code, provider string) error {
	if provider == "" {
		return fmt.Errorf("auth verify unlink oauth code: provider required")
	}

	storedContext, err := a.ConsumeVerificationCode(ctx, userID, code, PurposeUnlinkOAuth)
	if err != nil {
		return err
	}

	var bound unlinkOAuthCodeContext
	if len(storedContext) > 0 {
		if err := json.Unmarshal(storedContext, &bound); err != nil {
			a.log.WarnContext(ctx, "failed to parse unlink oauth code context",
				"user_id", userID, "error", err)
			return ErrCodeInvalid
		}
	}
	if bound.Provider != provider {
		a.log.WarnContext(ctx, "unlink oauth code provider mismatch",
			"user_id", userID, "issued_for", bound.Provider, "presented_for", provider)
		return ErrCodeInvalid
	}

	a.log.InfoContext(ctx, "unlink oauth code verified", "user_id", userID, "provider", provider)
	a.logSecurityEvent(ctx, userID, SecEventUnlinkOAuthCodeVerified, SecStatusSuccess, map[string]any{
		"provider": provider,
	})

	return nil
}
