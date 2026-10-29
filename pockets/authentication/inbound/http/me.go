package authenticationhttp

import (
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// GET /auth/me — session hydration (ruling 6). A SPA that authenticates over
// cookies cannot read its own session, so it hydrates the signed-in user from
// this route. It rides the SessionHydration authenticator, deliberately paying
// one revocation lookup so a revoked access JWT is denied within one round-trip —
// the same posture as GET /auth/methods. It is a bearer-safe GET with no request
// body, so it skips the browser-safe-mutation CSRF gate, and the handler sets
// Cache-Control: no-store so the profile is never retained by a shared cache.

// me returns the caller's current-user body — the same userResponse contract
// login and register return, built through the same constructor so the three
// paths cannot drift.
//
// A machine principal is NOT a current user: the authenticator admits a valid
// API key (no session row exists for one), so the CurrentUser gate below is what
// keeps the route human-only — a service-account principal gets the same 401 as
// an absent credential rather than a fabricated profile. An act-as-user service
// account is not that case: it resolves to its human owner (design §4.1), and
// this route then hydrates that owner's real profile.
func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	view, err := h.svc.CurrentUserView(r.Context(), userID)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	out := meResponse{userResponse: newUserResponse(view.User, view.Email, view.EmailVerified)}
	if cred, ok := h.svc.CurrentCredential(r.Context()); ok {
		out.MFAEnrollment = newMFAEnrollmentResponse(cred)
	}
	web.RespondJSONOK(w, out)
}

// meResponse is the GET /auth/me body: the current-user contract plus, under
// MFAConfig.Required all beside passkeys or TOTP, the caller's enrollment
// window.
type meResponse struct {
	userResponse
	MFAEnrollment *mfaEnrollmentResponse `json:"mfa_enrollment,omitempty"`
}

// mfaEnrollmentResponse is the caller's enrollment window in /auth/me and
// /auth/methods: present only on a first-party session below AAL2 whose token
// carries mfa_enroll_by, so an SPA can show the deadline before the 403.
type mfaEnrollmentResponse struct {
	Required bool   `json:"required"`
	EnrollBy string `json:"enroll_by"`
}

// newMFAEnrollmentResponse renders cred's enrollment window, or nil when it
// carries none.
func newMFAEnrollmentResponse(cred Credential) *mfaEnrollmentResponse {
	if cred.MFAEnrollBy.IsZero() {
		return nil
	}
	return &mfaEnrollmentResponse{Required: true, EnrollBy: cred.MFAEnrollBy.UTC().Format(time.RFC3339)}
}
