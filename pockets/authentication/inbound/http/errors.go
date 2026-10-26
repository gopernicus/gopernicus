package authenticationhttp

import (
	"errors"
	"net/http"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// challengeErrorFor maps a stable challenge-rail sentinel (design §5.8) to its named
// machine code and documented HTTP status: ErrChallengeExpired → challenge_expired
// (410), ErrChallengeInvalid → challenge_invalid (400), ErrTooManyAttempts →
// too_many_attempts (403). It returns (nil, false) for any other error so the caller
// falls back to the generic sdk-kind mapping.
//
// The service already collapses every non-success challenge disposition (no such
// challenge, wrong code, wrong context, malformed) into these three sentinels
// (challenge.go), so this mapper never distinguishes the underlying cause: the code
// and the generic message it carries are identical across causes (enumeration
// protection). It is the shared mapping both the JSON error writer (respondDomainError)
// and the form-arm status classifier (formFailure) consult, so both transports derive
// one outcome from one service error.
func challengeErrorFor(err error) (*web.Error, bool) {
	switch {
	case errors.Is(err, authlogic.ErrChallengeExpired):
		return web.NewError(http.StatusGone, "challenge expired").WithCode("challenge_expired"), true
	case errors.Is(err, authlogic.ErrTooManyAttempts):
		return web.NewError(http.StatusForbidden, "too many attempts").WithCode("too_many_attempts"), true
	case errors.Is(err, authlogic.ErrChallengeInvalid):
		return web.NewError(http.StatusBadRequest, "challenge invalid").WithCode("challenge_invalid"), true
	default:
		return nil, false
	}
}

// respondDomainError writes err as a JSON error, emitting the named challenge machine
// code (design §5.8) when err is a challenge-rail sentinel and otherwise falling back
// to the generic sdk-kind mapping (web.RespondJSONDomainError). Every JSON handler
// that redeems a challenge writes its error through this one seam so the JSON body
// code and the form-arm rerender status derive from a single mapping (transport
// parity).
func respondDomainError(w http.ResponseWriter, err error) {
	if errors.Is(err, authlogic.ErrRateLimited) || errors.Is(err, authlogic.ErrPasswordlessRateLimited) || errors.Is(err, authlogic.ErrIdentifierChangeRateLimited) {
		web.RespondJSONError(w, web.ErrTooManyRequests(tooManyErrMsg))
		return
	}
	if mapped, ok := challengeErrorFor(err); ok {
		web.RespondJSONError(w, mapped)
		return
	}
	web.RespondJSONDomainError(w, err)
}

// registrationErrorFor maps a registration-mode refusal to its 403 and code:
// registration_closed or registration_invitation_required. It returns
// (nil, false) for any other error.
func registrationErrorFor(err error) (*web.Error, bool) {
	switch {
	case errors.Is(err, authlogic.ErrRegistrationClosed):
		return web.ErrForbidden("registration is closed").WithCode(authlogic.RegistrationCodeClosed), true
	case errors.Is(err, authlogic.ErrRegistrationInvitationRequired):
		return web.ErrForbidden("registration requires an invitation").WithCode(authlogic.RegistrationCodeInvitationRequired), true
	default:
		return nil, false
	}
}

// respondRegistrationError writes a route's error, a registration-mode
// refusal with its named code.
func respondRegistrationError(w http.ResponseWriter, err error) {
	if mapped, ok := registrationErrorFor(err); ok {
		web.RespondJSONError(w, mapped)
		return
	}
	web.RespondJSONDomainError(w, err)
}

// passkeyErrorFor maps a passkey ceremony refusal to its wire code:
// ErrPasskeyRejected → passkey_rejected (401), ErrPasskeyLimit → passkey_limit
// (409), ErrPasskeyExists → passkey_exists (409). It returns (nil, false) for
// any other error.
func passkeyErrorFor(err error) (*web.Error, bool) {
	switch {
	case errors.Is(err, authlogic.ErrPasskeyRejected):
		return web.NewError(http.StatusUnauthorized, "passkey rejected").WithCode("passkey_rejected"), true
	case errors.Is(err, authlogic.ErrPasskeyLimit):
		return web.NewError(http.StatusConflict, "passkey limit reached").WithCode("passkey_limit"), true
	case errors.Is(err, authlogic.ErrPasskeyExists):
		return web.NewError(http.StatusConflict, "passkey already registered").WithCode("passkey_exists"), true
	default:
		return nil, false
	}
}

// respondPasskeyError writes a passkey route's error: the passkey codes first,
// then the challenge codes (an expired ceremony is challenge_expired, 410) and
// the generic mapping (a missing step-up is 403).
func respondPasskeyError(w http.ResponseWriter, err error) {
	if mapped, ok := passkeyErrorFor(err); ok {
		web.RespondJSONError(w, mapped)
		return
	}
	respondDomainError(w, err)
}
