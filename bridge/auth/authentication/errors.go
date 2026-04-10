package authentication

import (
	"errors"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// httpErrFor maps an authentication domain error to an HTTP error response.
//
// Recognized auth sentinels are translated into responses with stable,
// domain-specific codes. Unrecognized errors fall back to web.ErrFromDomain,
// which maps generic sdk/errs sentinels to safe responses.
//
// The codes returned here are part of the bridge's API contract — frontend
// SDKs map them to specific UI states (e.g. "show resend button" vs "show
// lockout message"), so they must remain stable across releases.
//
// Messages are deliberately generic — frontends should branch on the code,
// not the human-readable message. This avoids leaking semantic detail to
// callers (e.g. confirming "the password you tried is wrong" to an attacker
// who already stole a session token).
//
// Note: login failures bypass this mapper entirely and return a single
// generic 401 to prevent account enumeration (see httpLogin).
func httpErrFor(err error) *web.Error {
	switch {
	// Verification (email + OAuth link verification share these sentinels).
	case errors.Is(err, authentication.ErrCodeExpired):
		return web.ErrGone("verification failed").WithCode("verification_code_expired")
	case errors.Is(err, authentication.ErrCodeInvalid):
		return web.ErrBadRequest("verification failed").WithCode("verification_code_invalid")
	case errors.Is(err, authentication.ErrTooManyAttempts):
		return web.ErrForbidden("too many attempts").WithCode("too_many_attempts")

	// Tokens.
	case errors.Is(err, authentication.ErrTokenExpired):
		return web.ErrGone("token expired").WithCode("token_expired")

	// Credentials (used by change-password; login bypasses this mapper).
	case errors.Is(err, authentication.ErrInvalidCredentials):
		return web.ErrBadRequest("invalid credentials").WithCode("invalid_credentials")

	// Authentication method management. Applies to both password removal and
	// OAuth unlink — both can leave the user with no auth method.
	case errors.Is(err, authentication.ErrCannotRemoveLastMethod):
		return web.ErrConflict("cannot remove last authentication method").WithCode("cannot_remove_last_method")
	case errors.Is(err, authentication.ErrPasswordNotSet):
		return web.ErrNotFound("no password is set on this account").WithCode("password_not_set")

	// OAuth. Messages here can be readable — none leak credential state, they
	// describe configuration or flow-state errors that the frontend may surface.
	// ErrOAuthVerificationRequired is intentionally NOT handled here: it is
	// not an error response — callers translate it into a 202 success.
	case errors.Is(err, authentication.ErrUnsupportedProvider):
		return web.ErrBadRequest("unsupported oauth provider").WithCode("oauth_unsupported_provider")
	case errors.Is(err, authentication.ErrInvalidOAuthState):
		return web.ErrUnauthorized("invalid or expired oauth state").WithCode("oauth_invalid_state")
	case errors.Is(err, authentication.ErrOAuthAccountNotLinked):
		return web.ErrNotFound("oauth account not linked").WithCode("oauth_account_not_linked")
	case errors.Is(err, authentication.ErrOAuthAccountExists):
		return web.ErrConflict("oauth account already linked").WithCode("oauth_account_exists")
	case errors.Is(err, authentication.ErrInvalidFlowSecret):
		return web.ErrUnauthorized("invalid or missing flow secret").WithCode("oauth_invalid_flow_secret")
	case errors.Is(err, authentication.ErrOAuthNotConfigured):
		return web.ErrBadRequest("oauth not configured").WithCode("oauth_not_configured")
	case errors.Is(err, authentication.ErrInvalidRedirectURI):
		return web.ErrBadRequest("invalid redirect uri").WithCode("oauth_invalid_redirect_uri")
	}
	return web.ErrFromDomain(err)
}
