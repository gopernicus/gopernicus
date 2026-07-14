package authentication

import (
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
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
	case errors.Is(err, authsvc.ErrChallengeExpired):
		return web.NewError(http.StatusGone, "challenge expired").WithCode("challenge_expired"), true
	case errors.Is(err, authsvc.ErrTooManyAttempts):
		return web.NewError(http.StatusForbidden, "too many attempts").WithCode("too_many_attempts"), true
	case errors.Is(err, authsvc.ErrChallengeInvalid):
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
	if mapped, ok := challengeErrorFor(err); ok {
		web.RespondJSONError(w, mapped)
		return
	}
	web.RespondJSONDomainError(w, err)
}
