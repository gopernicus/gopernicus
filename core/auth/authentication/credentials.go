package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// Register creates a new user account with email and password.
//
// The returned [RegisterResult] includes the plaintext verification code.
// A [VerificationCodeRequestedEvent] is emitted for email delivery.
//
// If the email is already taken, Register returns a synthetic success result
// to prevent email enumeration. No verification code is generated or emitted.
//
// After creating the user and password records, Register runs any
// [AfterUserCreationHook] functions registered via [WithAfterUserCreationHooks]
// or [Authenticator.AddAfterUserCreationHooks]. If a hook returns an error,
// Register returns that error. Note that user creation is not transactional —
// the user and password records will already exist in the database. This is
// safe because the user remains unverified and cannot log in until they
// complete email verification. A subsequent registration attempt for the
// same email hits the duplicate-email path and returns synthetic success.
func (a *Authenticator) Register(ctx context.Context, email, password, displayName string) (*RegisterResult, error) {
	if email == "" {
		return nil, ErrInvalidCredentials
	}
	// Hash password check and bail early.
	hash, err := a.hasher.Hash(password)
	if err != nil {
		return nil, fmt.Errorf("auth register: hash password: %w", err)
	}

	// Check if email is taken.
	existing, err := a.repositories.users.GetByEmail(ctx, email)
	if err == nil {
		// Email exists — return synthetic success to prevent enumeration.
		a.log.InfoContext(ctx, "registration attempted for existing email")
		a.logSecurityEvent(ctx, existing.UserID, SecEventRegister, SecStatusBlocked, map[string]any{
			"email":  email,
			"reason": "duplicate_email",
		})
		return &RegisterResult{UserID: "redacted"}, nil
	}
	if !errors.Is(err, errs.ErrNotFound) {
		return nil, fmt.Errorf("auth register: %w", err)
	}

	// Create user.
	user, err := a.repositories.users.Create(ctx, CreateUserInput{
		Email:         email,
		DisplayName:   displayName,
		EmailVerified: false,
	})
	if err != nil {
		return nil, fmt.Errorf("auth register: create user: %w", err)
	}

	if err := a.repositories.passwords.Create(ctx, user.UserID, hash); err != nil {
		return nil, fmt.Errorf("auth register: store password: %w", err)
	}

	// Run after user creation hooks (e.g., resolve invitations, provision defaults).
	if err := a.runAfterUserCreationHooks(ctx, user); err != nil {
		return nil, fmt.Errorf("auth register: %w", err)
	}

	// Generate verification code.
	code, err := generateVerificationCode()
	if err != nil {
		return nil, fmt.Errorf("auth register: generate code: %w", err)
	}
	if err := a.repositories.codes.Create(ctx, VerificationCode{
		Identifier: email,
		CodeHash:   mustHashToken(code),
		Purpose:    PurposeEmailVerify,
		ExpiresAt:  time.Now().UTC().Add(a.config.VerificationCodeExpiry),
	}); err != nil {
		return nil, fmt.Errorf("auth register: store code: %w", err)
	}

	// Emit event for email delivery.
	if err := a.bus.Emit(ctx, VerificationCodeRequestedEvent{
		UserID:    user.UserID,
		Email:     email,
		Code:      code,
		ExpiresIn: a.config.VerificationCodeExpiry.String(),
	}); err != nil {
		a.log.WarnContext(ctx, "failed to emit verification code event", "user_id", user.UserID, "error", err)
	}

	a.log.InfoContext(ctx, "user registered", "user_id", user.UserID)
	a.logSecurityEvent(ctx, user.UserID, SecEventRegister, SecStatusSuccess, map[string]any{
		"email": email,
	})

	return &RegisterResult{
		UserID:           user.UserID,
		VerificationCode: code,
	}, nil
}

// DeleteUser revokes all sessions for the user and emits a
// [UserDeletionRequestedEvent] for downstream cleanup.
//
// Session revocation happens immediately to prevent continued access.
// All other data cleanup (passwords, OAuth links, verification codes,
// security event pseudonymization, domain data, the user record) is the
// responsibility of the event subscriber.
func (a *Authenticator) DeleteUser(ctx context.Context, userID string) error {
	user, err := a.repositories.users.Get(ctx, userID)
	if err != nil {
		return fmt.Errorf("auth delete user: lookup: %w", err)
	}

	// Revoke all sessions immediately — prevents continued access while
	// the subscriber processes the deletion.
	if err := a.repositories.sessions.DeleteAllForUser(ctx, userID); err != nil {
		a.log.WarnContext(ctx, "failed to revoke sessions during user deletion",
			"user_id", userID, "error", err)
	}

	if err := a.bus.Emit(ctx, UserDeletionRequestedEvent{
		UserID: userID,
		Email:  user.Email,
	}); err != nil {
		return fmt.Errorf("auth delete user: emit event: %w", err)
	}

	a.log.InfoContext(ctx, "user deletion requested", "user_id", userID)
	a.logSecurityEvent(ctx, userID, SecEventUserDeleted, SecStatusSuccess, nil)
	return nil
}

// Login authenticates a user with email and password and creates a session.
func (a *Authenticator) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	// Look up user.
	user, err := a.repositories.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth login: lookup user: %w", err)
	}

	if !user.Active {
		a.logSecurityEvent(ctx, user.UserID, SecEventLogin, SecStatusBlocked, map[string]any{
			"reason": "user_inactive",
		})
		return nil, ErrUserInactive
	}

	// Verify password hash before checking verification status.
	// This ensures the caller has the correct credentials before we
	// trigger any verification emails.
	pw, err := a.repositories.passwords.GetByUserID(ctx, user.UserID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth login: lookup password: %w", err)
	}
	if err := a.hasher.Compare(pw.Hash, password); err != nil {
		a.log.WarnContext(ctx, "failed login — invalid password")
		a.logSecurityEvent(ctx, user.UserID, SecEventLogin, SecStatusFailure, map[string]any{
			"reason": "invalid_password",
		})
		return nil, ErrInvalidCredentials
	}

	// Check verification status. If the email or password credential is
	// unverified but the password hash matched, auto-send a verification
	// code so the user can complete verification immediately.
	if !user.EmailVerified || !pw.Verified {
		reason := "email_not_verified"
		retErr := ErrEmailNotVerified
		if user.EmailVerified && !pw.Verified {
			reason = "password_not_verified"
			retErr = ErrPasswordNotVerified
		}
		a.logSecurityEvent(ctx, user.UserID, SecEventLogin, SecStatusBlocked, map[string]any{
			"reason": reason,
		})
		a.sendVerificationCode(ctx, user, PurposeEmailVerify)
		return nil, retErr
	}

	// Update last login.
	if err := a.repositories.users.SetLastLogin(ctx, user.UserID, time.Now().UTC()); err != nil {
		a.log.WarnContext(ctx, "failed to update last login", "user_id", user.UserID, "error", err)
	}

	// Create session.
	result, err := a.createSession(ctx, user)
	if err != nil {
		return nil, err
	}

	a.log.InfoContext(ctx, "user logged in", "user_id", user.UserID)
	a.logSecurityEvent(ctx, user.UserID, SecEventLogin, SecStatusSuccess, map[string]any{
		"user": user.UserID,
	})
	return result, nil
}
