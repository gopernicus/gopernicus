package authentication

import (
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// The registration-verification resend surface (CHAU-2.3). Two routes for one use
// case, deliberately shaped differently:
//
//   - POST /auth/verification/resend is PUBLIC and enumeration-safe. It is a
//     credential-establishment endpoint, so it carries the allowlisted-Origin gate
//     but NOT the double-submit CSRF gate — a user who cannot log in has no
//     session and therefore no __Host-auth_csrf cookie to compare, which is
//     exactly the population this route exists for. It ALWAYS answers 202 for
//     every target state.
//   - POST /auth/admin/users/{id}/verification/resend is AUTHORIZED and may report
//     real target state. It mounts with the rest of the admin surface, so it
//     exists only when the host wired Config.UserAdminCheck.

// resendVerificationRequest is the public body.
type resendVerificationRequest struct {
	Email string `json:"email"`
}

// resendVerification is the public, enumeration-safe resend.
//
// The response is byte-identical for unknown, malformed, verified, unverified,
// and deactivated addresses: 202 {"status":"accepted"}. The ONLY other outcomes
// are a 429 (a budget refused the request — which says nothing about the target)
// and honest infrastructure failures: 503 when the delivery queue refuses
// admission, 500 otherwise. Neither depends on target state.
//
// "Accepted" means ADMITTED, not delivered. There is deliberately no pollable
// receipt here: a receipt whose status eventually read "sent" versus "nothing to
// do" would be exactly the enumeration oracle the 202 exists to prevent.
func (h *handlers) resendVerification(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	var req resendVerificationRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.ResendVerification(r.Context(), req.Email); err != nil {
		switch {
		case errors.Is(err, authsvc.ErrVerificationResendRateLimited):
			web.RespondJSONError(w, web.ErrTooManyRequests("too many requests"))
		case deliveryUnavailable(err):
			// A bounded queue that refuses admission is an honest 503 for EVERY
			// address — never a 202 that quietly dropped the work.
			web.RespondJSONError(w, web.ErrUnavailable("could not process request"))
		default:
			web.RespondJSONError(w, web.ErrInternal("could not process request"))
		}
		return
	}
	web.RespondJSONAccepted(w, map[string]string{"status": "accepted"})
}

// adminResendVerification re-issues the target's registration verification code.
//
// Unlike the public route it reports real state, because the caller has already
// passed Config.UserAdminCheck: an unknown user is 404, an already-verified or
// deactivated account is a typed 409, and success returns a secret-free delivery
// receipt the operator can poll through GET /auth/delivery/status.
func (h *handlers) adminResendVerification(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	var body struct{}
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	id := web.Param(r, "id")
	principal, ok := h.adminPrincipal(w, r, authsvc.UserAdminResendVerification, id)
	if !ok {
		return
	}
	receipt, err := h.svc.ResendVerificationForUser(r.Context(), principal, id)
	if err != nil {
		switch {
		case errors.Is(err, authsvc.ErrAlreadyVerified):
			web.RespondJSONError(w, web.ErrStateConflict("the account's primary email is already verified").WithCode("already_verified"))
		case errors.Is(err, authsvc.ErrUserDeactivated):
			web.RespondJSONError(w, web.ErrStateConflict("the account is deactivated").WithCode("user_deactivated"))
		case deliveryUnavailable(err):
			web.RespondJSONError(w, web.ErrUnavailable("could not process request"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}
	web.RespondJSONAccepted(w, stepUpBeginResponse{Status: "sent", Receipt: receipt.Receipt})
}
