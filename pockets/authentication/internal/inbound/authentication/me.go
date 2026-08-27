package authentication

import (
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// GET /auth/me — session hydration (ruling 6). A SPA that authenticates over
// cookies cannot read its own session, so it hydrates the signed-in user from
// this route. It is live-session-gated (RequireLiveSession), deliberately paying
// one revocation lookup so a revoked access JWT is denied within one round-trip —
// the same posture as GET /auth/methods. It is a bearer-safe GET with no request
// body, so it skips the browser-safe-mutation CSRF gate, and the handler sets
// Cache-Control: no-store so the profile is never retained by a shared cache.

// me returns the caller's current-user body — the same userResponse contract
// login and register return, built through the same constructor so the three
// paths cannot drift.
//
// A machine principal is NOT a current user: RequireLiveSession admits a valid
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
	web.RespondJSONOK(w, newUserResponse(view.User, view.Email, view.EmailVerified))
}
