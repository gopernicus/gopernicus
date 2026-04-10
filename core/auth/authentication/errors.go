package authentication

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

var (
	// Credential errors.
	ErrInvalidCredentials    = fmt.Errorf("auth: %w", errs.ErrUnauthorized)
	ErrEmailNotVerified      = fmt.Errorf("auth email not verified: %w", errs.ErrForbidden)
	ErrPasswordNotVerified   = fmt.Errorf("auth password not verified: %w", errs.ErrForbidden)
	ErrPasswordNotSet        = fmt.Errorf("auth password not set: %w", errs.ErrNotFound)
	ErrOAuthAccountUnverified = fmt.Errorf("auth oauth account not verified: %w", errs.ErrForbidden)
	ErrEmailAlreadyExists    = fmt.Errorf("auth: %w", errs.ErrAlreadyExists)
	ErrUserInactive          = fmt.Errorf("auth user inactive: %w", errs.ErrForbidden)
	ErrCouldNotRegister      = fmt.Errorf("auth user register: %w", errs.ErrConflict)
	// ErrCannotRemoveLastMethod is returned when removing an authentication
	// method (OAuth account or password) would leave the user with no way to
	// sign in. Applies to both OAuth unlink and password removal.
	ErrCannotRemoveLastMethod = fmt.Errorf("auth: %w", errs.ErrConflict)
	// Session/token errors.
	ErrSessionNotFound = fmt.Errorf("auth session: %w", errs.ErrNotFound)
	ErrTokenExpired    = fmt.Errorf("auth token: %w", errs.ErrExpired)
	ErrTokenReuse      = fmt.Errorf("auth token reuse: %w", errs.ErrUnauthorized)

	// Verification errors.
	ErrCodeExpired          = fmt.Errorf("auth code: %w", errs.ErrExpired)
	ErrCodeInvalid          = fmt.Errorf("auth code: %w", errs.ErrUnauthorized)
	ErrTooManyAttempts      = fmt.Errorf("auth: %w", errs.ErrForbidden)
	ErrEmailAlreadyVerified = fmt.Errorf("auth: %w", errs.ErrConflict)

	// OAuth errors.
	ErrUnsupportedProvider       = fmt.Errorf("auth oauth: %w", errs.ErrInvalidInput)
	ErrInvalidOAuthState         = fmt.Errorf("auth oauth state: %w", errs.ErrUnauthorized)
	ErrOAuthAccountNotLinked     = fmt.Errorf("auth oauth: %w", errs.ErrNotFound)
	ErrOAuthAccountExists        = fmt.Errorf("auth oauth: %w", errs.ErrAlreadyExists)
	ErrOAuthVerificationRequired = fmt.Errorf("auth oauth: %w", errs.ErrForbidden)
	ErrInvalidFlowSecret         = fmt.Errorf("auth oauth: %w", errs.ErrUnauthorized)
	ErrOAuthNotConfigured        = fmt.Errorf("auth oauth: %w", errs.ErrInvalidInput)
	ErrInvalidRedirectURI        = fmt.Errorf("auth oauth redirect: %w", errs.ErrInvalidInput)

	// API key errors.
	ErrAPIKeyNotFound       = fmt.Errorf("auth api key: %w", errs.ErrNotFound)
	ErrAPIKeyExpired        = fmt.Errorf("auth api key: %w", errs.ErrExpired)
	ErrAPIKeyInactive       = fmt.Errorf("auth api key: %w", errs.ErrForbidden)
	ErrAPIKeysNotConfigured = fmt.Errorf("auth api keys: %w", errs.ErrInvalidInput)
)
