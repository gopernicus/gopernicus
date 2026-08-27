package authentication

import (
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// The user-administration surface (CHAU-1.6). Registered by Mount ONLY when the
// host supplied Config.UserAdminCheck AND the administration/fenced-mint
// repositories are wired — deny-by-absence, the Providers precedent. A host that
// wires nothing here has no admin routes at all, and every path below 404s.
//
// Every route is gated in the same order:
//
//  1. RequireLiveSession — sensitive reads and mutations must observe revocation
//     within one round-trip, not within AccessTokenTTL. A deactivated
//     administrator loses this surface immediately, because the transition
//     deleted their sessions.
//  2. the browser-safe mutation gate (allowlisted Origin + double-submit CSRF) —
//     on the POST mutations only; the GET reads are bearer-safe and carry no
//     body, matching /auth/methods.
//  3. Config.UserAdminCheck — the HOST's decision, run before any target
//     resolution or mutation, so an unauthorized caller cannot use timing or
//     error shape to probe which user ids exist.
//
// The handlers write Cache-Control: no-store: the directory carries unmasked
// contact data and must never be retained by a shared cache.

// userSummaryResponse is one directory row. It mirrors user.Summary exactly —
// no credential material, no auth revision. status_changed_at is omitted while
// the account has never transitioned, and primary_email is omitted when the
// subject holds no email identifier (absent means "none on file", never
// "hidden": this surface is already explicitly authorized, so nothing is masked).
type userSummaryResponse struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	Status          string `json:"status"`
	StatusChangedAt string `json:"status_changed_at,omitempty"`
	PrimaryEmail    string `json:"primary_email,omitempty"`
	EmailVerified   bool   `json:"email_verified"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// userStatusResponse is the body of a lifecycle mutation: the resulting summary
// plus whether THIS call performed the transition. changed=false means the
// account was already in the requested status — a safe, idempotent replay, not
// an error.
type userStatusResponse struct {
	User    userSummaryResponse `json:"user"`
	Changed bool                `json:"changed"`
}

func newUserSummaryResponse(s user.Summary) userSummaryResponse {
	out := userSummaryResponse{
		ID:            s.ID,
		DisplayName:   s.DisplayName,
		Status:        string(user.NormalizeStatus(s.Status)),
		PrimaryEmail:  s.PrimaryEmail,
		EmailVerified: s.EmailVerified,
		CreatedAt:     s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     s.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if !s.StatusChangedAt.IsZero() {
		out.StatusChangedAt = s.StatusChangedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// mountUserAdmin registers the optional administrative user surface. Called from
// Mount only when svc.UserAdminEnabled() and svc.UserAdminAuthorized() are both
// true.
func mountUserAdmin(r pocket.RouteRegistrar, h *handlers, liveSession, browserSafe web.Middleware) {
	r.Handle("GET", "/auth/admin/users", h.adminListUsers, liveSession)
	r.Handle("GET", "/auth/admin/users/{id}", h.adminGetUser, liveSession)
	r.Handle("POST", "/auth/admin/users/{id}/deactivate", h.adminDeactivateUser, liveSession, browserSafe)
	r.Handle("POST", "/auth/admin/users/{id}/reactivate", h.adminReactivateUser, liveSession, browserSafe)
	// The AUTHORIZED counterpart of the public, enumeration-safe resend (CHAU-2.3).
	// It rides the same gate chain as the lifecycle mutations and may report real
	// target state.
	r.Handle("POST", "/auth/admin/users/{id}/verification/resend", h.adminResendVerification, liveSession, browserSafe)
}

// adminPrincipal resolves the caller's effective principal and runs the host
// authorization check for action/target. It writes the response and returns
// ok=false on any refusal, so a handler's remaining body runs only for an
// authorized caller.
//
// A machine principal reaches the policy as a real principal: the pocket does
// not pre-decide whether a service account may administer users.
func (h *handlers) adminPrincipal(w http.ResponseWriter, r *http.Request, action authsvc.UserAdminAction, targetUserID string) (authsvc.Principal, bool) {
	principal, ok := h.svc.CurrentPrincipal(r.Context())
	if !ok {
		// RequireLiveSession already ran, so this is a wiring failure rather than a
		// missing credential. Fail closed either way.
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return authsvc.Principal{}, false
	}
	if err := h.svc.AuthorizeUserAdmin(r.Context(), principal, action, targetUserID); err != nil {
		// A denial and an infrastructure error both stop here; the shared mapper
		// turns sdk.ErrForbidden into 403 and anything else into 500, so a policy
		// outage never reads as permission.
		web.RespondJSONDomainError(w, err)
		return authsvc.Principal{}, false
	}
	return principal, true
}

// adminListUsers returns a page of the operator directory.
func (h *handlers) adminListUsers(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	if _, ok := h.adminPrincipal(w, r, authsvc.UserAdminList, ""); !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, user.OrderFields, user.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.svc.ListUsers(r.Context(), req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newUserSummaryResponse))
}

// adminGetUser returns one user's directory projection.
func (h *handlers) adminGetUser(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	id := web.Param(r, "id")
	if _, ok := h.adminPrincipal(w, r, authsvc.UserAdminRead, id); !ok {
		return
	}
	summary, err := h.svc.GetUserSummary(r.Context(), id)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newUserSummaryResponse(summary))
}

// adminDeactivateUser denies the target every new session and revokes what it
// already holds. Replaying it on an already-deactivated user is a 200 with
// changed=false.
func (h *handlers) adminDeactivateUser(w http.ResponseWriter, r *http.Request) {
	h.adminSetStatus(w, r, authsvc.UserAdminDeactivate, user.StatusDeactivated)
}

// adminReactivateUser returns the target to the active posture. It fabricates no
// session — the user must authenticate again.
func (h *handlers) adminReactivateUser(w http.ResponseWriter, r *http.Request) {
	h.adminSetStatus(w, r, authsvc.UserAdminReactivate, user.StatusActive)
}

// adminSetStatus is the shared body of the two lifecycle mutations. The target
// status is chosen by the ROUTE, never by the request body: a body-driven status
// would let a client name a value the host's policy was not asked about.
//
// Self-transition is not generically forbidden — a host policy may allow or
// refuse an administrator acting on their own account, and a last-admin
// invariant lives in the host's policy, not here.
func (h *handlers) adminSetStatus(w http.ResponseWriter, r *http.Request, action authsvc.UserAdminAction, status user.Status) {
	writeNoStore(w)
	if !requireJSON(w, r) {
		return
	}
	// These mutations carry no fields; the strict decode still rejects an unknown
	// key or an oversized body, matching every other mutation route.
	var body struct{}
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return
	}
	id := web.Param(r, "id")
	principal, ok := h.adminPrincipal(w, r, action, id)
	if !ok {
		return
	}
	summary, change, err := h.svc.SetUserStatus(r.Context(), principal, id, status)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, userStatusResponse{
		User:    newUserSummaryResponse(summary),
		Changed: change.Changed,
	})
}
